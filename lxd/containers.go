package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"
)

type containerType int

const (
	cTypeRegular  containerType = 0
	cTypeSnapshot containerType = 1
)

func containersGet(d *Daemon, r *http.Request) Response {
	slept := time.Millisecond * 0
	for {
		result, err := doContainersGet(d)
		if err == nil {
			return SyncResponse(true, result)
		}
		if !shared.IsDbLockedError(err) {
			shared.Debugf("DBERR: containersGet: error %q\n", err)
			return InternalError(err)
		}
		if slept == 30*time.Second {
			shared.Debugf("DBERR: containersGet: DB Locked for 30 seconds\n")
			return InternalError(err)
		}
		time.Sleep(100 * time.Millisecond)
		slept = slept + 100*time.Millisecond
	}
}

func doContainersGet(d *Daemon) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=?")
	rows, err := shared.DbQuery(d.db, q, cTypeRegular)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	result := []string{}
	for rows.Next() {
		container := ""
		err = rows.Scan(&container)
		if err != nil {
			return []string{}, err
		}

		result = append(result, fmt.Sprintf("/%s/containers/%s", shared.APIVersion, container))
	}
	err = rows.Err()
	if err != nil {
		return []string{}, err
	}

	return result, nil
}

type containerImageSource struct {
	Type string `json:"type"`

	/* for "image" type */
	Alias       string `json:"alias"`
	Fingerprint string `json:"fingerprint"`
	Server      string `json:"server"`

	/* for "migration" type */
	Mode       string            `json:"mode"`
	Operation  string            `json:"operation"`
	Websockets map[string]string `json:"secrets"`

	/* for "copy" type */
	Source string `json:"source"`
}

type containerPostReq struct {
	Name     string               `json:"name"`
	Source   containerImageSource `json:"source"`
	Config   map[string]string    `json:"config"`
	Profiles []string             `json:"profiles"`
}

func createFromImage(d *Daemon, req *containerPostReq) Response {
	var uuid string
	var err error
	if req.Source.Alias != "" {
		if req.Source.Mode == "pull" && req.Source.Server != "" {
			uuid, err = remoteGetImageFingerprint(d, req.Source.Server, req.Source.Alias)
			if err != nil {
				return InternalError(err)
			}
		} else {
			_, iId, err := dbAliasGet(d, req.Source.Alias)
			if err != nil {
				return InternalError(err)
			}
			uuid, err = dbImageGetById(d, iId)
			if err != nil {
				return InternalError(fmt.Errorf("Stale alias"))
			}
		}
	} else if req.Source.Fingerprint != "" {
		uuid = req.Source.Fingerprint
	} else {
		return BadRequest(fmt.Errorf("must specify one of alias or fingerprint for init from image"))
	}

	if req.Source.Server != "" {
		err := ensureLocalImage(d, req.Source.Server, uuid)
		if err != nil {
			return InternalError(err)
		}
	}

	_, err = dbImageGet(d, uuid, false)
	if err != nil {
		return SmartError(err)
	}

	dpath := shared.VarPath("lxc", req.Name)
	if shared.PathExists(dpath) {
		return InternalError(fmt.Errorf("Container exists"))
	}

	rootfsPath := fmt.Sprintf("%s/rootfs", dpath)
	err = os.MkdirAll(rootfsPath, 0700)
	if err != nil {
		return InternalError(fmt.Errorf("Error creating rootfs directory"))
	}

	name := req.Name
	_, err = dbCreateContainer(d, name, cTypeRegular, req.Config, req.Profiles)
	if err != nil {
		removeContainerPath(d, name)
		return SmartError(err)
	}

	/*
	 * extract the rootfs asynchronously
	 */
	run := shared.OperationWrap(func() error { return extractShiftRootfs(uuid, name, d) })
	return &asyncResponse{run: run, containers: []string{req.Name}}
}

func createFromNone(d *Daemon, req *containerPostReq) Response {

	_, err := dbCreateContainer(d, req.Name, cTypeRegular, req.Config, req.Profiles)
	if err != nil {
		return SmartError(err)
	}

	/* The container already exists, so don't do anything. */
	run := shared.OperationWrap(func() error { return nil })
	return &asyncResponse{run: run, containers: []string{req.Name}}
}

func createFromMigration(d *Daemon, req *containerPostReq) Response {

	if req.Source.Mode != "pull" {
		return NotImplemented
	}

	_, err := dbCreateContainer(d, req.Name, cTypeRegular, req.Config, req.Profiles)
	if err != nil {
		return SmartError(err)
	}

	c, err := newLxdContainer(req.Name, d)
	if err != nil {
		removeContainer(d, req.Name)
		return SmartError(err)
	}

	// rsync complaisn if the parent directory for the rootfs sync doesn't
	// exist
	dpath := shared.VarPath("lxc", req.Name)
	if err := os.MkdirAll(dpath, 0700); err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	config, err := shared.GetTLSConfig(d.certf, d.keyf)
	if err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	args := migration.MigrationSinkArgs{
		Url:       req.Source.Operation,
		Dialer:    websocket.Dialer{TLSClientConfig: config},
		Container: c.c,
		Secrets:   req.Source.Websockets,
		IdMap:     d.idMap,
	}

	sink, err := migration.NewMigrationSink(&args)
	if err != nil {
		removeContainer(d, req.Name)
		return BadRequest(err)
	}

	run := func() shared.OperationResult {
		err := sink()
		if err != nil {
			removeContainer(d, req.Name)
		}
		return shared.OperationError(err)
	}

	return &asyncResponse{run: run, containers: []string{req.Name}}
}

func createFromCopy(d *Daemon, req *containerPostReq) Response {
	if req.Source.Source == "" {
		return BadRequest(fmt.Errorf("must specify a source container"))
	}

	// Make sure the source exists.
	_, err := newLxdContainer(req.Source.Source, d)
	if err != nil {
		return SmartError(err)
	}

	_, err = dbCreateContainer(d, req.Name, cTypeRegular, req.Config, req.Profiles)
	if err != nil {
		return SmartError(err)
	}

	dpath := shared.VarPath("lxc", req.Name)
	if err := os.MkdirAll(dpath, 0700); err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	oldPath := migration.AddSlash(shared.VarPath("lxc", req.Source.Source, "rootfs"))
	newPath := fmt.Sprintf("%s/%s", dpath, "rootfs")
	run := func() shared.OperationResult {
		err := exec.Command("rsync", "-a", "--devices", oldPath, newPath).Run()
		return shared.OperationError(err)
	}

	return &asyncResponse{run: run, containers: []string{req.Name, req.Source.Source}}
}

func containersPost(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to create")

	if d.idMap == nil {
		return BadRequest(fmt.Errorf("shared's user has no subuids"))
	}

	req := containerPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		req.Name = strings.ToLower(petname.Generate(2, "-"))
		shared.Debugf("no name provided, creating %s", req.Name)
	}

	switch req.Source.Type {
	case "image":
		return createFromImage(d, &req)
	case "none":
		return createFromNone(d, &req)
	case "migration":
		return createFromMigration(d, &req)
	case "copy":
		return createFromCopy(d, &req)
	default:
		return BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}

}

func removeContainerPath(d *Daemon, name string) {
	cpath := shared.VarPath("lxc", name)
	err := os.RemoveAll(cpath)
	if err != nil {
		shared.Debugf("Error cleaning up %s: %s\n", cpath, err)
	}
}

func removeContainer(d *Daemon, name string) {
	removeContainerPath(d, name)
	dbRemoveContainer(d, name)
}

func extractShiftRootfs(uuid string, name string, d *Daemon) error {
	/*
	 * We want to use archive/tar for this, but that doesn't appear
	 * to be working for us (see lxd/images.go)
	 * So for now, we extract the rootfs.tar.xz from the image
	 * tarball to /var/lib/lxd/lxc/container/rootfs.tar.xz, then
	 * extract that under /var/lib/lxd/lxc/container/rootfs/
	 */
	dpath := shared.VarPath("lxc", name)
	imagefile := shared.VarPath("images", uuid)

	compression, _, err := detectCompression(imagefile)
	if err != nil {
		shared.Logf("Unkown compression type: %s", err)
		removeContainer(d, name)
		return err
	}

	args := []string{"-C", dpath, "--numeric-owner"}
	switch compression {
	case COMPRESSION_TAR:
		args = append(args, "-xf")
	case COMPRESSION_GZIP:
		args = append(args, "-zxf")
	case COMPRESSION_BZ2:
		args = append(args, "--jxf")
	case COMPRESSION_LZMA:
		args = append(args, "--lzma", "-xf")
	default:
		args = append(args, "-Jxf")
	}
	args = append(args, imagefile, "rootfs")

	output, err := exec.Command("tar", args...).Output()
	if err != nil {
		shared.Debugf("Untar of image: Output %s\nError %s\n", output, err)
		removeContainer(d, name)
		return err
	}

	rpath := shared.VarPath("lxc", name, "rootfs")
	err = d.idMap.ShiftRootfs(rpath)
	if err != nil {
		shared.Debugf("Shift of rootfs %s failed: %s\n", rpath, err)
		removeContainer(d, name)
		return err
	}

	/* Set an acl so the container root can descend the container dir */
	acl := fmt.Sprintf("%d:rx", d.idMap.Uidmin)
	_, err = exec.Command("setfacl", "-m", acl, dpath).Output()
	if err != nil {
		shared.Debugf("Error adding acl for container root: start will likely fail\n")
	}

	return nil
}

func dbRemoveContainer(d *Daemon, name string) {
	_, _ = shared.DbExec(d.db, "DELETE FROM containers WHERE name=?", name)
}

func dbGetContainerId(db *sql.DB, name string) (int, error) {
	q := "SELECT id FROM containers WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := shared.DbQueryRowScan(db, q, arg1, arg2)
	return id, err
}

func dbCreateContainer(d *Daemon, name string, ctype containerType, config map[string]string, profiles []string) (int, error) {
	id, err := dbGetContainerId(d.db, name)
	if err == nil {
		return 0, DbErrAlreadyDefined
	}

	if profiles == nil {
		profiles = []string{"default"}
	}

	tx, err := d.db.Begin()
	if err != nil {
		return 0, err
	}
	str := fmt.Sprintf("INSERT INTO containers (name, architecture, type) VALUES (?, 1, %d)",
		ctype)
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	result, err := stmt.Exec(name)
	if err != nil {
		tx.Rollback()
		return 0, err
	}

	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("Error inserting %s into database", name)
	}
	// TODO: is this really int64? we should fix it everywhere if so
	id = int(id64)
	if err := dbInsertContainerConfig(tx, id, config); err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := dbInsertProfiles(tx, id, profiles); err != nil {
		tx.Rollback()
		return 0, err
	}

	return id, shared.TxCommit(tx)
}

var containersCmd = Command{name: "containers", get: containersGet, post: containersPost}

func containerGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	//cId, err := dbGetContainerId(d.db, name)  will need cId to get info
	_, err := dbGetContainerId(d.db, name)
	if err != nil {
		return NotFound
	}
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, c.RenderState())
}

func containerDeleteSnapshots(d *Daemon, cname string) []string {
	prefix := fmt.Sprintf("%s/", cname)
	length := len(prefix)
	rows, err := shared.DbQuery(d.db, "SELECT name, id FROM containers WHERE type=? AND SUBSTR(name,1,?)=?",
		cTypeSnapshot, length, prefix)
	if err != nil {
		return nil
	}

	var results []string
	var ids []int

	for rows.Next() {
		var id int
		var sname string
		err = rows.Scan(&sname, &id)
		if err != nil {
			fmt.Printf("DBERR: containerDeleteSnapshots: scan returned error %q\n", err)
		}
		ids = append(ids, id)
		cdir := shared.VarPath("lxc", cname, "snapshots", sname)
		results = append(results, cdir)
	}
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: containerDeleteSnapshots: Err returned an error %q\n", err)
	}
	rows.Close()
	for _, id := range ids {
		_, _ = shared.DbExec(d.db, "DELETE FROM containers WHERE id=?", id)
	}
	return results
}

type containerConfigReq struct {
	Profiles []string          `json:"profiles"`
	Config   map[string]string `json:"config"`
	Devices  shared.Devices    `json:"devices"`
	Restore  string            `json:"restore"`
}

func containerSnapRestore(id int, snap string) Response {
	return NotImplemented
}

func dbClearContainerConfig(tx *sql.Tx, id int) error {
	_, err := tx.Exec("DELETE FROM containers_config WHERE container_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM containers_profiles WHERE container_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM containers_devices_config WHERE id IN
		(SELECT containers_devices_config.id
		 FROM containers_devices_config JOIN containers_devices
		 ON containers_devices_config.container_device_id=containers_devices.id
		 WHERE containers_devices.container_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM containers_devices WHERE container_id=?", id)
	return err
}

func dbInsertContainerConfig(tx *sql.Tx, id int, config map[string]string) error {
	str := "INSERT INTO containers_config (container_id, key, value) values (?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		if !ValidContainerConfigKey(k) {
			return fmt.Errorf("Bad key: %s\n", k)
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			shared.Debugf("Error adding configuration item %s = %s to container %d\n",
				k, v, id)
			return err
		}
	}

	return nil
}

func dbInsertProfiles(tx *sql.Tx, id int, profiles []string) error {
	apply_order := 1
	str := `INSERT INTO containers_profiles (container_id, profile_id, apply_order) VALUES
		(?, (SELECT id FROM profiles WHERE name=?), ?);`
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, p := range profiles {
		_, err = stmt.Exec(id, p, apply_order)
		if err != nil {
			shared.Debugf("Error adding profile %s to container: %s\n",
				p, err)
			return err
		}
		apply_order = apply_order + 1
	}

	return nil
}

// ExtractInterfaceFromConfigName returns "eth0" from "volatile.eth0.hwaddr",
// or an error if the key does not match this pattern.
func ExtractInterfaceFromConfigName(k string) (string, error) {

	re := regexp.MustCompile("volatile\\.([^.]*)\\.hwaddr")
	m := re.FindStringSubmatch(k)
	if m != nil && len(m) > 1 {
		return m[1], nil
	}

	return "", fmt.Errorf("%s did not match", k)
}

func ValidContainerConfigKey(k string) bool {
	switch k {
	case "limits.cpus":
		return true
	case "limits.memory":
		return true
	case "security.privileged":
		return true
	case "raw.apparmor":
		return true
	case "raw.lxc":
		return true
	}

	if _, err := ExtractInterfaceFromConfigName(k); err == nil {
		return true
	}

	return strings.HasPrefix(k, "user.")
}

func emptyProfile(l []string) bool {
	if len(l) == 0 {
		return true
	}
	if len(l) == 1 && l[0] == "" {
		return true
	}
	return false
}

/*
 * Update configuration, or, if 'restore:snapshot-name' is present, restore
 * the named snapshot
 */
func containerPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	shared.Debugf("containerPut: called with name %s\n", name)
	cId, err := dbGetContainerId(d.db, name)
	if err != nil {
		return NotFound
	}

	configRaw := containerConfigReq{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return BadRequest(err)
	}

	do := func() error {

		tx, err := d.db.Begin()
		if err != nil {
			return err
		}

		/* Update config or profiles */
		if err = dbClearContainerConfig(tx, cId); err != nil {
			shared.Debugf("Error clearing configuration for container %s\n", name)
			tx.Rollback()
			return err
		}

		if err = dbInsertContainerConfig(tx, cId, configRaw.Config); err != nil {
			shared.Debugf("Error inserting configuration for container %s\n", name)
			tx.Rollback()
			return err
		}

		/* handle profiles */
		if emptyProfile(configRaw.Profiles) {
			_, err := tx.Exec("DELETE from containers_profiles where container_id=?", cId)
			if err != nil {
				tx.Rollback()
				return err
			}
		} else {
			if err := dbInsertProfiles(tx, cId, configRaw.Profiles); err != nil {

				tx.Rollback()
				return err
			}
		}

		err = shared.AddDevices(tx, "container", cId, configRaw.Devices)
		if err != nil {
			tx.Rollback()
			return err
		}

		return shared.TxCommit(tx)
	}

	return AsyncResponse(shared.OperationWrap(do), nil)
}

type containerPostBody struct {
	Migration bool   `json:"migration"`
	Name      string `json:"name"`
}

func containerPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	body := containerPostBody{}
	if err := json.Unmarshal(buf, &body); err != nil {
		return BadRequest(err)
	}

	if body.Migration {
		ws, err := migration.NewMigrationSource(c.c)
		if err != nil {
			return InternalError(err)
		}

		return AsyncResponseWithWs(ws, nil)
	} else {
		if c.c.Running() {
			return BadRequest(fmt.Errorf("renaming of running container not allowed"))
		}

		_, err := dbCreateContainer(d, body.Name, cTypeRegular, c.config, c.profiles)
		if err != nil {
			return SmartError(err)
		}

		run := func() error {
			oldPath := fmt.Sprintf("%s/", shared.VarPath("lxc", c.name))
			newPath := fmt.Sprintf("%s/", shared.VarPath("lxc", body.Name))

			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}

			removeContainer(d, c.name)
			return nil
		}

		return AsyncResponse(shared.OperationWrap(run), nil)
	}
}

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	_, err := dbGetContainerId(d.db, name)
	if err != nil {
		return SmartError(err)
	}
	dirsToDelete := containerDeleteSnapshots(d, name)
	dbRemoveContainer(d, name)
	dirsToDelete = append(dirsToDelete, shared.VarPath("lxc", name))
	rmdir := func() error {
		for _, dir := range dirsToDelete {
			err := os.RemoveAll(dir)
			if err != nil {
				shared.Debugf("Error cleaning up %s: %s\n", dir, err)
			}
		}
		return nil
	}
	return AsyncResponse(shared.OperationWrap(rmdir), nil)
}

var containerCmd = Command{name: "containers/{name}", get: containerGet, put: containerPut, delete: containerDelete, post: containerPost}

func containerStateGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, c.RenderState().Status)
}

type containerStatePutReq struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
}

type lxdContainer struct {
	c        *lxc.Container
	id       int
	name     string
	config   map[string]string
	profiles []string
	devices  shared.Devices
}

func (c *lxdContainer) RenderState() *shared.ContainerState {
	return &shared.ContainerState{
		Name:     c.name,
		Profiles: c.profiles,
		Config:   c.config,
		Userdata: []byte{},
		Status:   shared.NewStatus(c.c, c.c.State()),
		Devices:  c.devices,
	}
}

func (c *lxdContainer) Start() error {
	return c.c.Start()
}

func (c *lxdContainer) Reboot() error {
	return c.c.Reboot()
}

func (c *lxdContainer) Freeze() error {
	return c.c.Freeze()
}

func (c *lxdContainer) isPrivileged() bool {
	switch strings.ToLower(c.config["security.privileged"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *lxdContainer) Shutdown(timeout time.Duration) error {
	return c.c.Shutdown(timeout)
}

func (c *lxdContainer) Stop() error {
	return c.c.Stop()
}

func (c *lxdContainer) Unfreeze() error {
	return c.c.Unfreeze()
}

func (d *lxdContainer) applyConfig(config map[string]string) error {
	var err error
	for k, v := range config {
		switch k {
		case "limits.cpus":
			// TODO - Come up with a way to choose cpus for multiple
			// containers
			var vint int
			count, err := fmt.Sscanf(v, "%d", &vint)
			if err != nil {
				return err
			}
			if count != 1 || vint < 0 || vint > 65000 {
				return fmt.Errorf("Bad cpu limit: %s\n", v)
			}
			cpuset := fmt.Sprintf("0-%d", vint-1)
			err = d.c.SetConfigItem("lxc.cgroup.cpuset.cpus", cpuset)
		case "limits.memory":
			err = d.c.SetConfigItem("lxc.cgroup.memory.limit_in_bytes", v)

		default:
			if strings.HasPrefix(k, "user.") {
				// ignore for now
				err = nil
			}

			/* Things like security.privileged need to be propagatged */
			d.config[k] = v
		}
		if err != nil {
			shared.Debugf("error setting %s: %q\n", k, err)
			return err
		}
	}

	if lxcConfig, ok := config["raw.lxc"]; ok {
		f, err := ioutil.TempFile("", "lxd_config_")
		if err != nil {
			return err
		}

		err = shared.WriteAll(f, []byte(lxcConfig))
		f.Close()
		defer os.Remove(f.Name())
		if err != nil {
			return err
		}

		if err := d.c.LoadConfigFile(f.Name()); err != nil {
			return err
		}
	}

	return nil
}

func applyProfile(daemon *Daemon, d *lxdContainer, p string) error {
	rows, err := shared.DbQuery(daemon.db, `SELECT key, value FROM profiles_config
		JOIN profiles ON profiles.id=profiles_config.profile_id
		WHERE profiles.name=?`, p)

	if err != nil {
		return err
	}
	defer rows.Close()

	config := map[string]string{}

	for rows.Next() {
		var k string
		var v string
		err = rows.Scan(&k, &v)
		if err != nil {
			fmt.Printf("DBERR: applyProfile: scan returned error %q\n", err)
			return err
		}
		shared.Debugf("applying %s: %s", k, v)
		config[k] = v
	}
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: applyProfile: Err returned an error %q\n", err)
		return err
	}

	newdevs, err := dbGetDevices(daemon, p, true)
	if err != nil {
		return err
	}
	for k, v := range newdevs {
		d.devices[k] = v
	}

	return d.applyConfig(config)
}

// GenerateMacAddr generates a mac address from a string template:
// e.g. "00:11:22:xx:xx:xx" -> "00:11:22:af:3e:51"
func GenerateMacAddr(template string) (string, error) {
	ret := bytes.Buffer{}

	for _, c := range template {
		if c == 'x' {
			c, err := rand.Int(rand.Reader, big.NewInt(16))
			if err != nil {
				return "", err
			}
			ret.WriteString(fmt.Sprintf("%x", c.Int64()))
		} else {
			ret.WriteString(string(c))
		}
	}

	return ret.String(), nil
}

func (c *lxdContainer) setupMacAddresses(d *Daemon) error {
	newConfigEntries := map[string]string{}

	for name, d := range c.devices {
		if d["type"] != "nic" {
			continue
		}

		found := false

		for key, val := range c.config {
			device, err := ExtractInterfaceFromConfigName(key)
			if err == nil && device == name {
				found = true
				d["hwaddr"] = val
			}
		}

		if !found {
			var hwaddr string
			var err error
			if d["hwaddr"] != "" {
				hwaddr, err = GenerateMacAddr(d["hwaddr"])
				if err != nil {
					return err
				}
			} else {
				hwaddr, err = GenerateMacAddr("00:16:3e:xx:xx:xx")
				if err != nil {
					return err
				}
			}

			if hwaddr != d["hwaddr"] {
				d["hwaddr"] = hwaddr
				key := fmt.Sprintf("volatile.%s.hwaddr", name)
				c.config[key] = hwaddr
				newConfigEntries[key] = hwaddr
			}
		}
	}

	if len(newConfigEntries) > 0 {

		tx, err := d.db.Begin()
		if err != nil {
			return err
		}

		if err := dbInsertContainerConfig(tx, c.id, newConfigEntries); err != nil {
			tx.Rollback()
			return err
		}

		return shared.TxCommit(tx)
	}

	return nil
}

func (c *lxdContainer) applyDevices() error {
	for name, d := range c.devices {
		if name == "type" {
			continue
		}

		configs, err := DeviceToLxc(d)
		if err != nil {
			return fmt.Errorf("Failed configuring device %s: %s\n", name, err)
		}
		for _, line := range configs {
			err := c.c.SetConfigItem(line[0], line[1])
			if err != nil {
				return fmt.Errorf("Failed configuring device %s: %s\n", name, err)
			}
		}
		shared.Debugf("Configured device %s\n", name)
	}
	return nil
}

func newLxdContainer(name string, daemon *Daemon) (*lxdContainer, error) {
	d := &lxdContainer{}

	arch := 0
	d.id = -1
	q := "SELECT id, architecture FROM containers WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&d.id, &arch}
	err := shared.DbQueryRowScan(daemon.db, q, arg1, arg2)
	if err != nil {
		return nil, err
	}
	if d.id == -1 {
		return nil, fmt.Errorf("Unknown container")
	}

	c, err := lxc.NewContainer(name, daemon.lxcpath)
	if err != nil {
		return nil, err
	}
	d.c = c

	dir := shared.LogPath(c.Name())
	err = os.MkdirAll(dir, 0700)
	if err != nil {
		return nil, err
	}

	if err = d.c.SetLogFile(filepath.Join(dir, "lxc.log")); err != nil {
		return nil, err
	}

	var txtarch string
	switch arch {
	case 0:
		txtarch = "x86_64"
	default:
		txtarch = "x86_64"
	}
	err = c.SetConfigItem("lxc.arch", txtarch)
	if err != nil {
		return nil, err
	}

	err = c.SetConfigItem("lxc.include", "/usr/share/lxc/config/ubuntu.common.conf")
	if err != nil {
		return nil, err
	}

	err = c.SetConfigItem("lxc.include", "/usr/share/lxc/config/ubuntu.userns.conf")
	if err != nil {
		return nil, err
	}

	config, err := dbGetConfig(daemon, d)
	if err != nil {
		return nil, err
	}
	d.config = config

	profiles, err := dbGetProfiles(daemon, d)
	if err != nil {
		return nil, err
	}
	d.profiles = profiles
	d.devices = shared.Devices{}
	d.name = name

	rootfsPath := shared.VarPath("lxc", name, "rootfs")
	err = c.SetConfigItem("lxc.rootfs", rootfsPath)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.loglevel", "0")
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.utsname", name)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.tty", "0")
	if err != nil {
		return nil, err
	}

	/* apply profiles */
	for _, p := range profiles {
		err := applyProfile(daemon, d, p)
		if err != nil {
			return nil, err
		}
	}

	/* get container_devices */
	newdevs, err := dbGetDevices(daemon, d.name, false)
	if err != nil {
		return nil, err
	}

	for k, v := range newdevs {
		d.devices[k] = v
	}

	if err := d.setupMacAddresses(daemon); err != nil {
		return nil, err
	}

	/* now add the lxc.* entries for the configured devices */
	err = d.applyDevices()
	if err != nil {
		return nil, err
	}

	if !d.isPrivileged() {
		uidstr := fmt.Sprintf("u 0 %d %d\n", daemon.idMap.Uidmin, daemon.idMap.Uidrange)
		err = c.SetConfigItem("lxc.id_map", uidstr)
		if err != nil {
			return nil, err
		}
		gidstr := fmt.Sprintf("g 0 %d %d\n", daemon.idMap.Gidmin, daemon.idMap.Gidrange)
		err = c.SetConfigItem("lxc.id_map", gidstr)
		if err != nil {
			return nil, err
		}
	}

	err = d.applyConfig(d.config)
	if err != nil {
		return nil, err
	}

	return d, nil
}

func containerStatePut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	raw := containerStatePutReq{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant
	// timeout).
	raw.Timeout = -1

	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	var do func() error
	switch shared.ContainerAction(raw.Action) {
	case shared.Start:
		do = c.Start
	case shared.Stop:
		if raw.Timeout == 0 || raw.Force {
			do = c.Stop
		} else {
			do = func() error { return c.Shutdown(time.Duration(raw.Timeout) * time.Second) }
		}
	case shared.Restart:
		do = c.Reboot
	case shared.Freeze:
		do = c.Freeze
	case shared.Unfreeze:
		do = c.Unfreeze
	default:
		return BadRequest(fmt.Errorf("unknown action %s", raw.Action))
	}

	return AsyncResponse(shared.OperationWrap(do), nil)
}

var containerStateCmd = Command{name: "containers/{name}/state", get: containerStateGet, put: containerStatePut}

func containerFileHandler(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	targetPath := r.FormValue("path")
	if targetPath == "" {
		return BadRequest(fmt.Errorf("missing path argument"))
	}

	var rootfs string
	if c.c.Running() {
		rootfs = fmt.Sprintf("/proc/%d/root", c.c.InitPid())
	} else {
		/*
		 * TODO: We should ask LXC about whether or not this rootfs is a block
		 * device, and if it is, whether or not it is actually mounted.
		 */
		rootfs = shared.VarPath("lxc", name, "rootfs")
	}

	/*
	 * Make sure someone didn't pass in ../../../etc/shadow or something.
	 */
	p := path.Clean(path.Join(rootfs, targetPath))
	if !strings.HasPrefix(p, path.Clean(rootfs)) {
		return BadRequest(fmt.Errorf("%s is not in the container's rootfs", p))
	}

	switch r.Method {
	case "GET":
		return containerFileGet(r, p)
	case "POST":
		return containerFilePut(r, p)
	default:
		return NotFound
	}
}

func containerFileGet(r *http.Request, path string) Response {
	fi, err := os.Stat(path)
	if err != nil {
		return SmartError(err)
	}

	/*
	 * Unfortunately, there's no portable way to do this:
	 * https://groups.google.com/forum/#!topic/golang-nuts/tGYjYyrwsGM
	 * https://groups.google.com/forum/#!topic/golang-nuts/ywS7xQYJkHY
	 */
	sb := fi.Sys().(*syscall.Stat_t)
	headers := map[string]string{
		"X-LXD-uid":  strconv.FormatUint(uint64(sb.Uid), 10),
		"X-LXD-gid":  strconv.FormatUint(uint64(sb.Gid), 10),
		"X-LXD-mode": fmt.Sprintf("%04o", fi.Mode()&os.ModePerm),
	}

	return FileResponse(r, path, filepath.Base(path), headers)
}

func containerFilePut(r *http.Request, p string) Response {

	uid, gid, mode, err := shared.ParseLXDFileHeaders(r.Header)
	if err != nil {
		return BadRequest(err)
	}

	fileinfo, err := os.Stat(path.Dir(p))
	if err != nil {
		return SmartError(err)
	}

	if !(fileinfo.IsDir()) {
		return SmartError(os.ErrNotExist)
	}

	f, err := os.Create(p)
	if err != nil {
		return SmartError(err)
	}
	defer f.Close()

	err = f.Chmod(mode)
	if err != nil {
		return SmartError(err)
	}

	err = f.Chown(uid, gid)
	if err != nil {
		return SmartError(err)
	}

	_, err = io.Copy(f, r.Body)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var containerFileCmd = Command{name: "containers/{name}/files", get: containerFileHandler, post: containerFileHandler}

func snapshotsDir(c *lxdContainer) string {
	return shared.VarPath("lxc", c.name, "snapshots")
}

func snapshotDir(c *lxdContainer, name string) string {
	return path.Join(snapshotsDir(c), name)
}

func snapshotStateDir(c *lxdContainer, name string) string {
	return path.Join(snapshotDir(c, name), "state")
}

func snapshotRootfsDir(c *lxdContainer, name string) string {
	return path.Join(snapshotDir(c, name), "rootfs")
}

func containerSnapshotsGet(d *Daemon, r *http.Request) Response {

	cname := mux.Vars(r)["name"]

	regexp := fmt.Sprintf("%s/", cname)
	length := len(regexp)
	rows, err := shared.DbQuery(d.db, "SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?",
		cTypeSnapshot, length, regexp)
	if err != nil {
		return SmartError(err)
	}
	defer rows.Close()

	var body []string

	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			fmt.Printf("DBERR: containerSnapshotsGet: scan returned error %q\n", err)
			return InternalError(err)
		}
		url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, cname, name)
		body = append(body, url)
	}
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: containerSnapshotsGet: Err returned an error %q\n", err)
		return InternalError(err)
	}

	return SyncResponse(true, body)
}

/*
 * Note, the code below doesn't deal with snapshots of snapshots.
 * To do that, we'll need to weed out based on # slashes in names
 */
func nextSnapshot(d *Daemon, name string) int {
	base := fmt.Sprintf("%s/snap", name)
	length := len(base)
	q := fmt.Sprintf("SELECT MAX(name) FROM containers WHERE type=? AND SUBSTR(name,1,?)=?")
	rows, err := shared.DbQuery(d.db, q, cTypeSnapshot, length, base)
	if err != nil {
		return 0
	}
	defer rows.Close()
	max := 0
	for rows.Next() {
		var tmp string
		var num int
		err = rows.Scan(&tmp)
		if err != nil {
			fmt.Printf("DBERR: nextSnapshot: scan returned error %q\n", err)
		}
		if len(tmp) <= length {
			continue
		}
		tmp2 := tmp[length:]
		count, err := fmt.Sscanf(tmp2, "%d", &num)
		if err != nil || count != 1 {
			continue
		}
		if num >= max {
			max = num + 1
		}
	}
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: nextSnapshot: Err returned an error %q\n", err)
	}
	return max
}

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	snapshotName, err := raw.GetString("name")
	if err != nil || snapshotName == "" {
		// come up with a name
		i := nextSnapshot(d, name)
		snapshotName = fmt.Sprintf("snap%d", i)
	}

	stateful, err := raw.GetBool("stateful")
	if err != nil {
		return BadRequest(err)
	}

	fullName := fmt.Sprintf("%s/%s", name, snapshotName)
	snapDir := snapshotDir(c, snapshotName)
	if shared.PathExists(snapDir) {
		return Conflict
	}

	err = os.MkdirAll(snapDir, 0700)
	if err != nil {
		return InternalError(err)
	}

	snapshot := func() error {

		StateDir := snapshotStateDir(c, snapshotName)
		err = os.MkdirAll(StateDir, 0700)
		if err != nil {
			return err
		}

		if stateful {
			// TODO - shouldn't we freeze for the duration of rootfs snapshot below?
			if !c.c.Running() {
				return fmt.Errorf("Container not running\n")
			}
			opts := lxc.CheckpointOptions{Directory: StateDir, Stop: true, Verbose: true}
			if err := c.c.Checkpoint(opts); err != nil {
				return err
			}
		}

		/* Create the db info */
		//cId, err := dbCreateContainer(d, snapshotName, cTypeSnapshot)
		_, err := dbCreateContainer(d, fullName, cTypeSnapshot, c.config, c.profiles)

		/* Create the directory and rootfs, set perms */
		/* Copy the rootfs */
		oldPath := fmt.Sprintf("%s/", shared.VarPath("lxc", name, "rootfs"))
		newPath := snapshotRootfsDir(c, snapshotName)
		err = exec.Command("rsync", "-a", "--devices", oldPath, newPath).Run()
		return err
	}

	return AsyncResponse(shared.OperationWrap(snapshot), nil)
}

var containerSnapshotsCmd = Command{name: "containers/{name}/snapshots", get: containerSnapshotsGet, post: containerSnapshotsPost}

func dbRemoveSnapshot(d *Daemon, cname string, sname string) {
	name := fmt.Sprintf("%s/%s", cname, sname)
	_, _ = shared.DbExec(d.db, "DELETE FROM containers WHERE type=? AND name=?", cTypeSnapshot, name)
}

func snapshotHandler(d *Daemon, r *http.Request) Response {
	containerName := mux.Vars(r)["name"]
	c, err := newLxdContainer(containerName, d)
	if err != nil {
		return SmartError(err)
	}

	snapshotName := mux.Vars(r)["snapshotName"]
	dir := snapshotDir(c, snapshotName)

	_, err = os.Stat(dir)
	if err != nil {
		return SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(c, snapshotName)
	case "POST":
		return snapshotPost(r, c, snapshotName)
	case "DELETE":
		return snapshotDelete(d, c, snapshotName)
	default:
		return NotFound
	}
}

func snapshotGet(c *lxdContainer, name string) Response {
	_, err := os.Stat(snapshotStateDir(c, name))
	body := shared.Jmap{"name": name, "stateful": err == nil}
	return SyncResponse(true, body)
}

func snapshotPost(r *http.Request, c *lxdContainer, oldName string) Response {
	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return BadRequest(err)
	}

	oldDir := snapshotDir(c, oldName)
	newDir := snapshotDir(c, newName)

	_, err = os.Stat(newDir)
	if !os.IsNotExist(err) {
		return InternalError(err)
	} else if err == nil {
		return Conflict
	}

	/*
	 * TODO: do we need to do something more intelligent here? We probably
	 * shouldn't do anything for stateful snapshots, since changing the fs
	 * out from under criu will cause it to fail, but it may be useful to
	 * do something for stateless ones.
	 */
	rename := func() error { return os.Rename(oldDir, newDir) }
	return AsyncResponse(shared.OperationWrap(rename), nil)
}

func snapshotDelete(d *Daemon, c *lxdContainer, name string) Response {
	dbRemoveSnapshot(d, c.name, name)
	dir := snapshotDir(c, name)
	remove := func() error { return os.RemoveAll(dir) }
	return AsyncResponse(shared.OperationWrap(remove), nil)
}

var containerSnapshotCmd = Command{name: "containers/{name}/snapshots/{snapshotName}", get: snapshotHandler, post: snapshotHandler, delete: snapshotHandler}

type execWs struct {
	command      []string
	container    *lxc.Container
	options      lxc.AttachOptions
	conns        []*websocket.Conn
	allConnected chan bool
	interactive  bool
	done         chan shared.OperationResult
	fds          map[int]string
}

func (s *execWs) Metadata() interface{} {
	fds := shared.Jmap{}
	for fd, secret := range s.fds {
		fds[string(fd)] = secret
	}

	return shared.Jmap{"fds": fds}
}

func (s *execWs) Connect(secret string, r *http.Request, w http.ResponseWriter) error {
	for fd, fdSecret := range s.fds {
		if secret == fdSecret {
			conn, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return err
			}

			s.conns[fd] = conn
			for _, c := range s.conns {
				if c == nil {
					return nil
				}
			}
			s.allConnected <- true
			return nil
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this operation actually exists */
	return os.ErrPermission
}

func runCommand(container *lxc.Container, command []string, options lxc.AttachOptions) shared.OperationResult {
	status, err := container.RunCommandStatus(command, options)
	if err != nil {
		shared.Debugf("Failed running command: %q", err.Error())
		return shared.OperationError(err)
	}

	metadata, err := json.Marshal(shared.Jmap{"return": status})
	if err != nil {
		return shared.OperationError(err)
	}

	return shared.OperationResult{Metadata: metadata, Error: nil}
}

func (s *execWs) Do() shared.OperationResult {
	<-s.allConnected

	var err error
	var ttys []*os.File
	var ptys []*os.File

	if s.interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)
		ptys[0], ttys[0], err = shared.OpenPty()
		s.options.StdinFd = ttys[0].Fd()
		s.options.StdoutFd = ttys[0].Fd()
		s.options.StderrFd = ttys[0].Fd()
	} else {
		ttys = make([]*os.File, 3)
		ptys = make([]*os.File, 3)
		for i := 0; i < len(ttys); i++ {
			ptys[i], ttys[i], err = shared.Pipe()
			if err != nil {
				return shared.OperationError(err)
			}
		}
		s.options.StdinFd = ptys[0].Fd()
		s.options.StdoutFd = ttys[1].Fd()
		s.options.StderrFd = ttys[2].Fd()
	}

	go func() {
		if s.interactive {
			shared.WebsocketMirror(s.conns[0], ptys[0], ptys[0])
		} else {
			for i := 0; i < len(ttys); i++ {
				go func(i int) {
					if i == 0 {
						<-shared.WebsocketRecvStream(ttys[i], s.conns[i])
						ttys[i].Close()
					} else {
						<-shared.WebsocketSendStream(s.conns[i], ptys[i])
						ptys[i].Close()
					}
				}(i)
			}
		}

		result := runCommand(
			s.container,
			s.command,
			s.options,
		)

		for _, tty := range ttys {
			tty.Close()
		}

		for _, pty := range ptys {
			pty.Close()
		}

		s.done <- result
	}()

	return <-s.done
}

type commandPostContent struct {
	Command     []string          `json:"command"`
	WaitForWS   bool              `json:"wait-for-websocket"`
	Interactive bool              `json:"interactive"`
	Environment map[string]string `json:"environment"`
}

func containerExecPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	if !c.c.Running() {
		return BadRequest(fmt.Errorf("Container is not running."))
	}

	post := commandPostContent{}
	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return BadRequest(err)
	}

	if err := json.Unmarshal(buf, &post); err != nil {
		return BadRequest(err)
	}

	opts := lxc.DefaultAttachOptions
	opts.ClearEnv = true
	opts.Env = []string{}

	if post.Environment != nil {
		for k, v := range post.Environment {
			if k == "HOME" {
				opts.Cwd = v
			}
			opts.Env = append(opts.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if post.WaitForWS {
		ws := &execWs{}
		ws.fds = map[int]string{}
		if post.Interactive {
			ws.conns = make([]*websocket.Conn, 1)
		} else {
			ws.conns = make([]*websocket.Conn, 3)
		}
		ws.allConnected = make(chan bool, 1)
		ws.interactive = post.Interactive
		ws.done = make(chan shared.OperationResult, 1)
		ws.options = opts
		for i := 0; i < len(ws.conns); i++ {
			ws.fds[i], err = shared.RandomCryptoString()
			if err != nil {
				return InternalError(err)
			}
		}

		ws.command = post.Command
		ws.container = c.c

		return AsyncResponseWithWs(ws, nil)
	}

	run := func() shared.OperationResult {

		nullDev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0666)
		if err != nil {
			return shared.OperationError(err)
		}
		defer nullDev.Close()
		nullfd := nullDev.Fd()

		opts.StdinFd = nullfd
		opts.StdoutFd = nullfd
		opts.StderrFd = nullfd

		return runCommand(c.c, post.Command, opts)
	}

	return AsyncResponse(run, nil)
}

var containerExecCmd = Command{name: "containers/{name}/exec", post: containerExecPost}
