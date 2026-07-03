package response

import (
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
	"github.com/canonical/lxd/shared/ws"
)

// Operation is an interface for a lxd or lxd-agent operation allowing [OperationResponse] and [OperationWebSocket] to be used for both.
type Operation interface {
	Render() (string, *api.Operation)
	Connect(r *http.Request, w http.ResponseWriter) (chan error, error)
}

// Operation response.
type operationResponse struct {
	op Operation
}

// OperationResponse returns an operation response.
func OperationResponse(op Operation) Response {
	return &operationResponse{op}
}

// Render builds operationResponse and writes it to http.ResponseWriter.
func (r *operationResponse) Render(w http.ResponseWriter, req *http.Request) error {
	url, md := r.op.Render()
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

// String implements [fmt.Stringer] for operationResponse.
func (r *operationResponse) String() string {
	_, md := r.op.Render()
	return md.ID
}

// Forwarded operation response.
//
// Returned when the operation has been created on another node.
type forwardedOperationResponse struct {
	op *api.Operation
}

// ForwardedOperationResponse creates a response that forwards the metadata of
// an operation created on another node.
func ForwardedOperationResponse(op *api.Operation) Response {
	return &forwardedOperationResponse{
		op: op,
	}
}

// Render builds forwardedOperationResponse and writes it to http.ResponseWriter.
func (r *forwardedOperationResponse) Render(w http.ResponseWriter, req *http.Request) error {
	url := api.NewURL().Path(version.APIVersion, "operations", r.op.ID).String()

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

	metrics.UseMetricsCallback(req, metrics.Success)

	return util.WriteJSON(w, body, debugLogger)
}

// String implements [fmt.Stringer] for forwardedOperationResponse.
func (r *forwardedOperationResponse) String() string {
	return r.op.ID
}

type operationWebSocket struct {
	op Operation
}

// OperationWebSocket returns a new websocket operation.
func OperationWebSocket(op Operation) Response {
	return &operationWebSocket{op}
}

// Render renders a websocket operation response.
func (r *operationWebSocket) Render(w http.ResponseWriter, req *http.Request) error {
	chanErr, err := r.op.Connect(req, w)
	if err != nil {
		return err
	}

	err = <-chanErr

	if err == nil {
		// If there was an error on Render, the callback function will be called during the error handling.
		metrics.UseMetricsCallback(req, metrics.Success)
	}

	return err
}

func (r *operationWebSocket) String() string {
	_, md := r.op.Render()
	return md.ID
}

type forwardedOperationWebSocket struct {
	id     string
	source *websocket.Conn // Connection to the node were the operation is running
}

// ForwardedOperationWebSocket returns a new forwarded websocket operation.
func ForwardedOperationWebSocket(id string, source *websocket.Conn) Response {
	return &forwardedOperationWebSocket{id, source}
}

// Render renders a forwarded websocket operation response.
func (r *forwardedOperationWebSocket) Render(w http.ResponseWriter, req *http.Request) error {
	// Upgrade target connection to websocket.
	target, err := ws.Upgrader.Upgrade(w, req, nil)
	if err != nil {
		return err
	}

	// Start proxying between sockets.
	<-ws.Proxy(r.source, target)

	// Make sure both sides are closed.
	_ = r.source.Close()
	_ = target.Close()

	// If there was an error on Render, the callback function will be called during the error handling.
	metrics.UseMetricsCallback(req, metrics.Success)

	return nil
}

// String implements [fmt.Stringer] for forwardedOperationWebsocket.
func (r *forwardedOperationWebSocket) String() string {
	return r.id
}
