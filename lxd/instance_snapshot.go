package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
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

// swagger:operation GET /1.0/instances/{name}/snapshots instances instance_snapshots_get
//
// Get the snapshots
//
// Returns a list of instance snapshots (URLs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/instances/foo/snapshots/snap0",
//               "/1.0/instances/foo/snapshots/snap1"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/snapshots?recursion=1 instances instance_snapshots_get_recursion1
//
// Get the snapshots
//
// Returns a list of instance snapshots (structs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of instance snapshots
//           items:
//             $ref: "#/definitions/InstanceSnapshot"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceSnapshotsGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	cname, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(cname) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

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
		snaps, err := d.db.Cluster.GetInstanceSnapshotsNames(projectName, cname)
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			_, snapName, _ := api.GetParentAndSnapshotName(snap)
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

// swagger:operation POST /1.0/instances/{name}/snapshots instances instance_snapshots_post
//
// Create a snapshot
//
// Creates a new snapshot.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: snapshot
//     description: Snapshot request
//     required: false
//     schema:
//       $ref: "#/definitions/InstanceSnapshotsPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceSnapshotsPost(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(context.Background(), tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err := dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		err = project.AllowSnapshotCreation(p)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
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
	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.InstanceSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		req.Name, err = instance.NextSnapshotName(d.State(), inst, "snap%d")
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Validate the name
	err = validate.IsURLSegmentSafe(req.Name)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid snapshot name: %w", err))
	}

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
		inst.SetOperation(op)
		return inst.Snapshot(req.Name, expiry, req.Stateful)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, operationtype.SnapshotCreate, resources, nil, snapshot, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func instanceSnapshotHandler(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	containerName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, containerName, instanceType)
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

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, containerName+shared.SnapshotDelimiter+snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(d.State(), inst, snapshotName)
	case "POST":
		return snapshotPost(d, r, inst, containerName)
	case "DELETE":
		return snapshotDelete(d.State(), r, inst, snapshotName)
	case "PUT":
		return snapshotPut(d, r, inst, snapshotName)
	case "PATCH":
		return snapshotPatch(d, r, inst, snapshotName)
	default:
		return response.NotFound(fmt.Errorf("Method %q not found", r.Method))
	}
}

// swagger:operation PATCH /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_patch
//
// Partially update snapshot
//
// Updates a subset of the snapshot config.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: snapshot
//     description: Snapshot update
//     required: false
//     schema:
//       $ref: "#/definitions/InstanceSnapshotPut"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func snapshotPatch(d *Daemon, r *http.Request, snapInst instance.Instance, name string) response.Response {
	// Only expires_at is currently editable, so PATCH is equivalent to PUT.
	return snapshotPut(d, r, snapInst, name)
}

// swagger:operation PUT /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_put
//
// Update snapshot
//
// Updates the snapshot config.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: snapshot
//     description: Snapshot update
//     required: false
//     schema:
//       $ref: "#/definitions/InstanceSnapshotPut"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func snapshotPut(d *Daemon, r *http.Request, snapInst instance.Instance, name string) response.Response {
	// Validate the ETag
	etag := []any{snapInst.ExpiryDate()}
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

		// Update instance configuration
		do = func(op *operations.Operation) error {
			args := db.InstanceArgs{
				Architecture: snapInst.Architecture(),
				Config:       snapInst.LocalConfig(),
				Description:  snapInst.Description(),
				Devices:      snapInst.LocalDevices(),
				Ephemeral:    snapInst.IsEphemeral(),
				Profiles:     snapInst.Profiles(),
				Project:      snapInst.Project().Name,
				ExpiryDate:   configRaw.ExpiresAt,
				Type:         snapInst.Type(),
				Snapshot:     snapInst.IsSnapshot(),
			}

			err = snapInst.Update(args, false)
			if err != nil {
				return err
			}

			return nil
		}
	}

	opType := operationtype.SnapshotUpdate

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if snapInst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), snapInst.Project().Name, operations.OperationClassTask, opType, resources, nil, do, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_get
//
// Get the snapshot
//
// Gets a specific instance snapshot.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     description: Instance snapshot
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           $ref: "#/definitions/InstanceSnapshot"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func snapshotGet(s *state.State, snapInst instance.Instance, name string) response.Response {
	render, _, err := snapInst.Render(storagePools.RenderSnapshotUsage(s, snapInst))
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{snapInst.ExpiryDate()}
	return response.SyncResponseETag(true, render.(*api.InstanceSnapshot), etag)
}

// swagger:operation POST /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_post
//
// Rename or move/migrate a snapshot
//
// Renames or migrates an instance snapshot to another server.
//
// The returned operation metadata will vary based on what's requested.
// For rename or move within the same server, this is a simple background operation with progress data.
// For migration, in the push case, this will similarly be a background
// operation with progress data, for the pull case, it will be a websocket
// operation with a number of secrets to be passed to the target server.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: snapshot
//     description: Snapshot migration
//     required: false
//     schema:
//       $ref: "#/definitions/InstanceSnapshotPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func snapshotPost(d *Daemon, r *http.Request, snapInst instance.Instance, containerName string) response.Response {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))

	raw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&raw)
	if err != nil {
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
			sourceName, _, _ := api.GetParentAndSnapshotName(containerName)
			if sourceName != reqNew.Name {
				return response.BadRequest(fmt.Errorf(`Copying `+
					`stateful containers requires that `+
					`source "%s" and `+`target "%s" name `+
					`be identical`, sourceName, reqNew.Name))
			}
		}

		ws, err := newMigrationSource(snapInst, reqNew.Live, true, false)
		if err != nil {
			return response.SmartError(err)
		}

		resources := map[string][]string{}
		resources["instances"] = []string{containerName}

		if snapInst.Type() == instancetype.Container {
			resources["containers"] = resources["instances"]
		}

		run := func(op *operations.Operation) error {
			return ws.Do(d.State(), op)
		}

		if req.Target != nil {
			// Push mode
			err := ws.ConnectContainerTarget(*req.Target)
			if err != nil {
				return response.InternalError(err)
			}

			op, err := operations.OperationCreate(d.State(), snapInst.Project().Name, operations.OperationClassTask, operationtype.SnapshotTransfer, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode
		op, err := operations.OperationCreate(d.State(), snapInst.Project().Name, operations.OperationClassWebsocket, operationtype.SnapshotTransfer, resources, ws.Metadata(), run, nil, ws.Connect, r)
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
		return response.BadRequest(fmt.Errorf("Invalid snapshot name: %w", err))
	}

	fullName := containerName + shared.SnapshotDelimiter + newName

	// Check that the name isn't already in use
	id, _ := d.db.Cluster.GetInstanceSnapshotID(snapInst.Project().Name, containerName, newName)
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name '%s' already in use", fullName))
	}

	rename := func(op *operations.Operation) error {
		return snapInst.Rename(fullName, false)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{containerName}

	if snapInst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), snapInst.Project().Name, operations.OperationClassTask, operationtype.SnapshotRename, resources, nil, rename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_delete
//
// Delete a snapshot
//
// Deletes the instance snapshot.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func snapshotDelete(s *state.State, r *http.Request, snapInst instance.Instance, name string) response.Response {
	remove := func(op *operations.Operation) error {
		return snapInst.Delete(false)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{snapInst.Name()}

	if snapInst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, snapInst.Project().Name, operations.OperationClassTask, operationtype.SnapshotDelete, resources, nil, remove, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
