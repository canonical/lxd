package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	driver "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/units"
)

// For 'dir' storage backend.
func dirSnapshotDeleteInternal(projectName, poolName string, snapshotName string) error {
	snapshotContainerMntPoint := driver.GetSnapshotMountPoint(projectName, poolName, snapshotName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	sourceContainerName, _, _ := shared.InstanceGetParentAndSnapshotName(snapshotName)
	snapshotContainerPath := driver.GetSnapshotMountPoint(projectName, poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceContainerName))
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// For 'btrfs' storage backend.
func btrfsSubVolumeCreate(subvol string) error {
	parentDestPath := filepath.Dir(subvol)
	if !shared.PathExists(parentDestPath) {
		err := os.MkdirAll(parentDestPath, 0711)
		if err != nil {
			return err
		}
	}

	_, err := shared.RunCommand(
		"btrfs",
		"subvolume",
		"create",
		subvol)
	if err != nil {
		return err
	}

	return nil
}

func btrfsSnapshotDeleteInternal(projectName, poolName string, snapshotName string) error {
	snapshotSubvolumeName := driver.GetSnapshotMountPoint(projectName, poolName, snapshotName)
	// Also delete any leftover .ro snapshot.
	roSnapshotSubvolumeName := fmt.Sprintf("%s.ro", snapshotSubvolumeName)
	names := []string{snapshotSubvolumeName, roSnapshotSubvolumeName}
	for _, name := range names {
		if shared.PathExists(name) && btrfsIsSubVolume(name) {
			err := btrfsSubVolumesDelete(name)
			if err != nil {
				return err
			}
		}
	}

	sourceSnapshotMntPoint := shared.VarPath("snapshots", project.Prefix(projectName, snapshotName))
	os.Remove(sourceSnapshotMntPoint)
	os.Remove(snapshotSubvolumeName)

	sourceName, _, _ := shared.InstanceGetParentAndSnapshotName(snapshotName)
	snapshotSubvolumePath := driver.GetSnapshotMountPoint(projectName, poolName, sourceName)
	os.Remove(snapshotSubvolumePath)
	if !shared.PathExists(snapshotSubvolumePath) {
		snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceName))
		os.Remove(snapshotMntPointSymlink)
	}

	return nil
}

func btrfsSubVolumeQGroup(subvol string) (string, error) {
	output, err := shared.RunCommand(
		"btrfs",
		"qgroup",
		"show",
		"-e",
		"-f",
		subvol)

	if err != nil {
		return "", fmt.Errorf("Quotas disabled on filesystem")
	}

	var qgroup string
	for _, line := range strings.Split(output, "\n") {
		if line == "" || strings.HasPrefix(line, "qgroupid") || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		qgroup = fields[0]
	}

	if qgroup == "" {
		return "", fmt.Errorf("Unable to find quota group")
	}

	return qgroup, nil
}

func btrfsSubVolumeDelete(subvol string) error {
	// Attempt (but don't fail on) to delete any qgroup on the subvolume
	qgroup, err := btrfsSubVolumeQGroup(subvol)
	if err == nil {
		shared.RunCommand(
			"btrfs",
			"qgroup",
			"destroy",
			qgroup,
			subvol)
	}

	// Attempt to make the subvolume writable
	shared.RunCommand("btrfs", "property", "set", subvol, "ro", "false")

	// Delete the subvolume itself
	_, err = shared.RunCommand(
		"btrfs",
		"subvolume",
		"delete",
		subvol)

	return err
}

func btrfsSubVolumesDelete(subvol string) error {
	// Delete subsubvols.
	subsubvols, err := btrfsSubVolumesGet(subvol)
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(subsubvols)))

	for _, subsubvol := range subsubvols {
		err := btrfsSubVolumeDelete(path.Join(subvol, subsubvol))
		if err != nil {
			return err
		}
	}

	// Delete the subvol itself
	err = btrfsSubVolumeDelete(subvol)
	if err != nil {
		return err
	}

	return nil
}

func btrfsSnapshot(s *state.State, source string, dest string, readonly bool) error {
	var output string
	var err error
	if readonly && !s.OS.RunningInUserNS {
		output, err = shared.RunCommand(
			"btrfs",
			"subvolume",
			"snapshot",
			"-r",
			source,
			dest)
	} else {
		output, err = shared.RunCommand(
			"btrfs",
			"subvolume",
			"snapshot",
			source,
			dest)
	}
	if err != nil {
		return fmt.Errorf(
			"subvolume snapshot failed, source=%s, dest=%s, output=%s",
			source,
			dest,
			output,
		)
	}

	return err
}

func btrfsIsSubVolume(subvolPath string) bool {
	fs := unix.Stat_t{}
	err := unix.Lstat(subvolPath, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID
	if fs.Ino != 256 {
		return false
	}

	return true
}

func btrfsSubVolumeIsRo(path string) bool {
	output, err := shared.RunCommand("btrfs", "property", "get", "-ts", path)
	if err != nil {
		return false
	}

	return strings.HasPrefix(string(output), "ro=true")
}

func btrfsSubVolumeMakeRo(path string) error {
	_, err := shared.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "true")
	return err
}

func btrfsSubVolumeMakeRw(path string) error {
	_, err := shared.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "false")
	return err
}

func btrfsSubVolumesGet(path string) ([]string, error) {
	result := []string{}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Unprivileged users can't get to fs internals
	filepath.Walk(path, func(fpath string, fi os.FileInfo, err error) error {
		// Skip walk errors
		if err != nil {
			return nil
		}

		// Ignore the base path
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories
		if !fi.IsDir() {
			return nil
		}

		// Check if a btrfs subvolume
		if btrfsIsSubVolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

// For 'zfs' storage backend.
func zfsPoolListSnapshots(pool string, path string) ([]string, error) {
	path = strings.TrimRight(path, "/")
	fullPath := pool
	if path != "" {
		fullPath = fmt.Sprintf("%s/%s", pool, path)
	}

	output, err := shared.RunCommand("zfs", "list", "-t", "snapshot", "-o", "name", "-H", "-d", "1", "-s", "creation", "-r", fullPath)
	if err != nil {
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

func zfsSnapshotDeleteInternal(projectName, poolName string, ctName string, onDiskPoolName string) error {
	sourceContainerName, sourceContainerSnapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(ctName)
	snapName := fmt.Sprintf("snapshot-%s", sourceContainerSnapOnlyName)

	if zfsFilesystemEntityExists(onDiskPoolName,
		fmt.Sprintf("containers/%s@%s",
			project.Prefix(projectName, sourceContainerName), snapName)) {
		removable, err := zfsPoolVolumeSnapshotRemovable(onDiskPoolName,
			fmt.Sprintf("containers/%s",
				project.Prefix(projectName, sourceContainerName)),
			snapName)
		if err != nil {
			return err
		}

		if removable {
			err = zfsPoolVolumeSnapshotDestroy(onDiskPoolName,
				fmt.Sprintf("containers/%s",
					project.Prefix(projectName, sourceContainerName)),
				snapName)
		} else {
			err = zfsPoolVolumeSnapshotRename(onDiskPoolName,
				fmt.Sprintf("containers/%s",
					project.Prefix(projectName, sourceContainerName)),
				snapName,
				fmt.Sprintf("copy-%s", uuid.NewRandom().String()))
		}
		if err != nil {
			return err
		}
	}

	// Delete the snapshot on its storage pool:
	// ${POOL}/snapshots/<snapshot_name>
	snapshotContainerMntPoint := driver.GetSnapshotMountPoint(projectName, poolName, ctName)
	if shared.PathExists(snapshotContainerMntPoint) {
		err := os.RemoveAll(snapshotContainerMntPoint)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the snapshot symlink:
	// ${LXD_DIR}/snapshots/<container_name> to ${POOL}/snapshots/<container_name>
	// by checking if the directory is empty.
	snapshotContainerPath := driver.GetSnapshotMountPoint(projectName, poolName, sourceContainerName)
	empty, _ := shared.PathIsEmpty(snapshotContainerPath)
	if empty == true {
		// Remove the snapshot directory for the container:
		// ${POOL}/snapshots/<source_container_name>
		err := os.Remove(snapshotContainerPath)
		if err != nil {
			return err
		}

		snapshotSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceContainerName))
		if shared.PathExists(snapshotSymlink) {
			err := os.Remove(snapshotSymlink)
			if err != nil {
				return err
			}
		}
	}

	// Legacy
	snapPath := shared.VarPath(fmt.Sprintf("snapshots/%s/%s.zfs", project.Prefix(projectName, sourceContainerName), sourceContainerSnapOnlyName))
	if shared.PathExists(snapPath) {
		err := os.Remove(snapPath)
		if err != nil {
			return err
		}
	}

	// Legacy
	parent := shared.VarPath(fmt.Sprintf("snapshots/%s", project.Prefix(projectName, sourceContainerName)))
	if ok, _ := shared.PathIsEmpty(parent); ok {
		err := os.Remove(parent)
		if err != nil {
			return err
		}
	}

	return nil
}

func zfsFilesystemEntityExists(pool string, path string) bool {
	vdev := pool
	if path != "" {
		vdev = fmt.Sprintf("%s/%s", pool, path)
	}

	output, err := shared.RunCommand("zfs", "get", "-H", "-o", "name", "type", vdev)
	if err != nil {
		return false
	}

	detectedName := strings.TrimSpace(output)
	return detectedName == vdev
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

func zfsFilesystemEntityPropertyGet(pool string, path string, key string) (string, error) {
	entity := pool
	if path != "" {
		entity = fmt.Sprintf("%s/%s", pool, path)
	}

	output, err := shared.RunCommand("zfs", "get", "-H", "-p", "-o", "value", key, entity)
	if err != nil {
		return "", errors.Wrap(err, "Failed to get ZFS config")
	}

	return strings.TrimRight(output, "\n"), nil
}

func zfsPoolVolumeSnapshotDestroy(pool, path string, name string) error {
	_, err := shared.RunCommand("zfs", "destroy", "-r", fmt.Sprintf("%s/%s@%s", pool, path, name))
	if err != nil {
		return errors.Wrap(err, "Failed to destroy ZFS snapshot")
	}

	return nil
}

func zfsPoolVolumeSnapshotRename(pool string, path string, oldName string, newName string) error {
	_, err := shared.RunCommand("zfs", "rename", "-r", fmt.Sprintf("%s/%s@%s", pool, path, oldName), fmt.Sprintf("%s/%s@%s", pool, path, newName))
	if err != nil {
		return errors.Wrap(err, "Failed to rename ZFS snapshot")
	}

	return nil
}

// For 'lvm' storage backend.
func lvmLVRename(vgName string, oldName string, newName string) error {
	_, err := shared.TryRunCommand("lvrename", vgName, oldName, newName)
	if err != nil {
		return fmt.Errorf("could not rename volume group from \"%s\" to \"%s\": %v", oldName, newName, err)
	}

	return nil
}

func lvmLVExists(lvName string) (bool, error) {
	_, err := shared.RunCommand("lvs", "--noheadings", "-o", "lv_attr", lvName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 5 {
					// logical volume not found
					return false, nil
				}
			}
		}

		return false, fmt.Errorf("error checking for logical volume \"%s\"", lvName)
	}

	return true, nil
}

func lvmVGActivate(lvmVolumePath string) error {
	_, err := shared.TryRunCommand("vgchange", "-ay", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("could not activate volume group \"%s\": %v", lvmVolumePath, err)
	}

	return nil
}

func lvmNameToLVName(containerName string) string {
	lvName := strings.Replace(containerName, "-", "--", -1)
	return strings.Replace(lvName, shared.SnapshotDelimiter, "-", -1)
}

func lvmDevPath(projectName, lvmPool string, volumeType string, lvmVolume string) string {
	lvmVolume = project.Prefix(projectName, lvmVolume)
	if volumeType == "" {
		return fmt.Sprintf("/dev/%s/%s", lvmPool, lvmVolume)
	}

	return fmt.Sprintf("/dev/%s/%s_%s", lvmPool, volumeType, lvmVolume)
}

func lvmGetLVSize(lvPath string) (string, error) {
	msg, err := shared.TryRunCommand("lvs", "--noheadings", "-o", "size", "--nosuffix", "--units", "b", lvPath)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve size of logical volume: %s: %s", string(msg), err)
	}

	sizeString := string(msg)
	sizeString = strings.TrimSpace(sizeString)
	size, err := strconv.ParseInt(sizeString, 10, 64)
	if err != nil {
		return "", err
	}

	detectedSize := units.GetByteSizeString(size, 0)

	return detectedSize, nil
}

func lvmLVName(lvmPool string, volumeType string, lvmVolume string) string {
	if volumeType == "" {
		return fmt.Sprintf("%s/%s", lvmPool, lvmVolume)
	}

	return fmt.Sprintf("%s/%s_%s", lvmPool, volumeType, lvmVolume)
}

func lvmContainerDeleteInternal(projectName, poolName string, ctName string, isSnapshot bool, vgName string, ctPath string) error {
	containerMntPoint := ""
	containerLvmName := lvmNameToLVName(ctName)
	if isSnapshot {
		containerMntPoint = driver.GetSnapshotMountPoint(projectName, poolName, ctName)
	} else {
		containerMntPoint = driver.GetContainerMountPoint(projectName, poolName, ctName)
	}

	if shared.IsMountPoint(containerMntPoint) {
		err := storageDrivers.TryUnmount(containerMntPoint, 0)
		if err != nil {
			return fmt.Errorf(`Failed to unmount container path `+
				`"%s": %s`, containerMntPoint, err)
		}
	}

	containerLvmDevPath := lvmDevPath(projectName, vgName,
		storagePoolVolumeAPIEndpointContainers, containerLvmName)

	lvExists, _ := lvmLVExists(containerLvmDevPath)
	if lvExists {
		err := lvmRemoveLV(projectName, vgName, storagePoolVolumeAPIEndpointContainers, containerLvmName)
		if err != nil {
			return err
		}
	}

	var err error
	if isSnapshot {
		sourceName, _, _ := shared.InstanceGetParentAndSnapshotName(ctName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", poolName, "containers-snapshots", project.Prefix(projectName, sourceName))
		snapshotMntPointSymlink := shared.VarPath("snapshots", project.Prefix(projectName, sourceName))
		err = deleteSnapshotMountpoint(containerMntPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
	} else {
		err = deleteContainerMountpoint(containerMntPoint, ctPath, "lvm")
	}
	if err != nil {
		return err
	}

	return nil
}

func lvmRemoveLV(project, vgName string, volumeType string, lvName string) error {
	lvmVolumePath := lvmDevPath(project, vgName, volumeType, lvName)

	_, err := shared.TryRunCommand("lvremove", "-f", lvmVolumePath)
	if err != nil {
		return fmt.Errorf("Could not remove LV named %s: %v", lvName, err)
	}

	return nil
}
