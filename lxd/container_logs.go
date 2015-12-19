package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

func containerLogsGet(d *Daemon, r *http.Request) Response {
	/* Let's explicitly *not* try to do a containerLoadByName here. In some
	 * cases (e.g. when container creation failed), the container won't
	 * exist in the DB but it does have some log files on disk.
	 *
	 * However, we should check this name and ensure it's a valid container
	 * name just so that people can't list arbitrary directories.
	 */
	name := mux.Vars(r)["name"]

	if err := containerValidName(name); err != nil {
		return BadRequest(err)
	}

	result := []map[string]interface{}{}

	dents, err := ioutil.ReadDir(shared.LogPath(name))
	if err != nil {
		return SmartError(err)
	}

	for _, f := range dents {
		result = append(result, map[string]interface{}{
			"name": f.Name(),
			"size": f.Size(),
		})
	}

	return SyncResponse(true, result)
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
		strings.HasPrefix(fname, "snapshot_")
}

func containerLogGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
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
	name := mux.Vars(r)["name"]
	file := mux.Vars(r)["file"]

	if err := containerValidName(name); err != nil {
		return BadRequest(err)
	}

	if !validLogFileName(file) {
		return BadRequest(fmt.Errorf("log file name %s not valid", file))
	}

	return SmartError(os.Remove(shared.LogPath(name, file)))
}

var containerLogCmd = Command{
	name:   "containers/{name}/logs/{file}",
	get:    containerLogGet,
	delete: containerLogDelete,
}
