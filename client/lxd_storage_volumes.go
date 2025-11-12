package lxd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/units"
)

// Storage volumes handling function

// GetStoragePoolVolumeNames returns the names of all volumes in a pool.
func (r *ProtocolLXD) GetStoragePoolVolumeNames(pool string) ([]string, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/storage-pools/" + url.PathEscape(pool) + "/volumes"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePoolVolumeNamesAllProjects returns the names of all volumes in a pool for all projects.
func (r *ProtocolLXD) GetStoragePoolVolumeNamesAllProjects(pool string) (map[string][]string, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("storage-pools", pool, "volumes").WithQuery("all-projects", "true")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNamesAllProjects(u.String(), urls...)
}

// GetStoragePoolVolumes returns a list of StorageVolume entries for the provided pool.
func (r *ProtocolLXD) GetStoragePoolVolumes(pool string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	// Fetch the raw value
	_, err = r.queryStruct(http.MethodGet, "/storage-pools/"+url.PathEscape(pool)+"/volumes?recursion=1", nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesAllProjects returns a list of StorageVolume entries for the provided pool for all projects.
func (r *ProtocolLXD) GetStoragePoolVolumesAllProjects(pool string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	url := api.NewURL().Path("storage-pools", pool, "volumes").
		WithQuery("recursion", "1").
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, url.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetVolumesWithFilter returns a filtered list of StorageVolume entries for all storage pools.
func (r *ProtocolLXD) GetVolumesWithFilter(filters []string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	url := api.NewURL().Path("storage-volumes").
		WithQuery("recursion", "1").
		WithQuery("filter", parseFilters(filters))

	// Fetch the raw value
	_, err = r.queryStruct(http.MethodGet, url.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetVolumesWithFilterAllProjects returns a filtered list of StorageVolume entries for all storage pools and for all projects.
func (r *ProtocolLXD) GetVolumesWithFilterAllProjects(filters []string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	url := api.NewURL().Path("storage-volumes").
		WithQuery("recursion", "1").
		WithQuery("filter", parseFilters(filters)).
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, url.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesWithFilter returns a filtered list of StorageVolume entries for the provided pool.
func (r *ProtocolLXD) GetStoragePoolVolumesWithFilter(pool string, filters []string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))
	// Fetch the raw value
	_, err = r.queryStruct(http.MethodGet, "/storage-pools/"+url.PathEscape(pool)+"/volumes?"+v.Encode(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesWithFilterAllProjects returns a filtered list of StorageVolume entries for the provided pool for all projects.
func (r *ProtocolLXD) GetStoragePoolVolumesWithFilterAllProjects(pool string, filters []string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	url := api.NewURL().Path("storage-pools", pool, "volumes").
		WithQuery("recursion", "1").
		WithQuery("filter", parseFilters(filters)).
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, url.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolume returns a StorageVolume entry for the provided pool and volume name.
func (r *ProtocolLXD) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, "", err
	}

	volume := api.StorageVolume{}

	// Fetch the raw value
	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volType) + "/" + url.PathEscape(name)
	etag, err := r.queryStruct(http.MethodGet, path, nil, "", &volume)
	if err != nil {
		return nil, "", err
	}

	return &volume, etag, nil
}

// GetStoragePoolVolumeState returns a StorageVolumeState entry for the provided pool and volume name.
func (r *ProtocolLXD) GetStoragePoolVolumeState(pool string, volType string, name string) (*api.StorageVolumeState, error) {
	err := r.CheckExtension("storage_volume_state")
	if err != nil {
		return nil, err
	}

	// Fetch the raw value
	state := api.StorageVolumeState{}
	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volType) + "/" + url.PathEscape(name) + "/state"
	_, err = r.queryStruct(http.MethodGet, path, nil, "", &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// CreateStoragePoolVolume defines a new storage volume.
func (r *ProtocolLXD) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) (Operation, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	var op Operation

	// Send the request
	path := api.NewURL().Path("storage-pools", url.PathEscape(pool), "volumes", url.PathEscape(volume.Type))
	err = r.CheckExtension("storage_and_profile_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPost, path.String(), volume, "")
	} else {
		op, _, err = r.queryOperation(http.MethodPost, path.String(), volume, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// CreateStoragePoolVolumeSnapshot defines a new storage volume.
func (r *ProtocolLXD) CreateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshot api.StorageVolumeSnapshotsPost) (Operation, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, err
	}

	// Send the request
	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volumeType) + "/" + url.PathEscape(volumeName) + "/snapshots"
	op, _, err := r.queryOperation(http.MethodPost, path, snapshot, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeSnapshotNames returns a list of snapshot names for the
// storage volume.
func (r *ProtocolLXD) GetStoragePoolVolumeSnapshotNames(pool string, volumeType string, volumeName string) ([]string, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volumeType) + "/" + url.PathEscape(volumeName) + "/snapshots"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePoolVolumeSnapshots returns a list of snapshots for the storage
// volume.
func (r *ProtocolLXD) GetStoragePoolVolumeSnapshots(pool string, volumeType string, volumeName string) ([]api.StorageVolumeSnapshot, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, err
	}

	snapshots := []api.StorageVolumeSnapshot{}

	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volumeType) + "/" + url.PathEscape(volumeName) + "/snapshots?recursion=1"
	_, err = r.queryStruct(http.MethodGet, path, nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetStoragePoolVolumeSnapshot returns a snapshots for the storage volume.
func (r *ProtocolLXD) GetStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (*api.StorageVolumeSnapshot, string, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, "", err
	}

	snapshot := api.StorageVolumeSnapshot{}

	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volumeType) + "/" + url.PathEscape(volumeName) + "/snapshots/" + url.PathEscape(snapshotName)
	etag, err := r.queryStruct(http.MethodGet, path, nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// RenameStoragePoolVolumeSnapshot renames a storage volume snapshot.
func (r *ProtocolLXD) RenameStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, snapshot api.StorageVolumeSnapshotPost) (Operation, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, err
	}

	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volumeType) + "/" + url.PathEscape(volumeName) + "/snapshots/" + url.PathEscape(snapshotName)
	// Send the request
	op, _, err := r.queryOperation(http.MethodPost, path, snapshot, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolumeSnapshot deletes a storage volume snapshot.
func (r *ProtocolLXD) DeleteStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (Operation, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, err
	}

	// Send the request
	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volumeType) + "/" + url.PathEscape(volumeName) + "/snapshots/" + url.PathEscape(snapshotName)

	op, _, err := r.queryOperation(http.MethodDelete, path, nil, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePoolVolumeSnapshot updates the volume to match the provided StoragePoolVolume struct.
func (r *ProtocolLXD) UpdateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, volume api.StorageVolumeSnapshotPut, ETag string) (Operation, error) {
	err := r.CheckExtension("storage_api_volume_snapshots")
	if err != nil {
		return nil, err
	}

	var op Operation

	// Send the request
	path := api.NewURL().Path("storage-pools", pool, "volumes", volumeType, volumeName, "snapshots", snapshotName)
	err = r.CheckExtension("storage_and_profile_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, path.String(), volume, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, path.String(), volume, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// MigrateStoragePoolVolume requests that LXD prepares for a storage volume migration.
func (r *ProtocolLXD) MigrateStoragePoolVolume(pool string, volume api.StorageVolumePost) (Operation, error) {
	err := r.CheckExtension("storage_api_remote_volume_handling")
	if err != nil {
		return nil, err
	}

	// Quick check.
	if !volume.Migration {
		return nil, errors.New("Can't ask for a rename through MigrateStoragePoolVolume")
	}

	var req any
	var path string

	srcVolParentName, srcVolSnapName, srcIsSnapshot := api.GetParentAndSnapshotName(volume.Name)
	if srcIsSnapshot {
		err := r.CheckExtension("storage_api_remote_volume_snapshot_copy")
		if err != nil {
			return nil, err
		}

		// Set the actual name of the snapshot without delimiter.
		req = api.StorageVolumeSnapshotPost{
			Name:      srcVolSnapName,
			Migration: volume.Migration,
			Target:    volume.Target,
		}

		path = api.NewURL().Path("storage-pools", pool, "volumes", "custom", srcVolParentName, "snapshots", srcVolSnapName).String()
	} else {
		req = volume
		path = api.NewURL().Path("storage-pools", pool, "volumes", "custom", volume.Name).String()
	}

	// Send the request
	op, _, err := r.queryOperation(http.MethodPost, path, req, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryMigrateStoragePoolVolume(source InstanceServer, pool string, req api.StorageVolumePost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, errors.New("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Target.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		var errors []remoteOperationResult
		for _, serverURL := range urls {
			req.Target.Operation = serverURL + "/1.0/operations/" + url.PathEscape(operation)

			// Send the request
			top, err := source.MigrateStoragePoolVolume(pool, req)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop := remoteOperation{
				targetOp: top,
				chDone:   make(chan bool),
			}

			for _, handler := range rop.handlers {
				_, _ = rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})

				if shared.IsConnectionError(err) {
					continue
				}

				break
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

// tryCreateStoragePoolVolume attempts to create a storage volume in the specified storage pool.
// It will try to do this on every server in the provided list of urls, and waits for the creation to be complete.
func (r *ProtocolLXD) tryCreateStoragePoolVolume(pool string, req api.StorageVolumesPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, errors.New("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Source.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		var errors []remoteOperationResult
		for _, serverURL := range urls {
			req.Source.Operation = serverURL + "/1.0/operations/" + url.PathEscape(operation)

			// Send the request
			path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(req.Type)
			top, _, err := r.queryOperation(http.MethodPost, path, req, "", true)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop := remoteOperation{
				targetOp: top,
				chDone:   make(chan bool),
			}

			for _, handler := range rop.handlers {
				_, _ = rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})

				if shared.IsConnectionError(err) {
					continue
				}

				break
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

// CopyStoragePoolVolume copies an existing storage volume.
func (r *ProtocolLXD) CopyStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (RemoteOperation, error) {
	err := r.CheckExtension("storage_api_local_volume_handling")
	if err != nil {
		return nil, err
	}

	if args != nil && args.VolumeOnly && r.CheckExtension("storage_api_volume_snapshots") != nil {
		return nil, errors.New("The target server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	if args != nil && args.Refresh && r.CheckExtension("custom_volume_refresh") != nil {
		return nil, errors.New("The target server is missing the required \"custom_volume_refresh\" API extension")
	}

	req := api.StorageVolumesPost{
		Name: args.Name,
		Type: volume.Type,
		Source: api.StorageVolumeSource{
			Name:       volume.Name,
			Type:       api.SourceTypeCopy,
			Pool:       sourcePool,
			VolumeOnly: args.VolumeOnly,
			Refresh:    args.Refresh,
		},
	}

	req.Config = volume.Config
	req.Description = volume.Description
	req.ContentType = volume.ContentType

	sourceInfo, err := source.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get source connection info: %w", err)
	}

	destInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get destination connection info: %w", err)
	}

	clusterInternalVolumeCopy := r.CheckExtension("cluster_internal_custom_volume_copy") == nil

	// Copy the storage pool volume locally.
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (volume.Location == r.clusterTarget || (volume.Location == "none" && r.clusterTarget == "") || clusterInternalVolumeCopy) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			err := r.CheckExtension("storage_api_project")
			if err != nil {
				return nil, err
			}

			req.Source.Project = sourceInfo.Project
		}

		if clusterInternalVolumeCopy {
			req.Source.Location = sourceInfo.Target
		}

		// Send the request
		op, _, err := r.queryOperation(http.MethodPost, "/storage-pools/"+url.PathEscape(pool)+"/volumes/"+url.PathEscape(volume.Type), req, "", true)
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

	err = r.CheckExtension("storage_api_remote_volume_handling")
	if err != nil {
		return nil, err
	}

	sourceReq := api.StorageVolumePost{
		Migration: true,
		Name:      volume.Name,
		Pool:      sourcePool,
	}

	if args != nil {
		sourceReq.VolumeOnly = args.VolumeOnly
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the container
		req.Source.Type = api.SourceTypeMigration
		req.Source.Mode = "push"

		// Send the request
		path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volume.Type)

		// Send the request
		op, _, err := r.queryOperation(http.MethodPost, path, req, "", true)
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			value, ok := v.(string)
			if ok {
				targetSecrets[k] = value
			}
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
		value, ok := v.(string)
		if ok {
			sourceSecrets[k] = value
		}
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = api.SourceTypeMigration
		req.Source.Mode = "push"

		// Send the request
		path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/" + url.PathEscape(volume.Type)

		// Send the request
		targetOp, _, err := r.queryOperation(http.MethodPost, path, req, "", true)
		if err != nil {
			return nil, err
		}

		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			value, ok := v.(string)
			if ok {
				targetSecrets[k] = value
			}
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
	req.Source.Type = api.SourceTypeMigration
	req.Source.Mode = "pull"
	req.Source.Operation = opAPI.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateStoragePoolVolume(pool, req, info.Addresses)
}

// MoveStoragePoolVolume renames or moves an existing storage volume.
func (r *ProtocolLXD) MoveStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeMoveArgs) (RemoteOperation, error) {
	err := r.CheckExtension("storage_api_local_volume_handling")
	if err != nil {
		return nil, err
	}

	if r != source {
		return nil, errors.New("Moving storage volumes between remotes is not implemented")
	}

	req := api.StorageVolumePost{
		Name: args.Name,
		Pool: pool,
	}

	if args.Project != "" {
		err := r.CheckExtension("storage_volume_project_move")
		if err != nil {
			return nil, err
		}

		req.Project = args.Project
	}

	// Send the request
	op, _, err := r.queryOperation(http.MethodPost, "/storage-pools/"+url.PathEscape(sourcePool)+"/volumes/"+url.PathEscape(volume.Type)+"/"+url.PathEscape(volume.Name), req, "", true)
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

// UpdateStoragePoolVolume updates the volume to match the provided StoragePoolVolume struct.
func (r *ProtocolLXD) UpdateStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePut, ETag string) (Operation, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	if volume.Restore != "" {
		err := r.CheckExtension("storage_api_volume_snapshots")
		if err != nil {
			return nil, err
		}
	}

	var op Operation

	// Send the request
	path := api.NewURL().Path("storage-pools", pool, "volumes", volType, name)
	err = r.CheckExtension("storage_and_profile_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, path.String(), volume, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, path.String(), volume, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolume deletes a storage pool.
func (r *ProtocolLXD) DeleteStoragePoolVolume(pool string, volType string, name string) (Operation, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	var op Operation

	// Send the request
	path := api.NewURL().Path("storage-pools", pool, "volumes", volType, name)
	err = r.CheckExtension("storage_and_profile_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodDelete, path.String(), nil, "")
	} else {
		op, _, err = r.queryOperation(http.MethodDelete, path.String(), nil, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameStoragePoolVolume renames a storage volume.
func (r *ProtocolLXD) RenameStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePost) (Operation, error) {
	err := r.CheckExtension("storage_api_volume_rename")
	if err != nil {
		return nil, err
	}

	path := api.NewURL().Path("storage-pools", pool, "volumes", volType, name)

	var op Operation

	// Send the request
	err = r.CheckExtension("storage_and_profile_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPost, path.String(), volume, "")
	} else {
		op, _, err = r.queryOperation(http.MethodPost, path.String(), volume, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeBackupNames returns a list of volume backup names.
func (r *ProtocolLXD) GetStoragePoolVolumeBackupNames(pool string, volName string) ([]string, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/storage-pools/" + url.PathEscape(pool) + "/volumes/custom/" + url.PathEscape(volName) + "/backups"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePoolVolumeBackups returns a list of custom volume backups.
func (r *ProtocolLXD) GetStoragePoolVolumeBackups(pool string, volName string) ([]api.StoragePoolVolumeBackup, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	// Fetch the raw value
	backups := []api.StoragePoolVolumeBackup{}

	_, err = r.queryStruct(http.MethodGet, "/storage-pools/"+url.PathEscape(pool)+"/volumes/custom/"+url.PathEscape(volName)+"/backups?recursion=1", nil, "", &backups)
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// GetStoragePoolVolumeBackup returns a custom volume backup.
func (r *ProtocolLXD) GetStoragePoolVolumeBackup(pool string, volName string, name string) (*api.StoragePoolVolumeBackup, string, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, "", err
	}

	// Fetch the raw value
	backup := api.StoragePoolVolumeBackup{}
	etag, err := r.queryStruct(http.MethodGet, "/storage-pools/"+url.PathEscape(pool)+"/volumes/custom/"+url.PathEscape(volName)+"/backups/"+url.PathEscape(name), nil, "", &backup)
	if err != nil {
		return nil, "", err
	}

	return &backup, etag, nil
}

// CreateStoragePoolVolumeBackup creates new custom volume backup.
func (r *ProtocolLXD) CreateStoragePoolVolumeBackup(pool string, volName string, backup api.StoragePoolVolumeBackupsPost) (Operation, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation(http.MethodPost, "/storage-pools/"+url.PathEscape(pool)+"/volumes/custom/"+url.PathEscape(volName)+"/backups", backup, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameStoragePoolVolumeBackup renames a custom volume backup.
func (r *ProtocolLXD) RenameStoragePoolVolumeBackup(pool string, volName string, name string, backup api.StoragePoolVolumeBackupPost) (Operation, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation(http.MethodPost, "/storage-pools/"+url.PathEscape(pool)+"/volumes/custom/"+url.PathEscape(volName)+"/backups/"+url.PathEscape(name), backup, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolumeBackup deletes a custom volume backup.
func (r *ProtocolLXD) DeleteStoragePoolVolumeBackup(pool string, volName string, name string) (Operation, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation(http.MethodDelete, "/storage-pools/"+url.PathEscape(pool)+"/volumes/custom/"+url.PathEscape(volName)+"/backups/"+url.PathEscape(name), nil, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeBackupFile requests the custom volume backup content.
func (r *ProtocolLXD) GetStoragePoolVolumeBackupFile(pool string, volName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	// Build the URL
	uri := r.httpBaseURL.String() + "/1.0/storage-pools/" + url.PathEscape(pool) + "/volumes/custom/" + url.PathEscape(volName) + "/backups/" + url.PathEscape(name) + "/export"

	if r.project != "" {
		uri += "?project=" + url.QueryEscape(r.project)
	}

	// Prepare the download request
	request, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	if r.httpUserAgent != "" {
		request.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Start the request
	response, doneCh, err := cancel.CancelableDownload(req.Canceler, r.DoHTTP, request)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()
	defer close(doneCh)

	if response.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(response)
		if err != nil {
			return nil, err
		}
	}

	// Handle the data
	body := response.Body
	if req.ProgressHandler != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: response.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: response.ContentLength,
				Handler: func(percent int64, speed int64) {
					req.ProgressHandler(ioprogress.ProgressData{Text: strconv.FormatInt(percent, 10) + "% (" + units.GetByteSizeString(speed, 2) + "/s)"})
				},
			},
		}
	}

	size, err := io.Copy(req.BackupFile, body)
	if err != nil {
		return nil, err
	}

	resp := BackupFileResponse{}
	resp.Size = size

	return &resp, nil
}

func (r *ProtocolLXD) createStoragePoolVolumeFromFile(pool string, args StoragePoolVolumeBackupArgs, fileType string) (Operation, error) {
	path := "/storage-pools/" + url.PathEscape(pool) + "/volumes/custom"

	// Prepare the HTTP request.
	reqURL, err := r.setQueryAttributes(r.httpBaseURL.String() + "/1.0" + path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	if args.Name != "" {
		req.Header.Set("X-LXD-name", args.Name)
	}

	if fileType != "" {
		req.Header.Set("X-LXD-type", fileType)
	}

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors.
	response, _, err := lxdParseResponse(resp)
	if err != nil {
		return nil, err
	}

	// Get to the operation.
	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	// Setup an Operation wrapper.
	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}

// CreateStoragePoolVolumeFromISO creates a custom volume from an ISO file.
func (r *ProtocolLXD) CreateStoragePoolVolumeFromISO(pool string, args StoragePoolVolumeBackupArgs) (Operation, error) {
	err := r.CheckExtension("custom_volume_iso")
	if err != nil {
		return nil, err
	}

	if args.Name == "" {
		return nil, errors.New("Missing volume name")
	}

	return r.createStoragePoolVolumeFromFile(pool, args, "iso")
}

// CreateStoragePoolVolumeFromTarball creates a custom filesystem volume from a tarball.
func (r *ProtocolLXD) CreateStoragePoolVolumeFromTarball(pool string, args StoragePoolVolumeBackupArgs) (Operation, error) {
	err := r.CheckExtension("import_custom_volume_tar")
	if err != nil {
		return nil, err
	}

	if args.Name == "" {
		return nil, errors.New("Missing volume name")
	}

	return r.createStoragePoolVolumeFromFile(pool, args, "tar")
}

// CreateStoragePoolVolumeFromBackup creates a custom volume from a backup file.
func (r *ProtocolLXD) CreateStoragePoolVolumeFromBackup(pool string, args StoragePoolVolumeBackupArgs) (Operation, error) {
	err := r.CheckExtension("custom_volume_backup")
	if err != nil {
		return nil, err
	}

	if args.Name != "" {
		err := r.CheckExtension("backup_override_name")
		if err != nil {
			return nil, err
		}
	}

	return r.createStoragePoolVolumeFromFile(pool, args, "")
}
