package main

import (
	"encoding/json"
	"fmt"
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
	c, err := containerLoadByName(d.State(), d.Storage, cname)
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
			render, err := snap.Render()
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

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	c, err := containerLoadByName(d.State(), d.Storage, name)
	if err != nil {
		return SmartError(err)
	}

	req := api.ContainerSnapshotsPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		// come up with a name
		i := d.db.ContainerNextSnapshot(name)
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

		_, err := containerCreateAsSnapshot(d.State(), d.Storage, args, c)
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
		d.Storage,
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
	render, err := sc.Render()
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, render.(*api.ContainerSnapshot))
}

func snapshotPost(d *Daemon, r *http.Request, sc container, containerName string) Response {
	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	migration, err := raw.GetBool("migration")
	if err == nil && migration {
		ws, err := NewMigrationSource(sc)
		if err != nil {
			return SmartError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{containerName}

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
	id, _ := d.db.ContainerId(fullName)
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
