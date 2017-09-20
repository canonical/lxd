package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/db"
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

	c, err := containerLoadById(d.State(), id)
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

	c, err := containerLoadById(d.State(), id)
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

	// Check whether the container exists on any of the storage pools.
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
		return BadRequest(fmt.Errorf(`The container "%s" seems to `+
			`exist on another storage pool`, req.Name))
	} else if len(containerMntPoints) != 1 {
		return BadRequest(fmt.Errorf(`The container "%s" does not `+
			`seem to exist on any storage pool`, req.Name))
	}

	// User needs to make sure that we can access the directory where
	// backup.yaml lives.
	containerMntPoint := containerMntPoints[0]
	isEmpty, err := shared.PathIsEmpty(containerMntPoint)
	if err != nil {
		return InternalError(err)
	}

	if isEmpty {
		return BadRequest(fmt.Errorf(`The container's directory "%s" `+
			`appears to be empty. Please ensure that the `+
			`container's storage volume is mounted`,
			containerMntPoint))
	}

	// Read in the backup.yaml file.
	backupYamlPath := shared.VarPath("storage-pools", containerPoolName,
		"containers", req.Name, "backup.yaml")
	backup, err := slurpBackupFile(backupYamlPath)
	if err != nil {
		return SmartError(err)
	}

	// Try to retrieve the storage pool the container supposedly lives on.
	var poolErr error
	poolID, pool, poolErr := db.StoragePoolGet(d.db, containerPoolName)
	if poolErr != nil {
		if poolErr != db.NoSuchObjectError {
			return SmartError(poolErr)
		}
	}

	if backup.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return BadRequest(fmt.Errorf(`No storage pool struct in the ` +
			`backup file found. The storage pool needs to be ` +
			`recovered manually`))
	}

	if poolErr == db.NoSuchObjectError {
		// Create the storage pool db entry if it doesn't exist.
		err := storagePoolDBCreate(d.State(), containerPoolName, "",
			backup.Pool.Driver, backup.Pool.Config)
		if err != nil {
			return SmartError(err)
		}

		poolID, err = db.StoragePoolGetID(d.db, containerPoolName)
		if err != nil {
			return SmartError(err)
		}
	} else {
		if backup.Pool.Name != containerPoolName {
			return BadRequest(fmt.Errorf(`The storage pool "%s" `+
				`the container was detected on does not match `+
				`the storage pool "%s" specified in the `+
				`backup file`, backup.Pool.Name, containerPoolName))
		}

		if backup.Pool.Driver != pool.Driver {
			return BadRequest(fmt.Errorf(`The storage pool's `+
				`"%s" driver "%s" conflicts with the driver `+
				`"%s" recorded in the container's backup file`,
				containerPoolName, pool.Driver, backup.Pool.Driver))
		}
	}

	initPool, err := storagePoolInit(d.State(), backup.Pool.Name)
	if err != nil {
		return InternalError(err)
	}

	ourMount, err := initPool.StoragePoolMount()
	if err != nil {
		return InternalError(err)
	}
	if ourMount {
		defer initPool.StoragePoolUmount()
	}

	existingSnapshots := []*api.ContainerSnapshot{}
	needForce := fmt.Errorf(`The snapshot does not exist on disk. Pass ` +
		`"force" to discard non-existing snapshots`)

	for _, snap := range backup.Snapshots {
		// retrieve on-disk pool name
		_, _, poolName := initPool.GetContainerPoolInfo()
		if err != nil {
			return InternalError(err)
		}

		switch backup.Pool.Driver {
		case "btrfs":
			snpMntPt := getSnapshotMountPoint(backup.Pool.Name, snap.Name)
			if !req.Force && (!shared.PathExists(snpMntPt) || !isBtrfsSubVolume(snpMntPt)) {
				return BadRequest(needForce)
			}
		case "dir":
			snpMntPt := getSnapshotMountPoint(backup.Pool.Name, snap.Name)
			if !req.Force && !shared.PathExists(snpMntPt) {
				return BadRequest(needForce)
			}
		case "lvm":
			ctLvmName := containerNameToLVName(snap.Name)
			ctLvName := getLVName(poolName,
				storagePoolVolumeAPIEndpointContainers,
				ctLvmName)
			exists, err := storageLVExists(ctLvName)
			if err != nil {
				return InternalError(err)
			}

			if !req.Force && !exists {
				return BadRequest(needForce)
			}
		case "rbd":
			clusterName := backup.Pool.Config["ceph.cluster_name"]
			userName := backup.Pool.Config["ceph.user.name"]
			ctName, csName, _ := containerGetParentAndSnapshotName(snap.Name)
			snapshotName := fmt.Sprintf("snapshot_%s", csName)

			exists := cephRBDSnapshotExists(clusterName, poolName,
				ctName, storagePoolVolumeTypeNameContainer,
				snapshotName, userName)
			if !req.Force && !exists {
				return BadRequest(needForce)
			}
		case "zfs":
			ctName, csName, _ := containerGetParentAndSnapshotName(snap.Name)
			snapshotName := fmt.Sprintf("snapshot-%s", csName)

			exists := zfsFilesystemEntityExists(poolName,
				fmt.Sprintf("containers/%s@%s", ctName,
					snapshotName))
			if !req.Force && !exists {
				return BadRequest(needForce)
			}
		}

		existingSnapshots = append(existingSnapshots, snap)
	}

	// Check if a storage volume entry for the container already exists.
	_, volume, ctVolErr := db.StoragePoolVolumeGetType(d.db, req.Name,
		storagePoolVolumeTypeContainer, poolID)
	if ctVolErr != nil {
		if ctVolErr != db.NoSuchObjectError {
			return SmartError(ctVolErr)
		}
	}
	// If a storage volume entry exists only proceed if force was specified.
	if ctVolErr == nil && !req.Force {
		return BadRequest(fmt.Errorf(`Storage volume for container `+
			`"%s" already exists in the database. Set "force" to `+
			`overwrite`, req.Name))
	}

	// Check if an entry for the container already exists in the db.
	_, containerErr := db.ContainerId(d.db, req.Name)
	if containerErr != nil {
		if containerErr != sql.ErrNoRows {
			return SmartError(containerErr)
		}
	}
	// If a db entry exists only proceed if force was specified.
	if containerErr == nil && !req.Force {
		return BadRequest(fmt.Errorf(`Entry for container "%s" `+
			`already exists in the database. Set "force" to `+
			`overwrite`, req.Name))
	}

	if backup.Volume == nil {
		return BadRequest(fmt.Errorf(`No storage volume struct in the ` +
			`backup file found. The storage volume needs to be ` +
			`recovered manually`))
	}

	if ctVolErr == nil {
		if volume.Name != backup.Volume.Name {
			return BadRequest(fmt.Errorf(`The name "%s" of the `+
				`storage volume is not identical to the `+
				`container's name "%s"`, volume.Name, req.Name))
		}

		if volume.Type != backup.Volume.Type {
			return BadRequest(fmt.Errorf(`The type "%s" of the `+
				`storage volume is not identical to the `+
				`container's type "%s"`, volume.Type,
				backup.Volume.Type))
		}

		// Remove the storage volume db entry for the container since
		// force was specified.
		err := db.StoragePoolVolumeDelete(d.db, req.Name,
			storagePoolVolumeTypeContainer, poolID)
		if err != nil {
			return SmartError(err)
		}
	}

	if containerErr == nil {
		// Remove the storage volume db entry for the container since
		// force was specified.
		err := db.ContainerRemove(d.db, req.Name)
		if err != nil {
			return SmartError(err)
		}
	}

	for _, snap := range existingSnapshots {
		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := db.ContainerId(d.db, snap.Name)
		if snapErr != nil {
			if snapErr != sql.ErrNoRows {
				return SmartError(snapErr)
			}
		}

		// If a db entry exists only proceed if force was specified.
		if snapErr == nil && !req.Force {
			return BadRequest(fmt.Errorf(`Entry for snapshot "%s" `+
				`already exists in the database. Set "force" `+
				`to overwrite`, snap.Name))
		}

		// Check if a storage volume entry for the snapshot already exists.
		_, _, csVolErr := db.StoragePoolVolumeGetType(d.db, snap.Name,
			storagePoolVolumeTypeContainer, poolID)
		if csVolErr != nil {
			if csVolErr != db.NoSuchObjectError {
				return SmartError(csVolErr)
			}
		}

		// If a storage volume entry exists only proceed if force was specified.
		if csVolErr == nil && !req.Force {
			return BadRequest(fmt.Errorf(`Storage volume for `+
				`snapshot "%s" already exists in the `+
				`database. Set "force" to overwrite`, snap.Name))
		}

		if snapErr == nil {
			err := db.ContainerRemove(d.db, snap.Name)
			if err != nil {
				return SmartError(err)
			}
		}

		if csVolErr == nil {
			err := db.StoragePoolVolumeDelete(d.db, snap.Name,
				storagePoolVolumeTypeContainer, poolID)
			if err != nil {
				return SmartError(err)
			}
		}

		baseImage := snap.Config["volatile.base_image"]

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return SmartError(err)
		}

		_, err = containerCreateInternal(d.State(), db.ContainerArgs{
			Architecture: arch,
			BaseImage:    baseImage,
			Config:       snap.Config,
			CreationDate: snap.CreationDate,
			LastUsedDate: snap.LastUsedDate,
			Ctype:        db.CTypeSnapshot,
			Devices:      snap.Devices,
			Ephemeral:    snap.Ephemeral,
			Name:         snap.Name,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		})
		if err != nil {
			return SmartError(err)
		}
	}

	baseImage := backup.Container.Config["volatile.base_image"]

	arch, err := osarch.ArchitectureId(backup.Container.Architecture)
	if err != nil {
		return SmartError(err)
	}
	_, err = containerCreateInternal(d.State(), db.ContainerArgs{
		Architecture: arch,
		BaseImage:    baseImage,
		Config:       backup.Container.Config,
		CreationDate: backup.Container.CreatedAt,
		LastUsedDate: backup.Container.LastUsedAt,
		Ctype:        db.CTypeRegular,
		Devices:      backup.Container.Devices,
		Ephemeral:    backup.Container.Ephemeral,
		Name:         backup.Container.Name,
		Profiles:     backup.Container.Profiles,
		Stateful:     backup.Container.Stateful,
	})
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var internalContainersCmd = Command{name: "containers", post: internalImport}
