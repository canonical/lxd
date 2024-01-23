package lxd

import (
	"fmt"
	"net/url"

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
	baseURL := "/storage-pools"
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
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
	_, err = r.queryStruct("GET", "/storage-pools?recursion=1", nil, "", &pools)
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
	etag, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s", url.PathEscape(name)), nil, "", &pool)
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
	_, _, err = r.query("POST", "/storage-pools", pool, "")
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
	_, _, err = r.query("PUT", fmt.Sprintf("/storage-pools/%s", url.PathEscape(name)), pool, ETag)
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
	_, _, err = r.query("DELETE", fmt.Sprintf("/storage-pools/%s", url.PathEscape(name)), nil, "")
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
	_, err = r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/resources", url.PathEscape(name)), nil, "", &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}
