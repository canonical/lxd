package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

func containerFileHandler(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	path := r.FormValue("path")
	if path == "" {
		return BadRequest(fmt.Errorf("missing path argument"))
	}

	switch r.Method {
	case "GET":
		return containerFileGet(c, path, r)
	case "POST":
		return containerFilePost(c, path, r)
	case "DELETE":
		return containerFileDelete(c, path, r)
	default:
		return NotFound(fmt.Errorf("Method '%s' not found", r.Method))
	}
}

func containerFileGet(c container, path string, r *http.Request) Response {
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

	// Pull the file from the container
	uid, gid, mode, type_, dirEnts, err := c.FilePull(path, temp.Name())
	if err != nil {
		os.Remove(temp.Name())
		return SmartError(err)
	}

	headers := map[string]string{
		"X-LXD-uid":  fmt.Sprintf("%d", uid),
		"X-LXD-gid":  fmt.Sprintf("%d", gid),
		"X-LXD-mode": fmt.Sprintf("%04o", mode),
		"X-LXD-type": type_,
	}

	if type_ == "file" || type_ == "symlink" {
		// Make a file response struct
		files := make([]fileResponseEntry, 1)
		files[0].identifier = filepath.Base(path)
		files[0].path = temp.Name()
		files[0].filename = filepath.Base(path)

		return FileResponse(r, files, headers, true)
	} else if type_ == "directory" {
		os.Remove(temp.Name())
		return SyncResponseHeaders(true, dirEnts, headers)
	} else {
		os.Remove(temp.Name())
		return InternalError(fmt.Errorf("bad file type %s", type_))
	}
}

func containerFilePost(c container, path string, r *http.Request) Response {
	// Extract file ownership and mode from headers
	uid, gid, mode, type_, write := shared.ParseLXDFileHeaders(r.Header)

	if !shared.StringInSlice(write, []string{"overwrite", "append"}) {
		return BadRequest(fmt.Errorf("Bad file write mode: %s", write))
	}

	if type_ == "file" {
		// Write file content to a tempfile
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

		// Transfer the file into the container
		err = c.FilePush("file", temp.Name(), path, uid, gid, mode, write)
		if err != nil {
			return InternalError(err)
		}

		return EmptySyncResponse
	} else if type_ == "symlink" {
		target, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return InternalError(err)
		}

		err = c.FilePush("symlink", string(target), path, uid, gid, mode, write)
		if err != nil {
			return InternalError(err)
		}
		return EmptySyncResponse
	} else if type_ == "directory" {
		err := c.FilePush("directory", "", path, uid, gid, mode, write)
		if err != nil {
			return InternalError(err)
		}
		return EmptySyncResponse
	} else {
		return BadRequest(fmt.Errorf("Bad file type: %s", type_))
	}
}

func containerFileDelete(c container, path string, r *http.Request) Response {
	err := c.FileRemove(path)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}
