package lxd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

// Storage volumes handling function

// GetStoragePoolVolumeNames returns the names of all volumes in a pool
func (r *ProtocolLXD) GetStoragePoolVolumeNames(pool string) ([]string, error) {
	if !r.HasExtension("storage") {
		return nil, fmt.Errorf("The server is missing the required \"storage\" API extension")
	}

	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes", url.PathEscape(pool)), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, uri := range urls {
		fields := strings.Split(uri, fmt.Sprintf("/storage-pools/%s/volumes/", url.PathEscape(pool)))
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
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes?recursion=1", url.PathEscape(pool)), nil, "", &volumes)
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
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
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
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))
	_, _, err := r.query("POST", path, volume, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateStoragePoolVolumeSnapshot defines a new storage volume
func (r *ProtocolLXD) CreateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshot api.StorageVolumeSnapshotsPost) (Operation, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName))
	op, _, err := r.queryOperation("POST", path, snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeSnapshotNames returns a list of snapshot names for the
// storage volume
func (r *ProtocolLXD) GetStoragePoolVolumeSnapshotNames(pool string, volumeType string, volumeName string) ([]string, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	urls := []string{}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName))
	_, err := r.queryStruct("GET", path, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, uri := range urls {
		fields := strings.Split(uri, path)
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetStoragePoolVolumeSnapshots returns a list of snapshots for the storage
// volume
func (r *ProtocolLXD) GetStoragePoolVolumeSnapshots(pool string, volumeType string, volumeName string) ([]api.StorageVolumeSnapshot, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	snapshots := []api.StorageVolumeSnapshot{}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots?recursion=1",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName))
	_, err := r.queryStruct("GET", path, nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetStoragePoolVolumeSnapshot returns a snapshots for the storage volume
func (r *ProtocolLXD) GetStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (*api.StorageVolumeSnapshot, string, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, "", fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	snapshot := api.StorageVolumeSnapshot{}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots/%s",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName),
		url.PathEscape(snapshotName))
	etag, err := r.queryStruct("GET", path, nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// RenameStoragePoolVolumeSnapshot renames a storage volume snapshot
func (r *ProtocolLXD) RenameStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, snapshot api.StorageVolumeSnapshotPost) (Operation, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots/%s", url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.PathEscape(snapshotName))
	// Send the request
	op, _, err := r.queryOperation("POST", path, snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolumeSnapshot deletes a storage volume snapshot
func (r *ProtocolLXD) DeleteStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (Operation, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s/snapshots/%s",
		url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.PathEscape(snapshotName))

	op, _, err := r.queryOperation("DELETE", path, nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePoolVolumeSnapshot updates the volume to match the provided StoragePoolVolume struct
func (r *ProtocolLXD) UpdateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, volume api.StorageVolumeSnapshotPut, ETag string) error {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots/%s", url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.PathEscape(snapshotName))
	_, _, err := r.queryOperation("PUT", path, volume, ETag)
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
	path := fmt.Sprintf("/storage-pools/%s/volumes/custom/%s", url.PathEscape(pool), volume.Name)
	op, _, err := r.queryOperation("POST", path, volume, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryMigrateStoragePoolVolume(source InstanceServer, pool string, req api.StorageVolumePost, urls []string) (RemoteOperation, error) {
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
			req.Target.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))

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
			req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))

			// Send the request
			path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(req.Type))
			top, _, err := r.queryOperation("POST", path, req, "")
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

// CopyStoragePoolVolume copies an existing storage volume
func (r *ProtocolLXD) CopyStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (RemoteOperation, error) {
	if !r.HasExtension("storage_api_local_volume_handling") {
		return nil, fmt.Errorf("The server is missing the required \"storage_api_local_volume_handling\" API extension")
	}

	if args != nil && args.VolumeOnly && !r.HasExtension("storage_api_volume_snapshots") {
		return nil, fmt.Errorf("The target server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	req := api.StorageVolumesPost{
		Name: args.Name,
		Type: volume.Type,
		Source: api.StorageVolumeSource{
			Name:       volume.Name,
			Type:       "copy",
			Pool:       sourcePool,
			VolumeOnly: args.VolumeOnly,
		},
	}
	req.Config = volume.Config
	req.Description = volume.Description
	req.ContentType = volume.ContentType

	if r == source {
		// Send the request
		op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type)), req, "")
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
		Migration:  true,
		Name:       volume.Name,
		Pool:       sourcePool,
		VolumeOnly: args.VolumeOnly,
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
		path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))

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
		path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))

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
func (r *ProtocolLXD) MoveStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeMoveArgs) (RemoteOperation, error) {
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
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(sourcePool), url.PathEscape(volume.Type), volume.Name), req, "")
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

	if volume.Restore != "" && !r.HasExtension("storage_api_volume_snapshots") {
		return fmt.Errorf("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
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
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
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
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))

	// Send the request
	_, _, err := r.query("POST", path, volume, "")
	if err != nil {
		return err
	}

	return nil
}

// GetStoragePoolVolumeBackupNames returns a list of volume backup names.
func (r *ProtocolLXD) GetStoragePoolVolumeBackupNames(pool string, volName string) ([]string, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Fetch the raw value
	urls := []string{}
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups", url.PathEscape(pool), url.PathEscape(volName)), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, uri := range urls {
		fields := strings.Split(uri, fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups", url.PathEscape(pool), url.PathEscape(volName)))
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetStoragePoolVolumeBackups returns a list of custom volume backups.
func (r *ProtocolLXD) GetStoragePoolVolumeBackups(pool string, volName string) ([]api.StoragePoolVolumeBackup, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Fetch the raw value
	backups := []api.StoragePoolVolumeBackup{}

	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups?recursion=1", url.PathEscape(pool), url.PathEscape(volName)), nil, "", &backups)
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// GetStoragePoolVolumeBackup returns a custom volume backup.
func (r *ProtocolLXD) GetStoragePoolVolumeBackup(pool string, volName string, name string) (*api.StoragePoolVolumeBackup, string, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, "", fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Fetch the raw value
	backup := api.StoragePoolVolumeBackup{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups/%s", url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name)), nil, "", &backup)
	if err != nil {
		return nil, "", err
	}

	return &backup, etag, nil
}

// CreateStoragePoolVolumeBackup creates new custom volume backup.
func (r *ProtocolLXD) CreateStoragePoolVolumeBackup(pool string, volName string, backup api.StoragePoolVolumeBackupsPost) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups", url.PathEscape(pool), url.PathEscape(volName)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil

}

// RenameStoragePoolVolumeBackup renames a custom volume backup.
func (r *ProtocolLXD) RenameStoragePoolVolumeBackup(pool string, volName string, name string, backup api.StoragePoolVolumeBackupPost) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups/%s", url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolumeBackup deletes a custom volume backup.
func (r *ProtocolLXD) DeleteStoragePoolVolumeBackup(pool string, volName string, name string) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups/%s", url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeBackupFile requests the custom volume backup content.
func (r *ProtocolLXD) GetStoragePoolVolumeBackupFile(pool string, volName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Build the URL
	uri := fmt.Sprintf("%s/1.0/storage-pools/%s/volumes/custom/%s/backups/%s/export", r.httpHost, url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name))

	if r.project != "" {
		uri += fmt.Sprintf("?project=%s", url.QueryEscape(r.project))
	}

	// Prepare the download request
	request, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	if r.httpUserAgent != "" {
		request.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Start the request
	response, doneCh, err := cancel.CancelableDownload(req.Canceler, r.http, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
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
					req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
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

// CreateStoragePoolVolumeFromBackup creates a custom volume from a backup file.
func (r *ProtocolLXD) CreateStoragePoolVolumeFromBackup(pool string, args StoragePoolVolumeBackupArgs) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, fmt.Errorf(`The server is missing the required "custom_volume_backup" API extension`)
	}

	if args.Name != "" && !r.HasExtension("backup_override_name") {
		return nil, fmt.Errorf(`The server is missing the required "backup_override_name" API extension`)
	}

	path := fmt.Sprintf("/storage-pools/%s/volumes/custom", url.PathEscape(pool))

	// Prepare the HTTP request.
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpHost, path))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	if args.Name != "" {
		req.Header.Set("X-LXD-name", args.Name)
	}

	// Set the user agent.
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request.
	resp, err := r.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
