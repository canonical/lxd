package lxd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

// Storage volumes handling function

// GetStoragePoolVolumeNames returns the names of all volumes in a pool
func (r *ProtocolLXD) GetStoragePoolVolumeNames(pool string) ([]string, error) {
	if !r.HasExtension("storage") {
		return nil, fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes", url.QueryEscape(pool)), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, uri := range urls {
		fields := strings.Split(uri, fmt.Sprintf("/storage-pools/%s/volumes/", url.QueryEscape(pool)))
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetStoragePoolVolumes returns a list of StorageVolume entries for the provided pool
func (r *ProtocolLXD) GetStoragePoolVolumes(pool string) ([]api.StorageVolume, error) {
	volumes := []api.StorageVolume{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes?recursion=1", url.QueryEscape(pool)), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolume returns a StorageVolume entry for the provided pool and volume name
func (r *ProtocolLXD) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	volume := api.StorageVolume{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name)), nil, "", &volume)
	if err != nil {
		return nil, "", err
	}

	return &volume, etag, nil
}

// CreateStoragePoolVolume defines a new storage volume
func (r *ProtocolLXD) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error {
	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s", url.QueryEscape(pool), url.QueryEscape(volume.Type)), volume, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateStoragePoolVolume updates the volume to match the provided StoragePoolVolume struct
func (r *ProtocolLXD) UpdateStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name)), volume, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePoolVolume deletes a storage pool
func (r *ProtocolLXD) DeleteStoragePoolVolume(pool string, volType string, name string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// RenameStoragePoolVolume renames a storage volume
func (r *ProtocolLXD) RenameStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePost) error {
	if !r.HasExtension("storage_api_volume_rename") {
		return fmt.Errorf("The server is missing the required \"storage_api_volume_rename\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name)), volume, "")
	if err != nil {
		return err
	}

	return nil
}
