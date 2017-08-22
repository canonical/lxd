package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

func containerSnapshotsGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	cname := mux.Vars(r)["name"]
	c, err := containerLoadByName(d.State(), cname)
	if err != nil {
		return SmartError(err)
	}

	snaps, err := c.Snapshots()
	if err != nil {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.ContainerSnapshot{}

	for _, snap := range snaps {
		_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())
		if recursion == 0 {
			url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", version.APIVersion, cname, snapName)
			resultString = append(resultString, url)
		} else {
			render, _, err := snap.Render()
			if err != nil {
				continue
			}

			resultMap = append(resultMap, render.(*api.ContainerSnapshot))
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

/*
 * Note, the code below doesn't deal with snapshots of snapshots.
 * To do that, we'll need to weed out based on # slashes in names
 */
func nextSnapshot(d *Daemon, name string) int {
	base := name + shared.SnapshotDelimiter + "snap"
	length := len(base)
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?")
	var numstr string
	inargs := []interface{}{db.CTypeSnapshot, length, base}
	outfmt := []interface{}{numstr}
	results, err := db.QueryScan(d.db, q, inargs, outfmt)
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

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	c, err := containerLoadByName(d.State(), name)
	if err != nil {
		return SmartError(err)
	}

	ourStart, err := c.StorageStart()
	if err != nil {
		return InternalError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	req := api.ContainerSnapshotsPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		// come up with a name
		i := nextSnapshot(d, name)
		req.Name = fmt.Sprintf("snap%d", i)
	}

	fullName := name +
		shared.SnapshotDelimiter +
		req.Name

	snapshot := func(op *operation) error {
		args := db.ContainerArgs{
			Name:         fullName,
			Ctype:        db.CTypeSnapshot,
			Config:       c.LocalConfig(),
			Profiles:     c.Profiles(),
			Ephemeral:    c.IsEphemeral(),
			BaseImage:    c.ExpandedConfig()["volatile.base_image"],
			Architecture: c.Architecture(),
			Devices:      c.LocalDevices(),
			Stateful:     req.Stateful,
		}

		_, err := containerCreateAsSnapshot(d.State(), args, c)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(operationClassTask, resources, nil, snapshot, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func snapshotHandler(d *Daemon, r *http.Request) Response {
	containerName := mux.Vars(r)["name"]
	snapshotName := mux.Vars(r)["snapshotName"]

	sc, err := containerLoadByName(
		d.State(),
		containerName+
			shared.SnapshotDelimiter+
			snapshotName)
	if err != nil {
		return SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(sc, snapshotName)
	case "POST":
		return snapshotPost(d, r, sc, containerName)
	case "DELETE":
		return snapshotDelete(sc, snapshotName)
	default:
		return NotFound
	}
}

func snapshotGet(sc container, name string) Response {
	render, _, err := sc.Render()
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, render.(*api.ContainerSnapshot))
}

func snapshotPost(d *Daemon, r *http.Request, sc container, containerName string) Response {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))

	raw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	migration, err := raw.GetBool("migration")
	if err == nil && migration {
		rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))
		rdr3 := ioutil.NopCloser(bytes.NewBuffer(body))

		req := api.ContainerPost{}
		err = json.NewDecoder(rdr2).Decode(&req)
		if err != nil {
			return BadRequest(err)
		}

		reqNew := api.ContainerSnapshotPost{}
		err = json.NewDecoder(rdr3).Decode(&reqNew)
		if err != nil {
			return BadRequest(err)
		}

		if reqNew.Name == "" {
			return BadRequest(fmt.Errorf(`A new name for the ` +
				`container must be provided`))
		}

		if reqNew.Live {
			sourceName, _, _ := containerGetParentAndSnapshotName(containerName)
			if sourceName != reqNew.Name {
				return BadRequest(fmt.Errorf(`Copying `+
					`stateful containers requires that `+
					`source "%s" and `+`target "%s" name `+
					`be identical`, sourceName, reqNew.Name))
			}
		}

		ws, err := NewMigrationSource(sc, reqNew.Live, true)
		if err != nil {
			return SmartError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{containerName}

		if req.Target != nil {
			// Push mode
			err := ws.ConnectTarget(*req.Target)
			if err != nil {
				return InternalError(err)
			}

			op, err := operationCreate(operationClassTask, resources, nil, ws.Do, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(operationClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return BadRequest(err)
	}

	fullName := containerName + shared.SnapshotDelimiter + newName

	// Check that the name isn't already in use
	id, _ := db.ContainerId(d.db, fullName)
	if id > 0 {
		return Conflict
	}

	rename := func(op *operation) error {
		return sc.Rename(fullName)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{containerName}

	op, err := operationCreate(operationClassTask, resources, nil, rename, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func snapshotDelete(sc container, name string) Response {
	remove := func(op *operation) error {
		return sc.Delete()
	}

	resources := map[string][]string{}
	resources["containers"] = []string{sc.Name()}

	op, err := operationCreate(operationClassTask, resources, nil, remove, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
