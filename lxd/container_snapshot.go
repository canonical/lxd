package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"

	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/response"
)

type containerSnapshotPostReq struct {
	Name     string `json:"name"`
	Stateful bool   `json:"stateful"`
}

func containerSnapshotsGet(d *Daemon, r *http.Request) response.Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	cname := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, cname)
	if err != nil {
		return response.SmartError(err)
	}

	snaps, err := c.Snapshots()
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*shared.SnapshotInfo{}

	for _, snap := range snaps {
		snapName := strings.SplitN(snap.Name(), shared.SnapshotDelimiter, 2)[1]
		if recursion == 0 {
			url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, cname, snapName)
			resultString = append(resultString, url)
		} else {
			render, _, err := snap.Render()
			if err != nil {
				continue
			}

			resultMap = append(resultMap, render.(*shared.SnapshotInfo))
		}
	}

	if recursion == 0 {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

/*
 * Note, the code below doesn't deal with snapshots of snapshots.
 * To do that, we'll need to weed out based on # slashes in names
 */
func nextSnapshot(d *Daemon, name string) int {
	base := name + shared.SnapshotDelimiter + "snap"
	length := len(base)
	q := fmt.Sprintf("SELECT MAX(name) FROM containers WHERE type=? AND SUBSTR(name,1,?)=?")
	var numstr string
	inargs := []interface{}{cTypeSnapshot, length, base}
	outfmt := []interface{}{numstr}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return 0
	}
	max := 0

	for _, r := range results {
		numstr = r[0].(string)
		if len(numstr) <= length {
			continue
		}
		substr := numstr[length:]
		var num int
		count, err := fmt.Sscanf(substr, "%d", &num)
		if err != nil || count != 1 {
			continue
		}
		if num >= max {
			max = num + 1
		}
	}

	return max
}

func containerSnapshotsPost(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	c, err := containerLoadByName(d, name)
	if err != nil {
		return response.SmartError(err)
	}

	req := containerSnapshotPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		// come up with a name
		i := nextSnapshot(d, name)
		req.Name = fmt.Sprintf("snap%d", i)
	}

	fullName := name +
		shared.SnapshotDelimiter +
		req.Name

	snapshot := func(op *operation.Operation) error {
		args := containerArgs{
			Name:         fullName,
			Ctype:        cTypeSnapshot,
			Config:       c.LocalConfig(),
			Profiles:     c.Profiles(),
			Ephemeral:    c.IsEphemeral(),
			BaseImage:    c.ExpandedConfig()["volatile.base_image"],
			Architecture: c.Architecture(),
			Devices:      c.LocalDevices(),
			Stateful:     req.Stateful,
		}

		_, err := containerCreateAsSnapshot(d, args, c)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operation.Create(operation.ClassTask, resources, nil, snapshot, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}

func snapshotHandler(d *Daemon, r *http.Request) response.Response {
	containerName := mux.Vars(r)["name"]
	snapshotName := mux.Vars(r)["snapshotName"]

	sc, err := containerLoadByName(
		d,
		containerName+
			shared.SnapshotDelimiter+
			snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(sc, snapshotName)
	case "POST":
		return snapshotPost(d, r, sc, containerName)
	case "DELETE":
		return snapshotDelete(sc, snapshotName)
	default:
		return response.NotFound
	}
}

func snapshotGet(sc container, name string) response.Response {
	render, _, err := sc.Render()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, render.(*shared.SnapshotInfo))
}

func snapshotPost(d *Daemon, r *http.Request, sc container, containerName string) response.Response {
	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return response.BadRequest(err)
	}

	migration, err := raw.GetBool("migration")
	if err == nil && migration {
		ws, err := NewMigrationSource(sc)
		if err != nil {
			return response.SmartError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{containerName}

		op, err := operation.Create(operation.ClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return response.InternalError(err)
		}

		return response.OperationResponse(op)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return response.BadRequest(err)
	}

	fullName := containerName + shared.SnapshotDelimiter + newName

	// Check that the name isn't already in use
	id, _ := dbContainerId(d.db, fullName)
	if id > 0 {
		return response.Conflict
	}

	rename := func(op *operation.Operation) error {
		return sc.Rename(fullName)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{containerName}

	op, err := operation.Create(operation.ClassTask, resources, nil, rename, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}

func snapshotDelete(sc container, name string) response.Response {
	remove := func(op *operation.Operation) error {
		return sc.Delete()
	}

	resources := map[string][]string{}
	resources["containers"] = []string{sc.Name()}

	op, err := operation.Create(operation.ClassTask, resources, nil, remove, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}
