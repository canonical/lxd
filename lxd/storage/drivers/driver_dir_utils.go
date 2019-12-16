package drivers

import (
	"fmt"
	"os"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/storage/quota"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/units"
)

// copyVolume copies a volume and its specific snapshots.
func (d *dir) copyVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, op *operations.Operation) error {
	if vol.contentType != ContentTypeFS || srcVol.contentType != ContentTypeFS {
		return fmt.Errorf("Content type not supported")
	}

	bwlimit := d.config["rsync.bwlimit"]

	// Get the volume ID for the new volumes, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return err
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err = vol.CreateMountPath()
	if err != nil {
		return err
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config)
			d.DeleteVolumeSnapshot(snapVol, op)
		}

		os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if len(srcSnapshots) > 0 && !srcVol.IsSnapshot() {
			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := shared.InstanceGetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err = srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
					// Copy the snapshot.
					_, err = rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
					return err
				}, op)

				fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
				snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config)

				// Create the snapshot itself.
				err = d.CreateVolumeSnapshot(snapVol, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				revertSnaps = append(revertSnaps, snapName)
			}
		}

		// Initialise the volume's quota using the volume ID.
		err = d.initQuota(volPath, volID)
		if err != nil {
			return err
		}

		// Set the quota if specified in volConfig or pool config.
		err = d.setQuota(volPath, volID, vol.config["size"])
		if err != nil {
			return err
		}

		// Copy source to destination (mounting each volume if needed).
		return srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
			return err
		}, op)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil // Don't revert.
	return nil
}

// setupInitialQuota enables quota on a new volume and sets with an initial quota from config.
// Returns a revert function that can be used to remove the quota if there is a subsequent error.
func (d *dir) setupInitialQuota(vol Volume) (func(), error) {
	// Extract specified size from pool or volume config.
	size := d.config["volume.size"]
	if vol.config["size"] != "" {
		size = vol.config["size"]
	}

	volPath := vol.MountPath()

	// Get the volume ID for the new volume, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return nil, err
	}

	// Define a function to revert the quota being setup.
	revertFunc := func() {
		d.deleteQuota(volPath, volID)
	}

	// Initialise the volume's quota using the volume ID.
	err = d.initQuota(volPath, volID)
	if err != nil {
		return nil, err
	}

	revert := true
	defer func() {
		if revert {
			revertFunc()
		}
	}()

	// Set the quota.
	err = d.setQuota(volPath, volID, size)
	if err != nil {
		return nil, err
	}

	revert = false
	return revertFunc, nil
}

// deleteQuota removes the project quota for a volID from a path.
func (d *dir) deleteQuota(path string, volID int64) error {
	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		// Skipping quota as underlying filesystem doesn't suppport project quotas.
		return nil
	}

	err = quota.SetProject(path, 0)
	if err != nil {
		return err
	}

	err = quota.SetProjectQuota(path, d.quotaProjectID(volID), 0)
	if err != nil {
		return err
	}

	return nil
}

// initQuota initialises the project quota on the path. The volID generates a quota project ID.
func (d *dir) initQuota(path string, volID int64) error {
	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		// Skipping quota as underlying filesystem doesn't suppport project quotas.
		return nil
	}

	err = quota.SetProject(path, d.quotaProjectID(volID))
	if err != nil {
		return err
	}

	return nil
}

// quotaProjectID generates a project quota ID from a volume ID.
func (d *dir) quotaProjectID(volID int64) uint32 {
	return uint32(volID + 10000)
}

// setQuota sets the project quota on the path. The volID generates a quota project ID.
func (d *dir) setQuota(path string, volID int64, size string) error {
	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	// If size not specified in volume config, then use pool's default volume.size setting.
	if size == "" || size == "0" {
		size = d.config["volume.size"]
	}

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		if sizeBytes > 0 {
			// Skipping quota as underlying filesystem doesn't suppport project quotas.
			d.logger.Warn("The backing filesystem doesn't support quotas, skipping quota", log.Ctx{"path": path})
		}
		return nil
	}

	err = quota.SetProjectQuota(path, d.quotaProjectID(volID), sizeBytes)
	if err != nil {
		return err
	}

	return nil
}
