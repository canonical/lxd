package main

import (
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
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
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
	internalContainersCmd,
	internalSQLCmd,
	internalClusterAcceptCmd,
	internalClusterRebalanceCmd,
	internalClusterAssignCmd,
	internalClusterContainerMovedCmd,
	internalGarbageCollectorCmd,
	internalRAFTSnapshotCmd,
	internalClusterHandoverCmd,
	internalClusterRaftNodeCmd,
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

var internalContainersCmd = APIEndpoint{
	Path: "containers",

	Post: APIEndpointAction{Handler: internalImportFromRecovery},
}

var internalGarbageCollectorCmd = APIEndpoint{
	Path: "gc",

	Get: APIEndpointAction{Handler: internalGC},
}

var internalRAFTSnapshotCmd = APIEndpoint{
	Path: "raft-snapshot",

	Get: APIEndpointAction{Handler: internalRAFTSnapshot},
}

func internalWaitReady(d *Daemon, r *http.Request) response.Response {
	// Check that we're not shutting down.
	var isClosing bool
	d.clusterMembershipMutex.RLock()
	isClosing = d.clusterMembershipClosing
	d.clusterMembershipMutex.RUnlock()
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
		logger.Error("The start hook failed to load", log.Ctx{"instance": inst.Name(), "err": err})
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
		logger.Error("The stopns hook failed to load", log.Ctx{"instance": inst.Name(), "err": err})
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
		logger.Error("The stop hook failed to load", log.Ctx{"instance": inst.Name(), "err": err})
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

type internalImportPost struct {
	Name              string `json:"name" yaml:"name"`
	Force             bool   `json:"force" yaml:"force"`
	AllowNameOverride bool   `json:"allow_name_override" yaml:"allow_name_override"`
}

// internalImportFromRecovery allows recovery of an instance that is already on disk and mounted.
// If recovery is successful the instance is unmounted at the end.
func internalImportFromRecovery(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)

	// Parse the request.
	req := &internalImportPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	resp := internalImport(d, projectName, req)
	if resp.String() != "success" {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.IsRunning() {
		// If the instance is running, then give the instance a chance to regenerate its config file, as
		// the internalImport function will have cleared its log directory (which contains the conf file).
		// This allows functionality that relies on a config file to continue after the recovery.
		err = inst.SaveConfigFile()
		if err != nil {
			return response.SmartError(errors.Wrapf(err, "Failed regenerating instance config file"))
		}
	} else {
		// If instance isn't running, then unmount instance volume to reset the mount and any left over
		// reference counters back its non-running state.
		_, err = pool.UnmountInstance(inst, nil)
		if err != nil {
			return response.SmartError(errors.Wrapf(err, "Failed unmounting instance"))
		}
	}

	// Reinitialise the instance's root disk quota even if no size specified (allows the storage driver the
	// opportunity to reinitialise the quota based on the new storage volume's DB ID).
	_, rootConfig, err := shared.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err == nil {
		err = pool.SetInstanceQuota(inst, rootConfig["size"], nil)
		if err != nil {
			return response.SmartError(errors.Wrapf(err, "Failed reinitializing root disk quota %q", rootConfig["size"]))
		}
	}

	return resp
}

// internalImport creates the instance and storage volume DB records.
// It expects the instance volume to be mounted so that the backup.yaml file is readable.
func internalImport(d *Daemon, projectName string, req *internalImportPost) response.Response {
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("The name of the instance is required"))
	}

	storagePoolsPath := shared.VarPath("storage-pools")
	storagePoolsDir, err := os.Open(storagePoolsPath)
	if err != nil {
		return response.InternalError(err)
	}

	// Get a list of all storage pools.
	storagePoolNames, err := storagePoolsDir.Readdirnames(-1)
	if err != nil {
		storagePoolsDir.Close()
		return response.InternalError(err)
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
			volStorageName := project.Instance(projectName, req.Name)
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

	// Sanity checks.
	if len(instanceMountPoints) > 1 {
		return response.BadRequest(fmt.Errorf(`The instance %q seems to exist on multiple storage pools`, req.Name))
	} else if len(instanceMountPoints) != 1 {
		return response.BadRequest(fmt.Errorf(`The instance %q does not seem to exist on any storage pool`, req.Name))
	}

	// User needs to make sure that we can access the directory where backup.yaml lives.
	instanceMountPoint := instanceMountPoints[0]
	isEmpty, err := shared.PathIsEmpty(instanceMountPoint)
	if err != nil {
		return response.InternalError(err)
	}

	if isEmpty {
		return response.BadRequest(fmt.Errorf(`The instance's directory %q appears to be empty. Please ensure that the instance's storage volume is mounted`, instanceMountPoint))
	}

	// Read in the backup.yaml file.
	backupYamlPath := filepath.Join(instanceMountPoint, "backup.yaml")
	backupConf, err := backup.ParseConfigYamlFile(backupYamlPath)
	if err != nil {
		return response.SmartError(err)
	}

	if req.AllowNameOverride && req.Name != "" {
		backupConf.Container.Name = req.Name
	}

	if req.Name != backupConf.Container.Name {
		return response.InternalError(fmt.Errorf("Instance name in request %q doesn't match instance name in backup config %q", req.Name, backupConf.Container.Name))
	}

	// Update snapshot names to include instance name (if needed).
	for i, snap := range backupConf.Snapshots {
		if !strings.Contains(snap.Name, "/") {
			backupConf.Snapshots[i].Name = fmt.Sprintf("%s/%s", backupConf.Container.Name, snap.Name)
		}
	}

	if backupConf.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return response.BadRequest(fmt.Errorf(`No storage pool struct in the backup file found. The storage pool needs to be recovered manually`))
	}

	// Try to retrieve the storage pool the instance supposedly lives on.
	pool, err := storagePools.GetPoolByName(d.State(), instancePoolName)
	if err == db.ErrNoSuchObject {
		// Create the storage pool db entry if it doesn't exist.
		_, err = storagePoolDBCreate(d.State(), instancePoolName, "", backupConf.Pool.Driver, backupConf.Pool.Config)
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Create storage pool database entry"))
		}

		pool, err = storagePools.GetPoolByName(d.State(), instancePoolName)
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Load storage pool database entry"))
		}
	} else if err != nil {
		return response.SmartError(errors.Wrap(err, "Find storage pool database entry"))
	}

	if backupConf.Pool.Name != instancePoolName {
		return response.BadRequest(fmt.Errorf(`The storage pool %q the instance was detected on does not match the storage pool %q specified in the backup file`, instancePoolName, backupConf.Pool.Name))
	}

	if backupConf.Pool.Driver != pool.Driver().Info().Name {
		return response.BadRequest(fmt.Errorf(`The storage pool's %q driver %q conflicts with the driver %q recorded in the instance's backup file`, instancePoolName, pool.Driver().Info().Name, backupConf.Pool.Driver))
	}

	// Check snapshots are consistent, and if not, if req.Force is true, then delete snapshots that do not exist in backup.yaml.
	existingSnapshots, err := pool.CheckInstanceBackupFileSnapshots(backupConf, projectName, req.Force, nil)
	if err != nil {
		if errors.Cause(err) == storagePools.ErrBackupSnapshotsMismatch {
			return response.InternalError(fmt.Errorf(`%s. Set "force" to discard non-existing snapshots`, err))
		}

		return response.InternalError(errors.Wrap(err, "Checking snapshots"))
	}

	// Check if a storage volume entry for the instance already exists.
	_, volume, ctVolErr := d.cluster.GetLocalStoragePoolVolume(projectName, req.Name, instanceDBVolType, pool.ID())
	if ctVolErr != nil {
		if ctVolErr != db.ErrNoSuchObject {
			return response.SmartError(ctVolErr)
		}
	}

	// If a storage volume entry exists only proceed if force was specified.
	if ctVolErr == nil && !req.Force {
		return response.BadRequest(fmt.Errorf(`Storage volume for instance %q already exists in the database. Set "force" to overwrite`, req.Name))
	}

	// Check if an entry for the instance already exists in the db.
	_, instanceErr := d.cluster.GetInstanceID(projectName, req.Name)
	if instanceErr != nil {
		if instanceErr != db.ErrNoSuchObject {
			return response.SmartError(instanceErr)
		}
	}

	// If a db entry exists only proceed if force was specified.
	if instanceErr == nil && !req.Force {
		return response.BadRequest(fmt.Errorf(`Entry for instance %q already exists in the database. Set "force" to overwrite`, req.Name))
	}

	if backupConf.Volume == nil {
		return response.BadRequest(fmt.Errorf(`No storage volume struct in the backup file found. The storage volume needs to be recovered manually`))
	}

	if ctVolErr == nil {
		if volume.Name != backupConf.Volume.Name {
			return response.BadRequest(fmt.Errorf(`The name %q of the storage volume is not identical to the instance's name "%s"`, volume.Name, req.Name))
		}

		if volume.Type != backupConf.Volume.Type {
			return response.BadRequest(fmt.Errorf(`The type %q of the storage volume is not identical to the instance's type %q`, volume.Type, backupConf.Volume.Type))
		}

		// Remove the storage volume db entry for the instance since force was specified.
		err := d.cluster.RemoveStoragePoolVolume(projectName, req.Name, instanceDBVolType, pool.ID())
		if err != nil {
			return response.SmartError(err)
		}
	}

	if instanceErr == nil {
		// Remove the storage volume db entry for the instance since force was specified.
		err := d.cluster.DeleteInstance(projectName, req.Name)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Prepare root disk entry if needed.
	rootDev := map[string]string{}
	rootDev["type"] = "disk"
	rootDev["path"] = "/"
	rootDev["pool"] = instancePoolName

	// Mark the filesystem as going through an import.
	importingFilePath := storagePools.InstanceImportingFilePath(instanceType, instancePoolName, projectName, req.Name)
	fd, err := os.Create(importingFilePath)
	if err != nil {
		return response.InternalError(err)
	}
	fd.Close()
	defer os.Remove(fd.Name())

	baseImage := backupConf.Container.Config["volatile.base_image"]

	// Add root device if missing.
	root, _, _ := shared.GetRootDiskDevice(backupConf.Container.Devices)
	if root == "" {
		if backupConf.Container.Devices == nil {
			backupConf.Container.Devices = map[string]map[string]string{}
		}

		rootDevName := "root"
		for i := 0; i < 100; i++ {
			if backupConf.Container.Devices[rootDevName] == nil {
				break
			}
			rootDevName = fmt.Sprintf("root%d", i)
			continue
		}

		backupConf.Container.Devices[rootDevName] = rootDev
	}

	arch, err := osarch.ArchitectureId(backupConf.Container.Architecture)
	if err != nil {
		return response.SmartError(err)
	}
	_, err = instanceCreateInternal(d.State(), db.InstanceArgs{
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
	})
	if err != nil {
		return response.SmartError(errors.Wrap(err, "Failed creating instance record"))
	}

	instancePath := storagePools.InstancePath(instanceType, projectName, req.Name, false)
	isPrivileged := false
	if backupConf.Container.Config["security.privileged"] == "" {
		isPrivileged = true
	}
	err = storagePools.CreateContainerMountpoint(instanceMountPoint, instancePath, isPrivileged)
	if err != nil {
		return response.InternalError(err)
	}

	for _, snap := range existingSnapshots {
		parts := strings.SplitN(snap.Name, shared.SnapshotDelimiter, 2)

		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := d.cluster.GetInstanceSnapshotID(projectName, parts[0], parts[1])
		if snapErr != nil {
			if snapErr != db.ErrNoSuchObject {
				return response.SmartError(snapErr)
			}
		}

		// If a db entry exists only proceed if force was specified.
		if snapErr == nil && !req.Force {
			return response.BadRequest(fmt.Errorf(`Entry for snapshot %q already exists in the database. Set "force" to overwrite`, snap.Name))
		}

		// Check if a storage volume entry for the snapshot already exists.
		_, _, csVolErr := d.cluster.GetLocalStoragePoolVolume(projectName, snap.Name, instanceDBVolType, pool.ID())
		if csVolErr != nil {
			if csVolErr != db.ErrNoSuchObject {
				return response.SmartError(csVolErr)
			}
		}

		// If a storage volume entry exists only proceed if force was specified.
		if csVolErr == nil && !req.Force {
			return response.BadRequest(fmt.Errorf(`Storage volume for snapshot %q already exists in the database. Set "force" to overwrite`, snap.Name))
		}

		if snapErr == nil {
			err := d.cluster.DeleteInstance(projectName, snap.Name)
			if err != nil {
				return response.SmartError(err)
			}
		}

		if csVolErr == nil {
			err := d.cluster.RemoveStoragePoolVolume(projectName, snap.Name, instanceDBVolType, pool.ID())
			if err != nil {
				return response.SmartError(err)
			}
		}

		baseImage := snap.Config["volatile.base_image"]

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return response.SmartError(err)
		}

		// Add root device if missing.
		root, _, _ := shared.GetRootDiskDevice(snap.Devices)
		if root == "" {
			if snap.Devices == nil {
				snap.Devices = map[string]map[string]string{}
			}

			rootDevName := "root"
			for i := 0; i < 100; i++ {
				if snap.Devices[rootDevName] == nil {
					break
				}
				rootDevName = fmt.Sprintf("root%d", i)
				continue
			}

			snap.Devices[rootDevName] = rootDev
		}

		_, err = instanceCreateInternal(d.State(), db.InstanceArgs{
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
			Name:         snap.Name,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		})
		if err != nil {
			return response.SmartError(errors.Wrapf(err, "Failed creating instance snapshot record %q", snap.Name))
		}

		// Recreate missing mountpoints and symlinks.
		volStorageName := project.Instance(projectName, snap.Name)
		snapshotMountPoint := storageDrivers.GetVolumeMountPath(instancePoolName, instanceVolType, volStorageName)
		snapshotPath := storagePools.InstancePath(instanceType, projectName, req.Name, true)
		snapshotTargetPath := storageDrivers.GetVolumeSnapshotDir(instancePoolName, instanceVolType, volStorageName)

		err = storagePools.CreateSnapshotMountpoint(snapshotMountPoint, snapshotTargetPath, snapshotPath)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return response.EmptySyncResponse
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
