package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

var instanceSnapshotNBDCmd = APIEndpoint{
	Path:            "instances/{name}/snapshots/{snapshotName}/nbd",
	MetricsType:     entity.TypeInstance,
	ProjectSpecific: true,

	Get: APIEndpointAction{Handler: instanceSnapshotNBDGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanConnectNBD, "name")},
}

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshotName}/nbd instances instance_snapshot_nbd_get
//
//	Get the instance snapshot NBD connection
//
//	Upgrades the request to a read-only NBD connection exporting the block volume snapshots of the instance
//	snapshot, each under an export named after its disk device at the time of the snapshot, together with the
//	bitmaps of the snapshot.
//	The instance must be a virtual machine and the snapshot must have been created with a bitmap.
//	The export runs as an operation on the cluster member serving it, which is listed and cancelled through
//	the operations API. Cancelling the operation closes the connection.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: device
//	    description: Name of a disk device of the snapshot whose volume snapshot is exported, repeated once per device, all of them when absent
//	    type: string
//	    example: root
//	  - in: query
//	    name: previous_snapshot_uuid
//	    description: Instance snapshot UUID of the previous snapshot, which limits the exported bitmaps to the ones created with that snapshot
//	    type: string
//	    example: 5b1e7c2a-3d4f-4e6a-9b8c-7d6e5f4a3b2c
//	responses:
//	  "101":
//	    description: Switching protocols to NBD
//	    headers:
//	      Location:
//	        description: URL of the operation representing the export
//	        type: string
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceSnapshotNBDGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	instName := r.PathValue("name")
	if shared.IsSnapshot(instName) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	snapshotName := r.PathValue("snapshotName")

	if r.Header.Get("Upgrade") != "nbd" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// An empty list exports every device.
	deviceNames := r.URL.Query()["device"]
	previousSnapshotUUID := request.QueryParam(r, "previous_snapshot_uuid")

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward the request if the instance is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(r.Context(), s, projectName, instName, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		conn, operationURL, err := client.GetInstanceSnapshotNBDConn(instName, snapshotName, deviceNames, previousSnapshotUUID)
		if err != nil {
			return response.SmartError(err)
		}

		// The serving member runs the operation representing the export.
		return response.UpgradeResponse(conn, "nbd", operationURL, nil)
	}

	snapInst, err := instance.LoadByProjectAndName(s, projectName, instName+shared.SnapshotDelimiter+snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	if snapInst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("NBD export is only supported for virtual machines"))
	}

	pool, err := storagePools.LoadByInstance(s, snapInst)
	if err != nil {
		return response.SmartError(err)
	}

	conn, cleanup, conflictReference, err := pool.GetInstanceSnapshotNBD(snapInst, deviceNames, previousSnapshotUUID)
	if err != nil {
		return response.SmartError(err)
	}

	// The relay closes the connection and runs cleanup once the operation runs it.
	revert := revert.New()
	defer revert.Fail()
	revert.Add(func() {
		_ = conn.Close()
		cleanup()
	})

	relay := response.NewUpgradeRelay(conn, "nbd", cleanup)

	run := func(ctx context.Context, _ *operations.Operation) error {
		return relay.Run(ctx)
	}

	opArgs := operations.OperationArgs{
		ProjectName:       projectName,
		EntityURL:         api.NewURL().Path(version.APIVersion, "instances", instName).Project(projectName),
		Type:              operationtype.InstanceNBDExport,
		Class:             operationtype.OperationClassTask,
		RunHook:           run,
		ConflictReference: conflictReference,
		Metadata: map[string]any{
			api.MetadataEntityURL: api.NewURL().Path(version.APIVersion, "instances", instName, "snapshots", snapshotName).Project(projectName).String(),
		},
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, opArgs)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()
	return relay.Response(op)
}
