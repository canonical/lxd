package lxd

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
)

var _ Operation = &devLXDOperation{}

// The Operation type represents an ongoing LXD operation (asynchronous processing).
type devLXDOperation struct {
	api.DevLXDOperation

	r *ProtocolDevLXD
}

// Cancel is a no-op.
func (op *devLXDOperation) Cancel() (err error) {
	return nil
}

// Get returns the available operation details as api.Operation.
func (op *devLXDOperation) Get() api.Operation {
	return api.Operation{
		ID:         op.ID,
		Status:     op.Status,
		StatusCode: op.StatusCode,
		Err:        op.Err,
	}
}

// Wait lets you wait until the operation reaches a final state.
func (op *devLXDOperation) Wait() error {
	return op.WaitContext(context.Background())
}

// WaitContext lets you wait until the operation reaches a final state with context.Context.
func (op *devLXDOperation) WaitContext(ctx context.Context) error {
	timeout := -1
	deadline, ok := ctx.Deadline()
	if ok {
		timeout = int(time.Until(deadline).Seconds())
	}

	opAPI, _, err := op.r.GetOperationWait(op.ID, timeout)
	if err != nil {
		return err
	}

	op.DevLXDOperation = *opAPI

	if opAPI.Err != "" {
		return errors.New(opAPI.Err)
	}

	return nil
}

// AddHandler is not implemented for devLXDOperation.
func (op *devLXDOperation) AddHandler(_ func(api.Operation)) (_ *EventTarget, err error) {
	return nil, errors.New("DevLXD operations do not support handlers")
}

// RemoveHandler is not implemented for devLXDOperation.
func (op *devLXDOperation) RemoveHandler(_ *EventTarget) (err error) {
	return errors.New("DevLXD operations do not support handlers")
}

// GetWebsocket is not implemented for devLXDOperation.
func (op *devLXDOperation) GetWebsocket(_ string) (_ *websocket.Conn, err error) {
	return nil, errors.New("DevLXD operations cannot provide websocket access")
}

// Refresh is not implemented for devLXDOperation.
func (op *devLXDOperation) Refresh() (err error) {
	return errors.New("DevLXD operations cannot be refreshed")
}

// GetOperationWait returns a DevLXDOperation entry for the provided uuid once it's complete or hits the timeout.
func (r *ProtocolDevLXD) GetOperationWait(uuid string, timeout int) (*api.DevLXDOperation, string, error) {
	var op api.DevLXDOperation

	etag, err := r.queryStruct(http.MethodGet, "/operations/"+url.PathEscape(uuid)+"/wait?timeout="+strconv.FormatInt(int64(timeout), 10), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}
