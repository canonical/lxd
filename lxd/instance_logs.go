package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
)

var instanceLogCmd = APIEndpoint{
	Name: "instanceLog",
	Path: "instances/{name}/logs/{file}",
	Aliases: []APIEndpointAlias{
		{Name: "containerLog", Path: "containers/{name}/logs/{file}"},
		{Name: "vmLog", Path: "virtual-machines/{name}/logs/{file}"},
	},

	Delete: APIEndpointAction{Handler: instanceLogDelete, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Get:    APIEndpointAction{Handler: instanceLogGet, AccessHandler: allowProjectPermission("containers", "view")},
}

var instanceLogsCmd = APIEndpoint{
	Name: "instanceLogs",
	Path: "instances/{name}/logs",
	Aliases: []APIEndpointAlias{
		{Name: "containerLogs", Path: "containers/{name}/logs"},
		{Name: "vmLogs", Path: "virtual-machines/{name}/logs"},
	},

	Get: APIEndpointAction{Handler: instanceLogsGet, AccessHandler: allowProjectPermission("containers", "view")},
}

// swagger:operation GET /1.0/instances/{name}/logs instances instance_logs_get
//
//	Get the log files
//
//	Returns a list of log files (URLs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/instances/foo/logs/lxc.conf",
//	              "/1.0/instances/foo/logs/lxc.log"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceLogsGet(d *Daemon, r *http.Request) response.Response {
	/* Let's explicitly *not* try to do a containerLoadByName here. In some
	 * cases (e.g. when container creation failed), the container won't
	 * exist in the DB but it does have some log files on disk.
	 *
	 * However, we should check this name and ensure it's a valid container
	 * name just so that people can't list arbitrary directories.
	 */

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	result := []string{}

	fullName := project.Instance(projectName, name)
	dents, err := os.ReadDir(shared.LogPath(fullName))
	if err != nil {
		return response.SmartError(err)
	}

	for _, f := range dents {
		if !validLogFileName(f.Name()) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/instances/%s/logs/%s", version.APIVersion, name, f.Name()))
	}

	return response.SyncResponse(true, result)
}

func validLogFileName(fname string) bool {
	/* Let's just require that the paths be relative, so that we don't have
	 * to deal with any escaping or whatever.
	 */
	return fname == "lxc.log" ||
		fname == "lxc.conf" ||
		fname == "qemu.log" ||
		fname == "qemu.conf" ||
		strings.HasPrefix(fname, "migration_") ||
		strings.HasPrefix(fname, "snapshot_") ||
		strings.HasPrefix(fname, "exec_")
}

// swagger:operation GET /1.0/instances/{name}/logs/{filename} instances instance_log_get
//
//	Get the log file
//
//	Gets the log file.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw file
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceLogGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	file, err := url.PathUnescape(mux.Vars(r)["file"])
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if !validLogFileName(file) {
		return response.BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	ent := response.FileResponseEntry{
		Path:     shared.LogPath(project.Instance(projectName, name), file),
		Filename: file,
	}

	d.State().Events.SendLifecycle(projectName, lifecycle.InstanceLogRetrieved.Event(file, inst, request.CreateRequestor(r), nil))

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
}

// swagger:operation DELETE /1.0/instances/{name}/logs/{filename} instances instance_log_delete
//
//	Delete the log file
//
//	Removes the log file.
//
//	---
//	produces:
//	  - application/json
//	parameters:
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
func instanceLogDelete(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	file, err := url.PathUnescape(mux.Vars(r)["file"])
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if !validLogFileName(file) {
		return response.BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	if file == "lxc.log" || file == "lxc.conf" {
		return response.BadRequest(fmt.Errorf("lxc.log and lxc.conf may not be deleted"))
	}

	err = os.Remove(shared.LogPath(project.Instance(projectName, name), file))
	if err != nil {
		return response.SmartError(err)
	}

	d.State().Events.SendLifecycle(projectName, lifecycle.InstanceLogDeleted.Event(file, inst, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
