package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	cron "gopkg.in/robfig/cron.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumeSnapshotsTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/snapshots",

	Get:  APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeSnapshotTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/snapshots/{snapshotName}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume.
	volumeName := mux.Vars(r)["name"]

	// Parse the request.
	req := api.StorageVolumeSnapshotsPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Get a snapshot name.
	if req.Name == "" {
		i := d.cluster.GetNextStorageVolumeSnapshotIndex(poolName, volumeName, volumeType, "snap%d")
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Validate the name
	err = storagePools.ValidName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that this isn't a restricted volume
	used, err := storagePools.VolumeUsedByDaemon(d.State(), poolName, volumeName)
	if err != nil {
		return response.InternalError(err)
	}

	if used {
		return response.BadRequest(fmt.Errorf("Volumes used by LXD itself cannot have snapshots"))
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Ensure that the snapshot doesn't already exist.
	_, _, err = d.cluster.GetLocalStoragePoolVolume(projectName, fmt.Sprintf("%s/%s", volumeName, req.Name), volumeType, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Snapshot '%s' already in use", req.Name))
	}

	// Get the parent volume so we can get the config.
	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	var expiry time.Time

	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = shared.GetSnapshotExpiry(time.Now(), vol.Config["snapshots.expiry"])
		if err != nil {
			return response.BadRequest(err)
		}
	}

	snapshot := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		return pool.CreateCustomVolumeSnapshot(projectName, volumeName, req.Name, expiry, op)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotCreate, resources, nil, snapshot, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	recursion := util.IsRecursionRequest(r)

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume type.
	volumeName := mux.Vars(r)["name"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volume snapshots of a given volume.
	volumes, err := d.cluster.GetLocalStoragePoolVolumeSnapshotsWithType(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := shared.InstanceGetParentAndSnapshotName(volume.Name)

		if !recursion {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return response.InternalError(err)
			}
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, poolName, apiEndpoint, volumeName, snapshotName))
		} else {
			_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volume.Name, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, vol.Name, vol.Type)
			if err != nil {
				return response.SmartError(err)
			}
			vol.UsedBy = volumeUsedBy

			tmp := &api.StorageVolumeSnapshot{}
			tmp.Config = vol.Config
			tmp.Description = vol.Description
			tmp.Name = vol.Name

			resultMap = append(resultMap, tmp)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	req := api.StorageVolumeSnapshotPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	snapshotRename := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		return pool.RenameCustomVolumeSnapshot(projectName, fullSnapshotName, req.Name, op)
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotRename, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	volID, volume, err := d.cluster.GetLocalStoragePoolVolume(projectName, fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := d.cluster.GetStorageVolumeSnapshotExpiry(volID)
	if err != nil {
		return response.SmartError(err)
	}

	snapshot := api.StorageVolumeSnapshot{}
	snapshot.Config = volume.Config
	snapshot.Description = volume.Description
	snapshot.Name = snapshotName
	snapshot.ExpiresAt = &expiry

	etag := []interface{}{snapshot.Name, snapshot.Description, snapshot.Config, expiry}

	return response.SyncResponseETag(true, &snapshot, etag)
}

// storagePoolVolumeSnapshotTypePut allows a snapshot's description to be changed.
func storagePoolVolumeSnapshotTypePut(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	volID, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := d.cluster.GetStorageVolumeSnapshotExpiry(volID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{snapshotName, vol.Description, vol.Config, expiry}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry = time.Time{}
	}

	do := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		// Handle custom volume update requests.
		return pool.UpdateCustomVolumeSnapshot(projectName, vol.Name, req.Description, nil, expiry, op)
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotUpdate, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	snapshotDelete := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		return pool.DeleteCustomVolumeSnapshot(projectName, fullSnapshotName, op)
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func pruneExpireCustomVolumeSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Get the list of expired custom volume snapshots.
		expiredSnapshots, err := d.cluster.GetExpiredStorageVolumeSnapshots()
		if err != nil {
			logger.Error("Unable to retrieve the list of expired custom volume snapshots", log.Ctx{"err": err})
			return
		}

		if len(expiredSnapshots) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return pruneExpiredCustomVolumeSnapshots(ctx, d, expiredSnapshots)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationCustomVolumeSnapshotsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired custom volume snapshots operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired custom volume snapshots")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to expire backups", log.Ctx{"err": err})
		}
		logger.Info("Done pruning expired custom volume snapshots")
	}

	f(context.Background())

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func pruneExpiredCustomVolumeSnapshots(ctx context.Context, d *Daemon, expiredSnapshots []db.StorageVolumeArgs) error {
	for _, s := range expiredSnapshots {
		pool, err := storagePools.GetPoolByName(d.State(), s.PoolName)
		if err != nil {
			return errors.Wrapf(err, "Failed to get pool %q", s.PoolName)
		}

		err = pool.DeleteCustomVolumeSnapshot(s.ProjectName, s.Name, nil)
		if err != nil {
			return errors.Wrapf(err, "Error deleting custom volume snapshot %s", s.Name)
		}
	}

	return nil
}

func autoCreateCustomVolumeSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		allVolumes, err := d.cluster.GetStoragePoolVolumesWithType(db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return
		}

		var volumes []db.StorageVolumeArgs

		// Figure out which need snapshotting (if any)
		for _, v := range allVolumes {
			schedule, ok := v.Config["snapshots.schedule"]
			if !ok {
				continue
			}

			// Extend our schedule to one that is accepted by the used cron parser
			sched, err := cron.Parse(fmt.Sprintf("* %s", schedule))
			if err != nil {
				continue
			}

			// Check if it's time to snapshot
			now := time.Now()

			// Truncate the time now back to the start of the minute, before passing to
			// the cron scheduler, as it will add 1s to the scheduled time and we don't
			// want the next scheduled time to roll over to the next minute and break
			// the time comparison below.
			now = now.Truncate(time.Minute)

			// Calculate the next scheduled time based on the snapshots.schedule
			// pattern and the time now.
			next := sched.Next(now)

			// Ignore everything that is more precise than minutes.
			next = next.Truncate(time.Minute)

			if !now.Equal(next) {
				continue
			}

			volumes = append(volumes, v)
		}

		if len(volumes) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return autoCreateCustomVolumeSnapshots(ctx, d, volumes)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotCreate, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start create volume snapshot operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Creating scheduled volume snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to create scheduled volume snapshots", log.Ctx{"err": err})
		}

		logger.Info("Done creating scheduled volume snapshots")
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func autoCreateCustomVolumeSnapshots(ctx context.Context, d *Daemon, volumes []db.StorageVolumeArgs) error {
	// Make the snapshots
	for _, v := range volumes {
		ch := make(chan error)
		go func() {
			snapshotName, err := volumeDetermineNextSnapshotName(d, v, "snap%d")
			if err != nil {
				logger.Error("Error retrieving next snapshot name", log.Ctx{"err": err, "volume": v})
				ch <- nil
				return
			}

			expiry, err := shared.GetSnapshotExpiry(time.Now(), v.Config["snapshots.expiry"])
			if err != nil {
				logger.Error("Error getting expiry date", log.Ctx{"err": err, "volume": v})
				ch <- nil
				return
			}

			pool, err := storagePools.GetPoolByName(d.State(), v.PoolName)
			if err != nil {
				logger.Error("Error retrieving pool", log.Ctx{"err": err, "pool": v.PoolName})
				ch <- nil
				return
			}

			err = pool.CreateCustomVolumeSnapshot(v.ProjectName, v.Name, snapshotName, expiry, nil)
			if err != nil {
				logger.Error("Error creating volume snapshot", log.Ctx{"err": err, "volume": v})
			}

			ch <- nil
		}()
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

func volumeDetermineNextSnapshotName(d *Daemon, volume db.StorageVolumeArgs, defaultPattern string) (string, error) {
	var err error

	pattern, ok := volume.Config["snapshots.pattern"]
	if !ok {
		pattern = defaultPattern
	}

	pattern, err = shared.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		i := d.cluster.GetNextStorageVolumeSnapshotIndex(volume.PoolName, volume.Name, db.StoragePoolVolumeTypeCustom, pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	var snapshots []db.StorageVolumeArgs
	var projects []string

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projects, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return "", err
	}

	pools, err := d.cluster.GetStoragePoolNames()
	if err != nil {
		return "", err
	}

	for _, pool := range pools {
		poolID, err := d.cluster.GetStoragePoolID(pool)
		if err != nil {
			return "", err
		}

		for _, project := range projects {
			snaps, err := d.cluster.GetLocalStoragePoolVolumeSnapshotsWithType(project, volume.Name, db.StoragePoolVolumeTypeCustom, poolID)
			if err != nil {
				return "", err
			}

			snapshots = append(snapshots, snaps...)
		}
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name)

		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	if snapshotExists {
		i := d.cluster.GetNextStorageVolumeSnapshotIndex(volume.PoolName, volume.Name, db.StoragePoolVolumeTypeCustom, pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}
