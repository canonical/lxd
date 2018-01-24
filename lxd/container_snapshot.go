package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

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

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

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
		i := d.cluster.ContainerNextSnapshot(name)
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Validate the name
	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Snapshot names may not contain slashes"))
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

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, snapshot, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func snapshotHandler(d *Daemon, r *http.Request) Response {
	containerName := mux.Vars(r)["name"]
	snapshotName := mux.Vars(r)["snapshotName"]

	response, err := ForwardedResponseIfContainerIsRemote(d, r, containerName)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

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

			op, err := operationCreate(d.cluster, operationClassTask, resources, nil, ws.Do, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(d.cluster, operationClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return BadRequest(err)
	}

	// Validate the name
	if strings.Contains(newName, "/") {
		return BadRequest(fmt.Errorf("Snapshot names may not contain slashes"))
	}

	fullName := containerName + shared.SnapshotDelimiter + newName

	// Check that the name isn't already in use
	id, _ := d.cluster.ContainerId(fullName)
	if id > 0 {
		return Conflict
	}

	rename := func(op *operation) error {
		return sc.Rename(fullName)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{containerName}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, rename, nil, nil)
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

	state := sc.DaemonState()
	op, err := operationCreate(state.Cluster, operationClassTask, resources, nil, remove, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
