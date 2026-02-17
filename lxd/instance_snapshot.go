package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

// swagger:operation GET /1.0/instances/{name}/snapshots instances instance_snapshots_get
//
//  Get the snapshots
//
//  Returns a list of instance snapshots (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/instances/foo/snapshots/snap0",
//                "/1.0/instances/foo/snapshots/snap1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/snapshots?recursion=1 instances instance_snapshots_get_recursion1
//
//	Get the snapshots
//
//	Returns a list of instance snapshots (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of instance snapshots
//	          items:
//	            $ref: "#/definitions/InstanceSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceSnapshotsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	cname, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(cname) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, cname, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	canView, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeInstanceSnapshot)
	if err != nil {
		return response.SmartError(err)
	}

	recursion, _ := util.IsRecursionRequest(r)
	resultString := []string{}
	resultMap := []*api.InstanceSnapshot{}

	if recursion == 0 {
		var snaps []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			snaps, err = tx.GetInstanceSnapshotsNames(ctx, projectName, cname)

			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			_, snapName, _ := api.GetParentAndSnapshotName(snap)

			if !canView(entity.InstanceSnapshotURL(projectName, cname, snapName)) {
				continue
			}

			if projectName == api.ProjectDefaultName {
				resultString = append(resultString, api.NewURL().Path(version.APIVersion, "instances", cname, "snapshots", snapName).String())
			} else {
				resultString = append(resultString, api.NewURL().Path(version.APIVersion, "instances", cname, "snapshots", snapName).Project(projectName).String())
			}
		}
	} else {
		c, err := instance.LoadByProjectAndName(s, projectName, cname)
		if err != nil {
			return response.SmartError(err)
		}

		snaps, err := c.Snapshots()
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			_, snapName, _ := api.GetParentAndSnapshotName(snap.Name())

			if !canView(entity.InstanceSnapshotURL(projectName, cname, snapName)) {
				continue
			}

			render, _, err := snap.Render(storagePools.RenderSnapshotUsage(s, snap))
			if err != nil {
				continue
			}

			renderedSnap, ok := render.(*api.InstanceSnapshot)
			if !ok {
				return response.InternalError(errors.New("Render didn't return a snapshot"))
			}

			resultMap = append(resultMap, renderedSnap)
		}
	}

	if recursion == 0 {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/instances/{name}/snapshots instances instance_snapshots_post
//
//	Create a snapshot
//
//	Creates a new snapshot.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: snapshot
//	    description: Snapshot request
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceSnapshotsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	var p *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(context.Background(), tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		err = limits.AllowSnapshotCreation(p)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
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
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.InstanceSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// When "features.storage.volumes" is disabled, only allow root disk to be snapshotted.
	if shared.IsFalse(p.Config["features.storage.volumes"]) && req.DiskVolumesMode == api.DiskVolumesModeAllExclusive {
		return response.BadRequest(errors.New("Project does not have features.storage.volumes enabled"))
	}

	if req.Name == "" {
		req.Name, err = instance.NextSnapshotName(s, inst, "snap%d")
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Validate the name
	err = validate.IsURLSegmentSafe(req.Name)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid snapshot name: %w", err))
	}

	snapshot := func(ctx context.Context, op *operations.Operation) error {
		inst.SetOperation(op)
		return inst.Snapshot(req.Name, req.ExpiresAt, req.Stateful, req.DiskVolumesMode)
	}

	instanceURL := api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName)
	resources := map[entity.Type][]api.URL{
		entity.TypeInstance: {*instanceURL},
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   instanceURL,
		Type:        operationtype.SnapshotCreate,
		Class:       operations.OperationClassTask,
		Resources:   resources,
		RunHook:     snapshot,
		Metadata: map[string]any{
			operations.EntityURL: api.NewURL().Path(version.APIVersion, "instances", name, "snapshots", req.Name).Project(projectName).String(),
		},
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func instanceSnapshotHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	instName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, instName, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	snapInst, err := instance.LoadByProjectAndName(s, projectName, instName+shared.SnapshotDelimiter+snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(s, r, snapInst)
	case "POST":
		return snapshotPost(s, r, snapInst)
	case "DELETE":
		return snapshotDelete(s, r, snapInst)
	case "PUT":
		return snapshotPut(s, r, snapInst)
	case "PATCH":
		return snapshotPatch(s, r, snapInst)
	default:
		return response.NotFound(fmt.Errorf("Method %q not found", r.Method))
	}
}

// swagger:operation PATCH /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_patch
//
//	Partially update snapshot
//
//	Updates a subset of the snapshot config.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: snapshot
//	    description: Snapshot update
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotPut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotPatch(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	// Only expires_at is currently editable, so PATCH is equivalent to PUT.
	return snapshotPut(s, r, snapInst)
}

// swagger:operation PUT /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_put
//
//	Update snapshot
//
//	Updates the snapshot config.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: snapshot
//	    description: Snapshot update
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotPut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotPut(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
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

	var do func(ctx context.Context, op *operations.Operation) error

	_, err = rj.GetString("expires_at")
	if err != nil {
		// Skip updating the snapshot since the requested key wasn't provided
		do = func(_ context.Context, _ *operations.Operation) error {
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
		do = func(_ context.Context, _ *operations.Operation) error {
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
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapInst.Name())

	args := operations.OperationArgs{
		ProjectName: snapInst.Project().Name,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name),
		Type:        opType,
		Class:       operations.OperationClassTask,
		RunHook:     do,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_get
//
//	Get the snapshot
//
//	Gets a specific instance snapshot.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Instance snapshot
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/InstanceSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotGet(s *state.State, _ *http.Request, snapInst instance.Instance) response.Response {
	render, _, err := snapInst.Render(storagePools.RenderSnapshotUsage(s, snapInst))
	if err != nil {
		return response.SmartError(err)
	}

	renderedSnap, ok := render.(*api.InstanceSnapshot)
	if !ok {
		return response.InternalError(errors.New("Render didn't return a snapshot"))
	}

	etag := []any{snapInst.ExpiryDate()}
	return response.SyncResponseETag(true, renderedSnap, etag)
}

// swagger:operation POST /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_post
//
//	Rename or move/migrate a snapshot
//
//	Renames or migrates an instance snapshot to another server.
//
//	The returned operation metadata will vary based on what's requested.
//	For rename or move within the same server, this is a simple background operation with progress data.
//	For migration, in the push case, this will similarly be a background
//	operation with progress data, for the pull case, it will be a websocket
//	operation with a number of secrets to be passed to the target server.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: snapshot
//	    description: Snapshot migration
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotPost(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))

	raw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&raw)
	if err != nil {
		return response.BadRequest(err)
	}

	parentName, snapName, _ := api.GetParentAndSnapshotName(snapInst.Name())

	migration, err := raw.GetBool("migration")
	if err == nil && migration {
		rdr2 := io.NopCloser(bytes.NewBuffer(body))
		rdr3 := io.NopCloser(bytes.NewBuffer(body))

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
			return response.BadRequest(errors.New("A new name for the instance must be provided"))
		}

		if reqNew.Live {
			if parentName != reqNew.Name {
				return response.BadRequest(fmt.Errorf("Instance name cannot be changed during stateful copy (%q to %q)", parentName, reqNew.Name))
			}
		}

		ws, err := newMigrationSource(snapInst, reqNew.Live, true, false, "", req.Target)
		if err != nil {
			return response.SmartError(err)
		}

		resources := map[entity.Type][]api.URL{
			entity.TypeInstanceSnapshot: {*api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name)},
		}

		run := func(ctx context.Context, op *operations.Operation) error {
			return ws.Do(s, op)
		}

		if req.Target != nil {
			// Push mode.
			args := operations.OperationArgs{
				ProjectName: snapInst.Project().Name,
				EntityURL:   api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name),
				Type:        operationtype.SnapshotTransfer,
				Class:       operations.OperationClassTask,
				Resources:   resources,
				RunHook:     run,
			}

			op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode.
		args := operations.OperationArgs{
			ProjectName: snapInst.Project().Name,
			EntityURL:   api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name),
			Type:        operationtype.SnapshotTransfer,
			Class:       operations.OperationClassWebsocket,
			Resources:   resources,
			Metadata:    ws.Metadata(),
			RunHook:     run,
			ConnectHook: ws.Connect,
		}

		op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
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

	fullName := parentName + shared.SnapshotDelimiter + newName

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use
		id, _ := tx.GetInstanceSnapshotID(ctx, snapInst.Project().Name, parentName, newName)
		if id > 0 {
			return fmt.Errorf("Name %q already in use", fullName)
		}

		return nil
	})
	if err != nil {
		return response.Conflict(err)
	}

	rename := func(_ context.Context, _ *operations.Operation) error {
		return snapInst.Rename(fullName, false)
	}

	resources := map[entity.Type][]api.URL{
		entity.TypeInstanceSnapshot: {*api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name)},
	}

	args := operations.OperationArgs{
		ProjectName: snapInst.Project().Name,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name),
		Type:        operationtype.SnapshotRename,
		Class:       operations.OperationClassTask,
		Resources:   resources,
		RunHook:     rename,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_delete
//
//	Delete a snapshot
//
//	Deletes the instance snapshot.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: disk-volumes
//	    description: Which disk volumes to include in instance snapshot deletion. Possible values are "root" or "all-exclusive".
//	    type: string
//	    example: all-exclusive
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotDelete(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	diskVolumesMode := request.QueryParam(r, "disk-volumes")
	if diskVolumesMode == "" {
		diskVolumesMode = api.DiskVolumesModeRoot
	}

	remove := func(_ context.Context, _ *operations.Operation) error {
		return snapInst.Delete(false, diskVolumesMode)
	}

	parentName, snapName, _ := api.GetParentAndSnapshotName(snapInst.Name())
	args := operations.OperationArgs{
		ProjectName: snapInst.Project().Name,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName).Project(snapInst.Project().Name),
		Type:        operationtype.SnapshotDelete,
		Class:       operations.OperationClassTask,
		RunHook:     remove,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
