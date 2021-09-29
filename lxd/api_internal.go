package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	runtimeDebug "runtime/debug"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/node"
	"github.com/lxc/lxd/lxd/db/query"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/units"
)

var apiInternal = []APIEndpoint{
	internalReadyCmd,
	internalShutdownCmd,
	internalContainerOnStartCmd,
	internalContainerOnStopNSCmd,
	internalContainerOnStopCmd,
	internalSQLCmd,
	internalClusterAcceptCmd,
	internalClusterRebalanceCmd,
	internalClusterAssignCmd,
	internalClusterInstanceMovedCmd,
	internalGarbageCollectorCmd,
	internalRAFTSnapshotCmd,
	internalClusterHandoverCmd,
	internalClusterRaftNodeCmd,
	internalImageRefreshCmd,
	internalImageOptimizeCmd,
}

var internalShutdownCmd = APIEndpoint{
	Path: "shutdown",

	Put: APIEndpointAction{Handler: internalShutdown},
}

var internalReadyCmd = APIEndpoint{
	Path: "ready",

	Get: APIEndpointAction{Handler: internalWaitReady},
}

var internalContainerOnStartCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstart",

	Get: APIEndpointAction{Handler: internalContainerOnStart},
}

var internalContainerOnStopNSCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstopns",

	Get: APIEndpointAction{Handler: internalContainerOnStopNS},
}

var internalContainerOnStopCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstop",

	Get: APIEndpointAction{Handler: internalContainerOnStop},
}

var internalSQLCmd = APIEndpoint{
	Path: "sql",

	Get:  APIEndpointAction{Handler: internalSQLGet},
	Post: APIEndpointAction{Handler: internalSQLPost},
}

var internalGarbageCollectorCmd = APIEndpoint{
	Path: "gc",

	Get: APIEndpointAction{Handler: internalGC},
}

var internalRAFTSnapshotCmd = APIEndpoint{
	Path: "raft-snapshot",

	Get: APIEndpointAction{Handler: internalRAFTSnapshot},
}

var internalImageRefreshCmd = APIEndpoint{
	Path: "testing/image-refresh",

	Get: APIEndpointAction{Handler: internalRefreshImage},
}

var internalImageOptimizeCmd = APIEndpoint{
	Path: "image-optimize",

	Post: APIEndpointAction{Handler: internalOptimizeImage},
}

type internalImageOptimizePost struct {
	Image api.Image `json:"image" yaml:"image"`
	Pool  string    `json:"pool" yaml:"pool"`
}

func internalOptimizeImage(d *Daemon, r *http.Request) response.Response {
	req := &internalImageOptimizePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = imageCreateInPool(d, &req.Image, req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalRefreshImage(d *Daemon, r *http.Request) response.Response {
	err := autoUpdateImages(d.shutdownCtx, d)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalWaitReady(d *Daemon, r *http.Request) response.Response {
	// Check that we're not shutting down.
	isClosing := d.shutdownCtx.Err() != nil
	if isClosing {
		return response.Unavailable(fmt.Errorf("LXD daemon is shutting down"))
	}

	select {
	case <-d.readyChan:
	default:
		return response.Unavailable(fmt.Errorf("LXD daemon not ready yet"))
	}

	return response.EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) response.Response {
	d.shutdownChan <- struct{}{}

	force := queryParam(r, "force")

	if force == "true" {
		d.shutdownChan <- struct{}{}
	}

	<-d.shutdownDoneCtx.Done()

	return response.EmptySyncResponse
}

// internalContainerHookLoadFromRequestReference loads the container from the instance reference in the request.
// It detects whether the instance reference is an instance ID or instance name and loads instance accordingly.
func internalContainerHookLoadFromReference(s *state.State, r *http.Request) (instance.Instance, error) {
	var inst instance.Instance
	instanceRef := mux.Vars(r)["instanceRef"]
	projectName := projectParam(r)

	instanceID, err := strconv.Atoi(instanceRef)
	if err == nil {
		inst, err = instance.LoadByID(s, instanceID)
		if err != nil {
			return nil, err
		}
	} else {
		inst, err = instance.LoadByProjectAndName(s, projectName, instanceRef)
		if err != nil {
			return nil, err
		}
	}

	if inst.Type() != instancetype.Container {
		return nil, fmt.Errorf("Instance is not container type")
	}

	return inst, nil
}

func internalContainerOnStart(d *Daemon, r *http.Request) response.Response {
	inst, err := internalContainerHookLoadFromReference(d.State(), r)
	if err != nil {
		logger.Error("The start hook failed to load", log.Ctx{"err": err})
		return response.SmartError(err)
	}

	err = inst.OnHook(instance.HookStart, nil)
	if err != nil {
		logger.Error("The start hook failed", log.Ctx{"instance": inst.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStopNS(d *Daemon, r *http.Request) response.Response {
	inst, err := internalContainerHookLoadFromReference(d.State(), r)
	if err != nil {
		logger.Error("The stopns hook failed to load", log.Ctx{"err": err})
		return response.SmartError(err)
	}

	target := queryParam(r, "target")
	if target == "" {
		target = "unknown"
	}
	netns := queryParam(r, "netns")

	args := map[string]string{
		"target": target,
		"netns":  netns,
	}

	err = inst.OnHook(instance.HookStopNS, args)
	if err != nil {
		logger.Error("The stopns hook failed", log.Ctx{"instance": inst.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) response.Response {
	inst, err := internalContainerHookLoadFromReference(d.State(), r)
	if err != nil {
		logger.Error("The stop hook failed to load", log.Ctx{"err": err})
		return response.SmartError(err)
	}

	target := queryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	args := map[string]string{
		"target": target,
	}

	err = inst.OnHook(instance.HookStop, args)
	if err != nil {
		logger.Error("The stop hook failed", log.Ctx{"instance": inst.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

type internalSQLDump struct {
	Text string `json:"text" yaml:"text"`
}

type internalSQLQuery struct {
	Database string `json:"database" yaml:"database"`
	Query    string `json:"query" yaml:"query"`
}

type internalSQLBatch struct {
	Results []internalSQLResult
}

type internalSQLResult struct {
	Type         string          `json:"type" yaml:"type"`
	Columns      []string        `json:"columns" yaml:"columns"`
	Rows         [][]interface{} `json:"rows" yaml:"rows"`
	RowsAffected int64           `json:"rows_affected" yaml:"rows_affected"`
}

// Perform a database dump.
func internalSQLGet(d *Daemon, r *http.Request) response.Response {
	database := r.FormValue("database")

	if !shared.StringInSlice(database, []string{"local", "global"}) {
		return response.BadRequest(fmt.Errorf("Invalid database"))
	}

	schemaFormValue := r.FormValue("schema")
	schemaOnly, err := strconv.Atoi(schemaFormValue)
	if err != nil {
		schemaOnly = 0
	}

	var schema string
	var db *sql.DB
	if database == "global" {
		db = d.cluster.DB()
		schema = cluster.FreshSchema()
	} else {
		db = d.db.DB()
		schema = node.FreshSchema()
	}

	tx, err := db.Begin()
	if err != nil {
		return response.SmartError(errors.Wrap(err, "failed to start transaction"))
	}
	defer tx.Rollback()
	dump, err := query.Dump(tx, schema, schemaOnly == 1)
	if err != nil {
		return response.SmartError(errors.Wrapf(err, "failed dump database %s", database))
	}
	return response.SyncResponse(true, internalSQLDump{Text: dump})
}

// Execute queries.
func internalSQLPost(d *Daemon, r *http.Request) response.Response {
	req := &internalSQLQuery{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if !shared.StringInSlice(req.Database, []string{"local", "global"}) {
		return response.BadRequest(fmt.Errorf("Invalid database"))
	}

	if req.Query == "" {
		return response.BadRequest(fmt.Errorf("No query provided"))
	}

	var db *sql.DB
	if req.Database == "global" {
		db = d.cluster.DB()
	} else {
		db = d.db.DB()
	}

	batch := internalSQLBatch{}

	if req.Query == ".sync" {
		d.gateway.Sync()
		return response.SyncResponse(true, batch)
	}

	for _, query := range strings.Split(req.Query, ";") {
		query = strings.TrimLeft(query, " ")

		if query == "" {
			continue
		}

		result := internalSQLResult{}

		tx, err := db.Begin()
		if err != nil {
			return response.SmartError(err)
		}

		if strings.HasPrefix(strings.ToUpper(query), "SELECT") {
			err = internalSQLSelect(tx, query, &result)
			tx.Rollback()
		} else {
			err = internalSQLExec(tx, query, &result)
			if err != nil {
				tx.Rollback()
			} else {
				err = tx.Commit()
			}
		}
		if err != nil {
			return response.SmartError(err)
		}

		batch.Results = append(batch.Results, result)
	}

	return response.SyncResponse(true, batch)
}

func internalSQLSelect(tx *sql.Tx, query string, result *internalSQLResult) error {
	result.Type = "select"

	rows, err := tx.Query(query)
	if err != nil {
		return errors.Wrap(err, "Failed to execute query")
	}

	defer rows.Close()

	result.Columns, err = rows.Columns()
	if err != nil {
		return errors.Wrap(err, "Failed to fetch colume names")
	}

	for rows.Next() {
		row := make([]interface{}, len(result.Columns))
		rowPointers := make([]interface{}, len(result.Columns))
		for i := range row {
			rowPointers[i] = &row[i]
		}

		err := rows.Scan(rowPointers...)
		if err != nil {
			return errors.Wrap(err, "Failed to scan row")
		}

		for i, column := range row {
			// Convert bytes to string. This is safe as
			// long as we don't have any BLOB column type.
			data, ok := column.([]byte)
			if ok {
				row[i] = string(data)
			}
		}

		result.Rows = append(result.Rows, row)
	}

	err = rows.Err()
	if err != nil {
		return errors.Wrap(err, "Got a row error")
	}

	return nil
}

func internalSQLExec(tx *sql.Tx, query string, result *internalSQLResult) error {
	result.Type = "exec"
	r, err := tx.Exec(query)
	if err != nil {
		return errors.Wrapf(err, "Failed to exec query")
	}

	result.RowsAffected, err = r.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Failed to fetch affected rows")
	}

	return nil
}

// internalImportFromBackup creates instance, storage pool and volume DB records from an instance's backup file.
// It expects the instance volume to be mounted so that the backup.yaml file is readable.
func internalImportFromBackup(d *Daemon, projectName string, instName string, force bool, allowNameOverride bool) error {
	if instName == "" {
		return fmt.Errorf("The name of the instance is required")
	}

	storagePoolsPath := shared.VarPath("storage-pools")
	storagePoolsDir, err := os.Open(storagePoolsPath)
	if err != nil {
		return err
	}

	// Get a list of all storage pools.
	storagePoolNames, err := storagePoolsDir.Readdirnames(-1)
	if err != nil {
		storagePoolsDir.Close()
		return err
	}
	storagePoolsDir.Close()

	// Check whether the instance exists on any of the storage pools as either a container or a VM.
	instanceMountPoints := []string{}
	instancePoolName := ""
	instanceType := instancetype.Container
	instanceVolType := storageDrivers.VolumeTypeContainer
	instanceDBVolType := db.StoragePoolVolumeTypeContainer

	for _, volType := range []storageDrivers.VolumeType{storageDrivers.VolumeTypeVM, storageDrivers.VolumeTypeContainer} {
		for _, poolName := range storagePoolNames {
			volStorageName := project.Instance(projectName, instName)
			instanceMntPoint := storageDrivers.GetVolumeMountPath(poolName, volType, volStorageName)

			if shared.PathExists(instanceMntPoint) {
				instanceMountPoints = append(instanceMountPoints, instanceMntPoint)
				instancePoolName = poolName
				instanceVolType = volType

				if volType == storageDrivers.VolumeTypeVM {
					instanceType = instancetype.VM
					instanceDBVolType = db.StoragePoolVolumeTypeVM
				} else {
					instanceType = instancetype.Container
					instanceDBVolType = db.StoragePoolVolumeTypeContainer
				}
			}
		}
	}

	// Quick checks.
	if len(instanceMountPoints) > 1 {
		return fmt.Errorf(`The instance %q seems to exist on multiple storage pools`, instName)
	} else if len(instanceMountPoints) != 1 {
		return fmt.Errorf(`The instance %q does not seem to exist on any storage pool`, instName)
	}

	// User needs to make sure that we can access the directory where backup.yaml lives.
	instanceMountPoint := instanceMountPoints[0]
	isEmpty, err := shared.PathIsEmpty(instanceMountPoint)
	if err != nil {
		return err
	}

	if isEmpty {
		return fmt.Errorf(`The instance's directory %q appears to be empty. Please ensure that the instance's storage volume is mounted`, instanceMountPoint)
	}

	// Read in the backup.yaml file.
	backupYamlPath := filepath.Join(instanceMountPoint, "backup.yaml")
	backupConf, err := backup.ParseConfigYamlFile(backupYamlPath)
	if err != nil {
		return err
	}

	if allowNameOverride && instName != "" {
		backupConf.Container.Name = instName
	}

	if instName != backupConf.Container.Name {
		return fmt.Errorf("Instance name requested %q doesn't match instance name in backup config %q", instName, backupConf.Container.Name)
	}

	if backupConf.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return fmt.Errorf(`No storage pool struct in the backup file found. The storage pool needs to be recovered manually`)
	}

	// Try to retrieve the storage pool the instance supposedly lives on.
	pool, err := storagePools.GetPoolByName(d.State(), instancePoolName)
	if err == db.ErrNoSuchObject {
		// Create the storage pool db entry if it doesn't exist.
		_, err = storagePoolDBCreate(d.State(), instancePoolName, "", backupConf.Pool.Driver, backupConf.Pool.Config)
		if err != nil {
			return errors.Wrap(err, "Create storage pool database entry")
		}

		pool, err = storagePools.GetPoolByName(d.State(), instancePoolName)
		if err != nil {
			return errors.Wrap(err, "Load storage pool database entry")
		}
	} else if err != nil {
		return errors.Wrap(err, "Find storage pool database entry")
	}

	if backupConf.Pool.Name != instancePoolName {
		return fmt.Errorf(`The storage pool %q the instance was detected on does not match the storage pool %q specified in the backup file`, instancePoolName, backupConf.Pool.Name)
	}

	if backupConf.Pool.Driver != pool.Driver().Info().Name {
		return fmt.Errorf(`The storage pool's %q driver %q conflicts with the driver %q recorded in the instance's backup file`, instancePoolName, pool.Driver().Info().Name, backupConf.Pool.Driver)
	}

	// Check snapshots are consistent, and if not, if req.Force is true, then delete snapshots that do not exist in backup.yaml.
	existingSnapshots, err := pool.CheckInstanceBackupFileSnapshots(backupConf, projectName, force, nil)
	if err != nil {
		if errors.Cause(err) == storagePools.ErrBackupSnapshotsMismatch {
			return fmt.Errorf(`%s. Set "force" to discard non-existing snapshots`, err)
		}

		return errors.Wrap(err, "Checking snapshots")
	}

	// Check if a storage volume entry for the instance already exists.
	_, volume, ctVolErr := d.cluster.GetLocalStoragePoolVolume(projectName, backupConf.Container.Name, instanceDBVolType, pool.ID())
	if ctVolErr != nil {
		if ctVolErr != db.ErrNoSuchObject {
			return ctVolErr
		}
	}

	// If a storage volume entry exists only proceed if force was specified.
	if ctVolErr == nil && !force {
		return fmt.Errorf(`Storage volume for instance %q already exists in the database. Set "force" to overwrite`, backupConf.Container.Name)
	}

	// Check if an entry for the instance already exists in the db.
	_, instanceErr := d.cluster.GetInstanceID(projectName, backupConf.Container.Name)
	if instanceErr != nil {
		if instanceErr != db.ErrNoSuchObject {
			return instanceErr
		}
	}

	// If a db entry exists only proceed if force was specified.
	if instanceErr == nil && !force {
		return fmt.Errorf(`Entry for instance %q already exists in the database. Set "force" to overwrite`, backupConf.Container.Name)
	}

	if backupConf.Volume == nil {
		return fmt.Errorf(`No storage volume struct in the backup file found. The storage volume needs to be recovered manually`)
	}

	if ctVolErr == nil {
		if volume.Name != backupConf.Volume.Name {
			return fmt.Errorf(`The name %q of the storage volume is not identical to the instance's name "%s"`, volume.Name, backupConf.Container.Name)
		}

		if volume.Type != backupConf.Volume.Type {
			return fmt.Errorf(`The type %q of the storage volume is not identical to the instance's type %q`, volume.Type, backupConf.Volume.Type)
		}

		// Remove the storage volume db entry for the instance since force was specified.
		err := d.cluster.RemoveStoragePoolVolume(projectName, backupConf.Container.Name, instanceDBVolType, pool.ID())
		if err != nil {
			return err
		}
	}

	if instanceErr == nil {
		// Remove the storage volume db entry for the instance since force was specified.
		err := d.cluster.DeleteInstance(projectName, backupConf.Container.Name)
		if err != nil {
			return err
		}
	}

	baseImage := backupConf.Container.Config["volatile.base_image"]

	profiles, err := d.State().Cluster.GetProfiles(projectName, backupConf.Container.Profiles)
	if err != nil {
		return errors.Wrapf(err, "Failed loading profiles for instance")
	}

	// Add root device if needed.
	if backupConf.Container.Devices == nil {
		backupConf.Container.Devices = make(map[string]map[string]string, 0)
	}

	if backupConf.Container.ExpandedDevices == nil {
		backupConf.Container.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	internalImportRootDevicePopulate(instancePoolName, backupConf.Container.Devices, backupConf.Container.ExpandedDevices, profiles)

	arch, err := osarch.ArchitectureId(backupConf.Container.Architecture)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	_, instOp, err := instance.CreateInternal(d.State(), db.InstanceArgs{
		Project:      projectName,
		Architecture: arch,
		BaseImage:    baseImage,
		Config:       backupConf.Container.Config,
		CreationDate: backupConf.Container.CreatedAt,
		Type:         instanceType,
		Description:  backupConf.Container.Description,
		Devices:      deviceConfig.NewDevices(backupConf.Container.Devices),
		Ephemeral:    backupConf.Container.Ephemeral,
		LastUsedDate: backupConf.Container.LastUsedAt,
		Name:         backupConf.Container.Name,
		Profiles:     backupConf.Container.Profiles,
		Stateful:     backupConf.Container.Stateful,
	}, true, nil, revert)
	if err != nil {
		return errors.Wrap(err, "Failed creating instance record")
	}
	defer instOp.Done(err)

	instancePath := storagePools.InstancePath(instanceType, projectName, backupConf.Container.Name, false)
	isPrivileged := false
	if backupConf.Container.Config["security.privileged"] == "" {
		isPrivileged = true
	}
	err = storagePools.CreateContainerMountpoint(instanceMountPoint, instancePath, isPrivileged)
	if err != nil {
		return err
	}

	for _, snap := range existingSnapshots {
		snapInstName := fmt.Sprintf("%s%s%s", backupConf.Container.Name, shared.SnapshotDelimiter, snap.Name)

		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := d.cluster.GetInstanceSnapshotID(projectName, backupConf.Container.Name, snap.Name)
		if snapErr != nil {
			if snapErr != db.ErrNoSuchObject {
				return snapErr
			}
		}

		// If a db entry exists only proceed if force was specified.
		if snapErr == nil && !force {
			return fmt.Errorf(`Entry for snapshot %q already exists in the database. Set "force" to overwrite`, snapInstName)
		}

		// Check if a storage volume entry for the snapshot already exists.
		_, _, csVolErr := d.cluster.GetLocalStoragePoolVolume(projectName, snapInstName, instanceDBVolType, pool.ID())
		if csVolErr != nil {
			if csVolErr != db.ErrNoSuchObject {
				return csVolErr
			}
		}

		// If a storage volume entry exists only proceed if force was specified.
		if csVolErr == nil && !force {
			return fmt.Errorf(`Storage volume for snapshot %q already exists in the database. Set "force" to overwrite`, snapInstName)
		}

		if snapErr == nil {
			err := d.cluster.DeleteInstance(projectName, snapInstName)
			if err != nil {
				return err
			}
		}

		if csVolErr == nil {
			err := d.cluster.RemoveStoragePoolVolume(projectName, snapInstName, instanceDBVolType, pool.ID())
			if err != nil {
				return err
			}
		}

		baseImage := snap.Config["volatile.base_image"]

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return err
		}

		profiles, err := d.State().Cluster.GetProfiles(projectName, snap.Profiles)
		if err != nil {
			return errors.Wrapf(err, "Failed loading profiles for instance snapshot %q", snapInstName)
		}

		// Add root device if needed.
		if snap.Devices == nil {
			snap.Devices = make(map[string]map[string]string, 0)
		}

		if snap.ExpandedDevices == nil {
			snap.ExpandedDevices = make(map[string]map[string]string, 0)
		}

		internalImportRootDevicePopulate(instancePoolName, snap.Devices, snap.ExpandedDevices, profiles)

		_, snapInstOp, err := instance.CreateInternal(d.State(), db.InstanceArgs{
			Project:      projectName,
			Architecture: arch,
			BaseImage:    baseImage,
			Config:       snap.Config,
			CreationDate: snap.CreatedAt,
			Type:         instanceType,
			Snapshot:     true,
			Devices:      deviceConfig.NewDevices(snap.Devices),
			Ephemeral:    snap.Ephemeral,
			LastUsedDate: snap.LastUsedAt,
			Name:         snapInstName,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		}, true, nil, revert)
		if err != nil {
			return errors.Wrapf(err, "Failed creating instance snapshot record %q", snap.Name)
		}
		defer snapInstOp.Done(err)

		// Recreate missing mountpoints and symlinks.
		volStorageName := project.Instance(projectName, snapInstName)
		snapshotMountPoint := storageDrivers.GetVolumeMountPath(instancePoolName, instanceVolType, volStorageName)
		snapshotPath := storagePools.InstancePath(instanceType, projectName, backupConf.Container.Name, true)
		snapshotTargetPath := storageDrivers.GetVolumeSnapshotDir(instancePoolName, instanceVolType, volStorageName)

		err = storagePools.CreateSnapshotMountpoint(snapshotMountPoint, snapshotTargetPath, snapshotPath)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// internalImportRootDevicePopulate considers the local and expanded devices from backup.yaml as well as the
// expanded devices in the current profiles and if needed will populate localDevices with a new root disk config
// to attempt to maintain the same effective config as specified in backup.yaml. Where possible no new root disk
// device will be added, if the root disk config in the current profiles matches the effective backup.yaml config.
func internalImportRootDevicePopulate(instancePoolName string, localDevices map[string]map[string]string, expandedDevices map[string]map[string]string, profiles []api.Profile) {
	// First, check if localDevices from backup.yaml has a root disk.
	rootName, _, _ := shared.GetRootDiskDevice(localDevices)
	if rootName != "" {
		localDevices[rootName]["pool"] = instancePoolName

		return // Local root disk device has been set to target pool.
	}

	// Next check if expandedDevices from backup.yaml has a root disk.
	expandedRootName, expandedRootConfig, _ := shared.GetRootDiskDevice(expandedDevices)

	// Extract root disk from expanded profile devices.
	profileExpandedDevices := db.ExpandInstanceDevices(deviceConfig.NewDevices(localDevices), profiles)
	profileExpandedRootName, profileExpandedRootConfig, _ := shared.GetRootDiskDevice(profileExpandedDevices.CloneNative())

	// Record whether we need to add a new local disk device.
	addLocalDisk := false

	// We need to add a local root disk if the profiles don't have a root disk.
	if profileExpandedRootName == "" {
		addLocalDisk = true
	} else {
		// Check profile expanded root disk is in the correct pool
		if profileExpandedRootConfig["pool"] != instancePoolName {
			addLocalDisk = true
		} else {
			// Check profile expanded root disk config matches the old expanded disk in backup.yaml.
			// Excluding the "pool" property, which we ignore, as we have already checked the new
			// profile root disk matches the target pool name.
			if expandedRootName != "" {
				for k := range expandedRootConfig {
					if k == "pool" {
						continue // Ignore old pool name.
					}

					if expandedRootConfig[k] != profileExpandedRootConfig[k] {
						addLocalDisk = true
						break
					}
				}

				for k := range profileExpandedRootConfig {
					if k == "pool" {
						continue // Ignore old pool name.
					}

					if expandedRootConfig[k] != profileExpandedRootConfig[k] {
						addLocalDisk = true
						break
					}
				}
			}
		}
	}

	// Add local root disk entry if needed.
	if addLocalDisk {
		rootDev := map[string]string{
			"type": "disk",
			"path": "/",
			"pool": instancePoolName,
		}

		// Inherit any extra root disk config from the expanded root disk from backup.yaml.
		if expandedRootName != "" {
			for k, v := range expandedRootConfig {
				if _, found := rootDev[k]; !found {
					rootDev[k] = v
				}
			}
		}

		// If there is already a device called "root" in the instance's config, but it does not qualify as
		// a root disk, then try to find a free name for the new root disk device.
		rootDevName := "root"
		for i := 0; i < 100; i++ {
			if localDevices[rootDevName] == nil {
				break
			}

			rootDevName = fmt.Sprintf("root%d", i)
			continue
		}

		localDevices[rootDevName] = rootDev
	}
}

func internalGC(d *Daemon, r *http.Request) response.Response {
	logger.Infof("Started forced garbage collection run")
	runtime.GC()
	runtimeDebug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logger.Infof("Heap allocated: %s", units.GetByteSizeString(int64(m.Alloc), 2))
	logger.Infof("Stack in use: %s", units.GetByteSizeString(int64(m.StackInuse), 2))
	logger.Infof("Requested from system: %s", units.GetByteSizeString(int64(m.Sys), 2))
	logger.Infof("Releasable to OS: %s", units.GetByteSizeString(int64(m.HeapIdle-m.HeapReleased), 2))

	logger.Infof("Completed forced garbage collection run")

	return response.EmptySyncResponse
}

func internalRAFTSnapshot(d *Daemon, r *http.Request) response.Response {
	logger.Infof("Started forced RAFT snapshot")
	err := d.gateway.Snapshot()
	if err != nil {
		logger.Errorf("Failed forced RAFT snapshot: %v", err)
		return response.InternalError(err)
	}

	logger.Infof("Completed forced RAFT snapshot")

	return response.EmptySyncResponse
}
