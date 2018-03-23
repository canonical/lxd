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
	if !r.HasExtension("storage") {
		return nil, fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

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
	if !r.HasExtension("storage") {
		return nil, "", fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	volume := api.StorageVolume{}

	// Fetch the raw value
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s",
		url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name))
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	etag, err := r.queryStruct("GET", path, nil, "", &volume)
	if err != nil {
		return nil, "", err
	}

	return &volume, etag, nil
}

// CreateStoragePoolVolume defines a new storage volume
func (r *ProtocolLXD) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error {
	if !r.HasExtension("storage") {
		return fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s", url.QueryEscape(pool), url.QueryEscape(volume.Type))
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	_, _, err := r.query("POST", path, volume, "")
	if err != nil {
		return err
	}

	return nil
}

// CopyStoragePoolVolume copies an existing storage volume
func (r *ProtocolLXD) CopyStoragePoolVolume(pool string, source ContainerServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (RemoteOperation, error) {
	if !r.HasExtension("storage_api_local_volume_handling") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_local_volume_handling\" API extension")
	}

	if r != source {
		return nil, fmt.Errorf("Copying storage volumes between remotes is not implemented")
	}

	req := api.StorageVolumesPost{
		Name: args.Name,
		Type: volume.Type,
		Source: api.StorageVolumeSource{
			Name: volume.Name,
			Type: "copy",
			Pool: sourcePool,
		},
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s", url.QueryEscape(pool), url.QueryEscape(volume.Type)), req, "")
	if err != nil {
		return nil, err
	}

	rop := remoteOperation{
		targetOp: op,
		chDone:   make(chan bool),
	}

	// Forward targetOp to remote op
	go func() {
		rop.err = rop.targetOp.Wait()
		close(rop.chDone)
	}()

	return &rop, nil
}

// MoveStoragePoolVolume renames or moves an existing storage volume
func (r *ProtocolLXD) MoveStoragePoolVolume(pool string, source ContainerServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeMoveArgs) (RemoteOperation, error) {
	if !r.HasExtension("storage_api_local_volume_handling") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_local_volume_handling\" API extension")
	}

	if r != source {
		return nil, fmt.Errorf("Moving storage volumes between remotes is not implemented")
	}

	req := api.StorageVolumePost{
		Name: args.Name,
		Pool: pool,
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.QueryEscape(sourcePool), url.QueryEscape(volume.Type), volume.Name), req, "")
	if err != nil {
		return nil, err
	}

	rop := remoteOperation{
		targetOp: op,
		chDone:   make(chan bool),
	}

	// Forward targetOp to remote op
	go func() {
		rop.err = rop.targetOp.Wait()
		close(rop.chDone)
	}()

	return &rop, nil
}

// UpdateStoragePoolVolume updates the volume to match the provided StoragePoolVolume struct
func (r *ProtocolLXD) UpdateStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePut, ETag string) error {
	if !r.HasExtension("storage") {
		return fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s",
		url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name))
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	_, _, err := r.query("PUT", path, volume, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePoolVolume deletes a storage pool
func (r *ProtocolLXD) DeleteStoragePoolVolume(pool string, volType string, name string) error {
	if !r.HasExtension("storage") {
		return fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s",
		url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name))
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	_, _, err := r.query("DELETE", path, nil, "")
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
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s",
		url.QueryEscape(pool), url.QueryEscape(volType), url.QueryEscape(name))
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}

	// Send the request
	_, _, err := r.query("POST", path, volume, "")
	if err != nil {
		return err
	}

	return nil
}
