package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

// swagger:operation PUT /1.0/instances/{name} instances instance_put
//
//	Update the instance
//
//	Updates the instance configuration or trigger a snapshot restore.
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
//	    name: instance
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/InstancePut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancePut(d *Daemon, r *http.Request) response.Response {
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

	s := d.State()
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)

	// Get the container
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	revert := revert.New()
	defer revert.Fail()

	unlock, err := instanceOperationLock(s.ShutdownCtx, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Add(func() {
		unlock()
	})

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{inst.Architecture(), inst.LocalConfig(), inst.LocalDevices(), inst.IsEphemeral(), inst.Profiles()}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	configRaw := api.InstancePut{}
	err = json.NewDecoder(r.Body).Decode(&configRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	architecture, err := osarch.ArchitectureId(configRaw.Architecture)
	if err != nil {
		architecture = 0
	}

	var do func(context.Context, *operations.Operation) error
	var opType operationtype.Type
	if configRaw.Restore == "" {
		// Check project limits.
		apiProfiles := make([]api.Profile, 0, len(configRaw.Profiles))
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			profiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, configRaw.Profiles)
			if err != nil {
				return err
			}

			profileConfigs, err := cluster.GetConfig(ctx, tx.Tx(), "profile")
			if err != nil {
				return err
			}

			profileDevices, err := cluster.GetDevices(ctx, tx.Tx(), "profile")
			if err != nil {
				return err
			}

			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
				if err != nil {
					return err
				}

				apiProfiles = append(apiProfiles, *apiProfile)
			}

			return limits.AllowInstanceUpdate(ctx, s.GlobalConfig, tx, projectName, name, configRaw, inst.LocalConfig())
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Update container configuration
		do = func(_ context.Context, _ *operations.Operation) error {
			defer unlock()

			args := db.InstanceArgs{
				Architecture: architecture,
				Config:       configRaw.Config,
				Description:  configRaw.Description,
				Devices:      deviceConfig.NewDevices(configRaw.Devices),
				Ephemeral:    configRaw.Ephemeral,
				Profiles:     apiProfiles,
				Project:      projectName,
			}

			err = inst.Update(args, true)
			if err != nil {
				return err
			}

			return nil
		}

		opType = operationtype.InstanceUpdate
	} else {
		// Snapshot Restore
		do = func(_ context.Context, _ *operations.Operation) error {
			defer unlock()

			return instanceSnapRestore(s, projectName, name, configRaw)
		}

		opType = operationtype.SnapshotRestore
	}

	resources := map[entity.Type][]api.URL{
		entity.TypeInstance: {*api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName)},
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName),
		Type:        opType,
		Class:       operations.OperationClassTask,
		Resources:   resources,
		RunHook:     do,
	}

	op, err := operations.CreateUserOperation(s, requestor, args)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}

func instanceSnapRestore(s *state.State, projectName string, name string, req api.InstancePut) error {
	// normalize snapshot name
	snap := req.Restore
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
			return api.NewStatusError(http.StatusBadRequest, "Snapshot "+snap+" does not exist")
		default:
			return err
		}
	}

	// Load the project to validate features.
	var p *api.Project
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		project, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = project.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// When "features.storage.volumes" is disabled, only allow root disk to be restored.
	if shared.IsFalse(p.Config["features.storage.volumes"]) && req.RestoreDiskVolumesMode == api.DiskVolumesModeAllExclusive {
		return errors.New("Project does not have features.storage.volumes enabled")
	}

	// Generate a new `volatile.uuid.generation` to differentiate this instance restored from a snapshot from the original instance.
	source.LocalConfig()["volatile.uuid.generation"] = uuid.New().String()

	err = inst.Restore(source, req.Stateful, req.RestoreDiskVolumesMode)
	if err != nil {
		return err
	}

	return nil
}
