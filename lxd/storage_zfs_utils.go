package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/project"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// zfsIsEnabled returns whether zfs backend is supported.
func zfsIsEnabled() bool {
	out, err := exec.LookPath("zfs")
	if err != nil || len(out) == 0 {
		return false
	}

	return true
}

// zfsToolVersionGet returns the ZFS tools version
func zfsToolVersionGet() (string, error) {
	// This function is only really ever relevant on Ubuntu as the only
	// distro that ships out of sync tools and kernel modules
	out, err := shared.RunCommand("dpkg-query", "--showformat=${Version}", "--show", "zfsutils-linux")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// zfsModuleVersionGet returns the ZFS module version
func zfsModuleVersionGet() (string, error) {
	var zfsVersion string

	if shared.PathExists("/sys/module/zfs/version") {
		out, err := ioutil.ReadFile("/sys/module/zfs/version")
		if err != nil {
			return "", fmt.Errorf("Could not determine ZFS module version")
		}

		zfsVersion = string(out)
	} else {
		out, err := shared.RunCommand("modinfo", "-F", "version", "zfs")
		if err != nil {
			return "", fmt.Errorf("Could not determine ZFS module version")
		}

		zfsVersion = out
	}

	return strings.TrimSpace(zfsVersion), nil
}

// zfsPoolVolumeCreate creates a ZFS dataset with a set of given properties.
func zfsPoolVolumeCreate(dataset string, properties ...string) (string, error) {
	cmd := []string{"zfs", "create"}

	for _, prop := range properties {
		cmd = append(cmd, []string{"-o", prop}...)
	}

	cmd = append(cmd, []string{"-p", dataset}...)

	return shared.RunCommand(cmd[0], cmd[1:]...)
}

func zfsPoolCheck(pool string) error {
	output, err := shared.RunCommand(
		"zfs", "get", "-H", "-o", "value", "type", pool)
	if err != nil {
		return err
	}

	poolType := strings.Split(output, "\n")[0]
	if poolType != "filesystem" {
		return fmt.Errorf("Unsupported pool type: %s", poolType)
	}

	return nil
}

// zfsPoolVolumeExists verifies if a specific ZFS pool or volume exists.
func zfsPoolVolumeExists(dataset string) (bool, error) {
	output, err := shared.RunCommand(
		"zfs", "list", "-Ho", "name")

	if err != nil {
		return false, err
	}

	for _, name := range strings.Split(output, "\n") {
		if name == dataset {
			return true, nil
		}
	}
	return false, nil
}

func zfsPoolCreate(pool string, vdev string) error {
	var err error

	dataset := ""

	if pool == "" {
		_, err := shared.RunCommand(
			"zfs", "create", "-p", "-o", "mountpoint=none", vdev)
		if err != nil {
			logger.Errorf("zfs create failed: %v", err)
			return errors.Wrap(err, "Failed to create ZFS filesystem")
		}
		dataset = vdev
	} else {
		_, err = shared.RunCommand(
			"zpool", "create", "-f", "-m", "none", "-O", "compression=on", pool, vdev)
		if err != nil {
			logger.Errorf("zfs create failed: %v", err)
			return errors.Wrap(err, "Failed to create the ZFS pool")
		}

		dataset = pool
	}

	err = zfsPoolApplyDefaults(dataset)
	if err != nil {
		return err
	}

	return nil
}

func zfsPoolApplyDefaults(dataset string) error {
	err := zfsPoolVolumeSet(dataset, "", "mountpoint", "none")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(dataset, "", "setuid", "on")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(dataset, "", "exec", "on")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(dataset, "", "devices", "on")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(dataset, "", "acltype", "posixacl")
	if err != nil {
		return err
	}

	err = zfsPoolVolumeSet(dataset, "", "xattr", "sa")
	if err != nil {
		return err
	}

	return nil
}

func zfsPoolVolumeClone(project, pool string, source string, name string, dest string, mountpoint string) error {
	_, err := shared.RunCommand(
		"zfs",
		"clone",
		"-p",
		"-o", fmt.Sprintf("mountpoint=%s", mountpoint),
		"-o", "canmount=noauto",
		fmt.Sprintf("%s/%s@%s", pool, source, name),
		fmt.Sprintf("%s/%s", pool, dest))
	if err != nil {
		logger.Errorf("zfs clone failed: %v", err)
		return errors.Wrap(err, "Failed to clone the filesystem")
	}

	subvols, err := zfsPoolListSubvolumes(pool, fmt.Sprintf("%s/%s", pool, source))
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := zfsPoolListSnapshots(pool, sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		destSubvol := dest + strings.TrimPrefix(sub, source)
		snapshotMntPoint := driver.GetSnapshotMountPoint(project, pool, destSubvol)

		_, err = shared.RunCommand(
			"zfs",
			"clone",
			"-p",
			"-o", fmt.Sprintf("mountpoint=%s", snapshotMntPoint),
			"-o", "canmount=noauto",
			fmt.Sprintf("%s/%s@%s", pool, sub, name),
			fmt.Sprintf("%s/%s", pool, destSubvol))
		if err != nil {
			logger.Errorf("zfs clone failed: %v", err)
			return errors.Wrap(err, "Failed to clone the sub-volume")
		}
	}

	return nil
}

func zfsFilesystemEntityDelete(vdev string, pool string) error {
	var err error
	if strings.Contains(pool, "/") {
		// Command to destroy a zfs dataset.
		_, err = shared.RunCommand("zfs", "destroy", "-r", pool)
	} else {
		// Command to destroy a zfs pool.
		_, err = shared.RunCommand("zpool", "destroy", "-f", pool)
	}
	if err != nil {
		return errors.Wrap(err, "Failed to delete the ZFS pool")
	}

	// Cleanup storage
	if filepath.IsAbs(vdev) && !shared.IsBlockdevPath(vdev) {
		os.RemoveAll(vdev)
	}

	return nil
}

func zfsPoolVolumeDestroy(pool string, path string) error {
	mountpoint, err := zfsFilesystemEntityPropertyGet(pool, path, "mountpoint")
	if err != nil {
		return err
	}

	if mountpoint != "none" && shared.IsMountPoint(mountpoint) {
		err := unix.Unmount(mountpoint, unix.MNT_DETACH)
		if err != nil {
			logger.Errorf("umount failed: %s", err)
			return err
		}
	}

	// Due to open fds or kernel refs, this may fail for a bit, give it 10s
	_, err = shared.TryRunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s", pool, path))

	if err != nil {
		logger.Errorf("zfs destroy failed: %v", err)
		return errors.Wrap(err, "Failed to destroy ZFS filesystem")
	}

	return nil
}

func zfsPoolVolumeCleanup(pool string, path string) error {
	if strings.HasPrefix(path, "deleted/") {
		// Cleanup of filesystems kept for refcount reason
		removablePath, err := zfsPoolVolumeSnapshotRemovable(pool, path, "")
		if err != nil {
			return err
		}

		// Confirm that there are no more clones
		if removablePath {
			if strings.Contains(path, "@") {
				// Cleanup snapshots
				err = zfsPoolVolumeDestroy(pool, path)
				if err != nil {
					return err
				}

				// Check if the parent can now be deleted
				subPath := strings.SplitN(path, "@", 2)[0]
				snaps, err := zfsPoolListSnapshots(pool, subPath)
				if err != nil {
					return err
				}

				if len(snaps) == 0 {
					err := zfsPoolVolumeCleanup(pool, subPath)
					if err != nil {
						return err
					}
				}
			} else {
				// Cleanup filesystems
				origin, err := zfsFilesystemEntityPropertyGet(pool, path, "origin")
				if err != nil {
					return err
				}
				origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", pool))

				err = zfsPoolVolumeDestroy(pool, path)
				if err != nil {
					return err
				}

				// Attempt to remove its parent
				if origin != "-" {
					err := zfsPoolVolumeCleanup(pool, origin)
					if err != nil {
						return err
					}
				}
			}

			return nil
		}
	} else if strings.HasPrefix(path, "containers") && strings.Contains(path, "@copy-") {
		// Just remove the copy- snapshot for copies of active containers
		err := zfsPoolVolumeDestroy(pool, path)
		if err != nil {
			return err
		}
	}

	return nil
}

func zfsFilesystemEntityPropertyGet(pool string, path string, key string) (string, error) {
	entity := pool
	if path != "" {
		entity = fmt.Sprintf("%s/%s", pool, path)
	}
	output, err := shared.RunCommand(
		"zfs",
		"get",
		"-H",
		"-p",
		"-o", "value",
		key,
		entity)
	if err != nil {
		return "", errors.Wrap(err, "Failed to get ZFS config")
	}

	return strings.TrimRight(output, "\n"), nil
}

func zfsPoolVolumeRename(pool string, source string, dest string, ignoreMounts bool) error {
	var err error

	for i := 0; i < 20; i++ {
		if ignoreMounts {
			_, err = shared.RunCommand(
				"/proc/self/exe",
				"forkzfs",
				"--",
				"rename",
				"-p",
				fmt.Sprintf("%s/%s", pool, source),
				fmt.Sprintf("%s/%s", pool, dest))
		} else {
			_, err = shared.RunCommand(
				"zfs",
				"rename",
				"-p",
				fmt.Sprintf("%s/%s", pool, source),
				fmt.Sprintf("%s/%s", pool, dest))
		}

		// Success
		if err == nil {
			return nil
		}

		// zfs rename can fail because of descendants, yet still manage the rename
		if !zfsFilesystemEntityExists(pool, source) && zfsFilesystemEntityExists(pool, dest) {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Timeout
	logger.Errorf("zfs rename failed: %v", err)
	return errors.Wrap(err, "Failed to rename ZFS filesystem")
}

func zfsPoolVolumeSet(pool string, path string, key string, value string) error {
	vdev := pool
	if path != "" {
		vdev = fmt.Sprintf("%s/%s", pool, path)
	}
	_, err := shared.RunCommand(
		"zfs",
		"set",
		fmt.Sprintf("%s=%s", key, value),
		vdev)
	if err != nil {
		logger.Errorf("zfs set failed: %v", err)
		return errors.Wrap(err, "Failed to set ZFS config")
	}

	return nil
}

func zfsPoolVolumeSnapshotCreate(pool string, path string, name string) error {
	_, err := shared.RunCommand(
		"zfs",
		"snapshot",
		"-r",
		fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		logger.Errorf("zfs snapshot failed: %v", err)
		return errors.Wrap(err, "Failed to create ZFS snapshot")
	}

	return nil
}

func zfsPoolVolumeSnapshotDestroy(pool, path string, name string) error {
	_, err := shared.RunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		logger.Errorf("zfs destroy failed: %v", err)
		return errors.Wrap(err, "Failed to destroy ZFS snapshot")
	}

	return nil
}

func zfsPoolVolumeSnapshotRestore(pool string, path string, name string) error {
	_, err := shared.TryRunCommand(
		"zfs",
		"rollback",
		fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		logger.Errorf("zfs rollback failed: %v", err)
		return errors.Wrap(err, "Failed to restore ZFS snapshot")
	}

	subvols, err := zfsPoolListSubvolumes(pool, fmt.Sprintf("%s/%s", pool, path))
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := zfsPoolListSnapshots(pool, sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		_, err = shared.TryRunCommand(
			"zfs",
			"rollback",
			fmt.Sprintf("%s/%s@%s", pool, sub, name))
		if err != nil {
			logger.Errorf("zfs rollback failed: %v", err)
			return errors.Wrap(err, "Failed to restore ZFS sub-volume snapshot")
		}
	}

	return nil
}

func zfsPoolVolumeSnapshotRename(pool string, path string, oldName string, newName string) error {
	_, err := shared.RunCommand(
		"zfs",
		"rename",
		"-r",
		fmt.Sprintf("%s/%s@%s", pool, path, oldName),
		fmt.Sprintf("%s/%s@%s", pool, path, newName))
	if err != nil {
		logger.Errorf("zfs snapshot rename failed: %v", err)
		return errors.Wrap(err, "Failed to rename ZFS snapshot")
	}

	return nil
}

func zfsMount(poolName string, path string) error {
	_, err := shared.TryRunCommand(
		"zfs",
		"mount",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		return errors.Wrap(err, "Failed to mount ZFS filesystem")
	}

	return nil
}

func zfsUmount(poolName string, path string, mountpoint string) error {
	output, err := shared.TryRunCommand(
		"zfs",
		"unmount",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		logger.Warnf("Failed to unmount ZFS filesystem via zfs unmount: %s. Trying lazy umount (MNT_DETACH)...", output)
		err := driver.TryUnmount(mountpoint, unix.MNT_DETACH)
		if err != nil {
			logger.Warnf("Failed to unmount ZFS filesystem via lazy umount (MNT_DETACH)...")
			return err
		}
	}

	return nil
}

func zfsPoolListSubvolumes(pool string, path string) ([]string, error) {
	output, err := shared.RunCommand(
		"zfs",
		"list",
		"-t", "filesystem",
		"-o", "name",
		"-H",
		"-r", path)
	if err != nil {
		logger.Errorf("zfs list failed: %v", err)
		return []string{}, errors.Wrap(err, "Failed to list ZFS filesystems")
	}

	children := []string{}
	for _, entry := range strings.Split(output, "\n") {
		if entry == "" {
			continue
		}

		if entry == path {
			continue
		}

		children = append(children, strings.TrimPrefix(entry, fmt.Sprintf("%s/", pool)))
	}

	return children, nil
}

func zfsPoolListSnapshots(pool string, path string) ([]string, error) {
	path = strings.TrimRight(path, "/")
	fullPath := pool
	if path != "" {
		fullPath = fmt.Sprintf("%s/%s", pool, path)
	}

	output, err := shared.RunCommand(
		"zfs",
		"list",
		"-t", "snapshot",
		"-o", "name",
		"-H",
		"-d", "1",
		"-s", "creation",
		"-r", fullPath)
	if err != nil {
		logger.Errorf("zfs list failed: %v", err)
		return []string{}, errors.Wrap(err, "Failed to list ZFS snapshots")
	}

	children := []string{}
	for _, entry := range strings.Split(output, "\n") {
		if entry == "" {
			continue
		}

		if entry == fullPath {
			continue
		}

		children = append(children, strings.SplitN(entry, "@", 2)[1])
	}

	return children, nil
}

func zfsPoolVolumeSnapshotRemovable(pool string, path string, name string) (bool, error) {
	var snap string
	if name == "" {
		snap = path
	} else {
		snap = fmt.Sprintf("%s@%s", path, name)
	}

	clones, err := zfsFilesystemEntityPropertyGet(pool, snap, "clones")
	if err != nil {
		return false, err
	}

	if clones == "-" || clones == "" {
		return true, nil
	}

	return false, nil
}

func zfsFilesystemEntityExists(pool string, path string) bool {
	vdev := pool
	if path != "" {
		vdev = fmt.Sprintf("%s/%s", pool, path)
	}
	output, err := shared.RunCommand(
		"zfs",
		"get",
		"-H",
		"-o",
		"name",
		"type",
		vdev)
	if err != nil {
		return false
	}

	detectedName := strings.TrimSpace(output)
	return detectedName == vdev
}

func (s *storageZfs) doContainerMount(projectName, name string, privileged bool) (bool, error) {
	logger.Debugf("Mounting ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	volumeName := project.Prefix(projectName, name)
	fs := fmt.Sprintf("containers/%s", volumeName)
	containerPoolVolumeMntPoint := driver.GetContainerMountPoint(projectName, s.pool.Name, name)

	containerMountLockID := getContainerMountLockID(s.pool.Name, name)
	lxdStorageMapLock.Lock()
	if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
		lxdStorageMapLock.Unlock()
		if _, ok := <-waitChannel; ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
		}
		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage volume.
		return false, nil
	}

	lxdStorageOngoingOperationMap[containerMountLockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	removeLockFromMap := func() {
		lxdStorageMapLock.Lock()
		if waitChannel, ok := lxdStorageOngoingOperationMap[containerMountLockID]; ok {
			close(waitChannel)
			delete(lxdStorageOngoingOperationMap, containerMountLockID)
		}
		lxdStorageMapLock.Unlock()
	}

	defer removeLockFromMap()

	// Since we're using mount() directly zfs will not automatically create
	// the mountpoint for us. So let's check and do it if needed.
	if !shared.PathExists(containerPoolVolumeMntPoint) {
		err := driver.CreateContainerMountpoint(containerPoolVolumeMntPoint, shared.VarPath(fs), privileged)
		if err != nil {
			return false, err
		}
	}

	ourMount := false
	if !shared.IsMountPoint(containerPoolVolumeMntPoint) {
		source := fmt.Sprintf("%s/%s", s.getOnDiskPoolName(), fs)
		zfsMountOptions := fmt.Sprintf("rw,zfsutil,mntpoint=%s", containerPoolVolumeMntPoint)
		mounterr := driver.TryMount(source, containerPoolVolumeMntPoint, "zfs", 0, zfsMountOptions)
		if mounterr != nil {
			if mounterr != unix.EBUSY {
				logger.Errorf("Failed to mount ZFS dataset \"%s\" onto \"%s\": %v", source, containerPoolVolumeMntPoint, mounterr)
				return false, errors.Wrapf(mounterr, "Failed to mount ZFS dataset \"%s\" onto \"%s\"", source, containerPoolVolumeMntPoint)
			}
			// EBUSY error in zfs are related to a bug we're
			// tracking. So ignore them for now, report back that
			// the mount isn't ours and proceed.
			logger.Warnf("ZFS returned EBUSY while \"%s\" is actually not a mountpoint", containerPoolVolumeMntPoint)
			return false, mounterr
		}
		ourMount = true
	}

	logger.Debugf("Mounted ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return ourMount, nil
}

func (s *storageZfs) doContainerDelete(projectName, name string) error {
	logger.Debugf("Deleting ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	poolName := s.getOnDiskPoolName()
	containerName := name
	fs := fmt.Sprintf("containers/%s", project.Prefix(projectName, containerName))
	containerPoolVolumeMntPoint := driver.GetContainerMountPoint(projectName, s.pool.Name, containerName)

	if zfsFilesystemEntityExists(poolName, fs) {
		removable := true
		snaps, err := zfsPoolListSnapshots(poolName, fs)
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			var err error
			removable, err = zfsPoolVolumeSnapshotRemovable(poolName, fs, snap)
			if err != nil {
				return err
			}

			if !removable {
				break
			}
		}

		if removable {
			origin, err := zfsFilesystemEntityPropertyGet(poolName, fs, "origin")
			if err != nil {
				return err
			}
			poolName := s.getOnDiskPoolName()
			origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", poolName))

			err = zfsPoolVolumeDestroy(poolName, fs)
			if err != nil {
				return err
			}

			err = zfsPoolVolumeCleanup(poolName, origin)
			if err != nil {
				return err
			}
		} else {
			err := zfsPoolVolumeSet(poolName, fs, "mountpoint", "none")
			if err != nil {
				return err
			}

			err = zfsPoolVolumeRename(poolName, fs, fmt.Sprintf("deleted/containers/%s", uuid.NewRandom().String()), true)
			if err != nil {
				return err
			}
		}
	}

	err := deleteContainerMountpoint(containerPoolVolumeMntPoint, shared.VarPath("containers", project.Prefix(projectName, name)), s.GetStorageTypeName())
	if err != nil {
		return err
	}

	snapshotZfsDataset := fmt.Sprintf("snapshots/%s", containerName)
	zfsPoolVolumeDestroy(poolName, snapshotZfsDataset)

	// Delete potential leftover snapshot mountpoints.
	snapshotMntPoint := driver.GetSnapshotMountPoint(projectName, s.pool.Name, containerName)
	if shared.PathExists(snapshotMntPoint) {
		err := os.RemoveAll(snapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Delete potential leftover snapshot symlinks:
	// ${LXD_DIR}/snapshots/<container_name> to ${POOL}/snapshots/<container_name>
	snapshotSymlink := shared.VarPath("snapshots", project.Prefix(projectName, containerName))
	if shared.PathExists(snapshotSymlink) {
		err := os.Remove(snapshotSymlink)
		if err != nil {
			return err
		}
	}

	logger.Debugf("Deleted ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}

func (s *storageZfs) doContainerCreate(projectName, name string, privileged bool) error {
	logger.Debugf("Creating empty ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)

	containerPath := shared.VarPath("containers", project.Prefix(projectName, name))
	containerName := name
	fs := fmt.Sprintf("containers/%s", project.Prefix(projectName, containerName))
	poolName := s.getOnDiskPoolName()
	dataset := fmt.Sprintf("%s/%s", poolName, fs)
	containerPoolVolumeMntPoint := driver.GetContainerMountPoint(projectName, s.pool.Name, containerName)

	// Create volume.
	msg, err := zfsPoolVolumeCreate(dataset, "mountpoint=none", "canmount=noauto")
	if err != nil {
		logger.Errorf("Failed to create ZFS storage volume for container \"%s\" on storage pool \"%s\": %s", s.volume.Name, s.pool.Name, msg)
		return err
	}

	// Set mountpoint.
	err = zfsPoolVolumeSet(poolName, fs, "mountpoint", containerPoolVolumeMntPoint)
	if err != nil {
		return err
	}

	err = driver.CreateContainerMountpoint(containerPoolVolumeMntPoint, containerPath, privileged)
	if err != nil {
		return err
	}

	logger.Debugf("Created empty ZFS storage volume for container \"%s\" on storage pool \"%s\"", s.volume.Name, s.pool.Name)
	return nil
}
