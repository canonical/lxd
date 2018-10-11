package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/node"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"

	log "github.com/lxc/lxd/shared/log15"
)

var apiInternal = []Command{
	internalReadyCmd,
	internalShutdownCmd,
	internalContainerOnStartCmd,
	internalContainerOnStopCmd,
	internalContainersCmd,
	internalSQLCmd,
	internalClusterAcceptCmd,
	internalClusterRebalanceCmd,
	internalClusterPromoteCmd,
	internalClusterContainerMovedCmd,
}

var internalShutdownCmd = Command{
	name: "shutdown",
	put:  internalShutdown,
}

var internalReadyCmd = Command{
	name: "ready",
	get:  internalWaitReady,
}

var internalContainerOnStartCmd = Command{
	name: "containers/{id}/onstart",
	get:  internalContainerOnStart,
}

var internalContainerOnStopCmd = Command{
	name: "containers/{id}/onstop",
	get:  internalContainerOnStop,
}

var internalSQLCmd = Command{
	name: "sql",
	get:  internalSQLGet,
	post: internalSQLPost,
}

var internalContainersCmd = Command{
	name: "containers",
	post: internalImport,
}

func internalWaitReady(d *Daemon, r *http.Request) Response {
	select {
	case <-d.readyChan:
	default:
		return Unavailable(fmt.Errorf("LXD daemon not ready yet"))
	}

	return EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) Response {
	d.shutdownChan <- struct{}{}

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
		logger.Error("The start hook failed", log.Ctx{"container": c.Name(), "err": err})
		return SmartError(err)
	}

	return EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	target := queryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	c, err := containerLoadById(d.State(), id)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStop(target)
	if err != nil {
		logger.Error("The stop hook failed", log.Ctx{"container": c.Name(), "err": err})
		return SmartError(err)
	}

	return EmptySyncResponse
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
func internalSQLGet(d *Daemon, r *http.Request) Response {
	database := r.FormValue("database")

	if !shared.StringInSlice(database, []string{"local", "global"}) {
		return BadRequest(fmt.Errorf("Invalid database"))
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
		return SmartError(errors.Wrap(err, "failed to start transaction"))
	}
	defer tx.Rollback()
	dump, err := query.Dump(tx, schema, schemaOnly == 1)
	if err != nil {
		return SmartError(errors.Wrapf(err, "failed dump database %s", database))
	}
	return SyncResponse(true, internalSQLDump{Text: dump})
}

// Execute queries.
func internalSQLPost(d *Daemon, r *http.Request) Response {
	req := &internalSQLQuery{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	if !shared.StringInSlice(req.Database, []string{"local", "global"}) {
		return BadRequest(fmt.Errorf("Invalid database"))
	}

	if req.Query == "" {
		return BadRequest(fmt.Errorf("No query provided"))
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
		return SyncResponse(true, batch)
	}

	for _, query := range strings.Split(req.Query, ";") {
		query = strings.TrimLeft(query, " ")

		if query == "" {
			continue
		}

		result := internalSQLResult{}

		tx, err := db.Begin()
		if err != nil {
			return SmartError(err)
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
			return SmartError(err)
		}

		batch.Results = append(batch.Results, result)
	}

	return SyncResponse(true, batch)
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
	project := projectParam(r)

	req := &internalImportPost{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		return BadRequest(fmt.Errorf(`The name of the container ` +
			`is required`))
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
		containerMntPoint := getContainerMountPoint(project, poolName, req.Name)
		if shared.PathExists(containerMntPoint) {
			containerMntPoints = append(containerMntPoints, containerMntPoint)
			containerPoolName = poolName
		}
	}

	// Sanity checks.
	if len(containerMntPoints) > 1 {
		return BadRequest(fmt.Errorf(`The container "%s" seems to `+
			`exist on multiple storage pools`, req.Name))
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
	backupYamlPath := filepath.Join(containerMntPoint, "backup.yaml")
	backup, err := slurpBackupFile(backupYamlPath)
	if err != nil {
		return SmartError(err)
	}

	// Update snapshot names to include container name (if needed)
	for i, snap := range backup.Snapshots {
		if !strings.Contains(snap.Name, "/") {
			backup.Snapshots[i].Name = fmt.Sprintf("%s/%s", backup.Container.Name, snap.Name)
		}
	}

	// Try to retrieve the storage pool the container supposedly lives on.
	var poolErr error
	poolID, pool, poolErr := d.cluster.StoragePoolGet(containerPoolName)
	if poolErr != nil {
		if poolErr != db.ErrNoSuchObject {
			return SmartError(poolErr)
		}
	}

	if backup.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return BadRequest(fmt.Errorf(`No storage pool struct in the ` +
			`backup file found. The storage pool needs to be ` +
			`recovered manually`))
	}

	if poolErr == db.ErrNoSuchObject {
		// Create the storage pool db entry if it doesn't exist.
		err := storagePoolDBCreate(d.State(), containerPoolName, "",
			backup.Pool.Driver, backup.Pool.Config)
		if err != nil {
			err = errors.Wrap(err, "Create storage pool database entry")
			return SmartError(err)
		}

		poolID, err = d.cluster.StoragePoolGetID(containerPoolName)
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
		err = errors.Wrap(err, "Initialize storage")
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

	// retrieve on-disk pool name
	_, _, poolName := initPool.GetContainerPoolInfo()
	if err != nil {
		return InternalError(err)
	}

	// Retrieve all snapshots that exist on disk.
	onDiskSnapshots := []string{}
	if len(backup.Snapshots) > 0 {
		switch backup.Pool.Driver {
		case "btrfs":
			snapshotsDirPath := getSnapshotMountPoint(project, poolName, req.Name)
			snapshotsDir, err := os.Open(snapshotsDirPath)
			if err != nil {
				return InternalError(err)
			}
			onDiskSnapshots, err = snapshotsDir.Readdirnames(-1)
			if err != nil {
				snapshotsDir.Close()
				return InternalError(err)
			}
			snapshotsDir.Close()
		case "dir":
			snapshotsDirPath := getSnapshotMountPoint(project, poolName, req.Name)
			snapshotsDir, err := os.Open(snapshotsDirPath)
			if err != nil {
				return InternalError(err)
			}
			onDiskSnapshots, err = snapshotsDir.Readdirnames(-1)
			if err != nil {
				snapshotsDir.Close()
				return InternalError(err)
			}
			snapshotsDir.Close()
		case "lvm":
			onDiskPoolName := backup.Pool.Config["lvm.vg_name"]
			msg, err := shared.RunCommand("lvs", "-o", "lv_name",
				onDiskPoolName, "--noheadings")
			if err != nil {
				return InternalError(err)
			}

			snaps := strings.Fields(msg)
			prefix := fmt.Sprintf("containers_%s-", req.Name)
			for _, v := range snaps {
				// ignore zombies
				if strings.HasPrefix(v, prefix) {
					onDiskSnapshots = append(onDiskSnapshots,
						v[len(prefix):])
				}
			}
		case "ceph":
			clusterName := "ceph"
			if backup.Pool.Config["ceph.cluster_name"] != "" {
				clusterName = backup.Pool.Config["ceph.cluster_name"]
			}

			userName := "admin"
			if backup.Pool.Config["ceph.user.name"] != "" {
				userName = backup.Pool.Config["ceph.user.name"]
			}

			onDiskPoolName := backup.Pool.Config["ceph.osd.pool_name"]
			snaps, err := cephRBDVolumeListSnapshots(clusterName,
				onDiskPoolName, projectPrefix(project, req.Name),
				storagePoolVolumeTypeNameContainer, userName)
			if err != nil {
				if err != db.ErrNoSuchObject {
					return InternalError(err)
				}
			}

			for _, v := range snaps {
				// ignore zombies
				if strings.HasPrefix(v, "snapshot_") {
					onDiskSnapshots = append(onDiskSnapshots,
						v[len("snapshot_"):])
				}
			}
		case "zfs":
			onDiskPoolName := backup.Pool.Config["zfs.pool_name"]
			snaps, err := zfsPoolListSnapshots(onDiskPoolName,
				fmt.Sprintf("containers/%s", req.Name))
			if err != nil {
				return InternalError(err)
			}

			for _, v := range snaps {
				// ignore zombies
				if strings.HasPrefix(v, "snapshot-") {
					onDiskSnapshots = append(onDiskSnapshots,
						v[len("snapshot-"):])
				}
			}

		}
	}

	if len(backup.Snapshots) != len(onDiskSnapshots) {
		if !req.Force {
			msg := `There are either snapshots that don't exist ` +
				`on disk anymore or snapshots that are not ` +
				`recorded in the "backup.yaml" file. Pass ` +
				`"force" to remove them`
			logger.Errorf(msg)
			return InternalError(fmt.Errorf(msg))
		}
	}

	// delete snapshots that do not exist in backup.yaml
	od := ""
	for _, od = range onDiskSnapshots {
		inBackupFile := false
		for _, ib := range backup.Snapshots {
			_, snapOnlyName, _ := containerGetParentAndSnapshotName(ib.Name)
			if od == snapOnlyName {
				inBackupFile = true
				break
			}
		}

		if inBackupFile {
			continue
		}

		if !req.Force {
			msg := `There are snapshots that are not recorded in ` +
				`the "backup.yaml" file. Pass "force" to ` +
				`remove them`
			logger.Errorf(msg)
			return InternalError(fmt.Errorf(msg))
		}

		var err error
		switch backup.Pool.Driver {
		case "btrfs":
			snapName := fmt.Sprintf("%s/%s", req.Name, od)
			err = btrfsSnapshotDeleteInternal(project, poolName, snapName)
		case "dir":
			snapName := fmt.Sprintf("%s/%s", req.Name, od)
			err = dirSnapshotDeleteInternal(project, poolName, snapName)
		case "lvm":
			onDiskPoolName := backup.Pool.Config["lvm.vg_name"]
			if onDiskPoolName == "" {
				onDiskPoolName = poolName
			}
			snapName := fmt.Sprintf("%s/%s", req.Name, od)
			snapPath := containerPath(snapName, true)
			err = lvmContainerDeleteInternal(project, poolName, req.Name,
				true, onDiskPoolName, snapPath)
		case "ceph":
			clusterName := "ceph"
			if backup.Pool.Config["ceph.cluster_name"] != "" {
				clusterName = backup.Pool.Config["ceph.cluster_name"]
			}

			userName := "admin"
			if backup.Pool.Config["ceph.user.name"] != "" {
				userName = backup.Pool.Config["ceph.user.name"]
			}

			onDiskPoolName := backup.Pool.Config["ceph.osd.pool_name"]
			snapName := fmt.Sprintf("snapshot_%s", projectPrefix(project, od))
			ret := cephContainerSnapshotDelete(clusterName,
				onDiskPoolName, projectPrefix(project, req.Name),
				storagePoolVolumeTypeNameContainer, snapName, userName)
			if ret < 0 {
				err = fmt.Errorf(`Failed to delete snapshot`)
			}
		case "zfs":
			onDiskPoolName := backup.Pool.Config["zfs.pool_name"]
			snapName := fmt.Sprintf("%s/%s", req.Name, od)
			err = zfsSnapshotDeleteInternal(project, poolName, snapName,
				onDiskPoolName)
		}
		if err != nil {
			logger.Warnf(`Failed to delete snapshot`)
		}
	}

	for _, snap := range backup.Snapshots {
		switch backup.Pool.Driver {
		case "btrfs":
			snpMntPt := getSnapshotMountPoint(project, backup.Pool.Name, snap.Name)
			if !shared.PathExists(snpMntPt) || !isBtrfsSubVolume(snpMntPt) {
				if req.Force {
					continue
				}
				return BadRequest(needForce)
			}
		case "dir":
			snpMntPt := getSnapshotMountPoint(project, backup.Pool.Name, snap.Name)
			if !shared.PathExists(snpMntPt) {
				if req.Force {
					continue
				}
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

			if !exists {
				if req.Force {
					continue
				}
				return BadRequest(needForce)
			}
		case "ceph":
			clusterName := "ceph"
			if backup.Pool.Config["ceph.cluster_name"] != "" {
				clusterName = backup.Pool.Config["ceph.cluster_name"]
			}

			userName := "admin"
			if backup.Pool.Config["ceph.user.name"] != "" {
				userName = backup.Pool.Config["ceph.user.name"]
			}

			onDiskPoolName := backup.Pool.Config["ceph.osd.pool_name"]
			ctName, csName, _ := containerGetParentAndSnapshotName(snap.Name)
			ctName = projectPrefix(project, ctName)
			csName = projectPrefix(project, csName)
			snapshotName := fmt.Sprintf("snapshot_%s", csName)

			exists := cephRBDSnapshotExists(clusterName,
				onDiskPoolName, ctName,
				storagePoolVolumeTypeNameContainer,
				snapshotName, userName)
			if !exists {
				if req.Force {
					continue
				}
				return BadRequest(needForce)
			}
		case "zfs":
			ctName, csName, _ := containerGetParentAndSnapshotName(snap.Name)
			snapshotName := fmt.Sprintf("snapshot-%s", csName)

			exists := zfsFilesystemEntityExists(poolName,
				fmt.Sprintf("containers/%s@%s", ctName,
					snapshotName))
			if !exists {
				if req.Force {
					continue
				}
				return BadRequest(needForce)
			}
		}

		existingSnapshots = append(existingSnapshots, snap)
	}

	// Check if a storage volume entry for the container already exists.
	_, volume, ctVolErr := d.cluster.StoragePoolNodeVolumeGetType(
		req.Name, storagePoolVolumeTypeContainer, poolID)
	if ctVolErr != nil {
		if ctVolErr != db.ErrNoSuchObject {
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
	_, containerErr := d.cluster.ContainerID(req.Name)
	if containerErr != nil {
		if containerErr != db.ErrNoSuchObject {
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
		err := d.cluster.StoragePoolVolumeDelete("default", req.Name,
			storagePoolVolumeTypeContainer, poolID)
		if err != nil {
			return SmartError(err)
		}
	}

	if containerErr == nil {
		// Remove the storage volume db entry for the container since
		// force was specified.
		err := d.cluster.ContainerRemove(project, req.Name)
		if err != nil {
			return SmartError(err)
		}
	}

	// Prepare root disk entry if needed
	rootDev := map[string]string{}
	rootDev["type"] = "disk"
	rootDev["path"] = "/"
	rootDev["pool"] = containerPoolName

	// Mark the filesystem as going through an import
	fd, err := os.Create(filepath.Join(containerMntPoint, ".importing"))
	if err != nil {
		return InternalError(err)
	}
	fd.Close()
	defer os.Remove(fd.Name())

	for _, snap := range existingSnapshots {
		// Check if an entry for the snapshot already exists in the db.
		_, snapErr := d.cluster.ContainerID(snap.Name)
		if snapErr != nil {
			if snapErr != db.ErrNoSuchObject {
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
		_, _, csVolErr := d.cluster.StoragePoolNodeVolumeGetType(snap.Name,
			storagePoolVolumeTypeContainer, poolID)
		if csVolErr != nil {
			if csVolErr != db.ErrNoSuchObject {
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
			err := d.cluster.ContainerRemove(project, snap.Name)
			if err != nil {
				return SmartError(err)
			}
		}

		if csVolErr == nil {
			err := d.cluster.StoragePoolVolumeDelete("default", snap.Name,
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

		// Add root device if missing
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

		_, err = containerCreateInternal(d.State(), db.ContainerArgs{
			Project:      project,
			Architecture: arch,
			BaseImage:    baseImage,
			Config:       snap.Config,
			CreationDate: snap.CreationDate,
			Ctype:        db.CTypeSnapshot,
			Devices:      snap.Devices,
			Ephemeral:    snap.Ephemeral,
			LastUsedDate: snap.LastUsedDate,
			Name:         snap.Name,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		})
		if err != nil {
			return SmartError(err)
		}

		// Recreate missing mountpoints and symlinks.
		snapshotMountPoint := getSnapshotMountPoint(project, backup.Pool.Name,
			snap.Name)
		sourceName, _, _ := containerGetParentAndSnapshotName(snap.Name)
		sourceName = projectPrefix(project, sourceName)
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", backup.Pool.Name, "containers-snapshots", sourceName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", sourceName)
		err = createSnapshotMountpoint(snapshotMountPoint, snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return InternalError(err)
		}
	}

	baseImage := backup.Container.Config["volatile.base_image"]

	// Add root device if missing
	root, _, _ := shared.GetRootDiskDevice(backup.Container.Devices)
	if root == "" {
		if backup.Container.Devices == nil {
			backup.Container.Devices = map[string]map[string]string{}
		}

		rootDevName := "root"
		for i := 0; i < 100; i++ {
			if backup.Container.Devices[rootDevName] == nil {
				break
			}
			rootDevName = fmt.Sprintf("root%d", i)
			continue
		}

		backup.Container.Devices[rootDevName] = rootDev
	}

	arch, err := osarch.ArchitectureId(backup.Container.Architecture)
	if err != nil {
		return SmartError(err)
	}
	_, err = containerCreateInternal(d.State(), db.ContainerArgs{
		Project:      project,
		Architecture: arch,
		BaseImage:    baseImage,
		Config:       backup.Container.Config,
		CreationDate: backup.Container.CreatedAt,
		Ctype:        db.CTypeRegular,
		Description:  backup.Container.Description,
		Devices:      backup.Container.Devices,
		Ephemeral:    backup.Container.Ephemeral,
		LastUsedDate: backup.Container.LastUsedAt,
		Name:         backup.Container.Name,
		Profiles:     backup.Container.Profiles,
		Stateful:     backup.Container.Stateful,
	})
	if err != nil {
		err = errors.Wrap(err, "Create container")
		return SmartError(err)
	}

	containerPath := containerPath(projectPrefix(project, req.Name), false)
	isPrivileged := false
	if backup.Container.Config["security.privileged"] == "" {
		isPrivileged = true
	}
	err = createContainerMountpoint(containerMntPoint, containerPath,
		isPrivileged)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}
