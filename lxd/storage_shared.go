package main

import (
	"fmt"
	"os"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

type storageShared struct {
	sType        storageType
	sTypeName    string
	sTypeVersion string

	s *state.State

	poolID int64
	pool   *api.StoragePool

	volume *api.StorageVolume
}

func (s *storageShared) GetStorageType() storageType {
	return s.sType
}

func (s *storageShared) GetStorageTypeName() string {
	return s.sTypeName
}

func (s *storageShared) GetStorageTypeVersion() string {
	return s.sTypeVersion
}

func (s *storageShared) shiftRootfs(c container, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	dpath := c.Path()
	rpath := c.RootfsPath()

	logger.Debugf("Shifting root filesystem \"%s\" for \"%s\"", rpath, c.Name())

	idmapset, err := c.IdmapSet()
	if err != nil {
		return errors.Wrap(err, "Get ID map")
	}

	if idmapset == nil {
		return fmt.Errorf("IdmapSet of container '%s' is nil", c.Name())
	}

	err = idmapset.ShiftRootfs(rpath, skipper)
	if err != nil {
		logger.Debugf("Shift of rootfs %s failed: %s", rpath, err)
		return errors.Wrap(err, "Shift rootfs")
	}

	/* Set an acl so the container root can descend the container dir */
	// TODO: i changed this so it calls s.setUnprivUserAcl, which does
	// the acl change only if the container is not privileged, think thats right.
	return s.setUnprivUserACL(c, dpath)
}

func (s *storageShared) setUnprivUserACL(c container, destPath string) error {
	idmapset, err := c.IdmapSet()
	if err != nil {
		return err
	}

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

	// Attempt to set a POSIX ACL first.
	acl := fmt.Sprintf("%d:rx", uid)
	_, err = shared.RunCommand("setfacl", "-m", acl, destPath)
	if err == nil {
		return nil
	}

	// Fallback to chmod if the fs doesn't support it.
	_, err = shared.RunCommand("chmod", "+x", destPath)
	if err != nil {
		logger.Debugf("Failed to set executable bit on the container path: %s", err)
		return err
	}

	return nil
}

func (s *storageShared) createImageDbPoolVolume(fingerprint string) error {
	// Fill in any default volume config.
	volumeConfig := map[string]string{}
	err := storageVolumeFillDefault(fingerprint, volumeConfig, s.pool)
	if err != nil {
		return err
	}

	// Create a db entry for the storage volume of the image.
	_, err = s.s.Cluster.StoragePoolVolumeCreate("default", fingerprint, "", storagePoolVolumeTypeImage, false, s.poolID, volumeConfig)
	if err != nil {
		// Try to delete the db entry on error.
		s.deleteImageDbPoolVolume(fingerprint)
		return err
	}

	return nil
}

func (s *storageShared) deleteImageDbPoolVolume(fingerprint string) error {
	err := s.s.Cluster.StoragePoolVolumeDelete("default", fingerprint, storagePoolVolumeTypeImage, s.poolID)
	if err != nil {
		return err
	}

	return nil
}
