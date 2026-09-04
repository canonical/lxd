package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

var instanceNBDCmd = APIEndpoint{
	Path:            "instances/{name}/nbd",
	MetricsType:     entity.TypeInstance,
	ProjectSpecific: true,

	Get: APIEndpointAction{Handler: instanceNBDGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanConnectNBD, "name")},
}

// swagger:operation GET /1.0/instances/{name}/nbd instances instance_nbd_get
//
//	Get the instance NBD connection
//
//	Upgrades the request to a read-only NBD connection exporting every non-shared block disk of the instance,
//	each under an export named after its device name. The instance must be a running virtual machine.
//	The export runs as an operation on the cluster member serving it, which is listed and cancelled through
//	the operations API. Cancelling the operation closes the connection.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: nbd
//	    description: NBD export
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceNBDGet"
//	responses:
//	  "101":
//	    description: Switching protocols to NBD
//	    headers:
//	      Location:
//	        description: URL of the operation representing the export, set when the responding cluster member serves it
//	        type: string
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceNBDGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	instName := r.PathValue("name")
	if shared.IsSnapshot(instName) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	if r.Header.Get("Upgrade") != "nbd" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// An empty body selects the defaults.
	args := api.InstanceNBDGet{}
	err := json.NewDecoder(r.Body).Decode(&args)
	if err != nil && !errors.Is(err, io.EOF) {
		return response.BadRequest(err)
	}

	// Redirect to correct server if needed.
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
		conn, err := client.GetInstanceNBDConn(instName, args)
		if err != nil {
			return response.SmartError(err)
		}

		// The serving member runs the operation representing the export.
		return response.UpgradeResponse(conn, "nbd", nil)
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, instName)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("NBD export is only supported for virtual machines"))
	}

	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	conn, cleanup, err := pool.GetInstanceAllDisksNBD(inst, args.Reuse)
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
		ProjectName: projectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", instName).Project(projectName),
		Type:        operationtype.InstanceNBDExport,
		Class:       operationtype.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, opArgs)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return relay.Response(op)
}
