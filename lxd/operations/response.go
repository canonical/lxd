package operations

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// Operation response.
type operationResponse struct {
	op *Operation
}

// OperationResponse returns an operation response.
func OperationResponse(op *Operation) response.Response {
	return &operationResponse{op}
}

// Render builds operationResponse and writes it to http.ResponseWriter.
func (r *operationResponse) Render(w http.ResponseWriter, req *http.Request) error {
	// Inject callback function on operation.
	// If the operation was completed as expected or cancelled by an user, it is considered a success.
	// Otherwise it is considered a failure.
	r.op.SetOnDone(func(op *Operation) {
		sc := op.Status()
		if sc == api.Success || sc == api.Cancelled {
			request.MetricsCallback(req, metrics.Success)
		} else {
			request.MetricsCallback(req, metrics.ErrorServer)
		}
	})

	err := r.op.Start()
	if err != nil {
		return err
	}

	url, md, err := r.op.Render()
	if err != nil {
		return err
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   md,
	}

	w.Header().Set("Location", url)

	code := 202
	w.WriteHeader(code)

	var debugLogger logger.Logger
	if debug {
		debugLogger = logger.AddContext(logger.Ctx{"http_code": code})
	}

	return util.WriteJSON(w, body, debugLogger)
}

func (r *operationResponse) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

// Forwarded operation response.
//
// Returned when the operation has been created on another node.
type forwardedOperationResponse struct {
	op      *api.Operation
	project string
}

// ForwardedOperationResponse creates a response that forwards the metadata of
// an operation created on another node.
func ForwardedOperationResponse(project string, op *api.Operation) response.Response {
	return &forwardedOperationResponse{
		op:      op,
		project: project,
	}
}

// Render builds forwardedOperationResponse and writes it to http.ResponseWriter.
func (r *forwardedOperationResponse) Render(w http.ResponseWriter, req *http.Request) error {
	url := fmt.Sprintf("/%s/operations/%s", version.APIVersion, r.op.ID)
	if r.project != "" {
		url += fmt.Sprintf("?project=%s", r.project)
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   r.op,
	}

	w.Header().Set("Location", url)

	code := 202
	w.WriteHeader(code)

	var debugLogger logger.Logger
	if debug {
		debugLogger = logger.AddContext(logger.Ctx{"http_code": code})
	}

	err := util.WriteJSON(w, body, debugLogger)

	if err == nil {
		// If there was an error on Render, the callback function will be called during the error handling.
		request.MetricsCallback(req, metrics.Success)
	}

	return err
}

func (r *forwardedOperationResponse) String() string {
	return r.op.ID
}
