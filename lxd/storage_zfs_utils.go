package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// zfsModuleVersionGet returhs the ZFS module version
func zfsModuleVersionGet() (string, error) {
	zfsVersion, err := ioutil.ReadFile("/sys/module/zfs/version")
	if err != nil {
		return "", fmt.Errorf("could not determine ZFS module version")
	}

	return strings.TrimSpace(string(zfsVersion)), nil
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

func zfsPoolCreate(pool string, vdev string) error {
	var output string
	var err error
	if pool == "" {
		output, err := shared.RunCommand(
			"zfs", "create", "-p", "-o", "mountpoint=none", vdev)
		if err != nil {
			logger.Errorf("zfs create failed: %s.", output)
			return fmt.Errorf("Failed to create ZFS filesystem: %s", output)
		}
	} else {
		output, err = shared.RunCommand(
			"zpool", "create", pool, vdev, "-f", "-m", "none", "-O", "compression=on")
		if err != nil {
			logger.Errorf("zfs create failed: %s.", output)
			return fmt.Errorf("Failed to create the ZFS pool: %s", output)
		}
	}

	return nil
}

func zfsPoolVolumeClone(pool string, source string, name string, dest string, mountpoint string) error {
	output, err := shared.RunCommand(
		"zfs",
		"clone",
		"-p",
		"-o", fmt.Sprintf("mountpoint=%s", mountpoint),
		"-o", "canmount=noauto",
		fmt.Sprintf("%s/%s@%s", pool, source, name),
		fmt.Sprintf("%s/%s", pool, dest))
	if err != nil {
		logger.Errorf("zfs clone failed: %s.", output)
		return fmt.Errorf("Failed to clone the filesystem: %s", output)
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
		snapshotMntPoint := getSnapshotMountPoint(pool, destSubvol)

		output, err := shared.RunCommand(
			"zfs",
			"clone",
			"-p",
			"-o", fmt.Sprintf("mountpoint=%s", snapshotMntPoint),
			"-o", "canmount=noauto",
			fmt.Sprintf("%s/%s@%s", pool, sub, name),
			fmt.Sprintf("%s/%s", pool, destSubvol))
		if err != nil {
			logger.Errorf("zfs clone failed: %s.", output)
			return fmt.Errorf("Failed to clone the sub-volume: %s", output)
		}
	}

	return nil
}

func zfsFilesystemEntityDelete(vdev string, pool string) error {
	var output string
	var err error
	if strings.Contains(pool, "/") {
		// Command to destroy a zfs dataset.
		output, err = shared.RunCommand("zfs", "destroy", "-r", pool)
	} else {
		// Command to destroy a zfs pool.
		output, err = shared.RunCommand("zpool", "destroy", "-f", pool)
	}
	if err != nil {
		return fmt.Errorf("Failed to delete the ZFS pool: %s", output)
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
		err := syscall.Unmount(mountpoint, syscall.MNT_DETACH)
		if err != nil {
			logger.Errorf("umount failed: %s.", err)
			return err
		}
	}

	// Due to open fds or kernel refs, this may fail for a bit, give it 10s
	output, err := shared.TryRunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s", pool, path))

	if err != nil {
		logger.Errorf("zfs destroy failed: %s.", output)
		return fmt.Errorf("Failed to destroy ZFS filesystem: %s", output)
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
	output, err := shared.RunCommand(
		"zfs",
		"get",
		"-H",
		"-p",
		"-o", "value",
		key,
		fmt.Sprintf("%s/%s", pool, path))
	if err != nil {
		return "", fmt.Errorf("Failed to get ZFS config: %s", output)
	}

	return strings.TrimRight(output, "\n"), nil
}

func zfsPoolVolumeRename(pool string, source string, dest string) error {
	var err error
	var output string

	for i := 0; i < 20; i++ {
		output, err = shared.RunCommand(
			"zfs",
			"rename",
			"-p",
			fmt.Sprintf("%s/%s", pool, source),
			fmt.Sprintf("%s/%s", pool, dest))

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
	logger.Errorf("zfs rename failed: %s.", output)
	return fmt.Errorf("Failed to rename ZFS filesystem: %s", output)
}

func zfsPoolVolumeSet(pool string, path string, key string, value string) error {
	vdev := pool
	if path != "" {
		vdev = fmt.Sprintf("%s/%s", pool, path)
	}
	output, err := shared.RunCommand(
		"zfs",
		"set",
		fmt.Sprintf("%s=%s", key, value),
		vdev)
	if err != nil {
		logger.Errorf("zfs set failed: %s.", output)
		return fmt.Errorf("Failed to set ZFS config: %s", output)
	}

	return nil
}

func zfsPoolVolumeSnapshotCreate(pool string, path string, name string) error {
	output, err := shared.RunCommand(
		"zfs",
		"snapshot",
		"-r",
		fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		logger.Errorf("zfs snapshot failed: %s.", output)
		return fmt.Errorf("Failed to create ZFS snapshot: %s", output)
	}

	return nil
}

func zfsPoolVolumeSnapshotDestroy(pool, path string, name string) error {
	output, err := shared.RunCommand(
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		logger.Errorf("zfs destroy failed: %s.", output)
		return fmt.Errorf("Failed to destroy ZFS snapshot: %s", output)
	}

	return nil
}

func zfsPoolVolumeSnapshotRestore(pool string, path string, name string) error {
	output, err := shared.TryRunCommand(
		"zfs",
		"rollback",
		fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		logger.Errorf("zfs rollback failed: %s.", output)
		return fmt.Errorf("Failed to restore ZFS snapshot: %s", output)
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

		output, err := shared.TryRunCommand(
			"zfs",
			"rollback",
			fmt.Sprintf("%s/%s@%s", pool, sub, name))
		if err != nil {
			logger.Errorf("zfs rollback failed: %s.", output)
			return fmt.Errorf("Failed to restore ZFS sub-volume snapshot: %s", output)
		}
	}

	return nil
}

func zfsPoolVolumeSnapshotRename(pool string, path string, oldName string, newName string) error {
	output, err := shared.RunCommand(
		"zfs",
		"rename",
		"-r",
		fmt.Sprintf("%s/%s@%s", pool, path, oldName),
		fmt.Sprintf("%s/%s@%s", pool, path, newName))
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

func zfsPoolListSubvolumes(pool string, path string) ([]string, error) {
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
		"type",
		"-H",
		"-o",
		"name",
		vdev)
	if err != nil {
		return false
	}

	detectedName := strings.TrimSpace(output)
	return detectedName == vdev
}

func storageEntitySetQuota(poolName string, volumeType int, volumeName string,
	refquota bool, size int64, data interface{}) error {
	logger.Debugf(`Setting ZFS quota for "%s"`, volumeName)

	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return fmt.Errorf("Invalid storage type")
	}

	var c container
	var fs string
	switch volumeType {
	case storagePoolVolumeTypeContainer:
		c = data.(container)
		fs = fmt.Sprintf("containers/%s", c.Name())
	case storagePoolVolumeTypeCustom:
		fs = fmt.Sprintf("custom/%s", volumeName)
	case storagePoolVolumeTypeImage:
		fs = fmt.Sprintf("images/%s", volumeName)
	}

	property := "quota"
	if refquota {
		property = "refquota"
	}

	var err error
	if size > 0 {
		err = zfsPoolVolumeSet(poolName, fs, property, fmt.Sprintf("%d", size))
	} else {
		err = zfsPoolVolumeSet(poolName, fs, property, "none")
	}

	if err != nil {
		return err
	}

	logger.Debugf(`Set ZFS quota for "%s"`, volumeName)
	return nil
}
