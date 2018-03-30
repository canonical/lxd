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

// MigrateStoragePoolVolume requests that LXD prepares for a storage volume migration
func (r *ProtocolLXD) MigrateStoragePoolVolume(pool string, volume api.StorageVolumePost) (Operation, error) {
	if !r.HasExtension("storage_api_remote_volume_handling") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_remote_volume_handling\" API extension")
	}

	// Sanity check
	if !volume.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateStoragePoolVolume")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/custom/%s", url.QueryEscape(pool), volume.Name)
	if r.clusterTarget != "" {
		path += fmt.Sprintf("?target=%s", r.clusterTarget)
	}
	op, _, err := r.queryOperation("POST", path, volume, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryMigrateStoragePoolVolume(source ContainerServer, pool string, req api.StorageVolumePost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Target.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := map[string]error{}
		for _, serverURL := range urls {
			req.Target.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.QueryEscape(operation))

			// Send the request
			top, err := source.MigrateStoragePoolVolume(pool, req)
			if err != nil {
				errors[serverURL] = err
				continue
			}

			rop := remoteOperation{
				targetOp: top,
				chDone:   make(chan bool),
			}

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors[serverURL] = err
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed storage volume creation", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

func (r *ProtocolLXD) tryCreateStoragePoolVolume(pool string, req api.StorageVolumesPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Source.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := map[string]error{}
		for _, serverURL := range urls {
			req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.QueryEscape(operation))

			// Send the request
			path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.QueryEscape(pool), url.QueryEscape(req.Type))
			if r.clusterTarget != "" {
				path += fmt.Sprintf("?target=%s", r.clusterTarget)
			}
			top, _, err := r.queryOperation("POST", path, req, "")
			if err != nil {
				continue
			}

			rop := remoteOperation{
				targetOp: top,
				chDone:   make(chan bool),
			}

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors[serverURL] = err
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed storage volume creation", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CopyStoragePoolVolume copies an existing storage volume
func (r *ProtocolLXD) CopyStoragePoolVolume(pool string, source ContainerServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (RemoteOperation, error) {
	if !r.HasExtension("storage_api_local_volume_handling") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_local_volume_handling\" API extension")
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

	if r == source {
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

	if !r.HasExtension("storage_api_remote_volume_handling") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_remote_volume_handling\" API extension")
	}

	sourceReq := api.StorageVolumePost{
		Migration: true,
		Name:      volume.Name,
		Pool:      sourcePool,
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the container
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Send the request
		path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.QueryEscape(pool), url.QueryEscape(volume.Type))
		if r.clusterTarget != "" {
			path += fmt.Sprintf("?target=%s", r.clusterTarget)
		}

		// Send the request
		op, _, err := r.queryOperation("POST", path, req, "")
		if err != nil {
			return nil, err
		}
		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.StorageVolumePostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateStoragePoolVolume(source, sourcePool, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	// Get secrets from source server
	op, err := source.MigrateStoragePoolVolume(sourcePool, sourceReq)
	if err != nil {
		return nil, err
	}
	opAPI := op.Get()

	// Prepare source server secrets for remote
	sourceSecrets := map[string]string{}
	for k, v := range opAPI.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Send the request
		path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.QueryEscape(pool), url.QueryEscape(volume.Type))
		if r.clusterTarget != "" {
			path += fmt.Sprintf("?target=%s", r.clusterTarget)
		}

		// Send the request
		targetOp, _, err := r.queryOperation("POST", path, req, "")
		if err != nil {
			return nil, err
		}
		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Launch the relay
		err = r.proxyMigration(targetOp.(*operation), targetSecrets, source, op.(*operation), sourceSecrets)
		if err != nil {
			return nil, err
		}

		// Prepare a tracking operation
		rop := remoteOperation{
			targetOp: targetOp,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Pull mode migration
	req.Source.Type = "migration"
	req.Source.Mode = "pull"
	req.Source.Operation = opAPI.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateStoragePoolVolume(pool, req, info.Addresses)
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
