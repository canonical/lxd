package drivers

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
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

		return errors.Wrapf(err, "Failed to list directory '%s'", path)
	}

	// Individually wipe all entries.
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		err := os.RemoveAll(entryPath)
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "Failed to remove '%s'", entryPath)
		}
	}

	return nil
}

func forceUnmount(path string) (bool, error) {
	unmounted := false

	for {
		// Check if already unmounted.
		if !shared.IsMountPoint(path) {
			return unmounted, nil
		}

		// Try a clean unmount first.
		err := TryUnmount(path, 0)
		if err != nil {
			// Fallback to lazy unmounting.
			err = unix.Unmount(path, unix.MNT_DETACH)
			if err != nil {
				return false, errors.Wrapf(err, "Failed to unmount '%s'", path)
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
		return errors.Wrapf(err, "Failed to mount '%s' on '%s'", src, dst)
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
		return errors.Wrapf(err, "Failed to unmount '%s'", path)
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
		err := os.Mkdir(snapshotsPath, 0700)
		if err != nil {
			return errors.Wrapf(err, "Failed to create directory '%s'", snapshotsPath)
		}

		return nil
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
			if err != nil && !os.IsNotExist(err) {
				return errors.Wrapf(err, "Failed to remove '%s'", snapshotsPath)
			}
		}
	}

	return nil
}

// createSparseFile creates a sparse empty file at specified location with specified size.
func createSparseFile(filePath string, sizeBytes int64) error {
	f, err := os.Create(filePath)
	if err != nil {
		return errors.Wrapf(err, "Failed to open %s", filePath)
	}
	defer f.Close()

	err = f.Chmod(0600)
	if err != nil {
		return errors.Wrapf(err, "Failed to chmod %s", filePath)
	}

	err = f.Truncate(sizeBytes)
	if err != nil {
		return errors.Wrapf(err, "Failed to create sparse file %s", filePath)
	}

	return nil
}

// roundVolumeBlockFileSizeBytes parses the supplied size string and then rounds it to the nearest 8k bytes.
func roundVolumeBlockFileSizeBytes(blockSize string) (int64, error) {
	blockSizeBytes, err := units.ParseByteSizeString(blockSize)
	if err != nil {
		return -1, err
	}

	// Qemu requires image files to be in traditional storage block boundaries.
	// We use 8k here to ensure our images are compatible with all of our backend drivers.
	const minBlockBoundary = 8192
	if blockSizeBytes < minBlockBoundary {
		blockSizeBytes = minBlockBoundary
	}

	// Round the size to closest minBlockBoundary bytes to avoid qemu boundary issues.
	return int64(blockSizeBytes/minBlockBoundary) * minBlockBoundary, nil
}

// ensureVolumeBlockFile creates or resizes the raw block file for a volume to the specified size.
func ensureVolumeBlockFile(path, blockSize string) error {
	if blockSize == "" {
		blockSize = defaultBlockSize
	}

	// Get rounded block size to avoid qemu boundary issues.
	blockSizeBytes, err := roundVolumeBlockFileSizeBytes(blockSize)
	if err != nil {
		return err
	}

	if shared.PathExists(path) {
		_, err = shared.RunCommand("qemu-img", "resize", "-f", "raw", path, fmt.Sprintf("%d", blockSizeBytes))
		if err != nil {
			return errors.Wrapf(err, "Failed resizing disk image %s to size %s", path, blockSize)
		}
	} else {
		// If path doesn't exist, then there has been no filler function
		// supplied to create it from another source. So instead create an empty
		// volume (use for PXE booting a VM).
		_, err = shared.RunCommand("qemu-img", "create", "-f", "raw", path, fmt.Sprintf("%d", blockSizeBytes))
		if err != nil {
			return errors.Wrapf(err, "Failed creating disk image %s as size %s", path, blockSize)
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
			output, err := shared.TryRunCommand("e2fsck", "-f", "-y", devPath)
			if err != nil {
				// e2fsck provides some context to errors on stdout.
				return errors.Wrapf(err, "%s", strings.TrimSpace(output))
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
			msg, err = shared.TryRunCommand("xfs_growfs", mountPath)
		case "btrfs":
			msg, err = shared.TryRunCommand("btrfs", "filesystem", "resize", "max", mountPath)
		default:
			return fmt.Errorf(`Growing not supported for filesystem type "%s"`, fsType)
		}

		if err != nil {
			return fmt.Errorf(`Could not extend underlying %s filesystem for "%s": %s`, fsType, devPath, msg)
		}

		return nil
	}, nil)
}

// renegerateFilesystemUUIDNeeded returns true if fsType requires UUID regeneration, false if not.
func renegerateFilesystemUUIDNeeded(fsType string) bool {
	switch fsType {
	case "btrfs":
		return true
	case "xfs":
		return true
	}

	return false
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

	return fmt.Errorf("Filesystem not supported")
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

// copyDevice copies one device path to another.
func copyDevice(inputPath, outputPath string) error {
	from, err := os.Open(inputPath)
	if err != nil {
		return errors.Wrapf(err, "Error opening file for reading: %s", inputPath)
	}
	defer from.Close()

	to, err := os.OpenFile(outputPath, os.O_WRONLY, 0)
	if err != nil {
		return errors.Wrapf(err, "Error opening file writing: %s", outputPath)
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		return errors.Wrapf(err, "Error copying file '%s' to '%s'", inputPath, outputPath)
	}

	return nil
}

// loopFilePath returns the loop file path for a storage pool.
func loopFilePath(poolName string) string {
	return filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", poolName))
}

// ShiftBtrfsRootfs shifts the BTRFS root filesystem.
func ShiftBtrfsRootfs(path string, diskIdmap *idmap.IdmapSet) error {
	return shiftBtrfsRootfs(path, diskIdmap, true)
}

// UnshiftBtrfsRootfs unshifts the BTRFS root filesystem.
func UnshiftBtrfsRootfs(path string, diskIdmap *idmap.IdmapSet) error {
	return shiftBtrfsRootfs(path, diskIdmap, false)
}

func shiftBtrfsRootfs(path string, diskIdmap *idmap.IdmapSet, shift bool) error {
	var err error
	roSubvols := []string{}
	subvols, _ := BTRFSSubVolumesGet(path)
	sort.Sort(sort.StringSlice(subvols))
	for _, subvol := range subvols {
		subvol = filepath.Join(path, subvol)

		if !BTRFSSubVolumeIsRo(subvol) {
			continue
		}

		roSubvols = append(roSubvols, subvol)
		BTRFSSubVolumeMakeRw(subvol)
	}

	if shift {
		err = diskIdmap.ShiftRootfs(path, nil)
	} else {
		err = diskIdmap.UnshiftRootfs(path, nil)
	}

	for _, subvol := range roSubvols {
		BTRFSSubVolumeMakeRo(subvol)
	}

	return err
}

// BTRFSSubVolumesGet gets subvolumes.
func BTRFSSubVolumesGet(path string) ([]string, error) {
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

// BTRFSSubVolumeIsRo returns if subvolume is read only.
func BTRFSSubVolumeIsRo(path string) bool {
	output, err := shared.RunCommand("btrfs", "property", "get", "-ts", path)
	if err != nil {
		return false
	}

	return strings.HasPrefix(string(output), "ro=true")
}

// BTRFSSubVolumeMakeRo makes a subvolume read only.
func BTRFSSubVolumeMakeRo(path string) error {
	_, err := shared.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "true")
	return err
}

// BTRFSSubVolumeMakeRw makes a sub volume read/write.
func BTRFSSubVolumeMakeRw(path string) error {
	_, err := shared.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "false")
	return err
}

// ShiftZFSSkipper indicates which files not to shift for ZFS.
func ShiftZFSSkipper(dir string, absPath string, fi os.FileInfo) bool {
	strippedPath := absPath
	if dir != "" {
		strippedPath = absPath[len(dir):]
	}

	if fi.IsDir() && strippedPath == "/.zfs/snapshot" {
		return true
	}

	return false
}
