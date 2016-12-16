package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
)

func containerLogsGet(d *Daemon, r *http.Request) response.Response {
	/* Let's explicitly *not* try to do a containerLoadByName here. In some
	 * cases (e.g. when container creation failed), the container won't
	 * exist in the DB but it does have some log files on disk.
	 *
	 * However, we should check this name and ensure it's a valid container
	 * name just so that people can't list arbitrary directories.
	 */
	name := mux.Vars(r)["name"]

	if err := containerValidName(name); err != nil {
		return response.BadRequest(err)
	}

	result := []string{}

	dents, err := ioutil.ReadDir(shared.LogPath(name))
	if err != nil {
		return response.SmartError(err)
	}

	for _, f := range dents {
		if !validLogFileName(f.Name()) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/containers/%s/logs/%s", shared.APIVersion, name, f.Name()))
	}

	return response.SyncResponse(true, result)
}

var containerLogsCmd = Command{
	name: "containers/{name}/logs",
	get:  containerLogsGet,
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

func containerLogGet(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	file := mux.Vars(r)["file"]

	if err := containerValidName(name); err != nil {
		return response.BadRequest(err)
	}

	if !validLogFileName(file) {
		return response.BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	ent := response.FileResponseEntry{
		Path:     shared.LogPath(name, file),
		Filename: file,
	}

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil, false)
}

func containerLogDelete(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	file := mux.Vars(r)["file"]

	if err := containerValidName(name); err != nil {
		return response.BadRequest(err)
	}

	if !validLogFileName(file) {
		return response.BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	return response.SmartError(os.Remove(shared.LogPath(name, file)))
}

var containerLogCmd = Command{
	name:   "containers/{name}/logs/{file}",
	get:    containerLogGet,
	delete: containerLogDelete,
}
