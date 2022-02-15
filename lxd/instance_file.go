package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
)

func instanceFileHandler(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name := mux.Vars(r)["name"]

	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	c, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	path := r.FormValue("path")
	if path == "" {
		return response.BadRequest(fmt.Errorf("Missing path argument"))
	}

	switch r.Method {
	case "GET":
		return instanceFileGet(c, path, r)
	case "POST":
		return instanceFilePost(c, path, r)
	case "DELETE":
		return instanceFileDelete(c, path, r)
	default:
		return response.NotFound(fmt.Errorf("Method '%s' not found", r.Method))
	}
}

// swagger:operation GET /1.0/instances/{name}/files instances instance_files_get
//
// Get a file
//
// Gets the file content. If it's a directory, a json list of files will be returned instead.
//
// ---
// produces:
//   - application/json
//   - application/octet-stream
// parameters:
//   - in: query
//     name: path
//     description: Path to the file
//     type: string
//     example: default
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//      description: Raw file or directory listing
//      headers:
//        X-LXD-uid:
//          description: File owner UID
//          schema:
//            type: integer
//        X-LXD-gid:
//          description: File owner GID
//          schema:
//            type: integer
//        X-LXD-mode:
//          description: Mode mask
//          schema:
//            type: integer
//        X-LXD-type:
//          description: Type of file (file, symlink or directory)
//          schema:
//            type: string
//      content:
//        application/octet-stream:
//          schema:
//            type: string
//            example: some-text
//        application/json:
//          schema:
//            type: array
//            items:
//              type: string
//            example: |-
//              [
//                "/etc",
//                "/home"
//              ]
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "404":
//     $ref: "#/responses/NotFound"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceFileGet(c instance.Instance, path string, r *http.Request) response.Response {
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
		files[0].Cleanup = func() { os.Remove(temp.Name()) }

		return response.FileResponse(r, files, headers)
	} else if type_ == "directory" {
		os.Remove(temp.Name())
		return response.SyncResponseHeaders(true, dirEnts, headers)
	} else {
		os.Remove(temp.Name())
		return response.InternalError(fmt.Errorf("bad file type %s", type_))
	}
}

// swagger:operation POST /1.0/instances/{name}/files instances instance_files_post
//
// Create or replace a file
//
// Creates a new file in the instance.
//
// ---
// consumes:
//   - application/octet-stream
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: path
//     description: Path to the file
//     type: string
//     example: default
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: raw_file
//     description: Raw file content
//   - in: header
//     name: X-LXD-uid
//     description: File owner UID
//     schema:
//       type: integer
//     example: 1000
//   - in: header
//     name: X-LXD-gid
//     description: File owner GID
//     schema:
//       type: integer
//     example: 1000
//   - in: header
//     name: X-LXD-mode
//     description: File mode
//     schema:
//       type: integer
//     example: 0644
//   - in: header
//     name: X-LXD-type
//     description: Type of file (file, symlink or directory)
//     schema:
//       type: string
//     example: file
//   - in: header
//     name: X-LXD-write
//     description: Write mode (overwrite or append)
//     schema:
//       type: string
//     example: overwrite
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "404":
//     $ref: "#/responses/NotFound"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceFilePost(c instance.Instance, path string, r *http.Request) response.Response {
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

// swagger:operation DELETE /1.0/instances/{name}/files instances instance_files_delete
//
// Delete a file
//
// Removes the file.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: path
//     description: Path to the file
//     type: string
//     example: default
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "404":
//     $ref: "#/responses/NotFound"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceFileDelete(c instance.Instance, path string, r *http.Request) response.Response {
	err := c.FileRemove(path)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
