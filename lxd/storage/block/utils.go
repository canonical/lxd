package block

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared"
)

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

// WaitDiskDeviceResize waits until the disk device reflects the new size.
func WaitDiskDeviceResize(ctx context.Context, diskPath string, newSizeBytes int64) error {
	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 30 seconds for the context
		// if no deadline is already configured.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	for {
		sizeBytes, err := DiskSizeBytes(diskPath)
		if err != nil {
			return fmt.Errorf("Error getting disk size: %w", err)
		}

		if sizeBytes == newSizeBytes {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		time.Sleep(500 * time.Millisecond)
	}
}
