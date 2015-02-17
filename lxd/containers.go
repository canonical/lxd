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

type containerImageSource struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type containerPostReq struct {
	Name   string               `json:"name"`
	Source containerImageSource `json:"source"`
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

	/* TODO: support other options here */
	if req.Source.Type != "local" {
		return NotImplemented
	}

	image := req.Source.Name
	_, uuid, err := dbGetImageId(image)
	if err != nil {
		return InternalError(err)
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
	_, err = dbCreateContainer(name)
	if err != nil {
		removeContainerPath(name)
		return InternalError(err)
	}

	/*
	 * extract the rootfs asynchronously
	 */
	run := shared.OperationWrap(func() error { return extractShiftRootfs(uuid, name, d) })
	return &asyncResponse{run: run, containers: []string{req.Name}}
}

func removeContainerPath(name string) {
	cpath := shared.VarPath("lxc", name)
	err := os.RemoveAll(cpath)
	if err != nil {
		shared.Debugf("Error cleaning up %s: %s\n", cpath, err)
	}
}

func extractShiftRootfs(uuid string, name string, d *Daemon) error {
	cleanup := func(err error) error {
		removeContainerPath(name)
		dbRemoveContainer(name)
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
	output, err := exec.Command("tar", "-C", dpath, "-Jxf", imagefile, "rootfs.tar.xz").Output()
	if err != nil {
		fmt.Printf("Untar of image: Output %s\nError %s\n", output, err)
		return cleanup(err)
	}

	rpath := shared.VarPath("lxc", name, "rootfs")
	tarfile := shared.VarPath("lxc", name, "rootfs.tar.xz")
	output, err = exec.Command("tar", "-C", rpath, "--numeric-owner", "-Jxpf", tarfile).Output()
	if err != nil {
		fmt.Printf("Untar of rootfs: Output %s\nError %s\n", output, err)
		return cleanup(err)
	}

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

func dbRemoveContainer(name string) {
	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return
	}
	defer db.Close()

	_, _ = db.Exec("DELETE FROM containers WHERE name=?", name)
}

func dbGetImageId(image string) (int, string, error) {
	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return 0, "", err
	}
	defer db.Close()

	/* todo - look at aliases */
	rows, err := db.Query("SELECT id, fingerprint FROM images WHERE fingerprint=?", image)
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

func dbCreateContainer(name string) (int, error) {
	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	id, err := dbGetContainerId(db, name)
	if err == nil {
		return 0, fmt.Errorf("%s already defined in database", name)
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare("INSERT INTO containers (name, architecture, type) VALUES (?, 1, 0)")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	_, err = stmt.Exec(name)
	if err != nil {
		return 0, err
	}
	tx.Commit()

	id, err = dbGetContainerId(db, name)
	if err != nil {
		return 0, fmt.Errorf("Error inserting %s into database", name)
	}

	return id, nil
}

var containersCmd = Command{name: "containers", post: containersPost}

func getContainerId(name string) (int, error) {
	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return dbGetContainerId(db, name)
}

func containerGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	//cId, err := getContainerId(name)  will need cId to get info
	_, err := getContainerId(name)
	if err != nil {
		return NotFound
	}
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		InternalError(err)
	}

	return SyncResponse(true, shared.CtoD(c))
}

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	fmt.Printf("delete: called with name %s\n", name)
	_, err := getContainerId(name)
	if err != nil {
		fmt.Printf("Delete: container %s not known", name)
		// rootfs may still exist though, so try to delete it
	}
	dbRemoveContainer(name)
	rmdir := func() error {
		removeContainerPath(name)
		return nil
	}
	fmt.Printf("running rmdir\n")
	return AsyncResponse(shared.OperationWrap(rmdir), nil)
}

var containerCmd = Command{name: "containers/{name}", get: containerGet, delete: containerDelete}

func containerStateGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
	}

	return SyncResponse(true, shared.CtoD(c).Status)
}

type containerStatePutReq struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
}

type lxdContainer struct {
	c  *lxc.Container
	id int
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
	return c.Unfreeze()
}

func newLxdContainer(name string, daemon *Daemon) (*lxdContainer, error) {
	d := &lxdContainer{}

	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, architecture FROM containers WHERE name=?", name)
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
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return NotFound
	}

	targetPath := r.FormValue("path")
	if targetPath == "" {
		return BadRequest(fmt.Errorf("missing path argument"))
	}

	var rootfs string
	if c.Running() {
		rootfs = fmt.Sprintf("/proc/%d/root", c.InitPid())
	} else {
		/*
		 * TODO: We should ask LXC about whether or not this rootfs is a block
		 * device, and if it is, whether or not it is actually mounted.
		 */
		rootfs = c.ConfigItem("lxc.rootfs")[0]
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
	case "PUT":
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

var containerFileCmd = Command{name: "containers/{name}/files", get: containerFileHandler, put: containerFileHandler}

func snapshotsDir(c *lxc.Container) string {
	return shared.VarPath("lxc", c.Name(), "snapshots")
}

func snapshotDir(c *lxc.Container, name string) string {
	return path.Join(snapshotsDir(c), name)
}

func snapshotStateDir(c *lxc.Container, name string) string {
	return path.Join(snapshotDir(c, name), "state")
}

func snapshotRootfsDir(c *lxc.Container, name string) string {
	return path.Join(snapshotDir(c, name), "rootfs")
}

func containerSnapshotsGet(d *Daemon, r *http.Request) Response {

	name := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
	}

	files, err := ioutil.ReadDir(snapshotsDir(c))
	if err != nil {
		if os.IsNotExist(err) {
			return SyncResponse(true, []shared.Jmap{})
		}
		return InternalError(err)
	}

	var body []string

	for _, file := range files {
		if file.IsDir() {
			url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, c.Name(), path.Base(file.Name()))
			body = append(body, url)
		}
	}

	return SyncResponse(true, body)
}

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
	}

	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	snapshotName, err := raw.GetString("name")
	if err != nil {
		return BadRequest(err)
	}

	stateful, err := raw.GetBool("stateful")
	if err != nil {
		return BadRequest(err)
	}

	err = os.MkdirAll(snapshotDir(c, snapshotName), 0700)
	if err != nil {
		return InternalError(err)
	}

	snapshot := func() error {

		if stateful {
			dir := snapshotStateDir(c, snapshotName)
			err := os.MkdirAll(dir, 0700)
			if err != nil {
				return err
			}

			opts := lxc.CheckpointOptions{Directory: dir, Stop: true, Verbose: true}
			if err := c.Checkpoint(opts); err != nil {
				return err
			}
		}

		/*
		 * TODO: Giving the Best backend here doesn't work, but that's
		 * what we want. So for now we use the default, which is just
		 * the directory backend.
		 */
		opts := lxc.CloneOptions{ConfigPath: snapshotsDir(c), KeepName: false, KeepMAC: true}
		return c.Clone(snapshotName, opts)
	}

	return AsyncResponse(shared.OperationWrap(snapshot), nil)
}

var containerSnapshotsCmd = Command{name: "containers/{name}/snapshots", get: containerSnapshotsGet, post: containerSnapshotsPost}

func snapshotHandler(d *Daemon, r *http.Request) Response {
	containerName := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(containerName, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
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
		return snapshotDelete(c, snapshotName)
	default:
		return NotFound
	}
}

func snapshotGet(c *lxc.Container, name string) Response {
	_, err := os.Stat(snapshotStateDir(c, name))
	body := shared.Jmap{"name": name, "stateful": err == nil}
	return SyncResponse(true, body)
}

func snapshotPost(r *http.Request, c *lxc.Container, oldName string) Response {
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

func snapshotDelete(c *lxc.Container, name string) Response {
	dir := snapshotDir(c, name)
	remove := func() error { return os.RemoveAll(dir) }
	return AsyncResponse(shared.OperationWrap(remove), nil)
}

var containerSnapshotCmd = Command{name: "containers/{name}/snapshots/{snapshotName}", get: snapshotHandler, post: snapshotHandler, delete: snapshotHandler}

type execWs struct {
	command   []string
	container *lxc.Container
	secret    string
	done      chan shared.OperationResult
}

func (s *execWs) Secret() string {
	return s.secret
}

func runCommand(container *lxc.Container, command []string, fd uintptr) shared.OperationResult {

	options := lxc.DefaultAttachOptions
	options.StdinFd = fd
	options.StdoutFd = fd
	options.StderrFd = fd
	options.ClearEnv = true

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

func (s *execWs) Do(conn *websocket.Conn) {
	pty, tty, err := pty.Open()
	if err != nil {
		s.done <- shared.OperationError(err)
		return
	}

	go func() {
		result := runCommand(s.container, s.command, tty.Fd())
		pty.Close()
		tty.Close()
		s.done <- result
	}()

	/*
	 * The pty will be passed to the container's Attach.  The two
	 * below goroutines will copy output from the socket to the
	 * pty.stdin, and from pty.std{out,err} to the socket
	 * If the RunCommand exits, we want ourselves (the gofunc) and
	 * the copy-goroutines to exit.  If the connection closes, we
	 * also want to exit
	 */
	shared.WebsocketMirror(conn, pty, pty)
}

type commandPostContent struct {
	Command   []string `json:"command"`
	WaitForWS bool     `json:"wait-for-websocket"`
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

	if post.WaitForWS {
		ws := &execWs{}
		ws.secret, err = shared.RandomCryptoString()
		if err != nil {
			return InternalError(err)
		}
		ws.command = post.Command
		ws.container = c
		ws.done = make(chan (shared.OperationResult))

		run := func() shared.OperationResult {
			return <-ws.done
		}

		return AsyncResponseWithWs(run, nil, ws)
	}
	run := func() shared.OperationResult {

		nullDev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0666)
		if err != nil {
			return shared.OperationError(err)
		}
		defer nullDev.Close()

		return runCommand(c, post.Command, nullDev.Fd())
	}
	return AsyncResponse(run, nil)
}

var containerExecCmd = Command{name: "containers/{name}/exec", post: containerExecPost}
