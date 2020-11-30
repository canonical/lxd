package drivers

import (
	"fmt"

	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/storage/quota"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/units"
)

// withoutGetVolID returns a copy of this struct but with a volIDFunc which will cause quotas to be skipped.
func (d *dir) withoutGetVolID() Driver {
	newDriver := &dir{}
	getVolID := func(volType VolumeType, volName string) (int64, error) { return volIDQuotaSkip, nil }
	newDriver.init(d.state, d.name, d.config, d.logger, getVolID, d.commonRules)
	newDriver.load()

	return newDriver
}

// setupInitialQuota enables quota on a new volume and sets with an initial quota from config.
// Returns a revert function that can be used to remove the quota if there is a subsequent error.
func (d *dir) setupInitialQuota(vol Volume) (func(), error) {
	if vol.IsVMBlock() {
		return nil, nil
	}

	volPath := vol.MountPath()

	// Get the volume ID for the new volume, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Initialise the volume's quota using the volume ID.
	err = d.initQuota(volPath, volID)
	if err != nil {
		return nil, err
	}

	// Define a function to revert the quota being setup.
	revertFunc := func() { d.deleteQuota(volPath, volID) }
	revert.Add(revertFunc)

	// Set the quota.
	err = d.setQuota(volPath, volID, vol.ConfigSize())
	if err != nil {
		return nil, err
	}

	revert.Success()
	return revertFunc, nil
}

// deleteQuota removes the project quota for a volID from a path.
func (d *dir) deleteQuota(path string, volID int64) error {
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore
		return nil
	}

	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		// Skipping quota as underlying filesystem doesn't suppport project quotas.
		return nil
	}

	err = quota.DeleteProject(path, d.quotaProjectID(volID))
	if err != nil {
		return err
	}

	return nil
}

// initQuota initialises the project quota on the path. The volID generates a quota project ID.
func (d *dir) initQuota(path string, volID int64) error {
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore
		return nil
	}

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
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore
		return 0
	}

	return uint32(volID + 10000)
}

// setQuota sets the project quota on the path. The volID generates a quota project ID.
func (d *dir) setQuota(path string, volID int64, size string) error {
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore.
		return nil
	}

	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		if sizeBytes > 0 {
			// Skipping quota as underlying filesystem doesn't suppport project quotas.
			d.logger.Warn("The backing filesystem doesn't support quotas, skipping set quota", log.Ctx{"path": path, "size": size, "volID": volID})
		}
		return nil
	}

	return quota.SetProjectQuota(path, d.quotaProjectID(volID), sizeBytes)
}
