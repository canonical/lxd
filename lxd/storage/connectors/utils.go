package connectors

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared"
)

// devicePathFilterFunc is a function that accepts device path and returns true
// if the path matches the required criteria.
type devicePathFilterFunc func(devPath string) bool

// GetDiskDevicePath checks whether the disk device with a given prefix and suffix
// exists in /dev/disk/by-id directory. A device path is returned if the device is
// found, otherwise an error is returned.
func GetDiskDevicePath(diskNamePrefix string, diskPathFilter devicePathFilterFunc) (string, error) {
	devPath, err := findDiskDevicePath(diskNamePrefix, diskPathFilter)
	if err != nil {
		return "", err
	}

	if devPath == "" {
		return "", fmt.Errorf("Device not found")
	}

	return devPath, nil
}

// WaitDiskDevicePath waits for the disk device to appear in /dev/disk/by-id.
// It periodically checks for the device to appear and returns the device path
// once it is found. If the device does not appear within the timeout, an error
// is returned.
func WaitDiskDevicePath(ctx context.Context, diskNamePrefix string, diskPathFilter devicePathFilterFunc) (string, error) {
	var err error
	var diskPath string

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		// Check if the device is already present.
		diskPath, err = findDiskDevicePath(diskNamePrefix, diskPathFilter)
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return "", err
		}

		// If the device is found, return the device path.
		if diskPath != "" {
			break
		}

		// Check if context is cancelled.
		err := ctx.Err()
		if err != nil {
			return "", err
		}

		time.Sleep(500 * time.Millisecond)
	}

	return diskPath, nil
}

// findDiskDevivePath iterates over device names in /dev/disk/by-id directory and
// returns the path to the disk device that matches the given prefix and suffix.
// Disk partitions are skipped, and an error is returned if the device is not found.
func findDiskDevicePath(diskNamePrefix string, diskPathFilter devicePathFilterFunc) (string, error) {
	var diskPaths []string

	// If there are no other disks on the system by id, the directory might not
	// even be there. Returns ENOENT in case the by-id/ directory does not exist.
	diskPaths, err := resources.GetDisksByID(diskNamePrefix)
	if err != nil {
		return "", err
	}

	for _, diskPath := range diskPaths {
		// Skip the disk if it is only a partition of the actual volume.
		if strings.Contains(diskPath, "-part") {
			continue
		}

		// Use custom disk path filter, if one is provided.
		if diskPathFilter != nil && !diskPathFilter(diskPath) {
			continue
		}

		// The actual device might not already be created.
		// Returns ENOENT in case the device does not exist.
		devPath, err := filepath.EvalSymlinks(diskPath)
		if err != nil {
			return "", err
		}

		return devPath, nil
	}

	return "", nil
}

// WaitDiskDeviceGone waits for the disk device to disappear from /dev/disk/by-id.
// It periodically checks for the device to disappear and returns once the device
// is gone. If the device does not disappear within the timeout, an error is returned.
func WaitDiskDeviceGone(ctx context.Context, diskPath string) bool {
	// Set upper boundary for the timeout to ensure this function does not run
	// indefinitely. The caller can set a shorter timeout if necessary.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

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
