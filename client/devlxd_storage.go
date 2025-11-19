package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetStoragePool retrieves the storage pool with a given name.
func (r *ProtocolDevLXD) GetStoragePool(poolName string) (*api.DevLXDStoragePool, string, error) {
	var pool api.DevLXDStoragePool

	url := api.NewURL().Path("storage-pools", poolName).URL
	etag, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &pool)
	if err != nil {
		return nil, "", err
	}

	return &pool, etag, nil
}

// GetStoragePoolVolumes retrieves the storage volumes from a storage pool with a given name.
func (r *ProtocolDevLXD) GetStoragePoolVolumes(poolName string) ([]api.DevLXDStorageVolume, error) {
	var vols []api.DevLXDStorageVolume

	url := api.NewURL().Path("storage-pools", poolName, "volumes").WithQuery("recursion", "1").URL
	r.setURLQueryAttributes(&url)

	_, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &vols)
	if err != nil {
		return nil, err
	}

	return vols, nil
}

// GetStoragePoolVolume retrieves the storage volume with a given name.
func (r *ProtocolDevLXD) GetStoragePoolVolume(poolName string, volType string, volName string) (*api.DevLXDStorageVolume, string, error) {
	var vol api.DevLXDStorageVolume

	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName).URL
	r.setURLQueryAttributes(&url)

	etag, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &vol)
	if err != nil {
		return nil, "", err
	}

	return &vol, etag, nil
}

// CreateStoragePoolVolume creates a new storage volume in a given storage pool.
func (r *ProtocolDevLXD) CreateStoragePoolVolume(poolName string, vol api.DevLXDStorageVolumesPost) (DevLXDOperation, error) {
	url := api.NewURL().Path("storage-pools", poolName, "volumes", vol.Type).URL
	r.setURLQueryAttributes(&url)

	op, _, err := r.queryOperation(http.MethodPost, url.String(), vol, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePoolVolume updates an existing storage volume in a given storage pool.
func (r *ProtocolDevLXD) UpdateStoragePoolVolume(poolName string, volType string, volName string, vol api.DevLXDStorageVolumePut, etag string) (DevLXDOperation, error) {
	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName).URL
	r.setURLQueryAttributes(&url)

	op, _, err := r.queryOperation(http.MethodPatch, url.String(), vol, etag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolume deletes a storage volume from a given storage pool.
func (r *ProtocolDevLXD) DeleteStoragePoolVolume(poolName string, volType string, volName string) (DevLXDOperation, error) {
	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName).URL
	r.setURLQueryAttributes(&url)

	op, _, err := r.queryOperation(http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeSnapshots retrieves the storage volume snapshots for the given volume.
func (r *ProtocolDevLXD) GetStoragePoolVolumeSnapshots(poolName string, volType string, volName string) ([]api.DevLXDStorageVolumeSnapshot, error) {
	var snapshots []api.DevLXDStorageVolumeSnapshot

	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName, "snapshots").WithQuery("recursion", "1").URL
	r.setURLQueryAttributes(&url)

	_, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetStoragePoolVolumeSnapshot retrieves the storage volume snapshot for the given volume.
func (r *ProtocolDevLXD) GetStoragePoolVolumeSnapshot(poolName string, volType string, volName string, snapshotName string) (*api.DevLXDStorageVolumeSnapshot, string, error) {
	var snap api.DevLXDStorageVolumeSnapshot

	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName, "snapshots", snapshotName).URL
	r.setURLQueryAttributes(&url)

	etag, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &snap)
	if err != nil {
		return nil, "", err
	}

	return &snap, etag, nil
}

// CreateStoragePoolVolumeSnapshot creates a new storage volume snapshot for the given storage volume.
func (r *ProtocolDevLXD) CreateStoragePoolVolumeSnapshot(poolName string, volType string, volName string, snapshot api.DevLXDStorageVolumeSnapshotsPost) (DevLXDOperation, error) {
	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName, "snapshots").URL
	r.setURLQueryAttributes(&url)

	op, _, err := r.queryOperation(http.MethodPost, url.String(), snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolumeSnapshot deletes a storage volume snapshot for the given storage volume.
func (r *ProtocolDevLXD) DeleteStoragePoolVolumeSnapshot(poolName string, volType string, volName string, snapshotName string) (DevLXDOperation, error) {
	url := api.NewURL().Path("storage-pools", poolName, "volumes", volType, volName, "snapshots", snapshotName).URL
	r.setURLQueryAttributes(&url)

	op, _, err := r.queryOperation(http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}
