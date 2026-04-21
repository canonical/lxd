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
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
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

		if req.Source.Type == "image" && req.Source.ImageRegistry != "" {
			if !project.RegistryAllowed(targetProject.Config, req.Source.ImageRegistry) {
				return api.StatusErrorf(http.StatusNotFound, "Image registry not found")
			}
		}

		dbInst, err := dbCluster.GetInstance(ctx, tx.Tx(), targetProject.Name, name)
		if err != nil {
			return fmt.Errorf("Failed loading instance: %w", err)
		}

		if req.Source.Type != api.SourceTypeNone {
			// Try to resolve the source image from cache and perform authorization checks.
			// This is needed to verify the caller has access to the image if it's from a different project,
			// and to retrieve the image's metadata.
			sourceImage, err = resolveSourceImageFromCache(r, s, tx, targetProject.Name, req.Source, &sourceImageRef, dbInst.Type.String())
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
		return response.BadRequest(errors.New("Instance must be stopped to be rebuilt"))
	}

	run := func(ctx context.Context, op *operations.Operation) error {
		if req.Source.Type == api.SourceTypeNone {
			return instanceRebuildFromEmpty(ctx, inst, op)
		}

		if req.Source.ImageRegistry != "" || req.Source.Project != "" {
			sourceImage, err = ensureDownloadedImageFitWithinBudget(ctx, s, op, *targetProject, sourceImageRef, req.Source, inst.Type().String())
			if err != nil {
				return err
			}
		}

		if sourceImage == nil {
			return errors.New("Image not provided for instance rebuild")
		}

		return instanceRebuildFromImage(ctx, s, inst, sourceImage, op)
	}

	args := operations.OperationArgs{
		ProjectName: targetProject.Name,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", name).Project(inst.Project().Name),
		Type:        operationtype.InstanceRebuild,
		Class:       operations.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
