package main

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var devLXDOperationWaitEndpoint = APIEndpoint{
	MetricsType: entity.TypeOperation,
	Path:        "operations/{id}/wait",
	Get:         APIEndpointAction{Handler: devLXDOperationsWaitHandler, AccessHandler: allowDevLXDAuthenticated},
}

var devLXDOperationEndpoint = APIEndpoint{
	MetricsType: entity.TypeOperation,
	Path:        "operations/{id}",
	Delete:      APIEndpointAction{Handler: devLXDOperationDeleteHandler, AccessHandler: allowDevLXDAuthenticated},
}

func devLXDOperationsWaitHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Allow access only to the project where current instance is running.
	projectName := inst.Project().Name
	opID := mux.Vars(r)["id"]

	// Determine the timeout based on the timeout query parameter and the request context's deadline.
	timeout := -1
	queryTimeout := r.FormValue("timeout")
	if queryTimeout != "" {
		timeout, err = strconv.Atoi(queryTimeout)
		if err != nil {
			return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Invalid timeout value"))
		}
	}

	// Wait for the operation to complete or timeout to be reached.
	url := api.NewURL().Path("1.0", "operations", opID).Project(projectName).WithQuery("timeout", strconv.FormatInt(int64(timeout), 10))
	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := operationWaitGet(d, req)
	op, err := response.NewResponseCapture(req).RenderToOperation(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	respOp := api.DevLXDOperation{
		ID:         op.ID,
		Status:     op.Status,
		StatusCode: op.StatusCode,
		Err:        op.Err,
	}

	return response.DevLXDResponse(http.StatusOK, respOp, "json")
}

func devLXDOperationDeleteHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Allow access only to the project where current instance is running.
	projectName := inst.Project().Name
	opID := mux.Vars(r)["id"]

	// Delete the operation.
	url := api.NewURL().Path("1.0", "operations", opID).Project(projectName)
	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := operationDelete(d, req)
	err = response.NewResponseCapture(req).Render(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}
