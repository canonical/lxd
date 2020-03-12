package storage

import (
	"github.com/pkg/errors"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

var lxdEarlyPatches = map[string]func(b *lxdBackend) error{}

var lxdLatePatches = map[string]func(b *lxdBackend) error{
	"storage_create_vm":                        lxdPatchStorageCreateVM,
	"storage_create_vm_again":                  lxdPatchStorageCreateVM,
	"storage_rename_custom_volume_add_project": lxdPatchStorageRenameCustomVolumeAddProject,
}

// Patches start here.
func lxdPatchStorageCreateVM(b *lxdBackend) error {
	return b.createStorageStructure(drivers.GetPoolMountPath(b.name))
}

// lxdPatchStorageRenameCustomVolumeAddProject renames all custom volumes in the default project (which is all of
// the custom volumes right now) to have the project prefix added to the storage device volume name.
// This is so we can added project support to custom volumes and avoid any name collisions.
func lxdPatchStorageRenameCustomVolumeAddProject(b *lxdBackend) error {
	// Get all custom volumes in default project on this node.
	// At this time, all custom volumes are in the default project.
	volumes, err := b.state.Cluster.StoragePoolNodeVolumesGet(project.Default, b.ID(), []int{db.StoragePoolVolumeTypeCustom})
	if err != nil && err != db.ErrNoSuchObject {
		return errors.Wrapf(err, "Failed getting custom volumes for default project")
	}

	revert := revert.New()
	defer revert.Fail()

	for _, v := range volumes {
		// Run inside temporary function to ensure revert has correct volume scope.
		err = func(curVol *api.StorageVolume) error {
			// There's no need to pass the config as it's not needed when renaming a volume.
			oldVol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, curVol.Name, nil)

			// Add default project prefix to current volume name.
			newVolStorageName := project.StorageVolume(project.Default, curVol.Name)
			newVol := b.newVolume(drivers.VolumeTypeCustom, drivers.ContentTypeFS, newVolStorageName, nil)

			// Check if volume has already been renamed.
			if b.driver.HasVolume(newVol) {
				logger.Infof("Skipping already renamed custom volume %q in pool %q", newVol.Name(), b.Name())
				return nil
			}

			// Check if volume is currently mounted.
			oldMntPath := drivers.GetVolumeMountPath(b.Name(), drivers.VolumeTypeCustom, curVol.Name)

			// If the volume is mounted we need to be careful how we rename it to avoid interrupting a
			// running instance's attached volumes.
			ourUnmount := false
			if shared.IsMountPoint(oldMntPath) {
				logger.Infof("Lazy unmount custom volume %q in pool %q", curVol.Name, b.Name())
				err = unix.Unmount(oldMntPath, unix.MNT_DETACH)
				if err != nil {
					return err
				}
				ourUnmount = true
			}

			logger.Infof("Renaming custom volume %q in pool %q to %q", curVol.Name, b.Name(), newVolStorageName)
			err = b.driver.RenameVolume(oldVol, newVolStorageName, nil)
			if err != nil {
				return err
			}

			// Ensure we don't use the wrong volume for revert by using a temporary function.
			revert.Add(func() {
				logger.Infof("Reverting rename of custom volume %q in pool %q to %q", newVol.Name(), b.Name(), curVol.Name)
				b.driver.RenameVolume(newVol, curVol.Name, nil)
			})

			if ourUnmount {
				logger.Infof("Mount custom volume %q in pool %q", newVolStorageName, b.Name())
				_, err = b.driver.MountVolume(newVol, nil)
				if err != nil {
					return err
				}
			}

			return nil
		}(v)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}
