package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	projecthelpers "github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

// swagger:operation PUT /1.0/instances/{name} instances instance_put
//
// Update the instance
//
// Updates the instance configuration or trigger a snapshot restore.
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
//     name: instance
//     description: Update request
//     schema:
//       $ref: "#/definitions/InstancePut"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instancePut(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)

	// Get the container
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.NotFound(err)
	}

	// Validate the ETag
	etag := []interface{}{inst.Architecture(), inst.LocalConfig(), inst.LocalDevices(), inst.IsEphemeral(), inst.Profiles()}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	configRaw := api.InstancePut{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return response.BadRequest(err)
	}

	architecture, err := osarch.ArchitectureId(configRaw.Architecture)
	if err != nil {
		architecture = 0
	}

	var do func(*operations.Operation) error
	var opType db.OperationType
	if configRaw.Restore == "" {
		// Check project limits.
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			return projecthelpers.AllowInstanceUpdate(tx, projectName, name, configRaw, inst.LocalConfig())
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Update container configuration
		do = func(op *operations.Operation) error {
			args := db.InstanceArgs{
				Architecture: architecture,
				Config:       configRaw.Config,
				Description:  configRaw.Description,
				Devices:      deviceConfig.NewDevices(configRaw.Devices),
				Ephemeral:    configRaw.Ephemeral,
				Profiles:     configRaw.Profiles,
				Project:      projectName,
			}

			err = inst.Update(args, true)
			if err != nil {
				return err
			}

			return nil
		}

		opType = db.OperationInstanceUpdate
	} else {
		// Snapshot Restore
		do = func(op *operations.Operation) error {
			return instanceSnapRestore(d.State(), projectName, name, configRaw.Restore, configRaw.Stateful)
		}

		opType = db.OperationSnapshotRestore
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, opType, resources, nil, do, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func instanceSnapRestore(s *state.State, projectName string, name string, snap string, stateful bool) error {
	// normalize snapshot name
	if !shared.IsSnapshot(snap) {
		snap = name + shared.SnapshotDelimiter + snap
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return err
	}

	source, err := instance.LoadByProjectAndName(s, projectName, snap)
	if err != nil {
		switch {
		case response.IsNotFoundError(err):
			return fmt.Errorf("Snapshot %s does not exist", snap)
		default:
			return err
		}
	}

	err = inst.Restore(source, stateful)
	if err != nil {
		return err
	}

	return nil
}
