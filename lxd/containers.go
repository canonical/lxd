package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/kr/pty"
	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/lxc/go-lxc.v2"
)

type containerType int

const (
	cTypeRegular  containerType = 0
	cTypeSnapshot containerType = 1
)

func containersGet(d *Daemon, r *http.Request) Response {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=%d", cTypeRegular)
	rows, err := d.db.Query(q)
	if err != nil {
		return InternalError(err)
	}

	result := []string{}
	defer rows.Close()
	for rows.Next() {
		container := ""
		if err := rows.Scan(&container); err != nil {
			return InternalError(err)
		}

		result = append(result, fmt.Sprintf("/%s/containers/%s", shared.APIVersion, container))
	}

	return SyncResponse(true, result)
}

type containerImageSource struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	Alias       string `json:"alias"`
	Fingerprint string `json:"fingerprint"`
}

type containerPostReq struct {
	Name   string               `json:"name"`
	Source containerImageSource `json:"source"`
	Config map[string]string    `json:"config"`
}

func createFromImage(d *Daemon, req *containerPostReq) Response {
	var uuid string
	if req.Source.Alias != "" {
		_, iId, err := dbAliasGet(d, req.Source.Alias)
		if err == nil {
			uuid, err = dbImageGetById(d, iId)
			if err != nil {
				return InternalError(fmt.Errorf("Stale alias"))
			}
		} else {
			return InternalError(err)
		}

	} else if req.Source.Fingerprint != "" {
		/*
		 * There must be a better way to do this given go's scoping,
		 * but I'm not sure what it is.
		 */
		_, uuidLocal, err := dbGetImageId(d, req.Source.Fingerprint)
		if err != nil {
			return InternalError(err)
		}
		uuid = uuidLocal
	} else {
		return BadRequest(fmt.Errorf("must specify one of alias or fingerprint for init from image"))
	}

	dpath := shared.VarPath("lxc", req.Name)
	if shared.PathExists(dpath) {
		return InternalError(fmt.Errorf("Container exists"))
	}

	rootfsPath := fmt.Sprintf("%s/rootfs", dpath)
	err := os.MkdirAll(rootfsPath, 0700)
	if err != nil {
		return InternalError(fmt.Errorf("Error creating rootfs directory"))
	}

	name := req.Name
	_, err = dbCreateContainer(d, name, cTypeRegular, req.Config)
	if err != nil {
		removeContainerPath(d, name)
		return InternalError(err)
	}

	/*
	 * extract the rootfs asynchronously
	 */
	run := shared.OperationWrap(func() error { return extractShiftRootfs(uuid, name, d) })
	return &asyncResponse{run: run, containers: []string{req.Name}}
}

func createFromNone(d *Daemon, req *containerPostReq) Response {

	_, err := dbCreateContainer(d, req.Name, cTypeRegular, req.Config)
	if err != nil {
		return InternalError(err)
	}

	/* The container already exists, so don't do anything. */
	run := shared.OperationWrap(func() error { return nil })
	return &asyncResponse{run: run, containers: []string{req.Name}}
}

func containersPost(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to create")

	if d.id_map == nil {
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
		return NotImplemented
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

func extractShiftRootfs(uuid string, name string, d *Daemon) error {
	cleanup := func(err error) error {
		removeContainerPath(d, name)
		dbRemoveContainer(d, name)
		return err
	}

	/*
	 * We want to use archive/tar for this, but that doesn't appear
	 * to be working for us (see lxd/images.go)
	 * So for now, we extract the rootfs.tar.xz from the image
	 * tarball to /var/lib/lxd/lxc/container/rootfs.tar.xz, then
	 * extract that under /var/lib/lxd/lxc/container/rootfs/
	 */
	dpath := shared.VarPath("lxc", name)
	fmt.Printf("uuid is %s\n", uuid)
	imagefile := shared.VarPath("images", uuid)
	output, err := exec.Command("tar", "-C", dpath, "--numeric-owner", "-Jxf", imagefile, "rootfs").Output()
	if err != nil {
		fmt.Printf("Untar of image: Output %s\nError %s\n", output, err)
		return cleanup(err)
	}

	rpath := shared.VarPath("lxc", name, "rootfs")
	err = d.id_map.ShiftRootfs(rpath)
	if err != nil {
		fmt.Printf("Shift of rootfs %s failed: %s\n", rpath, err)
		return cleanup(err)
	}

	/* Set an acl so the container root can descend the container dir */
	acl := fmt.Sprintf("%d:rx", d.id_map.Uidmin)
	_, err = exec.Command("setfacl", "-m", acl, dpath).Output()
	if err != nil {
		fmt.Printf("Error adding acl for container root: start will likely fail\n")
	}

	return nil
}

func dbRemoveContainer(d *Daemon, name string) {
	_, _ = d.db.Exec("DELETE FROM containers WHERE name=?", name)
}

func dbGetImageId(d *Daemon, image string) (int, string, error) {
	rows, err := d.db.Query("SELECT id, fingerprint FROM images WHERE fingerprint=?", image)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	id := -1
	var uuid string
	for rows.Next() {
		rows.Scan(&id, &uuid)
	}
	if id == -1 {
		return 0, "", fmt.Errorf("Unknown image")
	}
	return id, uuid, nil
}

func dbGetContainerId(db *sql.DB, name string) (int, error) {
	rows, err := db.Query("SELECT id FROM containers WHERE name=?", name)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	id := -1
	for rows.Next() {
		rows.Scan(&id)
		break
	}
	if id == -1 {
		return 0, fmt.Errorf("Error finding container %s", name)
	}
	return id, nil
}

func dbCreateContainer(d *Daemon, name string, ctype containerType, config map[string]string) (int, error) {
	id, err := dbGetContainerId(d.db, name)
	if err == nil {
		return 0, fmt.Errorf("%s already defined in database", name)
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

	str = fmt.Sprintf("INSERT INTO containers_config (container_id, key, value) VALUES(?, ?, ?)")
	insertConfig, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer insertConfig.Close()

	for key, value := range config {
		if key != "raw.lxc" {
			tx.Rollback()
			return 0, fmt.Errorf("only raw.lxc config is respected right now")
		}
		_, err := insertConfig.Exec(id, key, value)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return id, nil
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
		return InternalError(err)
	}

	return SyncResponse(true, c.RenderState())
}

func containerDeleteSnapshots(d *Daemon, cname string) []string {
	prefix := fmt.Sprintf("%s/", cname)
	length := len(prefix)
	rows, err := d.db.Query("SELECT name, id FROM containers WHERE type=? AND SUBSTR(name,1,?)=?",
		cTypeSnapshot, length, prefix)
	if err != nil {
		return nil
	}

	var results []string
	var ids []int

	for rows.Next() {
		var id int
		var sname string
		rows.Scan(&sname, &id)
		ids = append(ids, id)
		cdir := shared.VarPath("lxc", cname, "snapshots", sname)
		results = append(results, cdir)
	}
	rows.Close()
	for _, id := range ids {
		_, _ = d.db.Exec("DELETE FROM containers WHERE id=?", id)
	}
	return results
}

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	fmt.Printf("delete: called with name %s\n", name)
	_, err := dbGetContainerId(d.db, name)
	if err != nil {
		fmt.Printf("Delete: container %s not known", name)
		// rootfs may still exist though, so try to delete it
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
	fmt.Printf("running rmdir\n")
	return AsyncResponse(shared.OperationWrap(rmdir), nil)
}

var containerCmd = Command{name: "containers/{name}", get: containerGet, delete: containerDelete}

func containerStateGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, c.RenderState().Status)
}

type containerStatePutReq struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
}

type lxdContainer struct {
	c      *lxc.Container
	id     int
	name   string
	config map[string]string
}

func (c *lxdContainer) RenderState() *shared.ContainerState {
	return &shared.ContainerState{
		Name:     c.name,
		Profiles: []string{},
		Config:   c.config,
		Userdata: []byte{},
		Status:   shared.NewStatus(c.c.State()),
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

func (c *lxdContainer) Shutdown(timeout time.Duration) error {
	return c.c.Shutdown(timeout)
}

func (c *lxdContainer) Stop() error {
	return c.c.Stop()
}

func (c *lxdContainer) Unfreeze() error {
	return c.c.Unfreeze()
}

func newLxdContainer(name string, daemon *Daemon) (*lxdContainer, error) {
	d := &lxdContainer{}

	rows, err := daemon.db.Query("SELECT id, architecture FROM containers WHERE name=?", name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d.id = -1
	arch := 0
	for rows.Next() {
		var id int
		rows.Scan(&id, &arch)
		d.id = id
	}
	if d.id == -1 {
		return nil, fmt.Errorf("Unknown container")
	}

	c, err := lxc.NewContainer(name, daemon.lxcpath)
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

	/* todo - get and translate containers_config entries
	 * for now we hardcode some sane entries */
	rootfsPath := shared.VarPath("lxc", name, "rootfs")
	err = c.SetConfigItem("lxc.rootfs", rootfsPath)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.devttydir", "")
	if err != nil {
		return nil, err
	}
	uidstr := fmt.Sprintf("u 0 %d %d\n", daemon.id_map.Uidmin, daemon.id_map.Uidrange)
	err = c.SetConfigItem("lxc.id_map", uidstr)
	if err != nil {
		return nil, err
	}
	gidstr := fmt.Sprintf("g 0 %d %d\n", daemon.id_map.Gidmin, daemon.id_map.Gidrange)
	err = c.SetConfigItem("lxc.id_map", gidstr)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.loglevel", "0")
	if err != nil {
		return nil, err
	}
	logfile := shared.VarPath("lxc", name, "log")
	err = c.SetConfigItem("lxc.logfile", logfile)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.utsname", name)
	if err != nil {
		return nil, err
	}
	/* net config */
	err = c.SetConfigItem("lxc.network.type", "veth")
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.network.link", "lxcbr0")
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.network.flags", "up")
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.network.hwaddr", "00:16:3e:xx:xx:xx")
	if err != nil {
		return nil, err
	}

	config, err := dbGetConfig(daemon, d)
	if err != nil {
		return nil, err
	}
	d.config = config

	if lxcConfig, ok := config["raw.lxc"]; ok {
		for _, line := range strings.Split(lxcConfig, "\n") {
			contents := strings.SplitN(line, "=", 2)
			key := contents[0]
			value := ""
			if len(contents) > 1 {
				value = contents[1]
			}

			if err := c.SetConfigItem(key, value); err != nil {
				return nil, err
			}
		}
	}

	d.name = name
	d.c = c

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
		return BadRequest(err)
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
		return NotFound
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

type fileServe struct {
	req     *http.Request
	path    string
	fi      os.FileInfo
	content *os.File
}

func (r *fileServe) Render(w http.ResponseWriter) error {
	/*
	 * Unfortunately, there's no portable way to do this:
	 * https://groups.google.com/forum/#!topic/golang-nuts/tGYjYyrwsGM
	 * https://groups.google.com/forum/#!topic/golang-nuts/ywS7xQYJkHY
	 */
	sb := r.fi.Sys().(*syscall.Stat_t)
	w.Header().Set("X-LXD-uid", strconv.FormatUint(uint64(sb.Uid), 10))
	w.Header().Set("X-LXD-gid", strconv.FormatUint(uint64(sb.Gid), 10))
	w.Header().Set("X-LXD-mode", fmt.Sprintf("%04o", r.fi.Mode()&os.ModePerm))

	http.ServeContent(w, r.req, r.path, r.fi.ModTime(), r.content)
	r.content.Close()
	return nil
}

func containerFileGet(r *http.Request, path string) Response {
	f, err := os.Open(path)
	if err != nil {
		return SmartError(err)
	}

	fi, err := f.Stat()
	if err != nil {
		return InternalError(err)
	}

	return &fileServe{r, filepath.Base(path), fi, f}
}

func containerFilePut(r *http.Request, p string) Response {

	uid, gid, mode, err := shared.ParseLXDFileHeaders(r.Header)
	if err != nil {
		return BadRequest(err)
	}

	err = os.MkdirAll(path.Dir(p), mode)
	if err != nil {
		return SmartError(err)
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
	rows, err := d.db.Query("SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?",
		cTypeSnapshot, length, regexp)
	if err != nil {
		return InternalError(err)
	}
	defer rows.Close()

	var body []string

	for rows.Next() {
		var name string
		rows.Scan(&name)
		url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, cname, name)
		body = append(body, url)
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
	rows, err := d.db.Query(q, cTypeSnapshot, length, base)
	if err != nil {
		return 0
	}
	defer rows.Close()
	max := 0
	for rows.Next() {
		var tmp string
		var num int
		rows.Scan(&tmp)
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
		return InternalError(err)
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
	err = os.MkdirAll(snapDir, 0700)
	if err != nil {
		return InternalError(err)
	}

	snapshot := func() error {

		StateDir := snapshotStateDir(c, snapshotName)
		if shared.PathExists(StateDir) {
			return fmt.Errorf("Snapshot directory exists")
		}
		err = os.MkdirAll(StateDir, 0700)
		if err != nil {
			return fmt.Errorf("Error creating rootfs directory")
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
		_, err := dbCreateContainer(d, fullName, cTypeSnapshot, c.config)

		/* Create the directory and rootfs, set perms */
		/* Copy the rootfs */
		oldPath := fmt.Sprintf("%s/", shared.VarPath("lxc", name, "rootfs"))
		newPath := snapshotRootfsDir(c, snapshotName)
		fmt.Printf("Running rsync -a --devices %s %s\n", oldPath, newPath)
		err = exec.Command("rsync", "-a", "--devices", oldPath, newPath).Run()
		return err
	}

	return AsyncResponse(shared.OperationWrap(snapshot), nil)
}

var containerSnapshotsCmd = Command{name: "containers/{name}/snapshots", get: containerSnapshotsGet, post: containerSnapshotsPost}

func dbRemoveSnapshot(d *Daemon, cname string, sname string) {
	name := fmt.Sprintf("%s/%s", cname, sname)
	_, _ = d.db.Exec("DELETE FROM containers WHERE type=? AND name=?", cTypeSnapshot, name)
}

func snapshotHandler(d *Daemon, r *http.Request) Response {
	containerName := mux.Vars(r)["name"]
	c, err := newLxdContainer(containerName, d)
	if err != nil {
		return InternalError(err)
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

	ttys := make([]*os.File, 3)
	ptys := make([]*os.File, 3)
	var err error

	for i := 0; i < 3; i++ {
		ptys[i], ttys[i], err = pty.Open()
		if err != nil {
			return shared.OperationError(err)
		}
	}

	s.options.StdinFd = ttys[0].Fd()
	s.options.StdoutFd = ttys[1].Fd()
	s.options.StderrFd = ttys[2].Fd()

	go func() {
		for i := 0; i < 3; i++ {
			go func(i int) {
				if i == 0 {
					<-shared.WebsocketRecvStream(ptys[i], s.conns[i])
					ptys[i].Write([]byte{0x04})
				} else {
					shared.WebsocketSendStream(s.conns[i], ptys[i])
				}
			}(i)
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
	Environment map[string]string `json:"environment"`
}

func containerExecPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Running() {
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
		ws.conns = make([]*websocket.Conn, 3)
		ws.allConnected = make(chan bool, 1)
		ws.done = make(chan shared.OperationResult, 1)
		ws.options = opts
		for i := 0; i < 3; i++ {
			ws.fds[i], err = shared.RandomCryptoString()
			if err != nil {
				return InternalError(err)
			}
		}

		ws.command = post.Command
		ws.container = c

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

		return runCommand(c, post.Command, opts)
	}
	return AsyncResponse(run, nil)
}

var containerExecCmd = Command{name: "containers/{name}/exec", post: containerExecPost}
