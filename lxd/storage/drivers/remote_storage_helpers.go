package drivers

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"golang.org/x/sys/unix"
)

// This file here is to consolidate all useful helpers to reuse across different remote storage
// drivers which use NVMe/iSCSI/etc connectors like PowerFlex, Pure, HPE Alletra.

// remoteDriver is the extended internal interface for use only with helpers below.
type remoteDriver interface {
	driver

	connector() (connectors.Connector, error)

	getMappedDevPath(vol Volume, mapVolume bool) (string, revert.Hook, error)
	unmapVolume(vol Volume) error
}

// MountRemoteVolume mounts a volume and increments ref counter. Please call UnmountRemoteVolume() when done with the volume.
func MountRemoteVolume(d remoteDriver, vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	revert := revert.New()
	defer revert.Fail()

	// Activate volume if needed.
	volDevPath, cleanup, err := d.getMappedDevPath(vol, true)
	if err != nil {
		return err
	}

	revert.Add(cleanup)

	switch vol.contentType {
	case ContentTypeFS:
		mountPath := vol.MountPath()
		if !filesystem.IsMountPoint(mountPath) {
			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}

			fsType := vol.ConfigBlockFilesystem()

			if vol.mountFilesystemProbe {
				fsType, err = fsProbe(volDevPath)
				if err != nil {
					return fmt.Errorf("Failed probing filesystem: %w", err)
				}
			}

			mountFlags, mountOptions := filesystem.ResolveMountOptions(strings.Split(vol.ConfigBlockMountOptions(), ","))
			err = TryMount(context.TODO(), volDevPath, mountPath, fsType, mountFlags, mountOptions)
			if err != nil {
				return err
			}

			d.Logger().Debug("Mounted volume", logger.Ctx{"driver": d.Info().Name, "volName": vol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}

	case ContentTypeBlock:
		// For VMs, mount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			err := d.MountVolume(fsVol, op)
			if err != nil {
				return err
			}
		}
	}

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	revert.Success()
	return nil
}

func UnmountRemoteVolume(d remoteDriver, vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	mountPath := vol.MountPath()
	refCount := vol.MountRefCountDecrement()

	// Attempt to unmount the volume.
	if vol.contentType == ContentTypeFS && filesystem.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.Logger().Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
			return false, ErrInUse
		}

		err := TryUnmount(mountPath, unix.MNT_DETACH)
		if err != nil {
			return false, err
		}

		d.Logger().Debug("Unmounted volume", logger.Ctx{"driver": d.Info().Name, "volName": vol.name, "path": mountPath, "keepBlockDev": keepBlockDev})

		// Attempt to unmap.
		if !keepBlockDev {
			err = d.unmapVolume(vol)
			if err != nil {
				return false, err
			}
		}

		ourUnmount = true
	} else if vol.contentType == ContentTypeBlock {
		// For VMs, unmount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolume(fsVol, false, op)
			if err != nil {
				return false, err
			}
		}

		if !keepBlockDev {
			// Check if device is currently mapped (but don't map if not).
			devPath, _, _ := d.getMappedDevPath(vol, false)
			if devPath != "" && shared.PathExists(devPath) {
				if refCount > 0 {
					d.Logger().Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
					return false, ErrInUse
				}

				// Attempt to unmap.
				err := d.unmapVolume(vol)
				if err != nil {
					return false, err
				}

				ourUnmount = true
			}
		}
	}

	return ourUnmount, nil
}
