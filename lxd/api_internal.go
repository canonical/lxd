package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"

	log "gopkg.in/inconshreveable/log15.v2"
)

var apiInternal = []Command{
	internalReadyCmd,
	internalShutdownCmd,
	internalContainerOnStartCmd,
	internalContainerOnStopCmd,
	internalContainersCmd,
}

func internalReady(d *Daemon, r *http.Request) Response {
	if !d.SetupMode {
		return InternalError(fmt.Errorf("The server isn't currently in setup mode"))
	}

	err := d.Ready()
	if err != nil {
		return InternalError(err)
	}

	d.SetupMode = false

	return EmptySyncResponse
}

func internalWaitReady(d *Daemon, r *http.Request) Response {
	<-d.readyChan

	return EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) Response {
	d.shutdownChan <- true

	return EmptySyncResponse
}

func internalContainerOnStart(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStart()
	if err != nil {
		logger.Error("start hook failed", log.Ctx{"container": c.Name(), "err": err})
		return SmartError(err)
	}

	return EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	target := r.FormValue("target")
	if target == "" {
		target = "unknown"
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStop(target)
	if err != nil {
		logger.Error("stop hook failed", log.Ctx{"container": c.Name(), "err": err})
		return SmartError(err)
	}

	return EmptySyncResponse
}

var internalShutdownCmd = Command{name: "shutdown", put: internalShutdown}
var internalReadyCmd = Command{name: "ready", put: internalReady, get: internalWaitReady}
var internalContainerOnStartCmd = Command{name: "containers/{id}/onstart", get: internalContainerOnStart}
var internalContainerOnStopCmd = Command{name: "containers/{id}/onstop", get: internalContainerOnStop}

func slurpBackupFile(path string) (*backupFile, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	backup := backupFile{}

	if err := yaml.Unmarshal(data, &backup); err != nil {
		return nil, err
	}

	return &backup, nil
}

type internalImportPost struct {
	Name  string `json:"name" yaml:"name"`
	Force bool   `json:"force" yaml:"force"`
}

func internalImport(d *Daemon, r *http.Request) Response {
	req := &internalImportPost{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		return BadRequest(fmt.Errorf("The name of the container is required."))
	}

	storagePoolsPath := shared.VarPath("storage-pools")
	storagePoolsDir, err := os.Open(storagePoolsPath)
	if err != nil {
		return InternalError(err)
	}

	// Get a list of all storage pools.
	storagePoolNames, err := storagePoolsDir.Readdirnames(-1)
	if err != nil {
		storagePoolsDir.Close()
		return InternalError(err)
	}
	storagePoolsDir.Close()

	// Detect the container's mountpoint.
	containerMntPoints := []string{}
	containerPoolName := ""
	for _, poolName := range storagePoolNames {
		containerMntPoint := getContainerMountPoint(poolName, req.Name)
		if shared.PathExists(containerMntPoint) {
			containerMntPoints = append(containerMntPoints, containerMntPoint)
			containerPoolName = poolName
		}
	}

	// Sanity checks.
	if len(containerMntPoints) > 1 {
		return BadRequest(fmt.Errorf("The container \"%s\" seems to exist on another storage pool.", req.Name))
	} else if len(containerMntPoints) != 1 {
		return BadRequest(fmt.Errorf("The container \"%s\" does not seem to exist on any storage pool.", req.Name))
	}

	// User needs to make sure that we can access the directory where
	// backup.yaml lives.
	containerMntPoint := containerMntPoints[0]
	isEmpty, err := shared.PathIsEmpty(containerMntPoint)
	if err != nil {
		return InternalError(err)
	}

	if isEmpty {
		return BadRequest(fmt.Errorf("The container's directory \"%s\" appears to be empty. Please ensure that the container's storage volume is mounted.", containerMntPoint))
	}

	// Read in the backup.yaml file.
	backup, err := slurpBackupFile(shared.VarPath("containers", req.Name, "backup.yaml"))
	if err != nil {
		return SmartError(err)
	}

	// Try to retrieve the storage pool the container supposedly lives on.
	var poolErr error
	poolID, pool, poolErr := dbStoragePoolGet(d.db, containerPoolName)
	if poolErr != nil {
		if poolErr != NoSuchObjectError {
			return SmartError(poolErr)
		}
	}

	if backup.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return BadRequest(fmt.Errorf("No storage pool struct in the backup file found. The storage pool needs to be recovered manually."))
	}

	if poolErr == NoSuchObjectError {
		// Create the storage pool db entry if it doesn't exist.
		err := storagePoolDBCreate(d, containerPoolName, pool.Description, backup.Pool.Driver, backup.Pool.Config)
		if err != nil {
			return SmartError(err)
		}

		poolID, err = dbStoragePoolGetID(d.db, containerPoolName)
		if err != nil {
			return SmartError(err)
		}
	} else {
		if backup.Pool.Name != containerPoolName {
			return BadRequest(fmt.Errorf("The storage pool \"%s\" the container was detected on does not match the storage pool \"%s\" specified in the backup file.", backup.Pool.Name, containerPoolName))
		}

		if backup.Pool.Driver != pool.Driver {
			return BadRequest(fmt.Errorf("The storage pool's \"%s\" driver \"%s\" conflicts with the driver \"%s\" recorded in the container's backup file.", containerPoolName, pool.Driver, backup.Pool.Driver))
		}
	}

	// Check if a storage volume entry for the container already exists.
	_, volume, ctVolErr := dbStoragePoolVolumeGetType(d.db, req.Name, storagePoolVolumeTypeContainer, poolID)
	if ctVolErr != nil {
		if ctVolErr != NoSuchObjectError {
			return SmartError(ctVolErr)
		}
	}
	// If a storage volume entry exists only proceed if force was specified.
	if ctVolErr == nil && !req.Force {
		return BadRequest(fmt.Errorf("Storage volume for container \"%s\" already exists in the database. Set \"force\" to overwrite.", req.Name))
	}

	// Check if an entry for the container already exists in the db.
	_, containerErr := dbContainerId(d.db, req.Name)
	if containerErr != nil {
		if containerErr != sql.ErrNoRows {
			return SmartError(containerErr)
		}
	}
	// If a db entry exists only proceed if force was specified.
	if containerErr == nil && !req.Force {
		return BadRequest(fmt.Errorf("Entry for container \"%s\" already exists in the database. Set \"force\" to overwrite.", req.Name))
	}

	// Detect discrepancy between snapshots recorded in "backup.yaml" and
	// those actually existing on disk.
	snapshotNames := []string{}
	snapshotsPath := getSnapshotMountPoint(containerPoolName, req.Name)
	snapshotsDir, err := os.Open(snapshotsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return InternalError(err)
		}
	} else {
		// Get a list of all snapshots that exist on disk.
		snapshotNames, err = snapshotsDir.Readdirnames(-1)
		if err != nil {
			snapshotsDir.Close()
			return InternalError(err)
		}
		snapshotsDir.Close()
	}

	onDiskSnapshots := map[string]*api.ContainerSnapshot{}
	for _, snapName := range snapshotNames {
		fullSnapName := fmt.Sprintf("%s/%s", req.Name, snapName)
		snapshotMntPoint := getSnapshotMountPoint(containerPoolName, fullSnapName)
		if shared.PathExists(snapshotMntPoint) {
			onDiskSnapshots[fullSnapName] = nil
		}
	}

	// Pre-check snapshots to ensure that nothings gets messed up because
	// "force" is missing.
	for _, snap := range backup.Snapshots {
		// Kick out any snapshots that do not exist on-disk anymore.
		_, ok := onDiskSnapshots[snap.Name]
		if !ok {
			logger.Warnf("The snapshot \"%s\" for container \"%s\" does not exist on disk anymore. Skipping...", snap.Name, req.Name)
			continue
		}

		onDiskSnapshots[snap.Name] = snap

		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := dbContainerId(d.db, snap.Name)
		if snapErr != nil {
			if snapErr != sql.ErrNoRows {
				return SmartError(snapErr)
			}
		}

		// If a db entry exists only proceed if force was specified.
		if snapErr == nil && !req.Force {
			return BadRequest(fmt.Errorf("Entry for snapshot \"%s\" already exists in the database. Set \"force\" to overwrite.", snap.Name))
		}

		// Check if a storage volume entry for the snapshot already exists.
		_, _, snapVolErr := dbStoragePoolVolumeGetType(d.db, snap.Name, storagePoolVolumeTypeContainer, poolID)
		if snapVolErr != nil {
			if snapVolErr != NoSuchObjectError {
				return SmartError(snapVolErr)
			}
		}

		// If a storage volume entry exists only proceed if force was specified.
		if snapVolErr == nil && !req.Force {
			return BadRequest(fmt.Errorf("Storage volume for snapshot \"%s\" already exists in the database. Set \"force\" to overwrite.", snap.Name))
		}
	}

	if backup.Volume == nil {
		return BadRequest(fmt.Errorf("No storage volume struct in the backup file found. The storage volume needs to be recovered manually."))
	}

	if ctVolErr == nil {
		if volume.Name != backup.Volume.Name {
			return BadRequest(fmt.Errorf("The name \"%s\" of the storage volume is not identical to the container's name \"%s\".", volume.Name, req.Name))
		}

		if volume.Type != backup.Volume.Type {
			return BadRequest(fmt.Errorf("The type \"%s\" of the storage volume is not identical to the container's type \"%s\".", volume.Type, backup.Volume.Type))
		}

		// Remove the storage volume db entry for the container since
		// force was specified.
		err := dbStoragePoolVolumeDelete(d.db, req.Name, storagePoolVolumeTypeContainer, poolID)
		if err != nil {
			return SmartError(err)
		}
	}

	if containerErr == nil {
		// Remove the storage volume db entry for the container since
		// force was specified.
		err := dbContainerRemove(d.db, req.Name)
		if err != nil {
			return SmartError(err)
		}
	}

	baseImage := backup.Container.Config["volatile.base_image"]
	for k := range backup.Container.Config {
		if strings.HasPrefix(k, "volatile") {
			delete(backup.Container.Config, k)
		}
	}

	arch, err := osarch.ArchitectureId(backup.Container.Architecture)
	if err != nil {
		return SmartError(err)
	}
	_, err = containerCreateInternal(d, containerArgs{
		Architecture: arch,
		BaseImage:    baseImage,
		Config:       backup.Container.Config,
		CreationDate: backup.Container.CreatedAt,
		LastUsedDate: backup.Container.LastUsedAt,
		Ctype:        cTypeRegular,
		Devices:      backup.Container.Devices,
		Ephemeral:    backup.Container.Ephemeral,
		Name:         backup.Container.Name,
		Profiles:     backup.Container.Profiles,
		Stateful:     backup.Container.Stateful,
	})
	if err != nil {
		return SmartError(err)
	}

	for snapName, snap := range onDiskSnapshots {
		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := dbContainerId(d.db, snapName)
		if snapErr != nil {
			if snapErr != sql.ErrNoRows {
				return SmartError(snapErr)
			}
		}

		// If a db entry exists only proceed if force was specified.
		if snapErr == nil && !req.Force {
			return BadRequest(fmt.Errorf("Entry for snapshot \"%s\" already exists in the database. Set \"force\" to overwrite.", snapName))
		}

		// Check if a storage volume entry for the snapshot already exists.
		_, _, csVolErr := dbStoragePoolVolumeGetType(d.db, snapName, storagePoolVolumeTypeContainer, poolID)
		if csVolErr != nil {
			if csVolErr != NoSuchObjectError {
				return SmartError(csVolErr)
			}
		}

		// If a storage volume entry exists only proceed if force was specified.
		if csVolErr == nil && !req.Force {
			return BadRequest(fmt.Errorf("Storage volume for snapshot \"%s\" already exists in the database. Set \"force\" to overwrite.", snapName))
		}

		if snapErr == nil {
			err := dbContainerRemove(d.db, snapName)
			if err != nil {
				return SmartError(err)
			}
		}

		if csVolErr == nil {
			err := dbStoragePoolVolumeDelete(d.db, snapName, storagePoolVolumeTypeContainer, poolID)
			if err != nil {
				return SmartError(err)
			}
		}

		// Snapshot exists on disk but does not have an entry in the
		// "backup.yaml" file. Recreate it by copying the parent
		// container's settings.
		if snap == nil {
			logger.Warnf("The snapshot \"%s\" for the container \"%s\" exists on disk but not in the backup file. Restoring with parent container's settings.", snapName, req.Name)
			snap = &api.ContainerSnapshot{}
			snap.Config = backup.Container.Config
			snap.CreationDate = backup.Container.CreatedAt
			snap.LastUsedDate = backup.Container.LastUsedAt
			snap.Devices = backup.Container.Devices
			snap.Ephemeral = backup.Container.Ephemeral
			snap.Profiles = backup.Container.Profiles
			snap.Stateful = backup.Container.Stateful
			snap.Architecture = backup.Container.Architecture
		}

		baseImage := snap.Config["volatile.base_image"]
		for k := range snap.Config {
			if strings.HasPrefix(k, "volatile") {
				delete(snap.Config, k)
			}
		}

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return SmartError(err)
		}

		_, err = containerCreateInternal(d, containerArgs{
			Architecture: arch,
			BaseImage:    baseImage,
			Config:       snap.Config,
			CreationDate: snap.CreationDate,
			LastUsedDate: snap.LastUsedDate,
			Ctype:        cTypeSnapshot,
			Devices:      snap.Devices,
			Ephemeral:    snap.Ephemeral,
			Name:         snapName,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		})
		if err != nil {
			return SmartError(err)
		}
	}

	return EmptySyncResponse
}

var internalContainersCmd = Command{name: "containers", post: internalImport}
