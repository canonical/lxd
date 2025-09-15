package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// swagger:operation POST /1.0/instances/{name}/rebuild instances instance_rebuild_post
//
//	Rebuild an instance
//
//	Rebuild an instance using an alternate image or as empty.
//	---
//	consumes:
//	  - application/octet-stream
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
//	    description: InstanceRebuild request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/InstanceRebuildPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceRebuildPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	targetProjectName := request.ProjectParam(r)

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, targetProjectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
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
			return fmt.Errorf("Failed loading project %q: %w", targetProjectName, err)
		}

		targetProject, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		dbInst, err := dbCluster.GetInstance(ctx, tx.Tx(), targetProject.Name, name)
		if err != nil {
			return fmt.Errorf("Failed loading instance: %w", err)
		}

		if req.Source.Type != api.SourceTypeNone {
			sourceImage, err = getSourceImageFromInstanceSource(ctx, s, tx, targetProject.Name, req.Source, &sourceImageRef, dbInst.Type.String())
			if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
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
		return response.BadRequest(errors.New("Instance must be stopped to be rebuilt"))
	}

	run := func(op *operations.Operation) error {
		if req.Source.Type == api.SourceTypeNone {
			return instanceRebuildFromEmpty(inst, op)
		}

		if req.Source.Server != "" {
			sourceImage, err = ensureDownloadedImageFitWithinBudget(r.Context(), s, op, *targetProject, sourceImageRef, req.Source, inst.Type().String())
			if err != nil {
				return err
			}
		}

		if sourceImage == nil {
			return errors.New("Image not provided for instance rebuild")
		}

		return instanceRebuildFromImage(r.Context(), s, inst, sourceImage, op)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(r.Context(), s, targetProject.Name, operations.OperationClassTask, operationtype.InstanceRebuild, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
