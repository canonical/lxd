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
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
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
		return containerFilePut(c, path, r)
	default:
		return response.NotFound
	}
}

func containerFileGet(c container, path string, r *http.Request) response.Response {
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

	if type_ == "file" {
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

func containerFilePut(c container, path string, r *http.Request) response.Response {
	// Extract file ownership and mode from headers
	uid, gid, mode, type_ := shared.ParseLXDFileHeaders(r.Header)

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
		err = c.FilePush(temp.Name(), path, uid, gid, mode)
		if err != nil {
			return response.InternalError(err)
		}

		return response.EmptySyncResponse
	} else if type_ == "directory" {
		err := c.FilePush("", path, uid, gid, mode)
		if err != nil {
			return response.InternalError(err)
		}
		return response.EmptySyncResponse
	} else {
		return response.InternalError(fmt.Errorf("bad file type %s", type_))
	}
}
