package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/grant-he/lxd/lxd/operations"
	"github.com/grant-he/lxd/lxd/response"
	"github.com/grant-he/lxd/lxd/util"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
)

var operationCmd = APIEndpoint{
	Path: "operations/{id}",

	Delete: APIEndpointAction{Handler: operationDelete},
	Get:    APIEndpointAction{Handler: operationGet},
}

var operationsCmd = APIEndpoint{
	Path: "operations",

	Get: APIEndpointAction{Handler: operationsGet},
}

var operationWebsocket = APIEndpoint{
	Path: "operations/{id}/websocket",

	Get: APIEndpointAction{Handler: operationWebsocketGet},
}

func operationDelete(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = op.Cancel()
	if err != nil {
		return response.BadRequest(err)
	}

	return response.EmptySyncResponse
}

func operationGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]
	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	_, body, err = op.Render()
	if err != nil {
		log.Println(errors.Wrap(err, "Failed to handle operations request"))
	}

	return response.SyncResponse(true, body)
}

func operationsGet(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)

	localOperationURLs := func() (shared.Jmap, error) {
		// Get all the operations
		ops := operations.Clone()

		// Build a list of URLs
		body := shared.Jmap{}

		for _, v := range ops {
			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]string, 0)
			}

			body[status] = append(body[status].([]string), v.URL())
		}

		return body, nil
	}

	localOperations := func() (shared.Jmap, error) {
		// Get all the operations
		ops := operations.Clone()

		// Build a list of operations
		body := shared.Jmap{}

		for _, v := range ops {
			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]*api.Operation, 0)
			}

			_, op, err := v.Render()
			if err != nil {
				return nil, err
			}

			body[status] = append(body[status].([]*api.Operation), op)
		}

		return body, nil
	}

	// Start with local operations
	var md shared.Jmap
	var err error

	if recursion {
		md, err = localOperations()
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		md, err = localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}
	}

	return response.SyncResponse(true, md)
}

func operationWebsocketGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationWebSocket(r, op)
}
