package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

var operationCmd = APIEndpoint{
	Path: "operations/{id}",

	Delete: APIEndpointAction{Handler: operationDelete, AccessHandler: AllowAuthenticated},
	Get:    APIEndpointAction{Handler: operationGet, AccessHandler: AllowAuthenticated},
}

var operationsCmd = APIEndpoint{
	Path: "operations",

	Get: APIEndpointAction{Handler: operationsGet, AccessHandler: AllowAuthenticated},
}

var operationWait = APIEndpoint{
	Path: "operations/{id}/wait",

	Get: APIEndpointAction{Handler: operationWaitGet, AccessHandler: AllowAuthenticated},
}

var operationWebsocket = APIEndpoint{
	Path: "operations/{id}/websocket",

	Get: APIEndpointAction{Handler: operationWebsocketGet, AllowUntrusted: true},
}

// API functions
func operationGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		_, body, err = op.Render()
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, body)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		operation, err := tx.OperationByUUID(id)
		if err != nil {
			return err
		}

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return response.SmartError(err)
	}

	body, _, err = client.GetOperation(id)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, body)
}

func operationDelete(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		if op.Permission() != "" {
			project := op.Project()
			if project == "" {
				project = "default"
			}

			if !d.userHasPermission(r, project, op.Permission()) {
				return response.Forbidden(nil)
			}
		}

		_, err = op.Cancel()
		if err != nil {
			return response.BadRequest(err)
		}

		return response.EmptySyncResponse
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		operation, err := tx.OperationByUUID(id)
		if err != nil {
			return err
		}

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return response.SmartError(err)
	}

	err = client.DeleteOperation(id)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func operationsGet(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	recursion := util.IsRecursionRequest(r)

	localOperationURLs := func() (shared.Jmap, error) {
		// Get all the operations
		operations.Lock()
		localOps := operations.Operations()
		operations.Unlock()

		// Build a list of URLs
		body := shared.Jmap{}

		for _, v := range localOps {
			if v.Project() != "" && v.Project() != project {
				continue
			}
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
		operations.Lock()
		localOps := operations.Operations()
		operations.Unlock()

		// Build a list of operations
		body := shared.Jmap{}

		for _, v := range localOps {
			if v.Project() != "" && v.Project() != project {
				continue
			}
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

	// Check if called from a cluster node
	if isClusterNotification(r) {
		// Only return the local data
		if recursion {
			// Recursive queries
			body, err := localOperations()
			if err != nil {
				return response.InternalError(err)
			}

			return response.SyncResponse(true, body)
		}

		// Normal queries
		body, err := localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}

		return response.SyncResponse(true, body)
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

	// Check if clustered
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.InternalError(err)
	}

	// Return now if not clustered
	if !clustered {
		return response.SyncResponse(true, md)
	}

	// Get all nodes with running operations in this project.
	var nodes []string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		nodes, err = tx.OperationNodes(project)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get local address
	localAddress, err := node.HTTPSAddress(d.db)
	if err != nil {
		return response.InternalError(err)
	}

	cert := d.endpoints.NetworkCert()
	for _, node := range nodes {
		if node == localAddress {
			continue
		}

		// Connect to the remote server
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return response.SmartError(err)
		}

		// Get operation data
		ops, err := client.GetOperations()
		if err != nil {
			return response.SmartError(err)
		}

		// Merge with existing data
		for _, op := range ops {
			status := strings.ToLower(op.Status)

			_, ok := md[status]
			if !ok {
				if recursion {
					md[status] = make([]*api.Operation, 0)
				} else {
					md[status] = make([]string, 0)
				}
			}

			if recursion {
				md[status] = append(md[status].([]*api.Operation), &op)
			} else {
				md[status] = append(md[status].([]string), fmt.Sprintf("/1.0/operations/%s", op.ID))
			}
		}
	}

	return response.SyncResponse(true, md)
}

func operationWaitGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	timeout, err := shared.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return response.InternalError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		_, err = op.WaitFinal(timeout)
		if err != nil {
			return response.InternalError(err)
		}

		_, body, err := op.Render()
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, body)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		operation, err := tx.OperationByUUID(id)
		if err != nil {
			return err
		}

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return response.SmartError(err)
	}

	_, body, err := client.GetOperationWait(id, timeout)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, body)
}

type operationWebSocket struct {
	req *http.Request
	op  *operations.Operation
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

	return md.ID
}

type forwardedOperationWebSocket struct {
	req    *http.Request
	id     string
	source *websocket.Conn // Connection to the node were the operation is running
}

func (r *forwardedOperationWebSocket) Render(w http.ResponseWriter) error {
	target, err := shared.WebsocketUpgrader.Upgrade(w, r.req, nil)
	if err != nil {
		return err
	}
	<-shared.WebsocketProxy(r.source, target)
	return nil
}

func (r *forwardedOperationWebSocket) String() string {
	return r.id
}

func operationWebsocketGet(d *Daemon, r *http.Request) response.Response {
	id := mux.Vars(r)["id"]

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		return &operationWebSocket{r, op}
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	secret := r.FormValue("secret")
	if secret == "" {
		return response.BadRequest(fmt.Errorf("missing secret"))
	}

	var address string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		operation, err := tx.OperationByUUID(id)
		if err != nil {
			return err
		}

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return response.SmartError(err)
	}

	logger.Debugf("Forward operation websocket from node %s", address)
	source, err := client.GetOperationWebsocket(id, secret)
	if err != nil {
		return response.SmartError(err)
	}

	return &forwardedOperationWebSocket{r, id, source}
}
