package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var operationCmd = Command{
	name:   "operations/{id}",
	get:    operationAPIGet,
	delete: operationAPIDelete,
}

var operationsCmd = Command{
	name: "operations",
	get:  operationsAPIGet,
}

var operationWait = Command{
	name: "operations/{id}/wait",
	get:  operationAPIWaitGet,
}

var operationWebsocket = Command{
	name:         "operations/{id}/websocket",
	untrustedGet: true,
	get:          operationAPIWebsocketGet,
}

var operationsLock sync.Mutex
var operations map[string]*operation = make(map[string]*operation)

type operationClass int

const (
	operationClassTask      operationClass = 1
	operationClassWebsocket operationClass = 2
	operationClassToken     operationClass = 3
)

func (t operationClass) String() string {
	return map[operationClass]string{
		operationClassTask:      "task",
		operationClassWebsocket: "websocket",
		operationClassToken:     "token",
	}[t]
}

type operation struct {
	project     string
	id          string
	class       operationClass
	createdAt   time.Time
	updatedAt   time.Time
	status      api.StatusCode
	url         string
	resources   map[string][]string
	metadata    map[string]interface{}
	err         string
	readonly    bool
	canceler    *cancel.Canceler
	description string

	// Those functions are called at various points in the operation lifecycle
	onRun     func(*operation) error
	onCancel  func(*operation) error
	onConnect func(*operation, *http.Request, http.ResponseWriter) error

	// Channels used for error reporting and state tracking of background actions
	chanDone chan error

	// Locking for concurent access to the operation
	lock sync.Mutex

	cluster *db.Cluster
}

func (op *operation) done() {
	if op.readonly {
		return
	}

	op.lock.Lock()
	op.readonly = true
	op.onRun = nil
	op.onCancel = nil
	op.onConnect = nil
	close(op.chanDone)
	op.lock.Unlock()

	time.AfterFunc(time.Second*5, func() {
		operationsLock.Lock()
		_, ok := operations[op.id]
		if !ok {
			operationsLock.Unlock()
			return
		}

		delete(operations, op.id)
		operationsLock.Unlock()

		err := op.cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.OperationRemove(op.id)
		})
		if err != nil {
			logger.Warnf("Failed to delete operation %s: %s", op.id, err)
		}
	})
}

func (op *operation) Run() (chan error, error) {
	if op.status != api.Pending {
		return nil, fmt.Errorf("Only pending operations can be started")
	}

	chanRun := make(chan error, 1)

	op.lock.Lock()
	op.status = api.Running

	if op.onRun != nil {
		go func(op *operation, chanRun chan error) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.status = api.Failure
				op.err = SmartError(err).String()
				op.lock.Unlock()
				op.done()
				chanRun <- err

				logger.Debugf("Failure for %s operation: %s: %s", op.class.String(), op.id, err)

				_, md, _ := op.Render()
				eventSend(op.project, "operation", md)
				return
			}

			op.lock.Lock()
			op.status = api.Success
			op.lock.Unlock()
			op.done()
			chanRun <- nil

			op.lock.Lock()
			logger.Debugf("Success for %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend(op.project, "operation", md)
			op.lock.Unlock()
		}(op, chanRun)
	}
	op.lock.Unlock()

	logger.Debugf("Started %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend(op.project, "operation", md)

	return chanRun, nil
}

func (op *operation) Cancel() (chan error, error) {
	if op.status != api.Running {
		return nil, fmt.Errorf("Only running operations can be cancelled")
	}

	if !op.mayCancel() {
		return nil, fmt.Errorf("This operation can't be cancelled")
	}

	chanCancel := make(chan error, 1)

	op.lock.Lock()
	oldStatus := op.status
	op.status = api.Cancelling
	op.lock.Unlock()

	if op.onCancel != nil {
		go func(op *operation, oldStatus api.StatusCode, chanCancel chan error) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.status = oldStatus
				op.lock.Unlock()
				chanCancel <- err

				logger.Debugf("Failed to cancel %s operation: %s: %s", op.class.String(), op.id, err)
				_, md, _ := op.Render()
				eventSend(op.project, "operation", md)
				return
			}

			op.lock.Lock()
			op.status = api.Cancelled
			op.lock.Unlock()
			op.done()
			chanCancel <- nil

			logger.Debugf("Cancelled %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend(op.project, "operation", md)
		}(op, oldStatus, chanCancel)
	}

	logger.Debugf("Cancelling %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend(op.project, "operation", md)

	if op.canceler != nil {
		err := op.canceler.Cancel()
		if err != nil {
			return nil, err
		}
	}

	if op.onCancel == nil {
		op.lock.Lock()
		op.status = api.Cancelled
		op.lock.Unlock()
		op.done()
		chanCancel <- nil
	}

	logger.Debugf("Cancelled %s operation: %s", op.class.String(), op.id)
	_, md, _ = op.Render()
	eventSend(op.project, "operation", md)

	return chanCancel, nil
}

func (op *operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	if op.class != operationClassWebsocket {
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.status != api.Running {
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	op.lock.Lock()

	go func(op *operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			logger.Debugf("Failed to handle %s operation: %s: %s", op.class.String(), op.id, err)
			return
		}

		chanConnect <- nil

		logger.Debugf("Handled %s operation: %s", op.class.String(), op.id)
	}(op, chanConnect)
	op.lock.Unlock()

	logger.Debugf("Connected %s operation: %s", op.class.String(), op.id)

	return chanConnect, nil
}

func (op *operation) mayCancel() bool {
	if op.class == operationClassToken {
		return true
	}

	if op.onCancel != nil {
		return true
	}

	if op.canceler != nil && op.canceler.Cancelable() {
		return true
	}

	return false
}

func (op *operation) Render() (string, *api.Operation, error) {
	// Setup the resource URLs
	resources := op.resources
	if resources != nil {
		tmpResources := make(map[string][]string)
		for key, value := range resources {
			var values []string
			for _, c := range value {
				values = append(values, fmt.Sprintf("/%s/%s/%s", version.APIVersion, key, c))
			}
			tmpResources[key] = values
		}
		resources = tmpResources
	}

	return op.url, &api.Operation{
		ID:          op.id,
		Class:       op.class.String(),
		Description: op.description,
		CreatedAt:   op.createdAt,
		UpdatedAt:   op.updatedAt,
		Status:      op.status.String(),
		StatusCode:  op.status,
		Resources:   resources,
		Metadata:    op.metadata,
		MayCancel:   op.mayCancel(),
		Err:         op.err,
	}, nil
}

func (op *operation) WaitFinal(timeout int) (bool, error) {
	// Check current state
	if op.status.IsFinal() {
		return true, nil
	}

	// Wait indefinitely
	if timeout == -1 {
		<-op.chanDone
		return true, nil
	}

	// Wait until timeout
	if timeout > 0 {
		timer := time.NewTimer(time.Duration(timeout) * time.Second)
		select {
		case <-op.chanDone:
			return true, nil

		case <-timer.C:
			return false, nil
		}
	}

	return false, nil
}

func (op *operation) UpdateResources(opResources map[string][]string) error {
	if op.status != api.Pending && op.status != api.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.resources = opResources
	op.lock.Unlock()

	logger.Debugf("Updated resources for %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend(op.project, "operation", md)

	return nil
}

func (op *operation) UpdateMetadata(opMetadata interface{}) error {
	if op.status != api.Pending && op.status != api.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return err
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	logger.Debugf("Updated metadata for %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend(op.project, "operation", md)

	return nil
}

func operationCreate(cluster *db.Cluster, project string, opClass operationClass, opType db.OperationType, opResources map[string][]string, opMetadata interface{}, onRun func(*operation) error, onCancel func(*operation) error, onConnect func(*operation, *http.Request, http.ResponseWriter) error) (*operation, error) {
	// Main attributes
	op := operation{}
	op.project = project
	op.id = uuid.NewRandom().String()
	op.description = opType.Description()
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = api.Pending
	op.url = fmt.Sprintf("/%s/operations/%s", version.APIVersion, op.id)
	op.resources = opResources
	op.chanDone = make(chan error)
	op.cluster = cluster

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return nil, err
	}
	op.metadata = newMetadata

	// Callback functions
	op.onRun = onRun
	op.onCancel = onCancel
	op.onConnect = onConnect

	// Sanity check
	if op.class != operationClassWebsocket && op.onConnect != nil {
		return nil, fmt.Errorf("Only websocket operations can have a Connect hook")
	}

	if op.class == operationClassWebsocket && op.onConnect == nil {
		return nil, fmt.Errorf("Websocket operations must have a Connect hook")
	}

	if op.class == operationClassToken && op.onRun != nil {
		return nil, fmt.Errorf("Token operations can't have a Run hook")
	}

	if op.class == operationClassToken && op.onCancel != nil {
		return nil, fmt.Errorf("Token operations can't have a Cancel hook")
	}

	operationsLock.Lock()
	operations[op.id] = &op
	operationsLock.Unlock()

	err = op.cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.OperationAdd(project, op.id, opType)
		return err
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to add operation %s to database", op.id)
	}

	logger.Debugf("New %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend(op.project, "operation", md)

	return &op, nil
}

func operationGet(id string) (*operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}

// API functions
func operationAPIGet(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]

	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operationGet(id)
	if err == nil {
		_, body, err = op.Render()
		if err != nil {
			return SmartError(err)
		}

		return SyncResponse(true, body)
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
		return SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return SmartError(err)
	}

	body, _, err = client.GetOperation(id)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, body)
}

func operationAPIDelete(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]

	// First check if the query is for a local operation from this node
	op, err := operationGet(id)
	if err == nil {
		_, err = op.Cancel()
		if err != nil {
			return BadRequest(err)
		}

		return EmptySyncResponse
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
		return SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return SmartError(err)
	}

	err = client.DeleteOperation(id)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func operationsAPIGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	recursion := util.IsRecursionRequest(r)

	localOperationURLs := func() (shared.Jmap, error) {
		// Get all the operations
		operationsLock.Lock()
		ops := operations
		operationsLock.Unlock()

		// Build a list of URLs
		body := shared.Jmap{}

		for _, v := range ops {
			if v.project != "" && v.project != project {
				continue
			}
			status := strings.ToLower(v.status.String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]string, 0)
			}

			body[status] = append(body[status].([]string), v.url)
		}

		return body, nil
	}

	localOperations := func() (shared.Jmap, error) {
		// Get all the operations
		operationsLock.Lock()
		ops := operations
		operationsLock.Unlock()

		// Build a list of operations
		body := shared.Jmap{}

		for _, v := range ops {
			if v.project != "" && v.project != project {
				continue
			}
			status := strings.ToLower(v.status.String())
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
				return InternalError(err)
			}

			return SyncResponse(true, body)
		}

		// Normal queries
		body, err := localOperationURLs()
		if err != nil {
			return InternalError(err)
		}

		return SyncResponse(true, body)
	}

	// Start with local operations
	var md shared.Jmap
	var err error

	if recursion {
		md, err = localOperations()
		if err != nil {
			return InternalError(err)
		}
	} else {
		md, err = localOperationURLs()
		if err != nil {
			return InternalError(err)
		}
	}

	// Check if clustered
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return InternalError(err)
	}

	// Return now if not clustered
	if !clustered {
		return SyncResponse(true, md)
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
		return SmartError(err)
	}

	// Get local address
	localAddress, err := node.HTTPSAddress(d.db)
	if err != nil {
		return InternalError(err)
	}

	cert := d.endpoints.NetworkCert()
	for _, node := range nodes {
		if node == localAddress {
			continue
		}

		// Connect to the remote server
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return SmartError(err)
		}

		// Get operation data
		ops, err := client.GetOperations()
		if err != nil {
			return SmartError(err)
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

	return SyncResponse(true, md)
}

func operationAPIWaitGet(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]

	timeout, err := shared.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return InternalError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operationGet(id)
	if err == nil {
		_, err = op.WaitFinal(timeout)
		if err != nil {
			return InternalError(err)
		}

		_, body, err := op.Render()
		if err != nil {
			return SmartError(err)
		}

		return SyncResponse(true, body)
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
		return SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return SmartError(err)
	}

	_, body, err := client.GetOperationWait(id, timeout)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, body)
}

type operationWebSocket struct {
	req *http.Request
	op  *operation
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

func operationAPIWebsocketGet(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]

	// First check if the query is for a local operation from this node
	op, err := operationGet(id)
	if err == nil {
		return &operationWebSocket{r, op}
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	secret := r.FormValue("secret")
	if secret == "" {
		return BadRequest(fmt.Errorf("missing secret"))
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
		return SmartError(err)
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return SmartError(err)
	}

	logger.Debugf("Forward operation websocket from node %s", address)
	source, err := client.GetOperationWebsocket(id, secret)
	if err != nil {
		return SmartError(err)
	}

	return &forwardedOperationWebSocket{req: r, id: id, source: source}
}
