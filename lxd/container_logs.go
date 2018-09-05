package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
)

var containerLogCmd = Command{
	name:   "containers/{name}/logs/{file}",
	get:    containerLogGet,
	delete: containerLogDelete,
}

var containerLogsCmd = Command{
	name: "containers/{name}/logs",
	get:  containerLogsGet,
}

func containerLogsGet(d *Daemon, r *http.Request) Response {
	/* Let's explicitly *not* try to do a containerLoadByName here. In some
	 * cases (e.g. when container creation failed), the container won't
	 * exist in the DB but it does have some log files on disk.
	 *
	 * However, we should check this name and ensure it's a valid container
	 * name just so that people can't list arbitrary directories.
	 */
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	if err := containerValidName(name); err != nil {
		return BadRequest(err)
	}

	result := []string{}

	dents, err := ioutil.ReadDir(shared.LogPath(name))
	if err != nil {
		return SmartError(err)
	}

	for _, f := range dents {
		if !validLogFileName(f.Name()) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, name, f.Name()))
	}

	return SyncResponse(true, result)
}

func validLogFileName(fname string) bool {
	/* Let's just require that the paths be relative, so that we don't have
	 * to deal with any escaping or whatever.
	 */
	return fname == "lxc.log" ||
		fname == "lxc.conf" ||
		strings.HasPrefix(fname, "migration_") ||
		strings.HasPrefix(fname, "snapshot_") ||
		strings.HasPrefix(fname, "exec_")
}

func containerLogGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	file := mux.Vars(r)["file"]

	if err := containerValidName(name); err != nil {
		return BadRequest(err)
	}

	if !validLogFileName(file) {
		return BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	ent := fileResponseEntry{
		path:     shared.LogPath(name, file),
		filename: file,
	}

	return FileResponse(r, []fileResponseEntry{ent}, nil, false)
}

func containerLogDelete(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	file := mux.Vars(r)["file"]

	if err := containerValidName(name); err != nil {
		return BadRequest(err)
	}

	if !validLogFileName(file) {
		return BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	if file == "lxc.log" || file == "lxc.conf" {
		return BadRequest(fmt.Errorf("lxc.log and lxc.conf may not be deleted"))
	}

	return SmartError(os.Remove(shared.LogPath(name, file)))
}
