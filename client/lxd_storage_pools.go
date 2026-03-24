package lxd

import (
	"fmt"
	"net/http"
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
	_, err = r.queryStruct(http.MethodGet, "/storage-pools?recursion=1", nil, "", &pools)
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
	etag, err := r.queryStruct(http.MethodGet, "/storage-pools/"+url.PathEscape(name), nil, "", &pool)
	if err != nil {
		return nil, "", err
	}

	return &pool, etag, nil
}

// CreateStoragePool defines a new storage pool using the provided StoragePool struct.
func (r *ProtocolLXD) CreateStoragePool(pool api.StoragePoolsPost) (Operation, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	if pool.Driver == "ceph" {
		err := r.CheckExtension("storage_driver_ceph")
		if err != nil {
			return nil, err
		}
	}

	var op Operation

	// Send the request
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPost, "/storage-pools", pool, "")
	} else {
		op, _, err = r.queryOperation(http.MethodPost, "/storage-pools", pool, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePool updates the pool to match the provided StoragePool struct.
func (r *ProtocolLXD) UpdateStoragePool(name string, pool api.StoragePoolPut, ETag string) error {
	err := r.CheckExtension("storage")
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query(http.MethodPut, "/storage-pools/"+url.PathEscape(name), pool, ETag)
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
	_, _, err = r.query(http.MethodDelete, "/storage-pools/"+url.PathEscape(name), nil, "")
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
	_, err = r.queryStruct(http.MethodGet, fmt.Sprintf("/storage-pools/%s/resources", url.PathEscape(name)), nil, "", &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}
