package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

type lxdContainer struct {
	c            *lxc.Container
	daemon       *Daemon
	id           int
	name         string
	config       map[string]string
	profiles     []string
	devices      shared.Devices
	architecture int
	ephemeral    bool
	idmapset     *shared.IdmapSet
}

type execWs struct {
	command          []string
	container        *lxc.Container
	rootUid          int
	rootGid          int
	options          lxc.AttachOptions
	conns            map[int]*websocket.Conn
	allConnected     chan bool
	controlConnected chan bool
	interactive      bool
	done             chan shared.OperationResult
	fds              map[int]string
}

type commandPostContent struct {
	Command     []string          `json:"command"`
	WaitForWS   bool              `json:"wait-for-websocket"`
	Interactive bool              `json:"interactive"`
	Environment map[string]string `json:"environment"`
}

type containerConfigReq struct {
	Profiles []string          `json:"profiles"`
	Config   map[string]string `json:"config"`
	Devices  shared.Devices    `json:"devices"`
	Restore  string            `json:"restore"`
}

type containerStatePutReq struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
}

type containerPostBody struct {
	Migration bool   `json:"migration"`
	Name      string `json:"name"`
}

type containerPostReq struct {
	Name      string               `json:"name"`
	Source    containerImageSource `json:"source"`
	Config    map[string]string    `json:"config"`
	Profiles  []string             `json:"profiles"`
	Ephemeral bool                 `json:"ephemeral"`
}

type containerImageSource struct {
	Type string `json:"type"`

	/* for "image" type */
	Alias       string `json:"alias"`
	Fingerprint string `json:"fingerprint"`
	Server      string `json:"server"`
	Secret      string `json:"secret"`

	/*
	 * for "migration" and "copy" types, as an optimization users can
	 * provide an image hash to extract before the filesystem is rsync'd,
	 * potentially cutting down filesystem transfer time. LXD will not go
	 * and fetch this image, it will simply use it if it exists in the
	 * image store.
	 */
	BaseImage string `json:"base-image"`

	/* for "migration" type */
	Mode       string            `json:"mode"`
	Operation  string            `json:"operation"`
	Websockets map[string]string `json:"secrets"`

	/* for "copy" type */
	Source string `json:"source"`
}

var containersCmd = Command{
	name: "containers",
	get:  containersGet,
	post: containersPost,
}

var containerCmd = Command{
	name:   "containers/{name}",
	get:    containerGet,
	put:    containerPut,
	delete: containerDelete,
	post:   containerPost,
}

var containerStateCmd = Command{
	name: "containers/{name}/state",
	get:  containerStateGet,
	put:  containerStatePut,
}

var containerFileCmd = Command{
	name: "containers/{name}/files",
	get:  containerFileHandler,
	post: containerFileHandler,
}

var containerSnapshotsCmd = Command{
	name: "containers/{name}/snapshots",
	get:  containerSnapshotsGet,
	post: containerSnapshotsPost,
}

var containerSnapshotCmd = Command{
	name:   "containers/{name}/snapshots/{snapshotName}",
	get:    snapshotHandler,
	post:   snapshotHandler,
	delete: snapshotHandler,
}

var containerExecCmd = Command{
	name: "containers/{name}/exec",
	post: containerExecPost,
}

func containerWatchEphemeral(c *lxdContainer) {
	go func() {
		c.c.Wait(lxc.STOPPED, -1*time.Second)
		c.c.Wait(lxc.RUNNING, 1*time.Second)
		c.c.Wait(lxc.STOPPED, -1*time.Second)

		_, err := dbGetContainerId(c.daemon.db, c.name)
		if err != nil {
			return
		}

		removeContainer(c.daemon, c.name)
	}()
}

func containersWatch(d *Daemon) error {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=?")
	inargs := []interface{}{cTypeRegular}
	var name string
	outfmt := []interface{}{name}

	result, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return err
	}

	for _, r := range result {
		container, err := newLxdContainer(string(r[0].(string)), d)
		if err != nil {
			return err
		}

		if container.ephemeral == true && container.c.State() != lxc.STOPPED {
			containerWatchEphemeral(container)
		}
	}

	/*
	 * force collect the containers we created above; see comment in
	 * daemon.go:createCmd.
	 */
	runtime.GC()

	return nil
}

func containersRestart(d *Daemon) error {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? AND power_state=1")
	inargs := []interface{}{cTypeRegular}
	var name string
	outfmt := []interface{}{name}

	result, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return err
	}

	_, err = shared.DbExec(d.db, "UPDATE containers SET power_state=0")
	if err != nil {
		return err
	}

	for _, r := range result {
		container, err := newLxdContainer(string(r[0].(string)), d)
		if err != nil {
			return err
		}

		err = templateApply(container, "start")
		if err != nil {
			return err
		}

		container.c.Start()
	}

	return nil
}

func containersShutdown(d *Daemon) error {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=?")
	inargs := []interface{}{cTypeRegular}
	var name string
	outfmt := []interface{}{name}

	result, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	for _, r := range result {
		container, err := newLxdContainer(string(r[0].(string)), d)
		if err != nil {
			return err
		}

		_, err = shared.DbExec(d.db, "UPDATE containers SET power_state=1 WHERE name=?", container.name)
		if err != nil {
			return err
		}

		if container.c.State() != lxc.STOPPED {
			wg.Add(1)
			go func() {
				container.c.Shutdown(time.Second * 30)
				container.c.Stop()
				wg.Done()
			}()
		}
		wg.Wait()
	}

	return nil
}

func containerDeleteSnapshots(d *Daemon, cname string) error {
	prefix := fmt.Sprintf("%s/", cname)
	length := len(prefix)
	q := "SELECT name, id FROM containers WHERE type=? AND SUBSTR(name,1,?)=?"
	var id int
	var sname string
	inargs := []interface{}{cTypeSnapshot, length, prefix}
	outfmt := []interface{}{sname, id}
	results, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return err
	}

	var ids []int

	backing_fs, err := shared.GetFilesystem(shared.VarPath("lxc", cname))
	if err != nil && !os.IsNotExist(err) {
		shared.Debugf("Error cleaning up snapshots: %s\n", err)
		return err
	}

	for _, r := range results {
		sname = r[0].(string)
		id = r[1].(int)
		ids = append(ids, id)
		cdir := shared.VarPath("lxc", cname, "snapshots", sname)

		if backing_fs == "btrfs" {
			exec.Command("btrfs", "subvolume", "delete", cdir).Run()
		}
		os.RemoveAll(cdir)
	}

	for _, id := range ids {
		_, err = shared.DbExec(d.db, "DELETE FROM containers WHERE id=?", id)
		if err != nil {
			return err
		}
	}

	return nil
}

/*
 * This is called by lxd when called as "lxd forkstart <container>"
 * 'forkstart' is used instead of just 'start' in the hopes that people
 * do not accidentally type 'lxd start' instead of 'lxc start'
 *
 * We expect to read the lxcconfig over fd 3.
 */
func startContainer(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("Bad arguments: %q\n", args)
	}
	name := args[1]
	lxcpath := args[2]
	configPath := args[3]

	defer os.Remove(configPath)

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return fmt.Errorf("Error initializing container for start: %q", err)
	}
	err = c.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Error opening startup config file: %q", err)
	}

	return c.Start()
}

func (d *lxdContainer) tarStoreFile(linkmap map[uint64]string, offset int, tw *tar.Writer, path string, fi os.FileInfo) error {
	var err error
	var major, minor, nlink int
	var ino uint64

	link := ""
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(path)
		if err != nil {
			return err
		}
	}
	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	hdr.Name = path[offset:]
	if fi.IsDir() || fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		hdr.Size = 0
	} else {
		hdr.Size = fi.Size()
	}

	hdr.Uid, hdr.Gid, major, minor, ino, nlink, err = shared.GetFileStat(path)
	if err != nil {
		return fmt.Errorf("error getting file info: %s\n", err)
	}

	// unshift the id under /rootfs/ for unpriv containers
	if !d.isPrivileged() && strings.HasPrefix(hdr.Name, "/rootfs") {
		hdr.Uid, hdr.Gid = d.idmapset.ShiftFromNs(hdr.Uid, hdr.Gid)
		if hdr.Uid == -1 || hdr.Gid == -1 {
			return nil
		}
	}
	if major != -1 {
		hdr.Devmajor = int64(major)
		hdr.Devminor = int64(minor)
	}

	// If it's a hardlink we've already seen use the old name
	if fi.Mode().IsRegular() && nlink > 1 {
		if firstpath, found := linkmap[ino]; found {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = firstpath
			hdr.Size = 0
		} else {
			linkmap[ino] = hdr.Name
		}
	}

	// todo - handle xattrs

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("error writing header: %s\n", err)
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("tarStoreFile: error opening file: %s\n", err)
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("error copying file %s\n", err)
		}
	}
	return nil
}

/*
 * Export the container to a unshifted tarfile containing:
 * dir/
 *     metadata.yaml
 *     rootfs/
 */
func (d *lxdContainer) exportToTar(snap string, w io.Writer) error {
	if snap != "" && d.c.Running() {
		return fmt.Errorf("Cannot export a running container as image")
	}

	tw := tar.NewWriter(w)

	// keep track of the first path we saw for each path with nlink>1
	linkmap := map[uint64]string{}

	cDir := shared.VarPath("lxc", d.name)

	// Path inside the tar image is the pathname starting after cDir
	offset := len(cDir)

	fnam := filepath.Join(cDir, "metadata.yaml")
	writeToTar := func(path string, fi os.FileInfo, err error) error {
		if err := d.tarStoreFile(linkmap, offset, tw, path, fi); err != nil {
			shared.Debugf("error tarring up %s: %s\n", path, err)
			return err
		}
		return nil
	}

	fnam = filepath.Join(cDir, "metadata.yaml")
	if shared.PathExists(fnam) {
		fi, err := os.Lstat(fnam)
		if err != nil {
			shared.Debugf("Error statting %s during exportToTar\n", fnam)
			tw.Close()
			return err
		}
		if err := d.tarStoreFile(linkmap, offset, tw, fnam, fi); err != nil {
			shared.Debugf("exportToTar: error writing to tarfile: %s\n", err)
			tw.Close()
			return err
		}
	}
	fnam = filepath.Join(cDir, "rootfs")
	filepath.Walk(fnam, writeToTar)
	fnam = filepath.Join(cDir, "templates")
	if shared.PathExists(fnam) {
		filepath.Walk(fnam, writeToTar)
	}
	return tw.Close()
}
