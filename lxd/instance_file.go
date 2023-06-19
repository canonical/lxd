package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/sftp"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

func instanceFileHandler(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Redirect to correct server if needed.
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Load the instance.
	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Parse and cleanup the path.
	path := r.FormValue("path")
	if path == "" {
		return response.BadRequest(fmt.Errorf("Missing path argument"))
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	switch r.Method {
	case "GET":
		return instanceFileGet(d.State(), inst, path, r)
	case "HEAD":
		return instanceFileHead(d.State(), inst, path, r)
	case "POST":
		return instanceFilePost(d.State(), inst, path, r)
	case "DELETE":
		return instanceFileDelete(d.State(), inst, path, r)
	default:
		return response.NotFound(fmt.Errorf("Method %q not found", r.Method))
	}
}

// swagger:operation GET /1.0/instances/{name}/files instances instance_files_get
//
//	Get a file
//
//	Gets the file content. If it's a directory, a json list of files will be returned instead.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw file or directory listing
//	     headers:
//	       X-LXD-uid:
//	         description: File owner UID
//	         schema:
//	           type: integer
//	       X-LXD-gid:
//	         description: File owner GID
//	         schema:
//	           type: integer
//	       X-LXD-mode:
//	         description: Mode mask
//	         schema:
//	           type: integer
//	       X-LXD-modified:
//	         description: Last modified date
//	         schema:
//	           type: string
//	       X-LXD-type:
//	         description: Type of file (file, symlink or directory)
//	         schema:
//	           type: string
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	       application/json:
//	         schema:
//	           type: array
//	           items:
//	             type: string
//	           example: |-
//	             [
//	               "/etc",
//	               "/home"
//	             ]
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceFileGet(s *state.State, inst instance.Instance, path string, r *http.Request) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Get a SFTP client.
	client, err := inst.FileSFTP()
	if err != nil {
		return response.InternalError(err)
	}

	revert.Add(func() { _ = client.Close() })

	// Get the file stats.
	stat, err := client.Lstat(path)
	if err != nil {
		return response.SmartError(err)
	}

	fileType := "file"
	if stat.Mode().IsDir() {
		fileType = "directory"
	} else if stat.Mode()&os.ModeSymlink == os.ModeSymlink {
		fileType = "symlink"
	}

	fs := stat.Sys().(*sftp.FileStat)

	// Prepare the response.
	headers := map[string]string{
		"X-LXD-uid":      fmt.Sprintf("%d", fs.UID),
		"X-LXD-gid":      fmt.Sprintf("%d", fs.GID),
		"X-LXD-mode":     fmt.Sprintf("%04o", stat.Mode().Perm()),
		"X-LXD-modified": stat.ModTime().UTC().String(),
		"X-LXD-type":     fileType,
	}

	if fileType == "file" {
		// Open the file.
		file, err := client.Open(path)
		if err != nil {
			return response.SmartError(err)
		}

		revert.Add(func() { _ = file.Close() })

		// Setup cleanup logic.
		cleanup := revert.Clone()
		revert.Success()

		// Make a file response struct.
		files := make([]response.FileResponseEntry, 1)
		files[0].Identifier = filepath.Base(path)
		files[0].Filename = filepath.Base(path)
		files[0].File = file
		files[0].FileSize = stat.Size()
		files[0].FileModified = stat.ModTime()
		files[0].Cleanup = func() {
			cleanup.Fail()
		}

		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFileRetrieved.Event(inst, logger.Ctx{"path": path}))
		return response.FileResponse(r, files, headers)
	} else if fileType == "symlink" {
		// Find symlink target.
		target, err := client.ReadLink(path)
		if err != nil {
			return response.SmartError(err)
		}

		// If not an absolute symlink, need to mangle to something
		// relative to the source path. This is required because there
		// is no sftp function to get the final target path and RealPath doesn't
		// allow specifying the path to resolve from.
		if !strings.HasPrefix(target, "/") {
			target = filepath.Join(filepath.Dir(path), target)
		}

		// Convert to absolute path.
		target, err = client.RealPath(target)
		if err != nil {
			return response.SmartError(err)
		}

		// Make a file response struct.
		files := make([]response.FileResponseEntry, 1)
		files[0].Identifier = filepath.Base(path)
		files[0].Filename = filepath.Base(path)
		files[0].File = bytes.NewReader([]byte(target))
		files[0].FileModified = time.Now()
		files[0].FileSize = int64(len(target))

		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFileRetrieved.Event(inst, logger.Ctx{"path": path}))
		return response.FileResponse(r, files, headers)
	} else if fileType == "directory" {
		dirEnts := []string{}

		// List the directory.
		entries, err := client.ReadDir(path)
		if err != nil {
			return response.SmartError(err)
		}

		for _, entry := range entries {
			dirEnts = append(dirEnts, entry.Name())
		}

		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFileRetrieved.Event(inst, logger.Ctx{"path": path}))
		return response.SyncResponseHeaders(true, dirEnts, headers)
	} else {
		return response.InternalError(fmt.Errorf("Bad file type: %s", fileType))
	}
}

// swagger:operation HEAD /1.0/instances/{name}/files instances instance_files_head
//
//	Get metadata for a file
//
//	Gets the file or directory metadata.
//
//	---
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw file or directory listing
//	     headers:
//	       X-LXD-uid:
//	         description: File owner UID
//	         schema:
//	           type: integer
//	       X-LXD-gid:
//	         description: File owner GID
//	         schema:
//	           type: integer
//	       X-LXD-mode:
//	         description: Mode mask
//	         schema:
//	           type: integer
//	       X-LXD-modified:
//	         description: Last modified date
//	         schema:
//	           type: string
//	       X-LXD-type:
//	         description: Type of file (file, symlink or directory)
//	         schema:
//	           type: string
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceFileHead(s *state.State, inst instance.Instance, path string, r *http.Request) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Get a SFTP client.
	client, err := inst.FileSFTP()
	if err != nil {
		return response.InternalError(err)
	}

	revert.Add(func() { _ = client.Close() })

	// Get the file stats.
	stat, err := client.Lstat(path)
	if err != nil {
		return response.SmartError(err)
	}

	fileType := "file"
	if stat.Mode().IsDir() {
		fileType = "directory"
	} else if stat.Mode()&os.ModeSymlink == os.ModeSymlink {
		fileType = "symlink"
	}

	fs := stat.Sys().(*sftp.FileStat)

	// Prepare the response.
	headers := map[string]string{
		"X-LXD-uid":      fmt.Sprintf("%d", fs.UID),
		"X-LXD-gid":      fmt.Sprintf("%d", fs.GID),
		"X-LXD-mode":     fmt.Sprintf("%04o", stat.Mode().Perm()),
		"X-LXD-modified": stat.ModTime().UTC().String(),
		"X-LXD-type":     fileType,
	}

	// Return an empty body (per RFC for HEAD).
	return response.ManualResponse(func(w http.ResponseWriter) error {
		// Set the headers.
		for k, v := range headers {
			w.Header().Set(k, v)
		}

		// Flush the connection.
		w.WriteHeader(http.StatusOK)
		return nil
	})
}

// swagger:operation POST /1.0/instances/{name}/files instances instance_files_post
//
//	Create or replace a file
//
//	Creates a new file in the instance.
//
//	---
//	consumes:
//	  - application/octet-stream
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: raw_file
//	    description: Raw file content
//	  - in: header
//	    name: X-LXD-uid
//	    description: File owner UID
//	    schema:
//	      type: integer
//	    example: 1000
//	  - in: header
//	    name: X-LXD-gid
//	    description: File owner GID
//	    schema:
//	      type: integer
//	    example: 1000
//	  - in: header
//	    name: X-LXD-mode
//	    description: File mode
//	    schema:
//	      type: integer
//	    example: 0644
//	  - in: header
//	    name: X-LXD-type
//	    description: Type of file (file, symlink or directory)
//	    schema:
//	      type: string
//	    example: file
//	  - in: header
//	    name: X-LXD-write
//	    description: Write mode (overwrite or append)
//	    schema:
//	      type: string
//	    example: overwrite
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceFilePost(s *state.State, inst instance.Instance, path string, r *http.Request) response.Response {
	// Get a SFTP client.
	client, err := inst.FileSFTP()
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = client.Close() }()

	// Extract file ownership and mode from headers
	uid, gid, mode, type_, write := shared.ParseLXDFileHeaders(r.Header)

	if !shared.StringInSlice(write, []string{"overwrite", "append"}) {
		return response.BadRequest(fmt.Errorf("Bad file write mode: %s", write))
	}

	// Check if the file already exists.
	_, err = client.Stat(path)
	exists := err == nil

	if type_ == "file" {
		fileMode := os.O_RDWR

		if write == "overwrite" {
			fileMode |= os.O_CREATE | os.O_TRUNC
		}

		// Open/create the file.
		file, err := client.OpenFile(path, fileMode)
		if err != nil {
			return response.SmartError(err)
		}

		defer func() { _ = file.Close() }()

		// Go to the end of the file.
		_, err = file.Seek(0, io.SeekEnd)
		if err != nil {
			return response.InternalError(err)
		}

		// Transfer the file into the instance.
		_, err = io.Copy(file, r.Body)
		if err != nil {
			return response.InternalError(err)
		}

		if !exists {
			// Set file permissions.
			if mode >= 0 {
				err = file.Chmod(fs.FileMode(mode))
				if err != nil {
					return response.SmartError(err)
				}
			}

			// Set file ownership.
			if uid >= 0 || gid >= 0 {
				err = file.Chown(int(uid), int(gid))
				if err != nil {
					return response.SmartError(err)
				}
			}
		}

		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFilePushed.Event(inst, logger.Ctx{"path": path}))
		return response.EmptySyncResponse
	} else if type_ == "symlink" {
		// Figure out target.
		target, err := io.ReadAll(r.Body)
		if err != nil {
			return response.InternalError(err)
		}

		// Check if already setup.
		currentTarget, err := client.ReadLink(path)
		if err == nil && currentTarget == string(target) {
			return response.EmptySyncResponse
		}

		// Create the symlink.
		err = client.Symlink(string(target), path)
		if err != nil {
			return response.SmartError(err)
		}

		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFilePushed.Event(inst, logger.Ctx{"path": path}))
		return response.EmptySyncResponse
	} else if type_ == "directory" {
		// Check if it already exists.
		if exists {
			return response.EmptySyncResponse
		}

		// Create the directory.
		err = client.Mkdir(path)
		if err != nil {
			return response.SmartError(err)
		}

		// Set file permissions.
		if mode < 0 {
			// Default mode for directories (sftp doesn't know about umask).
			mode = 0750
		}

		err = client.Chmod(path, fs.FileMode(mode))
		if err != nil {
			return response.SmartError(err)
		}

		// Set file ownership.
		if uid >= 0 || gid >= 0 {
			err = client.Chown(path, int(uid), int(gid))
			if err != nil {
				return response.SmartError(err)
			}
		}

		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFilePushed.Event(inst, logger.Ctx{"path": path}))
		return response.EmptySyncResponse
	} else {
		return response.BadRequest(fmt.Errorf("Bad file type: %s", type_))
	}
}

// swagger:operation DELETE /1.0/instances/{name}/files instances instance_files_delete
//
//	Delete a file
//
//	Removes the file.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceFileDelete(s *state.State, inst instance.Instance, path string, r *http.Request) response.Response {
	// Get a SFTP client.
	client, err := inst.FileSFTP()
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = client.Close() }()

	// Delete the file.
	err = client.Remove(path)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceFileDeleted.Event(inst, logger.Ctx{"path": path}))
	return response.EmptySyncResponse
}
