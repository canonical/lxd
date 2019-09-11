package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

func containerSnapshotsGet(d *Daemon, r *http.Request) Response {
	// Instance type.
	instanceType := instance.TypeAny
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		instanceType = instance.TypeContainer
	}

	project := projectParam(r)
	cname := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, cname, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	recursion := util.IsRecursionRequest(r)
	resultString := []string{}
	resultMap := []*api.ContainerSnapshot{}

	if !recursion {
		snaps, err := d.cluster.ContainerGetSnapshots(project, cname)
		if err != nil {
			return SmartError(err)
		}

		for _, snap := range snaps {
			_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap)
			if project == "default" {
				url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", version.APIVersion, cname, snapName)
				resultString = append(resultString, url)
			} else {
				url := fmt.Sprintf("/%s/containers/%s/snapshots/%s?project=%s", version.APIVersion, cname, snapName, project)
				resultString = append(resultString, url)
			}
		}
	} else {
		c, err := containerLoadByProjectAndName(d.State(), project, cname)
		if err != nil {
			return SmartError(err)
		}

		snaps, err := c.Snapshots()
		if err != nil {
			return SmartError(err)
		}

		for _, snap := range snaps {
			render, _, err := snap.Render()
			if err != nil {
				continue
			}

			resultMap = append(resultMap, render.(*api.ContainerSnapshot))
		}
	}

	if !recursion {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	// Instance type.
	instanceType := instance.TypeAny
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		instanceType = instance.TypeContainer
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
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
	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	req := api.ContainerSnapshotsPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		req.Name, err = containerDetermineNextSnapshotName(d, c, "snap%d")
		if err != nil {
			return SmartError(err)
		}
	}

	// Validate the name
	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Snapshot names may not contain slashes"))
	}

	fullName := name +
		shared.SnapshotDelimiter +
		req.Name

	var expiry time.Time
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = shared.GetSnapshotExpiry(time.Now(), c.LocalConfig()["snapshots.expiry"])
		if err != nil {
			return BadRequest(err)
		}
	}

	snapshot := func(op *operation) error {
		args := db.ContainerArgs{
			Project:      c.Project(),
			Architecture: c.Architecture(),
			Config:       c.LocalConfig(),
			Type:         c.Type(),
			Snapshot:     true,
			Devices:      c.LocalDevices(),
			Ephemeral:    c.IsEphemeral(),
			Name:         fullName,
			Profiles:     c.Profiles(),
			Stateful:     req.Stateful,
			ExpiryDate:   expiry,
		}

		_, err := containerCreateAsSnapshot(d.State(), args, c)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationSnapshotCreate, resources, nil, snapshot, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerSnapshotHandler(d *Daemon, r *http.Request) Response {
	// Instance type.
	instanceType := instance.TypeAny
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		instanceType = instance.TypeContainer
	}

	project := projectParam(r)
	containerName := mux.Vars(r)["name"]
	snapshotName := mux.Vars(r)["snapshotName"]

	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, containerName, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	snapshotName, err = url.QueryUnescape(snapshotName)
	if err != nil {
		return SmartError(err)
	}
	sc, err := containerLoadByProjectAndName(
		d.State(),
		project, containerName+
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
	case "PUT":
		return snapshotPut(d, r, sc, snapshotName)
	default:
		return NotFound(fmt.Errorf("Method '%s' not found", r.Method))
	}
}

func snapshotPut(d *Daemon, r *http.Request, sc container, name string) Response {
	// Validate the ETag
	etag := []interface{}{sc.ExpiryDate()}
	err := util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	rj := shared.Jmap{}

	err = json.NewDecoder(r.Body).Decode(&rj)
	if err != nil {
		return InternalError(err)
	}

	var do func(op *operation) error

	_, err = rj.GetString("expires_at")
	if err != nil {
		// Skip updating the snapshot since the requested key wasn't provided
		do = func(op *operation) error {
			return nil
		}
	} else {
		body, err := json.Marshal(rj)
		if err != nil {
			return InternalError(err)
		}

		configRaw := api.ContainerSnapshotPut{}

		err = json.Unmarshal(body, &configRaw)
		if err != nil {
			return BadRequest(err)
		}

		// Update container configuration
		do = func(op *operation) error {
			args := db.ContainerArgs{
				Architecture: sc.Architecture(),
				Config:       sc.LocalConfig(),
				Description:  sc.Description(),
				Devices:      sc.LocalDevices(),
				Ephemeral:    sc.IsEphemeral(),
				Profiles:     sc.Profiles(),
				Project:      sc.Project(),
				ExpiryDate:   configRaw.ExpiresAt,
				Type:         sc.Type(),
				Snapshot:     sc.IsSnapshot(),
			}

			err = sc.Update(args, false)
			if err != nil {
				return err
			}

			return nil
		}
	}

	opType := db.OperationSnapshotUpdate

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, sc.Project(), operationClassTask, opType, resources, nil,
		do, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
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
			sourceName, _, _ := shared.ContainerGetParentAndSnapshotName(containerName)
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
			err := ws.ConnectContainerTarget(*req.Target)
			if err != nil {
				return InternalError(err)
			}

			op, err := operationCreate(d.cluster, sc.Project(), operationClassTask, db.OperationSnapshotTransfer, resources, nil, ws.Do, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(d.cluster, sc.Project(), operationClassWebsocket, db.OperationSnapshotTransfer, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
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
	id, _ := d.cluster.InstanceSnapshotID(sc.Project(), containerName, newName)
	if id > 0 {
		return Conflict(fmt.Errorf("Name '%s' already in use", fullName))
	}

	rename := func(op *operation) error {
		return sc.Rename(fullName)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{containerName}

	op, err := operationCreate(d.cluster, sc.Project(), operationClassTask, db.OperationSnapshotRename, resources, nil, rename, nil, nil)
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

	op, err := operationCreate(sc.DaemonState().Cluster, sc.Project(), operationClassTask, db.OperationSnapshotDelete, resources, nil, remove, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
