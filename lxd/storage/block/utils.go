package block

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared"
)

// DevDiskByID represents the system's path for disks identified by their ID.
const DevDiskByID = "/dev/disk/by-id"

// DeviceNameFilterFunc is a function that accepts device name and returns true
// if the name matches the required criteria.
type DeviceNameFilterFunc func(devPath string) bool

// DiskSizeBytes returns the size of a block disk (path can be either block device or raw file).
func DiskSizeBytes(blockDiskPath string) (int64, error) {
	if shared.IsBlockdevPath(blockDiskPath) {
		// Attempt to open the device path.
		f, err := os.Open(blockDiskPath)
		if err != nil {
			return -1, err
		}

		defer func() { _ = f.Close() }()
		fd := int(f.Fd())

		// Retrieve the block device size.
		res, err := unix.IoctlGetInt(fd, unix.BLKGETSIZE64)
		if err != nil {
			return -1, err
		}

		return int64(res), nil
	}

	// Block device is assumed to be a raw file.
	fi, err := os.Lstat(blockDiskPath)
	if err != nil {
		return -1, err
	}

	return fi.Size(), nil
}

// DiskBlockSize returns the physical block size of a block device.
func DiskBlockSize(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}

	defer func() { _ = f.Close() }()
	fd := int(f.Fd())

	// Retrieve the physical block size.
	res, err := unix.IoctlGetUint32(fd, unix.BLKPBSZGET)
	if err != nil {
		return 0, err
	}

	return res, nil
}

// DiskFSUUID returns the UUID of a filesystem on the device.
// An empty string is returned in case of a pristine disk.
func DiskFSUUID(pathName string) (string, error) {
	uuid, err := shared.RunCommand(context.TODO(), "blkid", "-s", "UUID", "-o", "value", pathName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Unwrap().(*exec.ExitError)

			// blkid manpage says that blkid exits with code 2 if it is impossible to gather any information about the device identifiers or device content.
			if ok && exitError.ExitCode() == 2 {
				return "", nil
			}
		}

		return "", fmt.Errorf("Failed retrieving filesystem UUID from device %q: %w", pathName, err)
	}

	return strings.TrimSpace(uuid), nil
}

// DiskFSType detects the filesystem type of the block device.
func DiskFSType(pathName string) (string, error) {
	fsType, err := shared.RunCommand(context.TODO(), "blkid", "-s", "TYPE", "-o", "value", pathName)
	if err != nil {
		return "", fmt.Errorf("Failed retrieving filesystem type from device %q: %w", pathName, err)
	}

	return strings.TrimSpace(fsType), nil
}

// WaitDiskDeviceGone waits for the disk device to disappear from /dev/disk/by-id.
// It periodically checks for the device to disappear and returns once the device
// is gone. If the device does not disappear within the timeout, an error is returned.
func WaitDiskDeviceGone(ctx context.Context, diskPath string) bool {
	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 30 seconds for the context
		// if no deadline is already configured.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	for {
		if !shared.PathExists(diskPath) {
			return true
		}

		if ctx.Err() != nil {
			return false
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// GetDisksByID returns all disks whose ID contains the filter prefix.
func GetDisksByID(filter DeviceNameFilterFunc) ([]string, error) {
	disks, err := os.ReadDir(DevDiskByID)
	if err != nil {
		return nil, fmt.Errorf("Failed getting disks by ID: %w", err)
	}

	var filteredDisks []string //nolint:prealloc
	for _, disk := range disks {
		// Skip the disk if it does not matches filter.
		if filter != nil && !filter(disk.Name()) {
			continue
		}

		filteredDisks = append(filteredDisks, path.Join(DevDiskByID, disk.Name()))
	}

	return filteredDisks, nil
}

// LoopDeviceSetupAlign creates a forced 512-byte aligned loop device.
func LoopDeviceSetupAlign(sourcePath string) (string, error) {
	out, err := shared.RunCommand(context.TODO(), "losetup", "-b", "512", "--find", "--nooverlap", "--show", sourcePath)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}
