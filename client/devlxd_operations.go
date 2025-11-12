package lxd

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/canonical/lxd/shared/api"
)

var _ DevLXDOperation = &devLXDOperation{}

// The Operation type represents an ongoing LXD operation (asynchronous processing).
type devLXDOperation struct {
	api.DevLXDOperation

	r *ProtocolDevLXD
}

// Get returns the available operation details as api.Operation.
func (op *devLXDOperation) Get() api.DevLXDOperation {
	return op.DevLXDOperation
}

// Cancel will request that LXD cancels the operation (if supported).
func (op *devLXDOperation) Cancel() (err error) {
	return op.r.DeleteOperation(op.ID)
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

// GetOperationWait returns a DevLXDOperation entry for the provided uuid once it's complete or hits the timeout.
func (r *ProtocolDevLXD) GetOperationWait(uuid string, timeout int) (*api.DevLXDOperation, string, error) {
	var op api.DevLXDOperation

	url := api.NewURL().Path("operations", url.PathEscape(uuid), "wait")
	url = url.WithQuery("timeout", strconv.FormatInt(int64(timeout), 10))

	etag, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// DeleteOperation deletes (cancels) a running operation.
func (r *ProtocolDevLXD) DeleteOperation(uuid string) error {
	url := api.NewURL().Path("operations", url.PathEscape(uuid))
	_, _, err := r.query(http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
