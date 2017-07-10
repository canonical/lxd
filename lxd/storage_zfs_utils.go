package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// zfsPoolVolumeCreate creates a ZFS dataset with a set of given properties.
func zfsPoolVolumeCreate(dataset string, properties ...string) (string, error) {
	cmd := []string{"zfs", "create"}

	for _, prop := range properties {
		cmd = append(cmd, []string{"-o", prop}...)
	}

	cmd = append(cmd, []string{"-p", dataset}...)

	return shared.RunCommand(cmd[0], cmd[1:]...)
}

func zfsPoolVolumeSet(dataset string, key string, value string) (string, error) {
	return shared.RunCommand("zfs",
		"set",
		fmt.Sprintf("%s=%s", key, value),
		dataset)
}

func (s *storageZfs) zfsPoolCheck(pool string) error {
	output, err := shared.RunCommand(
		"zfs", "get", "type", "-H", "-o", "value", pool)
	if err != nil {
		return fmt.Errorf(strings.Split(output, "\n")[0])
	}

	poolType := strings.Split(output, "\n")[0]
	if poolType != "filesystem" {
		return fmt.Errorf("Unsupported pool type: %s", poolType)
	}

	return nil
}

func (s *storageZfs) zfsPoolCreate() error {
	zpoolName := s.getOnDiskPoolName()
	vdev := s.pool.Config["source"]
	if vdev == "" {
		vdev = filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", s.pool.Name))
		s.pool.Config["source"] = vdev

		if s.pool.Config["zfs.pool_name"] == "" {
			s.pool.Config["zfs.pool_name"] = zpoolName
		}

		f, err := os.Create(vdev)
		if err != nil {
			return fmt.Errorf("Failed to open %s: %s", vdev, err)
		}
		defer f.Close()

		err = f.Chmod(0600)
		if err != nil {
			return fmt.Errorf("Failed to chmod %s: %s", vdev, err)
		}

		size, err := shared.ParseByteSizeString(s.pool.Config["size"])
		if err != nil {
			return err
		}
		err = f.Truncate(size)
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %s: %s", vdev, err)
		}

		output, err := shared.RunCommand(
			"zpool",
			"create", zpoolName, vdev,
			"-f", "-m", "none", "-O", "compression=on")
		if err != nil {
			return fmt.Errorf("Failed to create the ZFS pool: %s", output)
		}
	} else {
		// Unset size property since it doesn't make sense.
		s.pool.Config["size"] = ""

		if filepath.IsAbs(vdev) {
			if !shared.IsBlockdevPath(vdev) {
				return fmt.Errorf("custom loop file locations are not supported")
			}

			if s.pool.Config["zfs.pool_name"] == "" {
				s.pool.Config["zfs.pool_name"] = zpoolName
			}

			// This is a block device. Note, that we do not store the
			// block device path or UUID or PARTUUID or similar in
			// the database. All of those might change or might be
			// used in a special way (For example, zfs uses a single
			// UUID in a multi-device pool for all devices.). The
			// safest way is to just store the name of the zfs pool
			// we create.
			s.pool.Config["source"] = zpoolName
			output, err := shared.RunCommand(
				"zpool",
				"create", zpoolName, vdev,
				"-f", "-m", "none", "-O", "compression=on")
			if err != nil {
				return fmt.Errorf("Failed to create the ZFS pool: %s", output)
			}
		} else {
			if s.pool.Config["zfs.pool_name"] != "" {
				return fmt.Errorf("invalid combination of \"source\" and \"zfs.pool_name\" property")
			}
			s.pool.Config["zfs.pool_name"] = vdev
			s.dataset = vdev

			if strings.Contains(vdev, "/") {
				ok := s.zfsFilesystemEntityExists(vdev, false)
				if !ok {
					output, err := shared.RunCommand(
						"zfs",
						"create",
						"-p",
						"-o",
						"mountpoint=none",
						vdev)
					if err != nil {
						logger.Errorf("zfs create failed: %s.", output)
						return fmt.Errorf("Failed to create ZFS filesystem: %s", output)
					}
				} else {
					msg, err := zfsPoolVolumeSet(vdev, "mountpoint", "none")
					if err != nil {
						logger.Errorf("zfs set failed to unset dataset mountpoint %s", msg)
						return err
					}
				}
			} else {
				err := s.zfsPoolCheck(vdev)
				if err != nil {
					return err
				}

				subvols, err := s.zfsPoolListSubvolumes(vdev)
				if err != nil {
					return err
				}

				if len(subvols) > 0 {
					return fmt.Errorf("Provided ZFS pool (or dataset) isn't empty")
				}

				msg, err := zfsPoolVolumeSet(vdev, "mountpoint", "none")
				if err != nil {
					logger.Errorf("zfs set failed to unset dataset mountpoint %s", msg)
					return err
				}
			}
		}
	}

	// Create default dummy datasets to avoid zfs races during container
	// creation.
	poolName := s.getOnDiskPoolName()
	dataset := fmt.Sprintf("%s/containers", poolName)
	msg, err := zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("failed to create containers dataset: %s", msg)
		return err
	}

	fixperms := shared.VarPath("storage-pools", s.pool.Name, "containers")
	err = os.MkdirAll(fixperms, containersDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Chmod(fixperms, containersDirMode)
	if err != nil {
		logger.Warnf("failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(containersDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/images", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("failed to create images dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "images")
	err = os.MkdirAll(fixperms, imagesDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, imagesDirMode)
	if err != nil {
		logger.Warnf("failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(imagesDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/custom", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("failed to create custom dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "custom")
	err = os.MkdirAll(fixperms, customDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, customDirMode)
	if err != nil {
		logger.Warnf("failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(customDirMode), 8), err)
	}

	dataset = fmt.Sprintf("%s/deleted", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("failed to create deleted dataset: %s", msg)
		return err
	}

	dataset = fmt.Sprintf("%s/snapshots", poolName)
	msg, err = zfsPoolVolumeCreate(dataset, "mountpoint=none")
	if err != nil {
		logger.Errorf("failed to create snapshots dataset: %s", msg)
		return err
	}

	fixperms = shared.VarPath("storage-pools", s.pool.Name, "snapshots")
	err = os.MkdirAll(fixperms, snapshotsDirMode)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Chmod(fixperms, snapshotsDirMode)
	if err != nil {
		logger.Warnf("failed to chmod \"%s\" to \"0%s\": %s", fixperms, strconv.FormatInt(int64(snapshotsDirMode), 8), err)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeClone(source string, name string, dest string, mountpoint string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"clone",
		"-p",
		"-o", fmt.Sprintf("mountpoint=%s", mountpoint),
		"-o", "canmount=noauto",
		fmt.Sprintf("%s/%s@%s", poolName, source, name),
		fmt.Sprintf("%s/%s", poolName, dest))
	if err != nil {
		logger.Errorf("zfs clone failed: %s.", output)
		return fmt.Errorf("Failed to clone the filesystem: %s", output)
	}

	subvols, err := s.zfsPoolListSubvolumes(fmt.Sprintf("%s/%s", poolName, source))
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := s.zfsPoolListSnapshots(sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		destSubvol := dest + strings.TrimPrefix(sub, source)
		snapshotMntPoint := getSnapshotMountPoint(s.pool.Name, destSubvol)

		output, err := shared.RunCommand(
			"zfs",
			"clone",
			"-p",
			"-o", fmt.Sprintf("mountpoint=%s", snapshotMntPoint),
			"-o", "canmount=noauto",
			fmt.Sprintf("%s/%s@%s", poolName, sub, name),
			fmt.Sprintf("%s/%s", poolName, destSubvol))
		if err != nil {
			logger.Errorf("zfs clone failed: %s.", output)
			return fmt.Errorf("Failed to clone the sub-volume: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsFilesystemEntityDelete() error {
	var output string
	var err error
	poolName := s.getOnDiskPoolName()
	if strings.Contains(poolName, "/") {
		// Command to destroy a zfs dataset.
		output, err = shared.RunCommand("zfs", "destroy", "-r", poolName)
	} else {
		// Command to destroy a zfs pool.
		output, err = shared.RunCommand("zpool", "destroy", "-f", poolName)
	}
	if err != nil {
		return fmt.Errorf("Failed to delete the ZFS pool: %s", output)
	}

	// Cleanup storage
	vdev := s.pool.Config["source"]
	if filepath.IsAbs(vdev) && !shared.IsBlockdevPath(vdev) {
		os.RemoveAll(vdev)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeDestroy(path string) error {
	mountpoint, err := s.zfsFilesystemEntityPropertyGet(path, "mountpoint", true)
	if err != nil {
		return err
	}

	if mountpoint != "none" && shared.IsMountPoint(mountpoint) {
		err := syscall.Unmount(mountpoint, syscall.MNT_DETACH)
		if err != nil {
			logger.Errorf("umount failed: %s.", err)
			return err
		}
	}

	poolName := s.getOnDiskPoolName()
	// Due to open fds or kernel refs, this may fail for a bit, give it 10s
	output, err := shared.TryRunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s", poolName, path))

	if err != nil {
		logger.Errorf("zfs destroy failed: %s.", output)
		return fmt.Errorf("Failed to destroy ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeCleanup(path string) error {
	if strings.HasPrefix(path, "deleted/") {
		// Cleanup of filesystems kept for refcount reason
		removablePath, err := s.zfsPoolVolumeSnapshotRemovable(path, "")
		if err != nil {
			return err
		}

		// Confirm that there are no more clones
		if removablePath {
			if strings.Contains(path, "@") {
				// Cleanup snapshots
				err = s.zfsPoolVolumeDestroy(path)
				if err != nil {
					return err
				}

				// Check if the parent can now be deleted
				subPath := strings.SplitN(path, "@", 2)[0]
				snaps, err := s.zfsPoolListSnapshots(subPath)
				if err != nil {
					return err
				}

				if len(snaps) == 0 {
					err := s.zfsPoolVolumeCleanup(subPath)
					if err != nil {
						return err
					}
				}
			} else {
				// Cleanup filesystems
				origin, err := s.zfsFilesystemEntityPropertyGet(path, "origin", true)
				if err != nil {
					return err
				}
				poolName := s.getOnDiskPoolName()
				origin = strings.TrimPrefix(origin, fmt.Sprintf("%s/", poolName))

				err = s.zfsPoolVolumeDestroy(path)
				if err != nil {
					return err
				}

				// Attempt to remove its parent
				if origin != "-" {
					err := s.zfsPoolVolumeCleanup(origin)
					if err != nil {
						return err
					}
				}
			}

			return nil
		}
	} else if strings.HasPrefix(path, "containers") && strings.Contains(path, "@copy-") {
		// Just remove the copy- snapshot for copies of active containers
		err := s.zfsPoolVolumeDestroy(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storageZfs) zfsFilesystemEntityExists(path string, prefixPathWithPool bool) bool {
	output, _ := s.zfsFilesystemEntityPropertyGet(path, "name", prefixPathWithPool)

	// If prefixPathWithPool is false we assume that the path passed in
	// already is a valid zfs entity we want to check for.
	fsToCheck := path
	if prefixPathWithPool {
		fsToCheck = fmt.Sprintf("%s/%s", s.getOnDiskPoolName(), path)
	}
	if output == fsToCheck {
		return true
	}

	return false
}

func (s *storageZfs) zfsFilesystemEntityPropertyGet(path string, key string, prefixPathWithPool bool) (string, error) {
	// If prefixPathWithPool is false we assume that the path passed in
	// already is a valid zfs entity we want to check for.
	fsToCheck := path
	if prefixPathWithPool {
		fsToCheck = fmt.Sprintf("%s/%s", s.getOnDiskPoolName(), path)
	}

	output, err := shared.RunCommand(
		"zfs",
		"get",
		"-H",
		"-p",
		"-o", "value",
		key,
		fsToCheck)
	if err != nil {
		return "", fmt.Errorf("Failed to get ZFS config: %s", output)
	}

	return strings.TrimRight(output, "\n"), nil
}

func (s *storageZfs) zfsPoolVolumeRename(source string, dest string) error {
	var err error
	var output string

	poolName := s.getOnDiskPoolName()
	for i := 0; i < 20; i++ {
		output, err = shared.RunCommand(
			"zfs",
			"rename",
			"-p",
			fmt.Sprintf("%s/%s", poolName, source),
			fmt.Sprintf("%s/%s", poolName, dest))

		// Success
		if err == nil {
			return nil
		}

		// zfs rename can fail because of descendants, yet still manage the rename
		if !s.zfsFilesystemEntityExists(source, true) && s.zfsFilesystemEntityExists(dest, true) {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Timeout
	logger.Errorf("zfs rename failed: %s.", output)
	return fmt.Errorf("Failed to rename ZFS filesystem: %s", output)
}

func (s *storageZfs) zfsPoolVolumeSet(path string, key string, value string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"set",
		fmt.Sprintf("%s=%s", key, value),
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		logger.Errorf("zfs set failed: %s.", output)
		return fmt.Errorf("Failed to set ZFS config: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotCreate(path string, name string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"snapshot",
		"-r",
		fmt.Sprintf("%s/%s@%s", poolName, path, name))
	if err != nil {
		logger.Errorf("zfs snapshot failed: %s.", output)
		return fmt.Errorf("Failed to create ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotDestroy(path string, name string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s@%s", poolName, path, name))
	if err != nil {
		logger.Errorf("zfs destroy failed: %s.", output)
		return fmt.Errorf("Failed to destroy ZFS snapshot: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotRestore(path string, name string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.TryRunCommand(
		"zfs",
		"rollback",
		fmt.Sprintf("%s/%s@%s", poolName, path, name))
	if err != nil {
		logger.Errorf("zfs rollback failed: %s.", output)
		return fmt.Errorf("Failed to restore ZFS snapshot: %s", output)
	}

	subvols, err := s.zfsPoolListSubvolumes(fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		return err
	}

	for _, sub := range subvols {
		snaps, err := s.zfsPoolListSnapshots(sub)
		if err != nil {
			return err
		}

		if !shared.StringInSlice(name, snaps) {
			continue
		}

		output, err := shared.TryRunCommand(
			"zfs",
			"rollback",
			fmt.Sprintf("%s/%s@%s", poolName, sub, name))
		if err != nil {
			logger.Errorf("zfs rollback failed: %s.", output)
			return fmt.Errorf("Failed to restore ZFS sub-volume snapshot: %s", output)
		}
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeSnapshotRename(path string, oldName string, newName string) error {
	poolName := s.getOnDiskPoolName()
	output, err := shared.RunCommand(
		"zfs",
		"rename",
		"-r",
		fmt.Sprintf("%s/%s@%s", poolName, path, oldName),
		fmt.Sprintf("%s/%s@%s", poolName, path, newName))
	if err != nil {
		logger.Errorf("zfs snapshot rename failed: %s.", output)
		return fmt.Errorf("Failed to rename ZFS snapshot: %s", output)
	}

	return nil
}

func zfsMount(poolName string, path string) error {
	output, err := shared.TryRunCommand(
		"zfs",
		"mount",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		return fmt.Errorf("Failed to mount ZFS filesystem: %s", output)
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeMount(path string) error {
	return zfsMount(s.getOnDiskPoolName(), path)
}

func zfsUmount(poolName string, path string, mountpoint string) error {
	output, err := shared.TryRunCommand(
		"zfs",
		"unmount",
		fmt.Sprintf("%s/%s", poolName, path))
	if err != nil {
		logger.Warnf("Failed to unmount ZFS filesystem via zfs unmount: %s. Trying lazy umount (MNT_DETACH)...", output)
		err := tryUnmount(mountpoint, syscall.MNT_DETACH)
		if err != nil {
			logger.Warnf("Failed to unmount ZFS filesystem via lazy umount (MNT_DETACH)...")
			return err
		}
	}

	return nil
}

func (s *storageZfs) zfsPoolVolumeUmount(path string, mountpoint string) error {
	return zfsUmount(s.getOnDiskPoolName(), path, mountpoint)
}

func (s *storageZfs) zfsPoolListSubvolumes(path string) ([]string, error) {
	output, err := shared.RunCommand(
		"zfs",
		"list",
		"-t", "filesystem",
		"-o", "name",
		"-H",
		"-r", path)
	if err != nil {
		logger.Errorf("zfs list failed: %s.", output)
		return []string{}, fmt.Errorf("Failed to list ZFS filesystems: %s", output)
	}

	children := []string{}
	for _, entry := range strings.Split(output, "\n") {
		if entry == "" {
			continue
		}

		if entry == path {
			continue
		}

		poolName := s.getOnDiskPoolName()
		children = append(children, strings.TrimPrefix(entry, fmt.Sprintf("%s/", poolName)))
	}

	return children, nil
}

func (s *storageZfs) zfsPoolListSnapshots(path string) ([]string, error) {
	poolName := s.getOnDiskPoolName()
	path = strings.TrimRight(path, "/")
	fullPath := poolName
	if path != "" {
		fullPath = fmt.Sprintf("%s/%s", poolName, path)
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
		logger.Errorf("zfs list failed: %s.", output)
		return []string{}, fmt.Errorf("Failed to list ZFS snapshots: %s", output)
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

func (s *storageZfs) zfsPoolVolumeSnapshotRemovable(path string, name string) (bool, error) {
	var snap string
	if name == "" {
		snap = path
	} else {
		snap = fmt.Sprintf("%s@%s", path, name)
	}

	clones, err := s.zfsFilesystemEntityPropertyGet(snap, "clones", true)
	if err != nil {
		return false, err
	}

	if clones == "-" || clones == "" {
		return true, nil
	}

	return false, nil
}

func (s *storageZfs) zfsPoolGetUsers() ([]string, error) {
	poolName := s.getOnDiskPoolName()
	subvols, err := s.zfsPoolListSubvolumes(poolName)
	if err != nil {
		return []string{}, err
	}

	exceptions := []string{
		"containers",
		"images",
		"snapshots",
		"deleted",
		"deleted/containers",
		"deleted/images"}

	users := []string{}
	for _, subvol := range subvols {
		path := strings.Split(subvol, "/")

		// Only care about plausible LXD paths
		if !shared.StringInSlice(path[0], exceptions) {
			continue
		}

		// Ignore empty paths
		if shared.StringInSlice(subvol, exceptions) {
			continue
		}

		users = append(users, subvol)
	}

	return users, nil
}

func zfsFilesystemEntityExists(zfsEntity string) bool {
	output, err := shared.RunCommand(
		"zfs",
		"get",
		"type",
		"-H",
		"-o",
		"name",
		zfsEntity)
	if err != nil {
		return false
	}

	detectedName := strings.TrimSpace(output)
	if detectedName != zfsEntity {
		return false
	}

	return true
}
