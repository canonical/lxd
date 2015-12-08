package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

func containerFileHandler(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return SmartError(err)
	}

	targetPath := r.FormValue("path")
	if targetPath == "" {
		return BadRequest(fmt.Errorf("missing path argument"))
	}

	if !c.IsRunning() {
		return BadRequest(fmt.Errorf("container is not running"))
	}

	initPid := c.InitPID()

	switch r.Method {
	case "GET":
		return containerFileGet(d, initPid, r, targetPath)
	case "POST":
		idmapset, err := c.LastIdmapSet()
		if err != nil {
			return InternalError(err)
		}
		return containerFilePut(d, initPid, r, targetPath, idmapset)
	default:
		return NotFound
	}
}

func containerFileGet(d *Daemon, pid int, r *http.Request, path string) Response {
	/*
	 * Copy out of the ns to a temporary file, and then use that to serve
	 * the request from. This prevents us from having to worry about stuff
	 * like people breaking out of the container root by symlinks or
	 * ../../../s etc. in the path, since we can just rely on the kernel
	 * for correctness.
	 */
	temp, err := ioutil.TempFile("", "lxd_forkgetfile_")
	if err != nil {
		return InternalError(err)
	}
	defer temp.Close()

	cmd := exec.Command(
		d.execPath,
		"forkgetfile",
		temp.Name(),
		fmt.Sprintf("%d", pid),
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return InternalError(fmt.Errorf(strings.TrimRight(string(out), "\n")))
	}

	fi, err := temp.Stat()
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

	files := make([]fileResponseEntry, 1)
	files[0].identifier = filepath.Base(path)
	files[0].path = temp.Name()
	files[0].filename = filepath.Base(path)

	return FileResponse(r, files, headers, true)
}

func containerFilePut(d *Daemon, pid int, r *http.Request, p string, idmapset *shared.IdmapSet) Response {
	uid, gid, mode := shared.ParseLXDFileHeaders(r.Header)

	if idmapset != nil {
		uid, gid = idmapset.ShiftIntoNs(uid, gid)
	}

	temp, err := ioutil.TempFile("", "lxd_forkputfile_")
	if err != nil {
		return InternalError(err)
	}
	defer func() {
		temp.Close()
		os.Remove(temp.Name())
	}()

	_, err = io.Copy(temp, r.Body)
	if err != nil {
		return InternalError(err)
	}

	cmd := exec.Command(
		d.execPath,
		"forkputfile",
		temp.Name(),
		fmt.Sprintf("%d", pid),
		p,
		fmt.Sprintf("%d", uid),
		fmt.Sprintf("%d", gid),
		fmt.Sprintf("%d", mode&os.ModePerm),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return InternalError(fmt.Errorf(strings.TrimRight(string(out), "\n")))
	}

	return EmptySyncResponse
}
