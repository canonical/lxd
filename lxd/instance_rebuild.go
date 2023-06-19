package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// swagger:operation POST /1.0/instances/{instance_name}/rebuild instances instance_post
// Rebuild an instance.
//
// ---
// consumes:
//   - application/octet-stream
//
// produces:
//   - application/json
//
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: instance
//     description: InstanceRebuild request
//     required: true
//     schema:
//     $ref: "#/definitions/InstanceRebuildPost"
//
// responses:
//
//	"202":
//	  $ref: "#/responses/Operation"
//	"400":
//	  $ref: "#/responses/BadRequest"
//	"403":
//	  $ref: "#/responses/Forbidden"
//	"404":
//	  $ref: "#/responses/NotFound"
//	"500":
//	  $ref: "#/responses/InternalServerError"
func instanceRebuildPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	targetProjectName := projectParam(r)

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Parse the request
	req := api.InstanceRebuildPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	var targetProject *api.Project
	var sourceImage *api.Image
	var inst instance.Instance
	var sourceImageRef string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), targetProjectName)
		if err != nil {
			return fmt.Errorf("Failed loading project: %w", err)
		}

		targetProject, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		dbInst, err := dbCluster.GetInstance(ctx, tx.Tx(), targetProject.Name, name)
		if err != nil {
			return fmt.Errorf("Failed loading instance: %w", err)
		}

		if req.Source.Type != "none" {
			sourceImage, err = getSourceImageFromInstanceSource(ctx, s, tx, targetProject.Name, req.Source, &sourceImageRef, dbInst.Type.String())
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	inst, err = instance.LoadByProjectAndName(s, targetProject.Name, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Instance must be stopped to be rebuilt"))
	}

	run := func(op *operations.Operation) error {
		if req.Source.Type == "none" {
			return instanceRebuildFromEmpty(s, inst, op)
		}

		if req.Source.Server != "" {
			sourceImage, err = ensureDownloadedImageFitWithinBudget(s, r, op, *targetProject, sourceImage, sourceImageRef, req.Source, inst.Type().String())
			if err != nil {
				return err
			}
		}

		if sourceImage == nil {
			return fmt.Errorf("Image not provided for instance rebuild")
		}

		return instanceRebuildFromImage(s, r, inst, sourceImage, op)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, targetProject.Name, operations.OperationClassTask, operationtype.InstanceRebuild, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
