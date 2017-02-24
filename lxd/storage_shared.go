package main

import (
	"fmt"
	"os/exec"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type storageShared struct {
	storageCore

	d *Daemon

	poolID int64
	pool   *api.StoragePool

	volume *api.StorageVolume
}

func (ss *storageShared) shiftRootfs(c container) error {
	dpath := c.Path()
	rpath := c.RootfsPath()

	shared.LogDebugf("Shifting root filesystem \"%s\" for \"%s\".", rpath, c.Name())

	idmapset := c.IdmapSet()

	if idmapset == nil {
		return fmt.Errorf("IdmapSet of container '%s' is nil", c.Name())
	}

	err := idmapset.ShiftRootfs(rpath)
	if err != nil {
		shared.LogDebugf("Shift of rootfs %s failed: %s", rpath, err)
		return err
	}

	/* Set an acl so the container root can descend the container dir */
	// TODO: i changed this so it calls ss.setUnprivUserAcl, which does
	// the acl change only if the container is not privileged, think thats right.
	return ss.setUnprivUserAcl(c, dpath)
}

func (ss *storageShared) setUnprivUserAcl(c container, destPath string) error {
	idmapset := c.IdmapSet()

	// Skip for privileged containers
	if idmapset == nil {
		return nil
	}

	// Make sure the map is valid. Skip if container uid 0 == host uid 0
	uid, _ := idmapset.ShiftIntoNs(0, 0)
	switch uid {
	case -1:
		return fmt.Errorf("Container doesn't have a uid 0 in its map")
	case 0:
		return nil
	}

	// Attempt to set a POSIX ACL first. Fallback to chmod if the fs doesn't support it.
	acl := fmt.Sprintf("%d:rx", uid)
	_, err := exec.Command("setfacl", "-m", acl, destPath).CombinedOutput()
	if err != nil {
		_, err := exec.Command("chmod", "+x", destPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed to chmod the container path: %s.", err)
		}
	}

	return nil
}

func (ss *storageShared) createImageDbPoolVolume(fingerprint string) error {
	// Fill in any default volume config.
	volumeConfig := map[string]string{}
	err := storageVolumeFillDefault(ss.pool.Name, volumeConfig, ss.pool)
	if err != nil {
		return err
	}

	// Create a db entry for the storage volume of the image.
	_, err = dbStoragePoolVolumeCreate(ss.d.db, fingerprint, storagePoolVolumeTypeImage, ss.poolID, volumeConfig)
	if err != nil {
		// Try to delete the db entry on error.
		dbStoragePoolVolumeDelete(ss.d.db, fingerprint, storagePoolVolumeTypeImage, ss.poolID)
		return err
	}

	return nil
}

func (ss *storageShared) deleteImageDbPoolVolume(fingerprint string) error {
	err := dbStoragePoolVolumeDelete(ss.d.db, fingerprint, storagePoolVolumeTypeImage, ss.poolID)
	if err != nil {
		return err
	}

	return nil
}
