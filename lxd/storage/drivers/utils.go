package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// defaultVMBlockFilesystemSize is the size of a VM root device block volume's associated filesystem volume.
const defaultVMBlockFilesystemSize = "100MiB"

// MinBlockBoundary minimum block boundary size to use.
const MinBlockBoundary = 8192

// blockBackedAllowedFilesystems allowed filesystems for block volumes.
var blockBackedAllowedFilesystems = []string{"btrfs", "ext4", "xfs"}

// wipeDirectory empties the contents of a directory, but leaves it in place.
func wipeDirectory(path string) error {
	// List all entries.
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("Failed listing directory %q: %w", path, err)
	}

	// Individually wipe all entries.
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		err := os.RemoveAll(entryPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed removing %q: %w", entryPath, err)
		}
	}

	return nil
}

// forceRemoveAll wipes a path including any immutable/non-append files.
func forceRemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err != nil {
		_, _ = shared.RunCommandContext(context.TODO(), "chattr", "-ai", "-R", path)
		err = os.RemoveAll(path)
		if err != nil {
			return err
		}
	}

	return nil
}

// forceUnmount unmounts stacked mounts until no mountpoint remains.
func forceUnmount(path string) (bool, error) {
	unmounted := false

	for {
		// Check if already unmounted.
		if !filesystem.IsMountPoint(path) {
			return unmounted, nil
		}

		// Try a clean unmount first.
		err := TryUnmount(path, 0)
		if err != nil {
			// Fallback to lazy unmounting.
			err = unix.Unmount(path, unix.MNT_DETACH)
			if err != nil {
				return false, fmt.Errorf("Failed to unmount '%s': %w", path, err)
			}
		}

		unmounted = true
	}
}

// mountReadOnly performs a read-only bind-mount.
func mountReadOnly(srcPath string, dstPath string) (bool, error) {
	// Check if already mounted.
	if filesystem.IsMountPoint(dstPath) {
		return false, nil
	}

	// Create a mount entry.
	err := TryMount(context.TODO(), srcPath, dstPath, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	// Make it read-only.
	err = TryMount(context.TODO(), "", dstPath, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
	if err != nil {
		_, _ = forceUnmount(dstPath)
		return false, err
	}

	return true, nil
}

// sameMount checks if two paths are on the same mountpoint.
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
// By default the mount operation will be retried until the context expires.
// If no deadline is configured, a default timeout of 10 seconds is used.
// In case the mount operation doesn't return within the defined deadline,
// TryMount waits and doesn't return in case the context expired before that.
func TryMount(ctx context.Context, src string, dst string, fs string, flags uintptr, options string) error {
	var cancel context.CancelFunc

	// Set a default timeout in case it's not set by the caller.
	// Attempt 20 mounts over 10s.
	_, ok := ctx.Deadline()
	if !ok {
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

mountLoop:
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Failed to mount %q on %q using %q: %w", src, dst, fs, ctx.Err())
		default:
			err := unix.Mount(src, dst, fs, flags, options)
			if err == nil {
				break mountLoop
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return nil
}

// TryUnmount tries unmounting a filesystem multiple times. This is useful for unreliable backends.
func TryUnmount(path string, flags int) error {
	var err error

	for i := range 20 {
		err = unix.Unmount(path, flags)
		if err == nil {
			break
		}

		logger.Debug("Failed to unmount", logger.Ctx{"path": path, "attempt": i, "err": err})
		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return fmt.Errorf("Failed to unmount %q: %w", path, err)
	}

	return nil
}

// tryExists waits for a file to exist or the context being cancelled.
// The probe happens at intervals of 500 milliseconds.
func tryExists(ctx context.Context, path string) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
			if shared.PathExists(path) {
				return true
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// fsUUID returns the filesystem UUID for the given block path.
// error is returned if the given block device exists but has no UUID.
func fsUUID(path string) (string, error) {
	val, err := block.DiskFSUUID(path)
	if err != nil {
		return "", err
	}

	// blkid may return 0 and also produce no output for non-empty devices
	// without a UUID (e.g. a disk device with an MBR/GPT table)
	if val == "" {
		return "", fmt.Errorf("No UUID for device %q", path)
	}

	return val, nil
}

// fsProbe returns the filesystem type for the given block path.
func fsProbe(path string) (string, error) {
	val, err := shared.RunCommandContext(context.TODO(), "blkid", "-s", "TYPE", "-o", "value", path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(val), nil
}

// GetPoolMountPath returns the mountpoint of the given pool.
// {LXD_DIR}/storage-pools/<pool>.
func GetPoolMountPath(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// GetVolumeMountPath returns the mount path for a specific volume based on its pool and type and
// whether it is a snapshot or not. For VolumeTypeImage the volName is the image fingerprint.
func GetVolumeMountPath(poolName string, volType VolumeType, volName string) string {
	if shared.IsSnapshot(volName) {
		return shared.VarPath("storage-pools", poolName, string(volType)+"-snapshots", volName)
	}

	return shared.VarPath("storage-pools", poolName, string(volType), volName)
}

// GetVolumeSnapshotDir gets the snapshot mount directory for the parent volume.
func GetVolumeSnapshotDir(poolName string, volType VolumeType, volName string) string {
	parent, _, _ := api.GetParentAndSnapshotName(volName)
	return shared.VarPath("storage-pools", poolName, string(volType)+"-snapshots", parent)
}

// GetSnapshotVolumeName returns the full volume name for a parent volume and snapshot name.
func GetSnapshotVolumeName(parentName, snapshotName string) string {
	return parentName + shared.SnapshotDelimiter + snapshotName
}

// createParentSnapshotDirIfMissing creates the parent directory for volume snapshots.
func createParentSnapshotDirIfMissing(poolName string, volType VolumeType, volName string) error {
	snapshotsPath := GetVolumeSnapshotDir(poolName, volType, volName)

	// If it's missing, create it.
	if !shared.PathExists(snapshotsPath) {
		err := os.Mkdir(snapshotsPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to create parent snapshot directory %q: %w", snapshotsPath, err)
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
				return fmt.Errorf("Failed to remove '%s': %w", snapshotsPath, err)
			}
		}
	}

	return nil
}

// ensureSparseFile creates a sparse empty file at specified location with specified size.
// If the path already exists, the file is truncated to the requested size.
func ensureSparseFile(filePath string, sizeBytes int64) error {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("Failed to open %s: %w", filePath, err)
	}

	defer func() { _ = f.Close() }()

	err = f.Truncate(sizeBytes)
	if err != nil {
		return fmt.Errorf("Failed to create sparse file %s: %w", filePath, err)
	}

	return f.Close()
}

// ensureVolumeBlockFile creates new block file or enlarges the raw block file for a volume to the specified size.
// Returns true if resize took place, false if not. Requested size is rounded to nearest block size using
// roundVolumeBlockSizeBytes() before decision whether to resize is taken. Accepts unsupportedResizeTypes
// list that indicates which volume types it should not attempt to resize (when allowUnsafeResize=false) and
// instead return ErrNotSupported.
func ensureVolumeBlockFile(vol Volume, path string, sizeBytes int64, allowUnsafeResize bool, unsupportedResizeTypes ...VolumeType) (bool, error) {
	if sizeBytes <= 0 {
		return false, errors.New("Size cannot be zero")
	}

	// Get rounded block size to avoid QEMU boundary issues.
	sizeBytes = vol.driver.roundVolumeBlockSizeBytes(vol, sizeBytes)

	if shared.PathExists(path) {
		fi, err := os.Stat(path)
		if err != nil {
			return false, err
		}

		oldSizeBytes := fi.Size()
		if sizeBytes == oldSizeBytes {
			return false, nil
		}

		// Only perform pre-resize checks if we are not in "unsafe" mode.
		// In unsafe mode we expect the caller to know what they are doing and understand the risks.
		if !allowUnsafeResize {
			// Reject if would try and resize a volume type that is not supported.
			// This needs to come before the ErrCannotBeShrunk check below so that any resize attempt
			// is blocked with ErrNotSupported error.
			if slices.Contains(unsupportedResizeTypes, vol.volType) {
				return false, ErrNotSupported
			}

			if sizeBytes < oldSizeBytes {
				return false, fmt.Errorf("Block volumes cannot be shrunk: %w", ErrCannotBeShrunk)
			}

			if vol.MountInUse() {
				return false, ErrInUse // We don't allow online resizing of block volumes.
			}
		}

		err = ensureSparseFile(path, sizeBytes)
		if err != nil {
			return false, fmt.Errorf("Failed resizing disk image %q to size %d: %w", path, sizeBytes, err)
		}

		return true, nil
	}

	// If path doesn't exist, then there has been no filler function supplied to create it from another source.
	// So instead create an empty volume (use for PXE booting a VM).
	err := ensureSparseFile(path, sizeBytes)
	if err != nil {
		return false, fmt.Errorf("Failed creating disk image %q as size %d: %w", path, sizeBytes, err)
	}

	return false, nil
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

	cmd := []string{"mkfs." + fsType}
	if fsOptions.Label != "" {
		cmd = append(cmd, "-L", fsOptions.Label)
	}

	if fsType == "ext4" {
		cmd = append(cmd, "-E", "lazy_itable_init=0,lazy_journal_init=0")
	}

	// Always add the path to the device as the last argument for wider compatibility with versions of mkfs.
	cmd = append(cmd, path)

	msg, err = shared.TryRunCommand(cmd[0], cmd[1:]...)
	if err != nil {
		return msg, err
	}

	return "", nil
}

// filesystemTypeCanBeShrunk indicates if filesystems of fsType can be shrunk.
func filesystemTypeCanBeShrunk(fsType string) bool {
	if fsType == "" {
		fsType = DefaultFilesystem
	}

	if slices.Contains([]string{"ext4", "btrfs"}, fsType) {
		return true
	}

	return false
}

// shrinkFileSystem shrinks a filesystem if it is supported.
// EXT4 volumes will be unmounted temporarily if needed.
// BTRFS volumes will be mounted temporarily if needed.
// Accepts a force argument that indicates whether to skip some safety checks when resizing the volume.
// This should only be used if the volume will be deleted on resize error.
func shrinkFileSystem(fsType string, devPath string, vol Volume, byteSize int64, force bool) error {
	if fsType == "" {
		fsType = DefaultFilesystem
	}

	if !filesystemTypeCanBeShrunk(fsType) {
		return ErrCannotBeShrunk
	}

	// The smallest unit that resize2fs accepts in byte size (rather than blocks) is kilobytes.
	// btrfs filesystem resize also accepts kilobytes.
	strSize := strconv.FormatInt(byteSize/1024, 10) + "K"

	switch fsType {
	case "ext4":
		return vol.UnmountTask(func(op *operations.Operation) error {
			output, err := shared.RunCommandContext(context.TODO(), "e2fsck", "-f", "-y", devPath)
			if err != nil {
				exitCodeFSModified := false
				runErr, ok := err.(shared.RunError)
				if ok {
					exitError, ok := runErr.Unwrap().(*exec.ExitError)
					if ok {
						if exitError.ExitCode() == 1 {
							exitCodeFSModified = true
						}
					}
				}

				// e2fsck can return non-zero exit code if it has modified the filesystem, but
				// this isn't an error and we can proceed.
				if !exitCodeFSModified {
					// e2fsck provides some context to errors on stdout.
					return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
				}
			}

			var args []string
			if force {
				// Enable force mode if requested. Should only be done if volume will be deleted
				// on error as this can result in corrupting the filesystem if fails during resize.
				// This is useful because sometimes the pre-checks performed by resize2fs are not
				// accurate and would prevent a successful filesystem shrink.
				args = append(args, "-f")
			}

			args = append(args, devPath, strSize)
			_, err = shared.RunCommandContext(context.TODO(), "resize2fs", args...)
			if err != nil {
				return err
			}

			return nil
		}, true, nil)
	case "btrfs":
		return vol.MountTask(func(mountPath string, op *operations.Operation) error {
			_, err := shared.RunCommandContext(context.TODO(), "btrfs", "filesystem", "resize", strSize, mountPath)
			if err != nil {
				return err
			}

			return nil
		}, nil)
	}

	return fmt.Errorf("Unrecognised filesystem type %q", fsType)
}

// growFileSystem grows a filesystem if it is supported. The volume will be mounted temporarily if needed.
func growFileSystem(fsType string, devPath string, vol Volume) error {
	if fsType == "" {
		fsType = DefaultFilesystem
	}

	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var err error
		switch fsType {
		case "ext4":
			_, err = shared.TryRunCommand("resize2fs", devPath)
		case "xfs":
			_, err = shared.TryRunCommand("xfs_growfs", mountPath)
		case "btrfs":
			_, err = shared.TryRunCommand("btrfs", "filesystem", "resize", "max", mountPath)
		default:
			return fmt.Errorf("Unrecognised filesystem type %q", fsType)
		}

		if err != nil {
			return fmt.Errorf("Could not grow underlying %q filesystem for %q: %w", fsType, devPath, err)
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
func regenerateFilesystemUUID(fsType string, devPath string) error {
	switch fsType {
	case "btrfs":
		return regenerateFilesystemBTRFSUUID(devPath)
	case "xfs":
		return regenerateFilesystemXFSUUID(devPath)
	}

	return errors.New("Filesystem not supported")
}

// regenerateFilesystemBTRFSUUID changes the BTRFS filesystem UUID to a new randomly generated one.
func regenerateFilesystemBTRFSUUID(devPath string) error {
	// If the snapshot was taken whilst instance was running there may be outstanding transactions that will
	// cause btrfstune to corrupt superblock, so ensure these are cleared out first.
	_, err := shared.RunCommandContext(context.TODO(), "btrfs", "rescue", "zero-log", devPath)
	if err != nil {
		return err
	}

	// `-m` modifies the metadata_uuid which is much faster than `-u` that rewrites all metadata blocks.
	// The resulting filesystem needs kernel 5.0+ to be mounted or running `btrfstune -u` to regain compat
	// with older kernels.
	_, err = shared.RunCommandContext(context.TODO(), "btrfstune", "-f", "-m", devPath)
	if err != nil {
		return err
	}

	return nil
}

// regenerateFilesystemXFSUUID changes the XFS filesystem UUID to a new randomly generated one.
func regenerateFilesystemXFSUUID(devPath string) error {
	// Attempt to generate a new UUID.
	msg, err := shared.RunCommandContext(context.TODO(), "xfs_admin", "-U", "generate", devPath)
	if err != nil {
		return err
	}

	if msg != "" {
		// Exit 0 with a msg usually means some log entry getting in the way.
		_, err = shared.RunCommandContext(context.TODO(), "xfs_repair", "-o", "force_geometry", "-L", devPath)
		if err != nil {
			return err
		}

		// Attempt to generate a new UUID again.
		_, err = shared.RunCommandContext(context.TODO(), "xfs_admin", "-U", "generate", devPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// addNoRecoveryMountOption adds the `norecovery` mount option to the provided mount options.
// btrfs/ext4/xfs can do some form of recovery if the filesystem was not cleanly unmounted.
// To prevent this kind of write access, we mount the filesystem with the ro,norecovery mount options.
// For ext4, norecovery is a synonym for noload, which prevents the journal from being loaded on mounting.
func addNoRecoveryMountOption(mountOptions string, filesystem string) string {
	// If the filesystem is not one of the supported types, return unchanged.
	if !slices.Contains(blockBackedAllowedFilesystems, filesystem) {
		return mountOptions
	}

	// Add norecovery as the first mount options.
	if mountOptions == "" {
		return "norecovery"
	}

	// Already contains norecovery, so return unchanged.
	options := strings.Split(mountOptions, ",")

	// For ext4, noload is a synonym for norecovery.
	if filesystem == "ext4" {
		for i, option := range options {
			if option == "noload" {
				options[i] = "norecovery"
				break
			}
		}
	}

	if !slices.Contains(options, "norecovery") {
		options = append(options, "norecovery")
	}

	return strings.Join(options, ",")
}

// copyDevice copies one device path to another using dd running at low priority.
// It expects outputPath to exist already, so will not create it.
func copyDevice(inputPath string, outputPath string) error {
	cmd := []string{
		"nice", "-n19", // Run dd with low priority to reduce CPU impact on other processes.
		"dd", "if=" + inputPath, "of=" + outputPath,
		"bs=16M",       // Use large buffer to reduce syscalls and speed up copy.
		"conv=nocreat", // Don't create output file if missing (expect caller to have created output file).
	}

	// Check for Direct I/O support.
	from, err := os.OpenFile(inputPath, unix.O_DIRECT|unix.O_RDONLY, 0)
	if err == nil {
		cmd = append(cmd, "iflag=direct")
		_ = from.Close()
	}

	to, err := os.OpenFile(outputPath, unix.O_DIRECT|unix.O_WRONLY, 0)
	if err == nil {
		cmd = append(cmd, "oflag=direct")
		_ = to.Close()
	}

	_, err = shared.RunCommandContext(context.TODO(), cmd[0], cmd[1:]...)
	if err != nil {
		return err
	}

	return nil
}

// loopFilePath returns the loop file path for a storage pool.
func loopFilePath(poolName string) string {
	return filepath.Join(shared.VarPath("disks"), poolName+".img")
}

// ShiftBtrfsRootfs shifts the BTRFS root filesystem.
func ShiftBtrfsRootfs(path string, diskIdmap *idmap.IdmapSet) error {
	return shiftBtrfsRootfs(path, diskIdmap, true)
}

// UnshiftBtrfsRootfs unshifts the BTRFS root filesystem.
func UnshiftBtrfsRootfs(path string, diskIdmap *idmap.IdmapSet) error {
	return shiftBtrfsRootfs(path, diskIdmap, false)
}

// shiftBtrfsRootfs shifts a filesystem that main include read-only subvolumes.
func shiftBtrfsRootfs(path string, diskIdmap *idmap.IdmapSet, shift bool) error {
	var err error
	roSubvols := []string{}
	subvols, _ := btrfsSubVolumesGet(path)
	sort.Strings(subvols)
	d := &btrfs{}
	for _, subvol := range subvols {
		subvol = filepath.Join(path, subvol)

		if !btrfsSubVolumeIsRo(subvol) {
			continue
		}

		roSubvols = append(roSubvols, subvol)

		_ = d.setSubvolumeReadonlyProperty(subvol, false)
	}

	if shift {
		err = diskIdmap.ShiftRootfs(path, nil)
	} else {
		err = diskIdmap.UnshiftRootfs(path, nil)
	}

	for _, subvol := range roSubvols {
		_ = d.setSubvolumeReadonlyProperty(subvol, true)
	}

	return err
}

// btrfsSubVolumesGet gets subvolumes.
func btrfsSubVolumesGet(path string) ([]string, error) {
	result := []string{}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Unprivileged users can't get to fs internals.
	_ = filepath.WalkDir(path, func(fpath string, entry fs.DirEntry, err error) error {
		// Skip walk errors
		if err != nil {
			return nil
		}

		// Ignore the base path.
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories.
		if !entry.IsDir() {
			return nil
		}

		// Check if a btrfs subvolume.
		d := &btrfs{}
		if d.isSubvolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

// btrfsSubVolumeIsRo returns if subvolume is read only.
func btrfsSubVolumeIsRo(path string) bool {
	output, err := shared.RunCommandContext(context.TODO(), "btrfs", "property", "get", "-ts", path)
	if err != nil {
		return false
	}

	return strings.HasPrefix(string(output), "ro=true")
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

// OperationLockName returns the storage specific lock name to use with locking package.
func OperationLockName(operationName string, poolName string, volType VolumeType, contentType ContentType, volName string) string {
	return operationName + "/" + poolName + "/" + string(volType) + "/" + string(contentType) + "/" + volName
}

// loopFileSizeDefault returns the size in GiB to use as the default size for a pool loop file.
// This is based on the size of the filesystem of LXD's VarPath().
func loopFileSizeDefault() (uint64, error) {
	st := unix.Statfs_t{}
	err := unix.Statfs(shared.VarPath(), &st)
	if err != nil {
		return 0, fmt.Errorf("Failed getting free space of %q: %w", shared.VarPath(), err)
	}

	gibAvailable := uint64(st.Frsize) * st.Bavail / (1024 * 1024 * 1024)
	if gibAvailable > 30 {
		return 30, nil // Default to no more than 30GiB.
	} else if gibAvailable > 5 {
		return gibAvailable / 5, nil // Use 20% of free space otherwise.
	} else if gibAvailable == 5 {
		return gibAvailable, nil // Need at least 5GiB free.
	}

	return 0, errors.New("Insufficient free space to create default sized 5GiB pool")
}

// loopFileSetup sets up a loop device for the provided sourcePath.
// It tries to enable direct I/O if supported.
func loopDeviceSetup(sourcePath string) (string, error) {
	out, err := shared.RunCommandContext(context.TODO(), "losetup", "--find", "--nooverlap", "--direct-io=on", "--show", sourcePath)
	if err == nil {
		return strings.TrimSpace(out), nil
	}

	if !strings.Contains(err.Error(), "direct io") && !strings.Contains(err.Error(), "Invalid argument") {
		return "", err
	}

	out, err = shared.RunCommandContext(context.TODO(), "losetup", "--find", "--nooverlap", "--show", sourcePath)
	if err == nil {
		return strings.TrimSpace(out), nil
	}

	return "", err
}

// loopFileAutoDetach enables auto detach mode for a loop device.
func loopDeviceAutoDetach(loopDevPath string) error {
	_, err := shared.RunCommandContext(context.TODO(), "losetup", "--detach", loopDevPath)
	return err
}

// loopDeviceSetCapacity forces the loop driver to reread the size of the file associated with the specified loop device.
func loopDeviceSetCapacity(loopDevPath string) error {
	_, err := shared.RunCommandContext(context.TODO(), "losetup", "--set-capacity", loopDevPath)
	return err
}

// wipeBlockHeaders will wipe the first 4MB of a block device.
func wipeBlockHeaders(path string) error {
	// Open /dev/zero.
	fdZero, err := os.Open("/dev/zero")
	if err != nil {
		return err
	}

	defer fdZero.Close()

	// Open the target disk.
	fdDisk, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}

	defer fdDisk.Close()

	// Wipe the 4MiB header.
	_, err = io.CopyN(fdDisk, fdZero, 1024*1024*4)
	if err != nil {
		return err
	}

	return nil
}

// IsContentBlock returns true if the content type is either block or iso.
func IsContentBlock(contentType ContentType) bool {
	return contentType == ContentTypeBlock || contentType == ContentTypeISO
}

// roundAbove returns the next multiple of `above` greater than `val`.
func roundAbove(above, val int64) int64 {
	if val < above {
		val = above
	}

	rounded := int64(val/above) * above

	// Ensure the rounded size is at least x.
	if rounded < val {
		rounded += above
	}

	return rounded
}

// ResolveServerName returns the given server name if it is not "none".
// If the server name is "none", it retrieves and returns the server's hostname.
func ResolveServerName(serverName string) (string, error) {
	if serverName != "none" {
		return serverName, nil
	}

	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("Failed to get hostname: %w", err)
	}

	return hostname, nil
}

// remoteVolumeMapLock acquires a lock used when mapping or unmapping remote
// storage volumes. This lock prevents conflicts between operations trying to
// associate or disassociate volumes with the LXD host. If the lock is
// successfully acquired, unlock function is returned.
func remoteVolumeMapLock(connectorName string, driverName string) (locking.UnlockFunc, error) {
	l := logger.AddContext(logger.Ctx{"connector": connectorName, "driver": driverName})
	l.Debug("Acquiring lock for remote volume map")
	defer l.Debug("Lock acquired for remote volume map")

	return locking.Lock(context.TODO(), "RemoteVolumeMap_"+connectorName+"_"+driverName)
}

// ValidPoolName validates a pool name.
func ValidPoolName(poolName string) error {
	if poolName == "" {
		return errors.New("Cannot be empty")
	}

	if strings.HasPrefix(poolName, ".") {
		return errors.New("Cannot start with a dot")
	}

	if strings.HasPrefix(poolName, "-") {
		return errors.New("Cannot start with a hyphen")
	}

	if strings.Contains(poolName, shared.SnapshotDelimiter) {
		return errors.New("Cannot contain slashes")
	}

	for _, r := range poolName {
		if unicode.IsSpace(r) {
			return errors.New("Cannot contain white space")
		}
	}

	return nil
}

// ValidVolumeName validates a volume name.
func ValidVolumeName(volumeName string) error {
	if volumeName == "" {
		return errors.New("Cannot be empty")
	}

	if volumeName == ".." {
		return errors.New(`Cannot be ".."`)
	}

	if strings.Contains(volumeName, "\\") {
		return errors.New("Cannot contain backslashes")
	}

	if strings.Contains(volumeName, shared.SnapshotDelimiter) {
		return errors.New("Cannot contain slashes")
	}

	for _, r := range volumeName {
		if unicode.IsSpace(r) {
			return errors.New("Cannot contain white space")
		}
	}

	return nil
}
