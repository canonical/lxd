package lxd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

// Storage pool handling functions

// GetStoragePoolNames returns the names of all storage pools
func (r *ProtocolLXD) GetStoragePoolNames() ([]string, error) {
	if !r.HasExtension("storage") {
		return nil, fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/storage-pools", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, url := range urls {
		fields := strings.Split(url, "/storage-pools/")
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetStoragePools returns a list of StoragePool entries
func (r *ProtocolLXD) GetStoragePools() ([]api.StoragePool, error) {
	if !r.HasExtension("storage") {
		return nil, fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	pools := []api.StoragePool{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/storage-pools?recursion=1", nil, "", &pools)
	if err != nil {
		return nil, err
	}

	return pools, nil
}

// GetStoragePool returns a StoragePool entry for the provided pool name
func (r *ProtocolLXD) GetStoragePool(name string) (*api.StoragePool, string, error) {
	if !r.HasExtension("storage") {
		return nil, "", fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	pool := api.StoragePool{}

	// Fetch the raw value
	path := fmt.Sprintf("/storage-pools/%s", url.QueryEscape(name))
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	etag, err := r.queryStruct("GET", path, nil, "", &pool)
	if err != nil {
		return nil, "", err
	}

	return &pool, etag, nil
}

// CreateStoragePool defines a new storage pool using the provided StoragePool struct
func (r *ProtocolLXD) CreateStoragePool(pool api.StoragePoolsPost) error {
	if !r.HasExtension("storage") {
		return fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	if pool.Driver == "ceph" && !r.HasExtension("storage_driver_ceph") {
		return fmt.Errorf("The server is missing the required \"storage_driver_ceph\" API extension")
	}

	// Send the request
	path := "/storage-pools"
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	_, _, err := r.query("POST", path, pool, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateStoragePool updates the pool to match the provided StoragePool struct
func (r *ProtocolLXD) UpdateStoragePool(name string, pool api.StoragePoolPut, ETag string) error {
	if !r.HasExtension("storage") {
		return fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/storage-pools/%s", url.QueryEscape(name)), pool, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePool deletes a storage pool
func (r *ProtocolLXD) DeleteStoragePool(name string) error {
	if !r.HasExtension("storage") {
		return fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/storage-pools/%s", url.QueryEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetStoragePoolResources gets the resources available to a given storage pool
func (r *ProtocolLXD) GetStoragePoolResources(name string) (*api.ResourcesStoragePool, error) {
	if !r.HasExtension("resources") {
		return nil, fmt.Errorf("The server is missing the required \"resources\" API extension")
	}

	res := api.ResourcesStoragePool{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/resources", url.QueryEscape(name)), nil, "", &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}
