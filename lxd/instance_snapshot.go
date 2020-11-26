package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/validate"
	"github.com/lxc/lxd/shared/version"
)

func containerSnapshotsGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	cname := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, cname, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	recursion := util.IsRecursionRequest(r)
	resultString := []string{}
	resultMap := []*api.InstanceSnapshot{}

	if !recursion {
		snaps, err := d.cluster.GetInstanceSnapshotsNames(projectName, cname)
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap)
			if projectName == project.Default {
				url := fmt.Sprintf("/%s/instances/%s/snapshots/%s", version.APIVersion, cname, snapName)
				resultString = append(resultString, url)
			} else {
				url := fmt.Sprintf("/%s/instances/%s/snapshots/%s?project=%s", version.APIVersion, cname, snapName, projectName)
				resultString = append(resultString, url)
			}
		}
	} else {
		c, err := instance.LoadByProjectAndName(d.State(), projectName, cname)
		if err != nil {
			return response.SmartError(err)
		}

		snaps, err := c.Snapshots()
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			render, _, err := snap.Render(storagePools.RenderSnapshotUsage(d.State(), snap))
			if err != nil {
				continue
			}

			resultMap = append(resultMap, render.(*api.InstanceSnapshot))
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

func containerSnapshotsPost(d *Daemon, r *http.Request) response.Response {
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

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	inst, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.InstanceSnapshotsPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		req.Name, err = containerDetermineNextSnapshotName(d, inst, "snap%d")
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Validate the name
	err = validate.IsURLSegmentSafe(req.Name)
	if err != nil {
		return response.BadRequest(errors.Wrap(err, "Invalid snapshot name"))
	}

	fullName := name +
		shared.SnapshotDelimiter +
		req.Name

	var expiry time.Time
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = shared.GetSnapshotExpiry(time.Now(), inst.ExpandedConfig()["snapshots.expiry"])
		if err != nil {
			return response.BadRequest(err)
		}
	}

	snapshot := func(op *operations.Operation) error {
		args := db.InstanceArgs{
			Project:      inst.Project(),
			Architecture: inst.Architecture(),
			Config:       inst.LocalConfig(),
			Type:         inst.Type(),
			Snapshot:     true,
			Devices:      inst.LocalDevices(),
			Ephemeral:    inst.IsEphemeral(),
			Name:         fullName,
			Profiles:     inst.Profiles(),
			Stateful:     req.Stateful,
			ExpiryDate:   expiry,
		}

		_, err := instanceCreateAsSnapshot(d.State(), args, inst, op)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}
	resources["containers"] = resources["instances"]

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationSnapshotCreate, resources, nil, snapshot, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func containerSnapshotHandler(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	containerName := mux.Vars(r)["name"]
	snapshotName := mux.Vars(r)["snapshotName"]

	resp, err := forwardedResponseIfInstanceIsRemote(d, r, project, containerName, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	snapshotName, err = url.QueryUnescape(snapshotName)
	if err != nil {
		return response.SmartError(err)
	}
	inst, err := instance.LoadByProjectAndName(
		d.State(),
		project, containerName+
			shared.SnapshotDelimiter+
			snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(d.State(), inst, snapshotName)
	case "POST":
		return snapshotPost(d, r, inst, containerName)
	case "DELETE":
		return snapshotDelete(d.State(), inst, snapshotName)
	case "PUT":
		return snapshotPut(d, r, inst, snapshotName)
	default:
		return response.NotFound(fmt.Errorf("Method '%s' not found", r.Method))
	}
}

func snapshotPut(d *Daemon, r *http.Request, sc instance.Instance, name string) response.Response {
	// Validate the ETag
	etag := []interface{}{sc.ExpiryDate()}
	err := util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	rj := shared.Jmap{}

	err = json.NewDecoder(r.Body).Decode(&rj)
	if err != nil {
		return response.InternalError(err)
	}

	var do func(op *operations.Operation) error

	_, err = rj.GetString("expires_at")
	if err != nil {
		// Skip updating the snapshot since the requested key wasn't provided
		do = func(op *operations.Operation) error {
			return nil
		}
	} else {
		body, err := json.Marshal(rj)
		if err != nil {
			return response.InternalError(err)
		}

		configRaw := api.InstanceSnapshotPut{}

		err = json.Unmarshal(body, &configRaw)
		if err != nil {
			return response.BadRequest(err)
		}

		// Update container configuration
		do = func(op *operations.Operation) error {
			args := db.InstanceArgs{
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

	op, err := operations.OperationCreate(d.State(), sc.Project(), operations.OperationClassTask, opType, resources, nil,
		do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func snapshotGet(s *state.State, snapInst instance.Instance, name string) response.Response {
	render, _, err := snapInst.Render(storagePools.RenderSnapshotUsage(s, snapInst))
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, render.(*api.InstanceSnapshot))
}

func snapshotPost(d *Daemon, r *http.Request, sc instance.Instance, containerName string) response.Response {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))

	raw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&raw); err != nil {
		return response.BadRequest(err)
	}

	migration, err := raw.GetBool("migration")
	if err == nil && migration {
		rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))
		rdr3 := ioutil.NopCloser(bytes.NewBuffer(body))

		req := api.InstancePost{}
		err = json.NewDecoder(rdr2).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		reqNew := api.InstanceSnapshotPost{}
		err = json.NewDecoder(rdr3).Decode(&reqNew)
		if err != nil {
			return response.BadRequest(err)
		}

		if reqNew.Name == "" {
			return response.BadRequest(fmt.Errorf(`A new name for the ` +
				`container must be provided`))
		}

		if reqNew.Live {
			sourceName, _, _ := shared.InstanceGetParentAndSnapshotName(containerName)
			if sourceName != reqNew.Name {
				return response.BadRequest(fmt.Errorf(`Copying `+
					`stateful containers requires that `+
					`source "%s" and `+`target "%s" name `+
					`be identical`, sourceName, reqNew.Name))
			}
		}

		ws, err := newMigrationSource(sc, reqNew.Live, true)
		if err != nil {
			return response.SmartError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{containerName}

		run := func(op *operations.Operation) error {
			return ws.Do(d.State(), op)
		}

		if req.Target != nil {
			// Push mode
			err := ws.ConnectContainerTarget(*req.Target)
			if err != nil {
				return response.InternalError(err)
			}

			op, err := operations.OperationCreate(d.State(), sc.Project(), operations.OperationClassTask, db.OperationSnapshotTransfer, resources, nil, run, nil, nil)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode
		op, err := operations.OperationCreate(d.State(), sc.Project(), operations.OperationClassWebsocket, db.OperationSnapshotTransfer, resources, ws.Metadata(), run, nil, ws.Connect)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the name
	err = validate.IsURLSegmentSafe(newName)
	if err != nil {
		return response.BadRequest(errors.Wrap(err, "Invalid snapshot name"))
	}

	fullName := containerName + shared.SnapshotDelimiter + newName

	// Check that the name isn't already in use
	id, _ := d.cluster.GetInstanceSnapshotID(sc.Project(), containerName, newName)
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name '%s' already in use", fullName))
	}

	rename := func(op *operations.Operation) error {
		return sc.Rename(fullName)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{containerName}

	op, err := operations.OperationCreate(d.State(), sc.Project(), operations.OperationClassTask, db.OperationSnapshotRename, resources, nil, rename, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func snapshotDelete(s *state.State, sc instance.Instance, name string) response.Response {
	remove := func(op *operations.Operation) error {
		return sc.Delete(false)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{sc.Name()}

	op, err := operations.OperationCreate(s, sc.Project(), operations.OperationClassTask, db.OperationSnapshotDelete, resources, nil, remove, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
