package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/db/warningtype"
	"github.com/lxc/lxd/lxd/filter"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var warningsCmd = APIEndpoint{
	Path: "warnings",

	Get: APIEndpointAction{Handler: warningsGet},
}

var warningCmd = APIEndpoint{
	Path: "warnings/{id}",

	Get:    APIEndpointAction{Handler: warningGet},
	Patch:  APIEndpointAction{Handler: warningPatch},
	Put:    APIEndpointAction{Handler: warningPut},
	Delete: APIEndpointAction{Handler: warningDelete},
}

func filterWarnings(warnings []api.Warning, clauses []filter.Clause) []api.Warning {
	filtered := []api.Warning{}

	for _, warning := range warnings {
		if !filter.Match(warning, clauses) {
			continue
		}

		filtered = append(filtered, warning)
	}

	return filtered
}

// swagger:operation GET /1.0/warnings warnings warnings_get
//
//  List the warnings
//
//  Returns a list of warnings.
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
//      description: Sync response
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
//                "/1.0/warnings/39c61a48-cc17-40ae-8248-4f7b4cadedf4",
//                "/1.0/warnings/951779a5-2820-4d96-b01e-88fe820e5310"
//              ]
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/warnings?recursion=1 warnings warnings_get_recursion1
//
//	Get the warnings
//
//	Returns a list of warnings (structs).
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
//	          description: List of warnings
//	          items:
//	            $ref: "#/definitions/Warning"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func warningsGet(d *Daemon, r *http.Request) response.Response {
	// Parse the recursion field
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Parse filter value
	var clauses []filter.Clause

	filterStr := r.FormValue("filter")
	if filterStr != "" {
		clauses, err = filter.Parse(filterStr)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to filter warnings: %w", err))
		}
	}

	// Parse the project field
	projectName := queryParam(r, "project")

	var warnings []api.Warning
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filters := []cluster.WarningFilter{}
		if projectName != "" {
			filter := cluster.WarningFilter{Project: &projectName}
			filters = append(filters, filter)
		}

		dbWarnings, err := cluster.GetWarnings(ctx, tx.Tx(), filters...)
		if err != nil {
			return fmt.Errorf("Failed to get warnings: %w", err)
		}

		warnings = make([]api.Warning, len(dbWarnings))
		for i, w := range dbWarnings {
			warning := w.ToAPI()
			warning.EntityURL, err = getWarningEntityURL(ctx, tx.Tx(), &w)
			if err != nil {
				return err
			}

			warnings[i] = warning
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		var resultList []string

		for _, w := range filterWarnings(warnings, clauses) {
			url := fmt.Sprintf("/%s/warnings/%s", version.APIVersion, w.UUID)
			resultList = append(resultList, url)
		}

		return response.SyncResponse(true, resultList)
	}

	// Return detailed list of warning
	return response.SyncResponse(true, filterWarnings(warnings, clauses))
}

// swagger:operation GET /1.0/warnings/{uuid} warnings warning_get
//
//	Get the warning
//
//	Gets a specific warning.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Warning
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
//	          $ref: "#/definitions/Warning"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func warningGet(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	var resp api.Warning
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbWarning, err := cluster.GetWarning(ctx, tx.Tx(), id)
		if err != nil {
			return err
		}

		resp = dbWarning.ToAPI()

		resp.EntityURL, err = getWarningEntityURL(ctx, tx.Tx(), dbWarning)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, resp)
}

// swagger:operation PATCH /1.0/warnings/{uuid} warnings warning_patch
//
//	Partially update the warning
//
//	Updates a subset of the warning status.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: warning
//	    description: Warning status
//	    required: true
//	    schema:
//	      $ref: "#/definitions/WarningPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func warningPatch(d *Daemon, r *http.Request) response.Response {
	return warningPut(d, r)
}

// swagger:operation PUT /1.0/warnings/{uuid} warnings warning_put
//
//	Update the warning
//
//	Updates the warning status.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: warning
//	    description: Warning status
//	    required: true
//	    schema:
//	      $ref: "#/definitions/WarningPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func warningPut(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.WarningPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Currently, we only allow changing the status to acknowledged or new.
	status, ok := warningtype.StatusTypes[req.Status]
	if !ok {
		// Invalid status
		return response.BadRequest(fmt.Errorf("Invalid warning type %q", req.Status))
	}

	if status != warningtype.StatusAcknowledged && status != warningtype.StatusNew {
		return response.Forbidden(fmt.Errorf(`Status may only be set to "acknowledge" or "new"`))
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.UpdateWarningStatus(id, status)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if status == warningtype.StatusAcknowledged {
		d.State().Events.SendLifecycle(project.Default, lifecycle.WarningAcknowledged.Event(id, request.CreateRequestor(r), nil))
	} else {
		d.State().Events.SendLifecycle(project.Default, lifecycle.WarningReset.Event(id, request.CreateRequestor(r), nil))
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/warnings/{uuid} warnings warning_delete
//
//	Delete the warning
//
//	Removes the warning.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func warningDelete(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := cluster.DeleteWarning(ctx, tx.Tx(), id)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	d.State().Events.SendLifecycle(project.Default, lifecycle.WarningDeleted.Event(id, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

func pruneResolvedWarningsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operations.Operation) error {
			return pruneResolvedWarnings(ctx, d)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, operationtype.WarningsPruneResolved, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start prune resolved warnings operation", logger.Ctx{"err": err})
			return
		}

		logger.Info("Pruning resolved warnings")
		err = op.Start()
		if err != nil {
			logger.Error("Failed to prune resolved warnings", logger.Ctx{"err": err})
		}

		_, _ = op.Wait(ctx)
		logger.Info("Done pruning resolved warnings")
	}

	return f, task.Daily()
}

func pruneResolvedWarnings(ctx context.Context, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Retrieve warnings by resolved status.
		statusResolved := warningtype.StatusResolved
		filter := cluster.WarningFilter{
			Status: &statusResolved,
		}

		warnings, err := cluster.GetWarnings(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed to get resolved warnings: %w", err)
		}

		for _, w := range warnings {
			// Delete the warning if it has been resolved for at least 24 hours
			if time.Since(w.UpdatedDate) >= 24*time.Hour {
				err = cluster.DeleteWarning(ctx, tx.Tx(), w.UUID)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to delete warnings: %w", err)
	}

	return nil
}

// getWarningEntityURL fetches the entity corresponding to the warning from the database, and generates a URL.
func getWarningEntityURL(ctx context.Context, tx *sql.Tx, warning *cluster.Warning) (string, error) {
	if warning.EntityID == -1 || warning.EntityTypeCode == -1 {
		return "", nil
	}

	_, ok := cluster.EntityNames[warning.EntityTypeCode]
	if !ok {
		return "", fmt.Errorf("Unknown entity type")
	}

	var url string
	switch warning.EntityTypeCode {
	case cluster.TypeImage:
		entities, err := cluster.GetImages(ctx, tx, cluster.ImageFilter{ID: &warning.EntityID})
		if err != nil {
			return "", err
		}

		if len(entities) == 0 {
			return "", db.ErrUnknownEntityID
		}

		apiImage := api.Image{Fingerprint: entities[0].Fingerprint}
		url = apiImage.URL(version.APIVersion, entities[0].Project).String()
	case cluster.TypeProfile:
		entities, err := cluster.GetProfiles(ctx, tx, cluster.ProfileFilter{ID: &warning.EntityID})
		if err != nil {
			return "", err
		}

		if len(entities) == 0 {
			return "", db.ErrUnknownEntityID
		}

		apiProfile := api.Profile{Name: entities[0].Name}
		url = apiProfile.URL(version.APIVersion, entities[0].Project).String()
	case cluster.TypeProject:
		entities, err := cluster.GetProjects(ctx, tx, cluster.ProjectFilter{ID: &warning.EntityID})
		if err != nil {
			return "", err
		}

		if len(entities) == 0 {
			return "", db.ErrUnknownEntityID
		}

		apiProject := api.Project{Name: entities[0].Name}
		url = apiProject.URL(version.APIVersion).String()
	case cluster.TypeCertificate:
		entities, err := cluster.GetCertificates(ctx, tx, cluster.CertificateFilter{ID: &warning.EntityID})
		if err != nil {
			return "", err
		}

		if len(entities) == 0 {
			return "", db.ErrUnknownEntityID
		}

		apiCertificate := api.Certificate{Fingerprint: entities[0].Fingerprint}
		url = apiCertificate.URL(version.APIVersion).String()
	case cluster.TypeContainer:
		fallthrough
	case cluster.TypeInstance:
		entities, err := cluster.GetInstances(ctx, tx, cluster.InstanceFilter{ID: &warning.EntityID})
		if err != nil {
			return "", err
		}

		if len(entities) == 0 {
			return "", db.ErrUnknownEntityID
		}

		apiInstance := api.Instance{Name: entities[0].Name}
		url = apiInstance.URL(version.APIVersion, entities[0].Project).String()
	}

	return url, nil
}
