package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// Storage pool handling functions

// GetStoragePoolNames returns the names of all storage pools.
func (r *ProtocolLXD) GetStoragePoolNames() ([]string, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("storage-pools")
	baseURL := u.String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePools returns a list of StoragePool entries.
func (r *ProtocolLXD) GetStoragePools() ([]api.StoragePool, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	pools := []api.StoragePool{}

	// Fetch the raw value
	u := api.NewURL().Path("storage-pools").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &pools)
	if err != nil {
		return nil, err
	}

	return pools, nil
}

// GetStoragePool returns a StoragePool entry for the provided pool name.
func (r *ProtocolLXD) GetStoragePool(name string) (*api.StoragePool, string, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, "", err
	}

	pool := api.StoragePool{}

	// Fetch the raw value
	u := api.NewURL().Path("storage-pools", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &pool)
	if err != nil {
		return nil, "", err
	}

	return &pool, etag, nil
}

// CreateStoragePool defines a new storage pool using the provided StoragePool struct.
func (r *ProtocolLXD) CreateStoragePool(pool api.StoragePoolsPost) error {
	err := r.CheckExtension("storage")
	if err != nil {
		return err
	}

	if pool.Driver == "ceph" {
		err := r.CheckExtension("storage_driver_ceph")
		if err != nil {
			return err
		}
	}

	// Send the request
	u := api.NewURL().Path("storage-pools")
	_, _, err = r.query(http.MethodPost, u.String(), pool, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateStoragePool updates the pool to match the provided StoragePool struct.
func (r *ProtocolLXD) UpdateStoragePool(name string, pool api.StoragePoolPut, ETag string) error {
	err := r.CheckExtension("storage")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("storage-pools", name)
	_, _, err = r.query(http.MethodPut, u.String(), pool, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePool deletes a storage pool.
func (r *ProtocolLXD) DeleteStoragePool(name string) error {
	err := r.CheckExtension("storage")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("storage-pools", name)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetStoragePoolResources gets the resources available to a given storage pool.
func (r *ProtocolLXD) GetStoragePoolResources(name string) (*api.ResourcesStoragePool, error) {
	err := r.CheckExtension("resources")
	if err != nil {
		return nil, err
	}

	res := api.ResourcesStoragePool{}

	// Fetch the raw value
	u := api.NewURL().Path("storage-pools", name, "resources")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}
