package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/project"
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

	Delete: APIEndpointAction{Handler: containerLogDelete, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Get:    APIEndpointAction{Handler: containerLogGet, AccessHandler: allowProjectPermission("containers", "view")},
}

var instanceLogsCmd = APIEndpoint{
	Name: "instanceLogs",
	Path: "instances/{name}/logs",
	Aliases: []APIEndpointAlias{
		{Name: "containerLogs", Path: "containers/{name}/logs"},
		{Name: "vmLogs", Path: "virtual-machines/{name}/logs"},
	},

	Get: APIEndpointAction{Handler: containerLogsGet, AccessHandler: allowProjectPermission("containers", "view")},
}

func containerLogsGet(d *Daemon, r *http.Request) response.Response {
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
	name := mux.Vars(r)["name"]

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
	dents, err := ioutil.ReadDir(shared.LogPath(fullName))
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
		strings.HasPrefix(fname, "migration_") ||
		strings.HasPrefix(fname, "snapshot_") ||
		strings.HasPrefix(fname, "exec_")
}

func containerLogGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	file := mux.Vars(r)["file"]

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

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil, false)
}

func containerLogDelete(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	file := mux.Vars(r)["file"]

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

	return response.SmartError(os.Remove(shared.LogPath(name, file)))
}
