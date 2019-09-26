package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
)

func containerFileHandler(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	path := r.FormValue("path")
	if path == "" {
		return response.BadRequest(fmt.Errorf("missing path argument"))
	}

	switch r.Method {
	case "GET":
		return containerFileGet(c, path, r)
	case "POST":
		return containerFilePost(c, path, r)
	case "DELETE":
		return containerFileDelete(c, path, r)
	default:
		return response.NotFound(fmt.Errorf("Method '%s' not found", r.Method))
	}
}

func containerFileGet(c Instance, path string, r *http.Request) response.Response {
	/*
	 * Copy out of the ns to a temporary file, and then use that to serve
	 * the request from. This prevents us from having to worry about stuff
	 * like people breaking out of the container root by symlinks or
	 * ../../../s etc. in the path, since we can just rely on the kernel
	 * for correctness.
	 */
	temp, err := ioutil.TempFile("", "lxd_forkgetfile_")
	if err != nil {
		return response.InternalError(err)
	}
	defer temp.Close()

	// Pull the file from the container
	uid, gid, mode, type_, dirEnts, err := c.FilePull(path, temp.Name())
	if err != nil {
		os.Remove(temp.Name())
		return response.SmartError(err)
	}

	headers := map[string]string{
		"X-LXD-uid":  fmt.Sprintf("%d", uid),
		"X-LXD-gid":  fmt.Sprintf("%d", gid),
		"X-LXD-mode": fmt.Sprintf("%04o", mode),
		"X-LXD-type": type_,
	}

	if type_ == "file" || type_ == "symlink" {
		// Make a file response struct
		files := make([]response.FileResponseEntry, 1)
		files[0].Identifier = filepath.Base(path)
		files[0].Path = temp.Name()
		files[0].Filename = filepath.Base(path)

		return response.FileResponse(r, files, headers, true)
	} else if type_ == "directory" {
		os.Remove(temp.Name())
		return response.SyncResponseHeaders(true, dirEnts, headers)
	} else {
		os.Remove(temp.Name())
		return response.InternalError(fmt.Errorf("bad file type %s", type_))
	}
}

func containerFilePost(c Instance, path string, r *http.Request) response.Response {
	// Extract file ownership and mode from headers
	uid, gid, mode, type_, write := shared.ParseLXDFileHeaders(r.Header)

	if !shared.StringInSlice(write, []string{"overwrite", "append"}) {
		return response.BadRequest(fmt.Errorf("Bad file write mode: %s", write))
	}

	if type_ == "file" {
		// Write file content to a tempfile
		temp, err := ioutil.TempFile("", "lxd_forkputfile_")
		if err != nil {
			return response.InternalError(err)
		}
		defer func() {
			temp.Close()
			os.Remove(temp.Name())
		}()

		_, err = io.Copy(temp, r.Body)
		if err != nil {
			return response.InternalError(err)
		}

		// Transfer the file into the container
		err = c.FilePush("file", temp.Name(), path, uid, gid, mode, write)
		if err != nil {
			return response.InternalError(err)
		}

		return response.EmptySyncResponse
	} else if type_ == "symlink" {
		target, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return response.InternalError(err)
		}

		err = c.FilePush("symlink", string(target), path, uid, gid, mode, write)
		if err != nil {
			return response.InternalError(err)
		}
		return response.EmptySyncResponse
	} else if type_ == "directory" {
		err := c.FilePush("directory", "", path, uid, gid, mode, write)
		if err != nil {
			return response.InternalError(err)
		}
		return response.EmptySyncResponse
	} else {
		return response.BadRequest(fmt.Errorf("Bad file type: %s", type_))
	}
}

func containerFileDelete(c Instance, path string, r *http.Request) response.Response {
	err := c.FileRemove(path)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
