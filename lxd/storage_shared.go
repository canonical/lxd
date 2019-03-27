package main

import (
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
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

func (s *storageShared) createImageDbPoolVolume(fingerprint string) error {
	// Fill in any default volume config.
	volumeConfig := map[string]string{}
	err := storageVolumeFillDefault(fingerprint, volumeConfig, s.pool)
	if err != nil {
		return err
	}

	// Create a db entry for the storage volume of the image.
	_, err = s.s.Cluster.StoragePoolVolumeCreate(fingerprint, "", storagePoolVolumeTypeImage, s.poolID, volumeConfig)
	if err != nil {
		// Try to delete the db entry on error.
		s.deleteImageDbPoolVolume(fingerprint)
		return err
	}

	return nil
}

func (s *storageShared) deleteImageDbPoolVolume(fingerprint string) error {
	err := s.s.Cluster.StoragePoolVolumeDelete(fingerprint, storagePoolVolumeTypeImage, s.poolID)
	if err != nil {
		return err
	}

	return nil
}
