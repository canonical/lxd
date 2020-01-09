package drivers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/units"
)

// wipeDirectory empties the contents of a directory, but leaves it in place.
func wipeDirectory(path string) error {
	// List all entries.
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
	}

	// Individually wipe all entries.
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		err := os.RemoveAll(entryPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func forceUnmount(path string) (bool, error) {
	unmounted := false

	for {
		// Check if already unmounted
		if !shared.IsMountPoint(path) {
			return unmounted, nil
		}

		// Try a clean unmount first
		err := TryUnmount(path, 0)
		if err != nil {
			// Fallback to lazy unmounting
			err = unix.Unmount(path, unix.MNT_DETACH)
			if err != nil {
				return false, err
			}
		}

		unmounted = true
	}
}

func mountReadOnly(srcPath string, dstPath string) (bool, error) {
	// Check if already mounted.
	if shared.IsMountPoint(dstPath) {
		return false, nil
	}

	// Create a mount entry.
	err := TryMount(srcPath, dstPath, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	// Make it read-only.
	err = TryMount("", dstPath, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
	if err != nil {
		forceUnmount(dstPath)
		return false, err
	}

	return true, nil
}

func sameMount(srcPath string, dstPath string) bool {
	// Get the source vfs path information
	var srcFsStat unix.Statfs_t
	err := unix.Statfs(srcPath, &srcFsStat)
	if err != nil {
		return false
	}

	// Get the destination vfs path information
	var dstFsStat unix.Statfs_t
	err = unix.Statfs(dstPath, &dstFsStat)
	if err != nil {
		return false
	}

	// Compare statfs
	if srcFsStat.Type != dstFsStat.Type || srcFsStat.Fsid != dstFsStat.Fsid {
		return false
	}

	// Get the source path information
	var srcStat unix.Stat_t
	err = unix.Stat(srcPath, &srcStat)
	if err != nil {
		return false
	}

	// Get the destination path information
	var dstStat unix.Stat_t
	err = unix.Stat(dstPath, &dstStat)
	if err != nil {
		return false
	}

	// Compare inode
	if srcStat.Ino != dstStat.Ino {
		return false
	}

	return true
}

// TryMount tries mounting a filesystem multiple times. This is useful for unreliable backends.
func TryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	// Attempt 20 mounts over 10s
	for i := 0; i < 20; i++ {
		err = unix.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

// TryUnmount tries unmounting a filesystem multiple times. This is useful for unreliable backends.
func TryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = unix.Unmount(path, flags)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func tryExists(path string) bool {
	// Attempt 20 checks over 10s
	for i := 0; i < 20; i++ {
		if shared.PathExists(path) {
			return true
		}

		time.Sleep(500 * time.Millisecond)
	}

	return false
}

func fsUUID(path string) (string, error) {
	return shared.RunCommand("blkid", "-s", "UUID", "-o", "value", path)
}

func hasFilesystem(path string, fsType int64) bool {
	fs := unix.Statfs_t{}

	err := unix.Statfs(path, &fs)
	if err != nil {
		return false
	}

	if int64(fs.Type) != fsType {
		return false
	}

	return true
}

// GetPoolMountPath returns the mountpoint of the given pool.
// {LXD_DIR}/storage-pools/<pool>
func GetPoolMountPath(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// GetVolumeMountPath returns the mount path for a specific volume based on its pool and type and
// whether it is a snapshot or not. For VolumeTypeImage the volName is the image fingerprint.
func GetVolumeMountPath(poolName string, volType VolumeType, volName string) string {
	if shared.IsSnapshot(volName) {
		return shared.VarPath("storage-pools", poolName, fmt.Sprintf("%s-snapshots", string(volType)), volName)
	}

	return shared.VarPath("storage-pools", poolName, string(volType), volName)
}

// GetVolumeSnapshotDir gets the snapshot mount directory for the parent volume.
func GetVolumeSnapshotDir(poolName string, volType VolumeType, volName string) string {
	parent, _, _ := shared.InstanceGetParentAndSnapshotName(volName)
	return shared.VarPath("storage-pools", poolName, fmt.Sprintf("%s-snapshots", string(volType)), parent)
}

// GetSnapshotVolumeName returns the full volume name for a parent volume and snapshot name.
func GetSnapshotVolumeName(parentName, snapshotName string) string {
	return fmt.Sprintf("%s%s%s", parentName, shared.SnapshotDelimiter, snapshotName)
}

// createParentSnapshotDirIfMissing creates the parent directory for volume snapshots
func createParentSnapshotDirIfMissing(poolName string, volType VolumeType, volName string) error {
	snapshotsPath := GetVolumeSnapshotDir(poolName, volType, volName)

	// If it's missing, create it.
	if !shared.PathExists(snapshotsPath) {
		return os.Mkdir(snapshotsPath, 0700)
	}

	return nil
}

// deleteParentSnapshotDirIfEmpty removes the parent snapshot directory if it is empty.
// It accepts the pool name, volume type and parent volume name.
func deleteParentSnapshotDirIfEmpty(poolName string, volType VolumeType, volName string) error {
	snapshotsPath := GetVolumeSnapshotDir(poolName, volType, volName)

	// If it exists, try to delete it.
	if shared.PathExists(snapshotsPath) {
		isEmpty, err := shared.PathIsEmpty(snapshotsPath)
		if err != nil {
			return err
		}

		if isEmpty {
			err := os.Remove(snapshotsPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// createSparseFile creates a sparse empty file at specified location with specified size.
func createSparseFile(filePath string, sizeBytes int64) error {
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("Failed to open %s: %s", filePath, err)
	}
	defer f.Close()

	err = f.Chmod(0600)
	if err != nil {
		return fmt.Errorf("Failed to chmod %s: %s", filePath, err)
	}

	err = f.Truncate(sizeBytes)
	if err != nil {
		return fmt.Errorf("Failed to create sparse file %s: %s", filePath, err)
	}

	return nil
}

// ensureVolumeBlockFile creates or resizes the raw block file for a volume.
func ensureVolumeBlockFile(vol Volume, path string) error {
	blockSize := vol.ExpandedConfig("size")
	if blockSize == "" {
		blockSize = defaultBlockSize
	}

	blockSizeBytes, err := units.ParseByteSizeString(blockSize)
	if err != nil {
		return err
	}

	if shared.PathExists(path) {
		_, err = shared.RunCommand("qemu-img", "resize", "-f", "raw", path, fmt.Sprintf("%d", blockSizeBytes))
		if err != nil {
			return fmt.Errorf("Failed resizing disk image %s to size %s: %v", path, blockSize, err)
		}
	} else {
		// If path doesn't exist, then there has been no filler function
		// supplied to create it from another source. So instead create an empty
		// volume (use for PXE booting a VM).
		_, err = shared.RunCommand("qemu-img", "create", "-f", "raw", path, fmt.Sprintf("%d", blockSizeBytes))
		if err != nil {
			return fmt.Errorf("Failed creating disk image %s as size %s: %v", path, blockSize, err)
		}
	}

	return nil
}

// mkfsOptions represents options for filesystem creation.
type mkfsOptions struct {
	Label string
}

// makeFSType creates the provided filesystem.
func makeFSType(path string, fsType string, options *mkfsOptions) (string, error) {
	var err error
	var msg string

	fsOptions := options
	if fsOptions == nil {
		fsOptions = &mkfsOptions{}
	}

	cmd := []string{fmt.Sprintf("mkfs.%s", fsType), path}
	if fsOptions.Label != "" {
		cmd = append(cmd, "-L", fsOptions.Label)
	}

	if fsType == "ext4" {
		cmd = append(cmd, "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0")
	}

	msg, err = shared.TryRunCommand(cmd[0], cmd[1:]...)
	if err != nil {
		return msg, err
	}

	return "", nil
}

// mountOption represents an individual mount option.
type mountOption struct {
	capture bool
	flag    uintptr
}

// mountOptions represents a list of possible mount options.
var mountOptions = map[string]mountOption{
	"async":         {false, unix.MS_SYNCHRONOUS},
	"atime":         {false, unix.MS_NOATIME},
	"bind":          {true, unix.MS_BIND},
	"defaults":      {true, 0},
	"dev":           {false, unix.MS_NODEV},
	"diratime":      {false, unix.MS_NODIRATIME},
	"dirsync":       {true, unix.MS_DIRSYNC},
	"exec":          {false, unix.MS_NOEXEC},
	"lazytime":      {true, unix.MS_LAZYTIME},
	"mand":          {true, unix.MS_MANDLOCK},
	"noatime":       {true, unix.MS_NOATIME},
	"nodev":         {true, unix.MS_NODEV},
	"nodiratime":    {true, unix.MS_NODIRATIME},
	"noexec":        {true, unix.MS_NOEXEC},
	"nomand":        {false, unix.MS_MANDLOCK},
	"norelatime":    {false, unix.MS_RELATIME},
	"nostrictatime": {false, unix.MS_STRICTATIME},
	"nosuid":        {true, unix.MS_NOSUID},
	"rbind":         {true, unix.MS_BIND | unix.MS_REC},
	"relatime":      {true, unix.MS_RELATIME},
	"remount":       {true, unix.MS_REMOUNT},
	"ro":            {true, unix.MS_RDONLY},
	"rw":            {false, unix.MS_RDONLY},
	"strictatime":   {true, unix.MS_STRICTATIME},
	"suid":          {false, unix.MS_NOSUID},
	"sync":          {true, unix.MS_SYNCHRONOUS},
}

// resolveMountOptions resolves the provided mount options.
func resolveMountOptions(options string) (uintptr, string) {
	mountFlags := uintptr(0)
	tmp := strings.SplitN(options, ",", -1)
	for i := 0; i < len(tmp); i++ {
		opt := tmp[i]
		do, ok := mountOptions[opt]
		if !ok {
			continue
		}

		if do.capture {
			mountFlags |= do.flag
		} else {
			mountFlags &= ^do.flag
		}

		copy(tmp[i:], tmp[i+1:])
		tmp[len(tmp)-1] = ""
		tmp = tmp[:len(tmp)-1]
		i--
	}

	return mountFlags, strings.Join(tmp, ",")
}

// shrinkFileSystem shrinks a filesystem if it is supported. Ext4 volumes will be unmounted temporarily if needed.
func shrinkFileSystem(fsType string, devPath string, vol Volume, byteSize int64) error {
	// The smallest unit that resize2fs accepts in byte size (rather than blocks) is kilobytes.
	strSize := fmt.Sprintf("%dK", byteSize/1024)

	switch fsType {
	case "": // if not specified, default to ext4.
		fallthrough
	case "xfs":
		return fmt.Errorf(`Shrinking not supported for filesystem type "%s". A dump, mkfs, and restore are required`, fsType)
	case "ext4":
		return vol.UnmountTask(func(op *operations.Operation) error {
			_, err := shared.TryRunCommand("e2fsck", "-f", "-y", devPath)
			if err != nil {
				return err
			}

			_, err = shared.TryRunCommand("resize2fs", devPath, strSize)
			if err != nil {
				return err
			}

			return nil
		}, nil)
	case "btrfs":
		return vol.MountTask(func(mountPath string, op *operations.Operation) error {
			_, err := shared.TryRunCommand("btrfs", "filesystem", "resize", strSize, mountPath)
			if err != nil {
				return err
			}

			return nil
		}, nil)
	default:
		return fmt.Errorf(`Shrinking not supported for filesystem type "%s"`, fsType)
	}
}

// growFileSystem grows a filesystem if it is supported. The volume will be mounted temporarily if needed.
func growFileSystem(fsType string, devPath string, vol Volume) error {
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var msg string
		var err error
		switch fsType {
		case "": // if not specified, default to ext4
			fallthrough
		case "ext4":
			msg, err = shared.TryRunCommand("resize2fs", devPath)
		case "xfs":
			msg, err = shared.TryRunCommand("xfs_growfs", devPath)
		case "btrfs":
			msg, err = shared.TryRunCommand("btrfs", "filesystem", "resize", "max", mountPath)
		default:
			return fmt.Errorf(`Growing not supported for filesystem type "%s"`, fsType)
		}

		if err != nil {
			errorMsg := fmt.Sprintf(`Could not extend underlying %s filesystem for "%s": %s`, fsType, devPath, msg)
			return fmt.Errorf(errorMsg)
		}

		return nil
	}, nil)
}

// regenerateFilesystemUUID changes the filesystem UUID to a new randomly generated one if the fsType requires it.
// Otherwise this function does nothing.
func regenerateFilesystemUUID(fsType, devPath string) error {
	switch fsType {
	case "btrfs":
		return regenerateFilesystemBTRFSUUID(devPath)
	case "xfs":
		return regenerateFilesystemXFSUUID(devPath)
	}

	return nil
}

// regenerateFilesystemBTRFSUUID changes the BTRFS filesystem UUID to a new randomly generated one.
func regenerateFilesystemBTRFSUUID(devPath string) error {
	_, err := shared.RunCommand("btrfstune", "-f", "-u", devPath)
	if err != nil {
		return err
	}

	return nil
}

// regenerateFilesystemXFSUUID changes the XFS filesystem UUID to a new randomly generated one.
func regenerateFilesystemXFSUUID(devPath string) error {
	// Attempt to generate a new UUID.
	msg, err := shared.RunCommand("xfs_admin", "-U", "generate", devPath)
	if err != nil {
		return err
	}

	if msg != "" {
		// Exit 0 with a msg usually means some log entry getting in the way.
		_, err = shared.RunCommand("xfs_repair", "-o", "force_geometry", "-L", devPath)
		if err != nil {
			return err
		}

		// Attempt to generate a new UUID again.
		_, err = shared.RunCommand("xfs_admin", "-U", "generate", devPath)
		if err != nil {
			return err
		}
	}

	return nil
}
