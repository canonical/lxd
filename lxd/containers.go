package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
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
	if req.Source.Type != "remote" {
		return NotImplemented
	}

	if req.Source.URL != "https+lxc-images://images.linuxcontainers.org" {
		return NotImplemented
	}

	if req.Source.Name != "lxc-images/ubuntu/trusty/amd64" {
		return NotImplemented
	}

	opts := lxc.TemplateOptions{
		Template: "download",
		Distro:   "ubuntu",
		Release:  "trusty",
		Arch:     "amd64",
	}

	c, err := lxc.NewContainer(req.Name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	/*
	 * Set the id mapping. This may not be how we want to do it, but it's a
	 * start.  First, we remove any id_map lines in the config which might
	 * have come from ~/.config/lxc/default.conf.  Then add id mapping based
	 * on Domain.id_map
	 */
	if d.id_map != nil {
		shared.Debugf("setting custom idmap")
		err = c.SetConfigItem("lxc.id_map", "")
		if err != nil {
			shared.Debugf("Failed to clear id mapping, continuing")
		}
		uidstr := fmt.Sprintf("u 0 %d %d\n", d.id_map.Uidmin, d.id_map.Uidrange)
		shared.Debugf("uidstr is %s\n", uidstr)
		err = c.SetConfigItem("lxc.id_map", uidstr)
		if err != nil {
			return InternalError(err)
		}
		gidstr := fmt.Sprintf("g 0 %d %d\n", d.id_map.Gidmin, d.id_map.Gidrange)
		err = c.SetConfigItem("lxc.id_map", gidstr)
		if err != nil {
			return InternalError(err)
		}
	}

	/*
	 * Actually create the container
	 */
	run := shared.OperationWrap(func() error { return c.Create(opts) })
	return &asyncResponse{run: run, containers: []string{req.Name}}
}

var containersCmd = Command{name: "containers", post: containersPost}

func containerGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
	}

	return SyncResponse(true, shared.CtoD(c))
}

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
	}

	return AsyncResponse(shared.OperationWrap(c.Destroy), nil)
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

func containerStatePut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	raw := containerStatePutReq{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant
	// timeout).
	raw.Timeout = -1

	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if !c.Defined() {
		return NotFound
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

	if !c.Defined() {
		return NotFound
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
