package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
)

func operationAPIGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	op, err := operation.Get(id)
	if err != nil {
		return response.NotFound
	}

	_, body, err := op.Render()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, body)
}

func operationAPIDelete(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	op, err := operation.Get(id)
	if err != nil {
		return response.NotFound
	}

	_, err = op.Cancel()
	if err != nil {
		return response.BadRequest(err)
	}

	return response.EmptySyncResponse
}

var operationCmd = Command{name: "operations/{id}", get: operationAPIGet, delete: operationAPIDelete}

func operationsAPIGet(d *Daemon, r *http.Request) response.Response {
	var md shared.Jmap

	recursion := d.isRecursionRequest(r)

	md = shared.Jmap{}

	for _, v := range operation.Operations() {
		status := strings.ToLower(v.Status().String())
		_, ok := md[status]
		if !ok {
			if recursion {
				md[status] = make([]*shared.Operation, 0)
			} else {
				md[status] = make([]string, 0)
			}
		}

		if !recursion {
			md[status] = append(md[status].([]string), v.Url())
			continue
		}

		_, body, err := v.Render()
		if err != nil {
			continue
		}

		md[status] = append(md[status].([]*shared.Operation), body)
	}

	return response.SyncResponse(true, md)
}

var operationsCmd = Command{name: "operations", get: operationsAPIGet}

func operationAPIWaitGet(d *Daemon, r *http.Request) response.Response {
	timeout, err := shared.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return response.InternalError(err)
	}

	id := mux.Vars(r)["id"]
	op, err := operation.Get(id)
	if err != nil {
		return response.NotFound
	}

	_, err = op.WaitFinal(timeout)
	if err != nil {
		return response.InternalError(err)
	}

	_, body, err := op.Render()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, body)
}

var operationWait = Command{name: "operations/{id}/wait", get: operationAPIWaitGet}

type operationWebSocket struct {
	req *http.Request
	op  *operation.Operation
}

func (r *operationWebSocket) Render(w http.ResponseWriter) error {
	chanErr, err := r.op.Connect(r.req, w)
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

	return md.Id
}

func operationAPIWebsocketGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]
	op, err := operation.Get(id)
	if err != nil {
		return response.NotFound
	}

	return &operationWebSocket{r, op}
}

var operationWebsocket = Command{name: "operations/{id}/websocket", untrustedGet: true, get: operationAPIWebsocketGet}
