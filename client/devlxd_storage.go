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
