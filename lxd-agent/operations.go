package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
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

// operationDelete cancels a local LXD operation identified by the given ID.
func operationDelete(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

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

// operationGet retrieves information about a local LXD operation identified by the given ID.
func operationGet(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	_, body, err = op.Render()
	if err != nil {
		log.Println(fmt.Errorf("Failed to handle operations request: %w", err))
	}

	return response.SyncResponse(true, body)
}

// operationsGet retrieves information about local LXD operations based on the request recursion level.
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

// operationWebsocketGet establishes a WebSocket connection for the specified local LXD operation.
func operationWebsocketGet(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationWebSocket(r, op)
}
