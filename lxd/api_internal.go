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
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
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
	Path: "containers/{id}/onstart",

	Get: APIEndpointAction{Handler: internalContainerOnStart},
}

var internalContainerOnStopNSCmd = APIEndpoint{
	Path: "containers/{id}/onstopns",

	Get: APIEndpointAction{Handler: internalContainerOnStopNS},
}

var internalContainerOnStopCmd = APIEndpoint{
	Path: "containers/{id}/onstop",

	Get: APIEndpointAction{Handler: internalContainerOnStop},
}

var internalSQLCmd = APIEndpoint{
	Path: "sql",

	Get:  APIEndpointAction{Handler: internalSQLGet},
	Post: APIEndpointAction{Handler: internalSQLPost},
}

var internalContainersCmd = APIEndpoint{
	Path: "containers",

	Post: APIEndpointAction{Handler: internalImport},
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
	select {
	case <-d.readyChan:
	default:
		return response.Unavailable(fmt.Errorf("LXD daemon not ready yet"))
	}

	return response.EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) response.Response {
	d.shutdownChan <- struct{}{}

	return response.EmptySyncResponse
}

func internalContainerOnStart(d *Daemon, r *http.Request) response.Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	inst, err := instance.LoadByID(d.State(), id)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	c := inst.(*containerLXC)
	err = c.OnStart()
	if err != nil {
		logger.Error("The start hook failed", log.Ctx{"container": c.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStopNS(d *Daemon, r *http.Request) response.Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	target := queryParam(r, "target")
	if target == "" {
		target = "unknown"
	}
	netns := queryParam(r, "netns")

	inst, err := instance.LoadByID(d.State(), id)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	c := inst.(*containerLXC)
	err = c.OnStopNS(target, netns)
	if err != nil {
		logger.Error("The stopns hook failed", log.Ctx{"container": c.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) response.Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	target := queryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	inst, err := instance.LoadByID(d.State(), id)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	c := inst.(*containerLXC)
	err = c.OnStop(target)
	if err != nil {
		logger.Error("The stop hook failed", log.Ctx{"container": c.Name(), "err": err})
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
	Name  string `json:"name" yaml:"name"`
	Force bool   `json:"force" yaml:"force"`
}

func internalImport(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)

	req := &internalImportPost{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		return response.BadRequest(fmt.Errorf(`The name of the instance is required`))
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

	// Check whether the container exists on any of the storage pools.
	containerMntPoints := []string{}
	containerPoolName := ""
	for _, poolName := range storagePoolNames {
		containerMntPoint := storagePools.GetContainerMountPoint(projectName, poolName, req.Name)
		if shared.PathExists(containerMntPoint) {
			containerMntPoints = append(containerMntPoints, containerMntPoint)
			containerPoolName = poolName
		}
	}

	// Sanity checks.
	if len(containerMntPoints) > 1 {
		return response.BadRequest(fmt.Errorf(`The instance %q seems to exist on multiple storage pools`, req.Name))
	} else if len(containerMntPoints) != 1 {
		return response.BadRequest(fmt.Errorf(`The instance %q does not seem to exist on any storage pool`, req.Name))
	}

	// User needs to make sure that we can access the directory where backup.yaml lives.
	containerMntPoint := containerMntPoints[0]
	isEmpty, err := shared.PathIsEmpty(containerMntPoint)
	if err != nil {
		return response.InternalError(err)
	}

	if isEmpty {
		return response.BadRequest(fmt.Errorf(`The instance's directory %q appears to be empty. Please ensure that the instance's storage volume is mounted`, containerMntPoint))
	}

	// Read in the backup.yaml file.
	backupYamlPath := filepath.Join(containerMntPoint, "backup.yaml")
	backupConf, err := backup.ParseInstanceConfigYamlFile(backupYamlPath)
	if err != nil {
		return response.SmartError(err)
	}

	if req.Name != backupConf.Container.Name {
		return response.InternalError(fmt.Errorf("Instance name in request %q doesn't match instance name in backup config %q", req.Name, backupConf.Container.Name))
	}

	// Update snapshot names to include container name (if needed).
	for i, snap := range backupConf.Snapshots {
		if !strings.Contains(snap.Name, "/") {
			backupConf.Snapshots[i].Name = fmt.Sprintf("%s/%s", backupConf.Container.Name, snap.Name)
		}
	}

	if backupConf.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return response.BadRequest(fmt.Errorf(`No storage pool struct in the backup file found. The storage pool needs to be recovered manually`))
	}

	// Try to retrieve the storage pool the container supposedly lives on.
	pool, err := storagePools.GetPoolByName(d.State(), containerPoolName)
	if err == db.ErrNoSuchObject {
		// Create the storage pool db entry if it doesn't exist.
		_, err = storagePoolDBCreate(d.State(), containerPoolName, "", backupConf.Pool.Driver, backupConf.Pool.Config)
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Create storage pool database entry"))
		}

		pool, err = storagePools.GetPoolByName(d.State(), containerPoolName)
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Load storage pool database entry"))
		}
	} else if err != nil {
		return response.SmartError(errors.Wrap(err, "Find storage pool database entry"))
	}

	if backupConf.Pool.Name != containerPoolName {
		return response.BadRequest(fmt.Errorf(`The storage pool %q the instance was detected on does not match the storage pool %q specified in the backup file`, containerPoolName, backupConf.Pool.Name))
	}

	if backupConf.Pool.Driver != pool.Driver().Info().Name {
		return response.BadRequest(fmt.Errorf(`The storage pool's %q driver %q conflicts with the driver %q recorded in the instance's backup file`, containerPoolName, pool.Driver().Info().Name, backupConf.Pool.Driver))
	}

	// Check snapshots are consistent, and if not, if req.Force is true, then delete snapshots that do not exist in backup.yaml.
	existingSnapshots, err := pool.CheckInstanceBackupFileSnapshots(backupConf, projectName, req.Force, nil)
	if err != nil {
		if errors.Cause(err) == storagePools.ErrBackupSnapshotsMismatch {
			return response.InternalError(fmt.Errorf(`%s. Set "force" to discard non-existing snapshots`, err))
		}

		return response.InternalError(errors.Wrap(err, "Checking snapshots"))
	}

	// Check if a storage volume entry for the container already exists.
	_, volume, ctVolErr := d.cluster.StoragePoolNodeVolumeGetTypeByProject(projectName, req.Name, storagePoolVolumeTypeContainer, pool.ID())
	if ctVolErr != nil {
		if ctVolErr != db.ErrNoSuchObject {
			return response.SmartError(ctVolErr)
		}
	}

	// If a storage volume entry exists only proceed if force was specified.
	if ctVolErr == nil && !req.Force {
		return response.BadRequest(fmt.Errorf(`Storage volume for instance %q already exists in the database. Set "force" to overwrite`, req.Name))
	}

	// Check if an entry for the container already exists in the db.
	_, containerErr := d.cluster.ContainerID(projectName, req.Name)
	if containerErr != nil {
		if containerErr != db.ErrNoSuchObject {
			return response.SmartError(containerErr)
		}
	}

	// If a db entry exists only proceed if force was specified.
	if containerErr == nil && !req.Force {
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

		// Remove the storage volume db entry for the container since force was specified.
		err := d.cluster.StoragePoolVolumeDelete(projectName, req.Name, storagePoolVolumeTypeContainer, pool.ID())
		if err != nil {
			return response.SmartError(err)
		}
	}

	if containerErr == nil {
		// Remove the storage volume db entry for the container since force was specified.
		err := d.cluster.InstanceRemove(projectName, req.Name)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Prepare root disk entry if needed.
	rootDev := map[string]string{}
	rootDev["type"] = "disk"
	rootDev["path"] = "/"
	rootDev["pool"] = containerPoolName

	// Mark the filesystem as going through an import.
	importingFilePath := storagePools.InstanceImportingFilePath(instancetype.Container, containerPoolName, projectName, req.Name)
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
		Type:         instancetype.Container,
		Description:  backupConf.Container.Description,
		Devices:      deviceConfig.NewDevices(backupConf.Container.Devices),
		Ephemeral:    backupConf.Container.Ephemeral,
		LastUsedDate: backupConf.Container.LastUsedAt,
		Name:         backupConf.Container.Name,
		Profiles:     backupConf.Container.Profiles,
		Stateful:     backupConf.Container.Stateful,
	})
	if err != nil {
		err = errors.Wrap(err, "Create container")
		return response.SmartError(err)
	}

	containerPath := storagePools.InstancePath(instancetype.Container, projectName, req.Name, false)
	isPrivileged := false
	if backupConf.Container.Config["security.privileged"] == "" {
		isPrivileged = true
	}
	err = storagePools.CreateContainerMountpoint(containerMntPoint, containerPath, isPrivileged)
	if err != nil {
		return response.InternalError(err)
	}

	for _, snap := range existingSnapshots {
		parts := strings.SplitN(snap.Name, shared.SnapshotDelimiter, 2)

		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := d.cluster.InstanceSnapshotID(projectName, parts[0], parts[1])
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
		_, _, csVolErr := d.cluster.StoragePoolNodeVolumeGetTypeByProject(projectName, snap.Name, storagePoolVolumeTypeContainer, pool.ID())
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
			err := d.cluster.InstanceRemove(projectName, snap.Name)
			if err != nil {
				return response.SmartError(err)
			}
		}

		if csVolErr == nil {
			err := d.cluster.StoragePoolVolumeDelete(projectName, snap.Name, storagePoolVolumeTypeContainer, pool.ID())
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
			Type:         instancetype.Container,
			Snapshot:     true,
			Devices:      deviceConfig.NewDevices(snap.Devices),
			Ephemeral:    snap.Ephemeral,
			LastUsedDate: snap.LastUsedAt,
			Name:         snap.Name,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Recreate missing mountpoints and symlinks.
		snapshotMountPoint := storagePools.GetSnapshotMountPoint(projectName, backupConf.Pool.Name, snap.Name)
		sourceName, _, _ := shared.InstanceGetParentAndSnapshotName(snap.Name)
		sourceName = project.Prefix(projectName, sourceName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", backupConf.Pool.Name, "containers-snapshots", sourceName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		err = storagePools.CreateSnapshotMountpoint(snapshotMountPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
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
