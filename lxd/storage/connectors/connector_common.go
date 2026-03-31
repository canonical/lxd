package connectors

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

type common struct {
	serverUUID string
}

func (c common) doNotImplement() {}

// Type returns TypeUnknown.
func (c common) Type() ConnectorType {
	return TypeUnknown
}

// Version returns ErrNotSupported for all version retrieval attempts.
func (c common) Version() (string, error) {
	return "", ErrNotSupported
}

// QualifiedName returns ErrNotSupported for all qualified name retrieval attempts.
func (c common) QualifiedName() (string, error) {
	return "", ErrNotSupported
}

// LoadModules returns ErrNotSupported for all module loading attempts.
func (c common) LoadModules() error {
	return ErrNotSupported
}

// Discover returns ErrNotSupported for all discovery attempts.
func (c common) Discover(ctx context.Context, discoveryAddresses ...string) ([]Target, error) {
	return nil, ErrNotSupported
}

// Connect returns ErrNotSupported for all connection attempts.
func (c common) Connect(ctx context.Context, targets ...Target) (revert.Hook, error) {
	return nil, ErrNotSupported
}

// Disconnect returns ErrNotSupported for all disconnection attempts.
func (c common) Disconnect(ctx context.Context, targets ...Target) error {
	return ErrNotSupported
}

// GetDiskDevicePath checks whether the disk device passing the given filter
// exists in /dev/disk/by-id directory.
//
// When wait is true instead of checking once it periodically checks for
// the device to appear and returns the device path once it is found. If
// the device does not appear until the context is done (or within a 30 seconds
// timeout, if the passed context do not have timeout specified), an error is
// returned.
func (c common) GetDiskDevicePath(ctx context.Context, wait bool, diskNameFilter block.DeviceNameFilterFunc) (string, error) {
	if !wait {
		devicePath, err := findDiskDevicePath(diskNameFilter)
		if err != nil {
			return "", err
		}

		if devicePath == "" {
			return "", errors.New("Device not found")
		}

		return devicePath, nil
	}

	ctx, cancel := shared.WithDefaultTimeout(ctx, 30*time.Second)
	defer cancel()

	var devicePath string
	err := shared.WaitFuncErr(ctx, 500*time.Millisecond, func() (bool, error) {
		// Check if the device is already present.
		var err error
		devicePath, err = findDiskDevicePath(diskNameFilter)
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return false, err
		}

		// If the device is found, stop waiting.
		return devicePath != "", nil
	})

	return devicePath, err
}

// findDiskDevicePath iterates over device names in /dev/disk/by-id directory
// and returns the path to the disk device that matches the given prefix and
// suffix. Disk partitions are skipped, and an error is returned if the device
// is not found.
func findDiskDevicePath(diskNameFilter block.DeviceNameFilterFunc) (string, error) {
	if diskNameFilter == nil {
		diskNameFilter = func(diskPath string) bool { return true }
	}

	// Skip the disk if it is only a partition of the actual volume.
	diskNameFilterWithoutPartitions := func(diskPath string) bool {
		return !strings.Contains(diskPath, "-part") && diskNameFilter(diskPath)
	}

	// If there are no other disks on the system by id, the directory might not
	// even be there. Returns ENOENT in case the by-id/ directory does not exist.
	diskPaths, err := block.GetDisksByID(diskNameFilterWithoutPartitions)
	if err != nil {
		return "", err
	}

	if len(diskPaths) == 0 {
		return "", nil
	}

	if len(diskPaths) > 1 {
		logger.Warn("Warning more than a single disk device candidate found", logger.Ctx{"foundDevices": diskPaths})
	}

	// The actual device might not already be created.
	// Returns ENOENT in case the device does not exist.
	devicePath, err := filepath.EvalSymlinks(diskPaths[0])
	if err != nil {
		return "", err
	}

	return devicePath, nil
}

// WaitDiskDeviceResize waits until the disk device reflects the new size.
func (c common) WaitDiskDeviceResize(ctx context.Context, devicePath string, newSizeBytes int64) error {
	ctx, cancel := shared.WithDefaultTimeout(ctx, 30*time.Second)
	defer cancel()

	return shared.WaitFuncErr(ctx, 500*time.Millisecond, func() (bool, error) {
		sizeBytes, err := block.DiskSizeBytes(devicePath)
		if err != nil {
			return false, fmt.Errorf("Error getting disk size: %w", err)
		}

		return sizeBytes == newSizeBytes, nil
	})
}

// RemoveDiskDevice does nothing.
func (c common) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	return nil
}
