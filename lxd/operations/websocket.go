package operations

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/ws"
)

type operationWebSocket struct {
	op *Operation
}

// OperationWebSocket returns a new websocket operation.
func OperationWebSocket(op *Operation) response.Response {
	return &operationWebSocket{op}
}

// Render renders a websocket operation response.
func (r *operationWebSocket) Render(w http.ResponseWriter, req *http.Request) error {
	chanErr, err := r.op.Connect(req, w)
	if err != nil {
		return err
	}

	err = <-chanErr
	return err
}

func (r *operationWebSocket) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

type forwardedOperationWebSocket struct {
	id     string
	source *websocket.Conn // Connection to the node were the operation is running
}

// ForwardedOperationWebSocket returns a new forwarted websocket operation.
func ForwardedOperationWebSocket(id string, source *websocket.Conn) response.Response {
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

	return nil
}

func (r *forwardedOperationWebSocket) String() string {
	return r.id
}
