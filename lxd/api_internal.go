package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	runtimeDebug "runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/warningtype"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
)

var apiInternal = []APIEndpoint{
	internalBGPStateCmd,
	internalClusterAcceptCmd,
	internalClusterAssignCmd,
	internalClusterHandoverCmd,
	internalClusterRaftNodeCmd,
	internalClusterRebalanceCmd,
	internalClusterHealCmd,
	internalContainerOnStartCmd,
	internalContainerOnStartHostCmd,
	internalContainerOnStopCmd,
	internalContainerOnStopNSCmd,
	internalGarbageCollectorCmd,
	internalImageOptimizeCmd,
	internalImageRefreshCmd,
	internalRAFTSnapshotCmd,
	internalReadyCmd,
	internalShutdownCmd,
	internalSQLCmd,
	internalWarningCreateCmd,
	internalIdentityCacheRefreshCmd,
	internalPruneTokenCmd,
	internalOperationWaitCmd,
}

var internalShutdownCmd = APIEndpoint{
	Path: "shutdown",

	Put: APIEndpointAction{Handler: internalShutdown, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalReadyCmd = APIEndpoint{
	Path: "ready",

	Get: APIEndpointAction{Handler: internalWaitReady, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStartCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstart",

	Get: APIEndpointAction{Handler: internalContainerOnStart, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStartHostCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstarthost",

	Get: APIEndpointAction{Handler: internalContainerOnStartHost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStopNSCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstopns",

	Get: APIEndpointAction{Handler: internalContainerOnStopNS, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStopCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstop",

	Get: APIEndpointAction{Handler: internalContainerOnStop, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalSQLCmd = APIEndpoint{
	Path: "sql",

	Get:  APIEndpointAction{Handler: internalSQLGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Post: APIEndpointAction{Handler: internalSQLPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalGarbageCollectorCmd = APIEndpoint{
	Path: "gc",

	Get: APIEndpointAction{Handler: internalGC, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalRAFTSnapshotCmd = APIEndpoint{
	Path: "raft-snapshot",

	Get: APIEndpointAction{Handler: internalRAFTSnapshot, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalImageRefreshCmd = APIEndpoint{
	Path: "testing/image-refresh",

	Get: APIEndpointAction{Handler: internalRefreshImage, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalImageOptimizeCmd = APIEndpoint{
	Path: "image-optimize",

	Post: APIEndpointAction{Handler: internalOptimizeImage, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalWarningCreateCmd = APIEndpoint{
	Path: "testing/warnings",

	Post: APIEndpointAction{Handler: internalCreateWarning, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalBGPStateCmd = APIEndpoint{
	Path: "testing/bgp",

	Get: APIEndpointAction{Handler: internalBGPState, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalPruneTokenCmd = APIEndpoint{
	Path: "testing/prune-tokens",
	Post: APIEndpointAction{Handler: removeTokenHandler, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalOperationWaitCmd = APIEndpoint{
	Path: "testing/operation-wait",
	Post: APIEndpointAction{Handler: operationWaitHandler, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalIdentityCacheRefreshCmd = APIEndpoint{
	Path: "identity-cache-refresh",

	Post: APIEndpointAction{Handler: internalIdentityCacheRefresh, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

type internalImageOptimizePost struct {
	Image   api.Image `json:"image"    yaml:"image"`
	Pool    string    `json:"pool"     yaml:"pool"`
	Project string    `json:"project"  yaml:"project"`
}

type internalWarningCreatePost struct {
	Location   string      `json:"location"    yaml:"location"`
	Project    string      `json:"project"     yaml:"project"`
	EntityType entity.Type `json:"entity_type" yaml:"entity_type"`
	EntityID   int         `json:"entity_id"   yaml:"entity_id"`
	TypeCode   int         `json:"type_code"   yaml:"type_code"`
	Message    string      `json:"message"     yaml:"message"`
}

// internalCreateWarning creates a warning, and is used for testing only.
func internalCreateWarning(d *Daemon, r *http.Request) response.Response {
	req := internalWarningCreatePost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// If entity type is set, check it is valid and fail if it isn't.
	if req.EntityType != "" {
		err = req.EntityType.Validate()
		if err != nil {
			return response.BadRequest(fmt.Errorf("Invalid entity type: %w", err))
		}
	}

	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpsertWarning(ctx, req.Location, req.Project, req.EntityType, req.EntityID, warningtype.Type(req.TypeCode), req.Message)
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create warning: %w", err))
	}

	return response.EmptySyncResponse
}

func internalOptimizeImage(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := &internalImageOptimizePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = imageCreateInPool(s, &req.Image, req.Pool, req.Project)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalRefreshImage(d *Daemon, _ *http.Request) response.Response {
	s := d.State()

	err := autoUpdateImages(s.ShutdownCtx, s)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalWaitReady(d *Daemon, r *http.Request) response.Response {
	// Check that we're not shutting down.
	isClosing := d.State().ShutdownCtx.Err() != nil
	if isClosing {
		return response.Unavailable(errors.New("LXD daemon is shutting down"))
	}

	timeoutSeconds := request.QueryParam(r, "timeout")
	waitNetwork := shared.IsTrue(request.QueryParam(r, "network"))
	waitStorage := shared.IsTrue(request.QueryParam(r, "storage"))

	notReadyErr := errors.New("LXD daemon not ready yet")
	networkNotReadyErr := errors.New("Networks not ready yet")
	storageNotReadyErr := errors.New("Storage pools not ready yet")

	if timeoutSeconds != "" {
		timeoutSecondsUint, err := strconv.ParseUint(timeoutSeconds, 10, 32)
		if err != nil {
			return response.SmartError(fmt.Errorf("Invalid timeout: %w", err))
		}

		// If timeout is 0 then block indefinitely but if >0 use that as max wait seconds.
		ctx := r.Context()
		if timeoutSecondsUint > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSecondsUint)*time.Second)
			defer cancel()
		}

		// If network is true, then block until all networks are marked as ready.
		if waitNetwork {
			select {
			case <-d.waitNetworkReady.Done(): // Block until all networks are ready.
			case <-ctx.Done(): // Don't leave this go routine around if client disconnects or timeout reached.
				return response.Unavailable(networkNotReadyErr)
			}
		}

		// If storage is true, then block until all storage pools are marked as ready.
		if waitStorage {
			select {
			case <-d.waitStorageReady.Done(): // Block until all storage pools are ready.
			case <-ctx.Done(): // Don't leave this go routine around if client disconnects or timeout reached.
				return response.Unavailable(storageNotReadyErr)
			}
		}

		select {
		case <-d.waitReady.Done(): // Block until LXD is ready and then return EmptySyncResponse.
		case <-ctx.Done(): // Don't leave this go routine around if client disconnects or timeout reached.
			return response.Unavailable(notReadyErr)
		}
	} else if d.waitReady.Err() == nil {
		return response.Unavailable(notReadyErr)
	} else if waitNetwork && d.waitNetworkReady.Err() == nil {
		return response.Unavailable(networkNotReadyErr)
	} else if waitStorage && d.waitStorageReady.Err() == nil {
		return response.Unavailable(storageNotReadyErr)
	}

	return response.EmptySyncResponse // LXD is ready.
}

func internalShutdown(d *Daemon, r *http.Request) response.Response {
	force := request.QueryParam(r, "force")
	logger.Info("Asked to shutdown by API", logger.Ctx{"force": force})

	if d.State().ShutdownCtx.Err() != nil {
		return response.SmartError(api.StatusErrorf(http.StatusTooManyRequests, "Shutdown already in progress"))
	}

	forceCtx, forceCtxCancel := context.WithCancel(context.Background())

	if force == "true" {
		forceCtxCancel() // Don't wait for operations to finish.
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		defer forceCtxCancel()

		select {
		case <-d.setupChan: // Wait for daemon to start.
		case <-r.Context().Done(): // Don't leave this go routine around if client disconnects.
		}

		// Run shutdown sequence synchronously.
		stopErr := d.Stop(forceCtx, unix.SIGPWR)
		err := response.SmartError(stopErr).Render(w, r)
		if err != nil {
			return err
		}

		// Send the response before the LXD daemon process ends.
		f, ok := w.(http.Flusher)
		if !ok {
			return errors.New("http.ResponseWriter is not type http.Flusher")
		}

		f.Flush()

		go func() {
			<-r.Context().Done()    // Wait until request is finished.
			d.shutdownDoneCh <- nil // Send nil error to cmdDaemon to ensure LXD isn't restarted by systemd.
		}()

		return nil
	})
}

// internalContainerHookLoadFromRequestReference loads the container from the instance reference in the request.
// It detects whether the instance reference is an instance ID or instance name and loads instance accordingly.
func internalContainerHookLoadFromReference(s *state.State, r *http.Request) (instance.Instance, error) {
	var inst instance.Instance
	instanceRef, err := url.PathUnescape(mux.Vars(r)["instanceRef"])
	if err != nil {
		return nil, err
	}

	projectName := request.ProjectParam(r)

	instanceID, err := strconv.Atoi(instanceRef)
	if err == nil {
		inst, err = instance.LoadByID(s, instanceID)
		if err != nil {
			return nil, err
		}
	} else {
		inst, err = instance.LoadByProjectAndName(s, projectName, instanceRef)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil, err
			}

			// Defend against path traversal attacks.
			err = instancetype.ValidName(instanceRef, false)
			if err != nil {
				return nil, fmt.Errorf("Invalid instance name %q: %w", instanceRef, err)
			}

			// If DB not available, try loading from backup file.
			logger.Warn("Failed loading instance from database, trying backup file", logger.Ctx{"project": projectName, "instance": instanceRef, "err": err})

			instancePath := filepath.Join(shared.VarPath("containers"), project.Instance(projectName, instanceRef))
			inst, err = instance.LoadFromBackup(s, projectName, instancePath)
			if err != nil {
				return nil, fmt.Errorf("Failed loading instance from backup file: %w", err)
			}
		}
	}

	if inst.Type() != instancetype.Container {
		return nil, errors.New("Instance is not container type")
	}

	return inst, nil
}

func internalContainerOnStart(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The start hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	err = inst.OnHook(instance.HookStart, nil)
	if err != nil {
		l.Error("The start hook failed", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStartHost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The start-host hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	lxcPID := request.QueryParam(r, "lxc_pid")
	if lxcPID == "" {
		err := errors.New("No lxc_pid GET parameter was provided")
		l.Error("The start-host hook failed", logger.Ctx{"err": err})
		return response.BadRequest(err)
	}

	args := map[string]string{
		"LXC_PID": lxcPID,
	}

	err = inst.OnHook(instance.HookStartHost, args)
	if err != nil {
		l.Error("The start-host hook failed", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStopNS(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The stopns hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	target := request.QueryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	netns := request.QueryParam(r, "netns")

	args := map[string]string{
		"target": target,
		"netns":  netns,
	}

	err = inst.OnHook(instance.HookStopNS, args)
	if err != nil {
		l.Error("The stopns hook failed", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The stop hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	target := request.QueryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	args := map[string]string{
		"target": target,
	}

	err = inst.OnHook(instance.HookStop, args)
	if err != nil {
		l.Error("The stop hook failed", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

type internalSQLDump struct {
	Text string `json:"text" yaml:"text"`
}

type internalSQLQuery struct {
	Database string `json:"database" yaml:"database"`
	Query    string `json:"query"    yaml:"query"`
}

type internalSQLBatch struct {
	Results []internalSQLResult `json:"Results" yaml:"results"`
}

type internalSQLResult struct {
	Type         string   `json:"type"          yaml:"type"`
	Columns      []string `json:"columns"       yaml:"columns"`
	Rows         [][]any  `json:"rows"          yaml:"rows"`
	RowsAffected int64    `json:"rows_affected" yaml:"rows_affected"`
}

// Perform a database dump.
func internalSQLGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	database := r.FormValue("database")

	if !slices.Contains([]string{"local", "global"}, database) {
		return response.BadRequest(errors.New("Invalid database"))
	}

	schemaFormValue := r.FormValue("schema")
	schemaOnly, err := strconv.Atoi(schemaFormValue)
	if err != nil {
		schemaOnly = 0
	}

	var db *sql.DB
	if database == "global" {
		db = s.DB.Cluster.DB()
	} else {
		db = s.DB.Node.DB()
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to start transaction: %w", err))
	}

	defer func() { _ = tx.Rollback() }()

	dump, err := query.Dump(r.Context(), tx, schemaOnly == 1)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed dump database %s: %w", database, err))
	}

	return response.SyncResponse(true, internalSQLDump{Text: dump})
}

// Execute queries.
func internalSQLPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := &internalSQLQuery{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if !slices.Contains([]string{"local", "global"}, req.Database) {
		return response.BadRequest(errors.New("Invalid database"))
	}

	if req.Query == "" {
		return response.BadRequest(errors.New("No query provided"))
	}

	var db *sql.DB
	if req.Database == "global" {
		db = s.DB.Cluster.DB()
	} else {
		db = s.DB.Node.DB()
	}

	batch := internalSQLBatch{}

	if req.Query == ".sync" {
		d.gateway.Sync()
		return response.SyncResponse(true, batch)
	}

	for query := range strings.SplitSeq(req.Query, ";") {
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
			_ = tx.Rollback()
		} else {
			err = internalSQLExec(tx, query, &result)
			if err != nil {
				_ = tx.Rollback()
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
		return fmt.Errorf("Failed to execute query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	result.Columns, err = rows.Columns()
	if err != nil {
		return fmt.Errorf("Failed to fetch colume names: %w", err)
	}

	for rows.Next() {
		row := make([]any, len(result.Columns))
		rowPointers := make([]any, len(result.Columns))
		for i := range row {
			rowPointers[i] = &row[i]
		}

		err := rows.Scan(rowPointers...)
		if err != nil {
			return fmt.Errorf("Failed to scan row: %w", err)
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
		return fmt.Errorf("Got a row error: %w", err)
	}

	return nil
}

func internalSQLExec(tx *sql.Tx, query string, result *internalSQLResult) error {
	result.Type = "exec"
	r, err := tx.Exec(query)
	if err != nil {
		return fmt.Errorf("Failed to exec query: %w", err)
	}

	result.RowsAffected, err = r.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to fetch affected rows: %w", err)
	}

	return nil
}

// internalImportFromBackup creates instance, storage pool and volume DB records from an instance's backup file.
// It expects the instance volume to be mounted so that the backup.yaml file is readable.
// Also accepts an optional map of device overrides.
func internalImportFromBackup(ctx context.Context, s *state.State, projectName string, instName string, allowNameOverride bool, deviceOverrides map[string]map[string]string) error {
	if instName == "" {
		return errors.New("The name of the instance is required")
	}

	storagePoolsPath := shared.VarPath("storage-pools")
	storagePoolsDir, err := os.Open(storagePoolsPath)
	if err != nil {
		return err
	}

	// Get a list of all storage pools.
	storagePoolNames, err := storagePoolsDir.Readdirnames(-1)
	if err != nil {
		_ = storagePoolsDir.Close()
		return err
	}

	_ = storagePoolsDir.Close()

	// Check whether the instance exists on any of the storage pools as either a container or a VM.
	instanceMountPoints := []string{}
	instancePoolName := ""
	instanceType := instancetype.Container
	instanceVolType := storageDrivers.VolumeTypeContainer
	instanceDBVolType := cluster.StoragePoolVolumeTypeContainer

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
					instanceDBVolType = cluster.StoragePoolVolumeTypeVM
				} else {
					instanceType = instancetype.Container
					instanceDBVolType = cluster.StoragePoolVolumeTypeContainer
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

	if backupConf.Instance == nil {
		return errors.New("Instance definition in backup config is missing")
	}

	if allowNameOverride && instName != "" {
		backupConf.Instance.Name = instName
	}

	if instName != backupConf.Instance.Name {
		return fmt.Errorf("Requested instance name %q doesn't match instance name %q in backup config", instName, backupConf.Instance.Name)
	}

	if len(backupConf.Pools) == 0 {
		// We don't know what kind of storage type the pool is.
		return errors.New("No storage pool struct in the backup file found. The storage pool needs to be recovered manually")
	}

	rootVolPool, err := backupConf.RootVolumePool()
	if err != nil {
		return fmt.Errorf("Failed getting the root volume's pool: %w", err)
	}

	// Try to retrieve the storage pool the instance supposedly lives on.
	pool, err := storagePools.LoadByName(s, instancePoolName)
	if response.IsNotFoundError(err) {
		// Create the storage pool db entry if it doesn't exist.
		_, err = storagePoolDBCreate(ctx, s, instancePoolName, "", rootVolPool.Driver, rootVolPool.Config)
		if err != nil {
			return fmt.Errorf("Create storage pool database entry: %w", err)
		}

		pool, err = storagePools.LoadByName(s, instancePoolName)
		if err != nil {
			return fmt.Errorf("Load storage pool database entry: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("Find storage pool database entry: %w", err)
	}

	if rootVolPool.Name != instancePoolName {
		return fmt.Errorf(`The storage pool %q the instance was detected on does not match the storage pool %q specified in the backup file`, instancePoolName, rootVolPool.Name)
	}

	if rootVolPool.Driver != pool.Driver().Info().Name {
		return fmt.Errorf(`The storage pool's %q driver %q conflicts with the driver %q recorded in the instance's backup file`, instancePoolName, pool.Driver().Info().Name, rootVolPool.Driver)
	}

	// Check snapshots are consistent.
	existingSnapshots, err := pool.CheckInstanceBackupFileSnapshots(backupConf, projectName, nil)
	if err != nil {
		return fmt.Errorf("Failed checking snapshots: %w", err)
	}

	// Check if a storage volume entry for the instance already exists.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, instanceDBVolType, backupConf.Instance.Name, true)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	if dbVolume != nil {
		return fmt.Errorf(`Storage volume for instance %q already exists in the database`, backupConf.Instance.Name)
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if an entry for the instance already exists in the db.
		_, err := tx.GetInstanceID(ctx, projectName, backupConf.Instance.Name)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	if err == nil {
		return fmt.Errorf(`Entry for instance %q already exists in the database`, backupConf.Instance.Name)
	}

	if len(backupConf.Volumes) == 0 {
		return errors.New(`No storage volume struct in the backup file found. The storage volume needs to be recovered manually`)
	}

	if dbVolume != nil {
		rootVol, err := backupConf.RootVolume()
		if err != nil {
			return fmt.Errorf("Failed getting the root volume: %w", err)
		}

		if dbVolume.Name != rootVol.Name {
			return fmt.Errorf(`The storage volume name %q does not match the instance's name %q`, dbVolume.Name, backupConf.Instance.Name)
		}

		if dbVolume.Type != rootVol.Type {
			return fmt.Errorf(`The storage volume type %q does not match the instance's type %q`, dbVolume.Type, rootVol.Type)
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Remove the storage volume db entry for the instance since force was specified.
			return tx.RemoveStoragePoolVolume(ctx, projectName, backupConf.Instance.Name, instanceDBVolType, pool.ID())
		})
		if err != nil {
			return err
		}
	}

	var profiles []api.Profile

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		profiles, err = tx.GetProfiles(ctx, projectName, backupConf.Instance.Profiles)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed loading profiles for instance: %w", err)
	}

	// Initialise the devices maps.
	if backupConf.Instance.Devices == nil {
		backupConf.Instance.Devices = make(map[string]map[string]string, 0)
	}

	if backupConf.Instance.ExpandedDevices == nil {
		backupConf.Instance.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	// Apply device overrides.
	// Do this before calling internalImportRootDevicePopulate so that device overrides are taken into account.
	resultingDevices, err := shared.ApplyDeviceOverrides(backupConf.Instance.Devices, backupConf.Instance.ExpandedDevices, deviceOverrides)
	if err != nil {
		return err
	}

	backupConf.Instance.Devices = resultingDevices

	// Add root device if needed.
	// And ensure root device is associated with same pool as instance has been imported to.
	internalImportRootDevicePopulate(instancePoolName, backupConf.Instance.Devices, backupConf.Instance.ExpandedDevices, profiles)

	revert := revert.New()
	defer revert.Fail()

	if backupConf.Instance == nil {
		return errors.New("No instance config in backup config")
	}

	instDBArgs, err := backup.ConfigToInstanceDBArgs(s, backupConf, projectName, true)
	if err != nil {
		return err
	}

	_, instOp, cleanup, err := instance.CreateInternal(s, *instDBArgs, true)
	if err != nil {
		return fmt.Errorf("Failed creating instance record: %w", err)
	}

	revert.Add(cleanup)
	defer instOp.Done(err)

	instancePath := storagePools.InstancePath(instanceType, projectName, backupConf.Instance.Name, false)
	isPrivileged := backupConf.Instance.Config["security.privileged"] == ""

	err = storagePools.CreateContainerMountpoint(instanceMountPoint, instancePath, isPrivileged)
	if err != nil {
		return err
	}

	for _, snap := range existingSnapshots {
		snapInstName := backupConf.Instance.Name + shared.SnapshotDelimiter + snap.Name

		snapErr := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Check if an entry for the snapshot already exists in the db.
			_, err := tx.GetInstanceSnapshotID(ctx, projectName, backupConf.Instance.Name, snap.Name)

			return err
		})
		if snapErr != nil && !response.IsNotFoundError(snapErr) {
			return snapErr
		}

		if snapErr == nil {
			return fmt.Errorf(`Entry for snapshot %q already exists in the database`, snapInstName)
		}

		// Check if a storage volume entry for the snapshot already exists.
		var dbVolume *db.StorageVolume
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, instanceDBVolType, snapInstName, true)
			if err != nil && !response.IsNotFoundError(err) {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		// If a storage volume entry exists only proceed if force was specified.
		if dbVolume != nil {
			return fmt.Errorf(`Storage volume for snapshot %q already exists in the database`, snapInstName)
		}

		baseImage := snap.Config["volatile.base_image"]

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return err
		}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			profiles, err = tx.GetProfiles(ctx, projectName, snap.Profiles)

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed loading profiles for instance snapshot %q: %w", snapInstName, err)
		}

		// Add root device if needed.
		if snap.Devices == nil {
			snap.Devices = make(map[string]map[string]string, 0)
		}

		if snap.ExpandedDevices == nil {
			snap.ExpandedDevices = make(map[string]map[string]string, 0)
		}

		internalImportRootDevicePopulate(instancePoolName, snap.Devices, snap.ExpandedDevices, profiles)

		_, snapInstOp, cleanup, err := instance.CreateInternal(s, db.InstanceArgs{
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
			Profiles:     profiles,
			Stateful:     snap.Stateful,
		}, true)
		if err != nil {
			return fmt.Errorf("Failed creating instance snapshot record %q: %w", snap.Name, err)
		}

		revert.Add(cleanup)
		defer snapInstOp.Done(err)

		// Recreate missing mountpoints and symlinks.
		volStorageName := project.Instance(projectName, snapInstName)
		snapshotMountPoint := storageDrivers.GetVolumeMountPath(instancePoolName, instanceVolType, volStorageName)
		snapshotPath := storagePools.InstancePath(instanceType, projectName, backupConf.Instance.Name, true)
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
	rootName, _, err := instancetype.GetRootDiskDevice(localDevices)
	if err == nil && rootName != "" {
		localDevices[rootName]["pool"] = instancePoolName

		return // Local root disk device has been set to target pool.
	}

	// Next check if expandedDevices from backup.yaml has a root disk.
	expandedRootName, expandedRootConfig, _ := instancetype.GetRootDiskDevice(expandedDevices)

	// Extract root disk from expanded profile devices.
	profileExpandedDevices := instancetype.ExpandInstanceDevices(deviceConfig.NewDevices(localDevices), profiles)
	profileExpandedRootName, profileExpandedRootConfig, _ := instancetype.GetRootDiskDevice(profileExpandedDevices.CloneNative())

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
				_, found := rootDev[k]
				if !found {
					rootDev[k] = v
				}
			}
		}

		// If there is already a device called "root" in the instance's config, but it does not qualify as
		// a root disk, then try to find a free name for the new root disk device.
		rootDevName := "root"
		for i := range 100 {
			if localDevices[rootDevName] == nil {
				break
			}

			rootDevName = "root" + strconv.FormatInt(int64(i), 10)
			continue
		}

		localDevices[rootDevName] = rootDev
	}
}

func internalGC(_ *Daemon, _ *http.Request) response.Response {
	logger.Info("Started forced garbage collection run")
	runtime.GC()
	runtimeDebug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logger.Infof("Heap allocated: %s", units.GetByteSizeStringIEC(int64(m.Alloc), 2))
	logger.Infof("Stack in use: %s", units.GetByteSizeStringIEC(int64(m.StackInuse), 2))
	logger.Infof("Requested from system: %s", units.GetByteSizeStringIEC(int64(m.Sys), 2))
	logger.Infof("Releasable to OS: %s", units.GetByteSizeStringIEC(int64(m.HeapIdle-m.HeapReleased), 2))

	logger.Info("Completed forced garbage collection run")

	return response.EmptySyncResponse
}

func internalRAFTSnapshot(_ *Daemon, _ *http.Request) response.Response {
	logger.Warn("Forced RAFT snapshot not supported")

	return response.InternalError(errors.New("Not supported"))
}

func internalBGPState(d *Daemon, _ *http.Request) response.Response {
	s := d.State()

	return response.SyncResponse(true, s.BGP.Debug())
}

func internalIdentityCacheRefresh(d *Daemon, _ *http.Request) response.Response {
	logger.Debug("Received identity cache update notification - refreshing cache")
	d.State().UpdateIdentityCache()
	return response.EmptySyncResponse
}
