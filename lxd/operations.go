package main

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/satori/go.uuid"

	"github.com/lxc/lxd/shared"
)

var operationLock sync.Mutex
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
	id          string
	cancellable bool
	class       operationClass
	createdAt   time.Time
	updatedAt   time.Time
	status      shared.StatusCode
	url         string
	resources   map[string][]string
	metadata    map[string]interface{}
	err         string
	readonly    bool

	// Those functions are called at various points in the operation lifecycle
	onRun     func(*operation) error
	onCancel  func(*operation) error
	onConnect func(*operation, *http.Request, http.ResponseWriter) error

	// Those channels are used for error reporting of background actions
	chanRun     chan error
	chanCancel  chan error
	chanConnect chan error
	chanDone    chan error

	// Locking for concurent access to the operation
	lock sync.Mutex
}

func (op *operation) done() {
	if op.readonly {
		return
	}

	op.lock.Lock()
	op.readonly = true
	close(op.chanDone)
	op.lock.Unlock()

	time.AfterFunc(time.Second*5, func() {
		operationLock.Lock()
		_, ok := operations[op.id]
		if !ok {
			operationLock.Unlock()
			return
		}

		delete(operations, op.id)
		operationLock.Unlock()

		/*
		 * When we create a new lxc.Container, it adds a finalizer (via
		 * SetFinalizer) that frees the struct. However, it sometimes
		 * takes the go GC a while to actually free the struct,
		 * presumably since it is a small amount of memory.
		 * Unfortunately, the struct also keeps the log fd open, so if
		 * we leave too many of these around, we end up running out of
		 * fds. So, let's explicitly do a GC to collect these at the
		 * end of each request.
		 */
		runtime.GC()
	})
}

func (op *operation) Run() (chan error, error) {
	if op.status != shared.Pending {
		return nil, fmt.Errorf("Only pending operations can be started")
	}

	op.lock.Lock()
	op.chanRun = make(chan error, 1)
	op.status = shared.Running

	if op.onRun != nil {
		go func(op *operation) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.status = shared.Failure
				op.err = err.Error()
				op.chanRun <- err
				op.lock.Unlock()
				op.done()

				shared.Debugf("Failure for %s operation: %s", op.class.String(), op.id)

				_, md, _ := op.Render()
				eventSend("operation", md)
				return
			}

			op.lock.Lock()
			op.status = shared.Success
			op.chanRun <- nil
			op.lock.Unlock()
			op.done()

			shared.Debugf("Success for %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend("operation", md)
		}(op)
	}
	op.lock.Unlock()

	shared.Debugf("Started %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return op.chanRun, nil
}

func (op *operation) Cancel() (chan error, error) {
	if op.status != shared.Running {
		return nil, fmt.Errorf("Only running operations can be cancelled")
	}

	if !op.cancellable {
		return nil, fmt.Errorf("This operation can't be cancelled")
	}

	op.lock.Lock()
	op.chanCancel = make(chan error, 1)
	oldStatus := op.status
	op.status = shared.Cancelling
	op.lock.Unlock()

	if op.onCancel != nil {
		go func(op *operation, oldStatus shared.StatusCode) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.status = oldStatus
				op.chanCancel <- err
				op.lock.Unlock()

				shared.Debugf("Failed to cancel %s operation: %s", op.class.String(), op.id)
				_, md, _ := op.Render()
				eventSend("operation", md)
				return
			}

			op.lock.Lock()
			op.status = shared.Cancelled
			op.chanCancel <- nil
			op.lock.Unlock()
			op.done()

			shared.Debugf("Cancelled %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend("operation", md)
		}(op, oldStatus)
	}

	shared.Debugf("Cancelling %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	if op.onCancel == nil {
		op.lock.Lock()
		op.status = shared.Cancelled
		op.chanCancel <- nil
		op.lock.Unlock()
		op.done()
	}

	shared.Debugf("Cancelled %s operation: %s", op.class.String(), op.id)
	_, md, _ = op.Render()
	eventSend("operation", md)

	return op.chanCancel, nil
}

func (op *operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	if op.class != operationClassWebsocket {
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.status != shared.Running {
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	op.lock.Lock()
	op.chanConnect = make(chan error, 1)

	go func(op *operation) {
		err := op.onConnect(op, r, w)
		if err != nil {
			op.lock.Lock()
			op.chanConnect <- err
			op.lock.Unlock()

			shared.Debugf("Failed to handle %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend("operation", md)
			return
		}

		op.lock.Lock()
		op.chanConnect <- nil
		op.lock.Unlock()

		shared.Debugf("Handled %s operation: %s", op.class.String(), op.id)
		_, md, _ := op.Render()
		eventSend("operation", md)
	}(op)
	op.lock.Unlock()

	shared.Debugf("Connected %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return op.chanConnect, nil
}

func (op *operation) Render() (string, *shared.Operation, error) {
	// Setup the resource URLs
	resources := op.resources
	if resources != nil {
		tmpResources := make(map[string][]string)
		for key, value := range resources {
			var values []string
			for _, c := range value {
				values = append(values, fmt.Sprintf("/%s/%s/%s", shared.APIVersion, key, c))
			}
			tmpResources[key] = values
		}
		resources = tmpResources
	}

	md := shared.Jmap(op.metadata)

	return op.url, &shared.Operation{
		Id:         op.id,
		CreatedAt:  op.createdAt,
		UpdatedAt:  op.updatedAt,
		Status:     op.status.String(),
		StatusCode: op.status,
		Resources:  resources,
		Metadata:   &md,
		MayCancel:  op.cancellable,
		Err:        op.err,
	}, nil
}

func (op *operation) WaitFinal(timeout int) (bool, error) {
	// Check current state
	if op.status.IsFinal() {
		return true, nil
	}

	// Wait indefinitely
	if timeout == -1 {
		for {
			<-op.chanDone
			return true, nil
		}
	}

	// Wait until timeout
	if timeout > 0 {
		timer := time.NewTimer(time.Duration(timeout) * time.Second)
		for {
			select {
			case <-op.chanDone:
				return false, nil

			case <-timer.C:
				break
			}
		}
	}

	return false, nil
}

func (op *operation) UpdateResources(opResources map[string][]string) error {
	if op.status != shared.Pending && op.status != shared.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.resources = opResources
	op.lock.Unlock()

	shared.Debugf("Updated resources for %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return nil
}

func (op *operation) UpdateMetadata(opMetadata interface{}) error {
	if op.status != shared.Pending && op.status != shared.Running {
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

	shared.Debugf("Updated metadata for %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return nil
}

func operationCreate(opClass operationClass, opResources map[string][]string, opMetadata interface{},
	onRun func(*operation) error,
	onCancel func(*operation) error,
	onConnect func(*operation, *http.Request, http.ResponseWriter) error) (*operation, error) {

	// Main attributes
	op := operation{}
	op.id = uuid.NewV4().String()
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = shared.Pending
	op.url = fmt.Sprintf("/%s/operations/%s", shared.APIVersion, op.id)
	op.resources = opResources
	op.chanDone = make(chan error)

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

	op.cancellable = op.onCancel != nil || op.class == operationClassToken

	operationLock.Lock()
	operations[op.id] = &op
	operationLock.Unlock()

	shared.Debugf("New %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return &op, nil
}

func operationGet(id string) (*operation, error) {
	operationLock.Lock()
	op, ok := operations[id]
	operationLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}

// API functions
func operationAPIGet(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]

	op, err := operationGet(id)
	if err != nil {
		return NotFound
	}

	_, body, err := op.Render()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, body)
}

func operationAPIDelete(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]

	op, err := operationGet(id)
	if err != nil {
		return NotFound
	}

	_, err = op.Cancel()
	if err != nil {
		return BadRequest(err)
	}

	return EmptySyncResponse
}

var operationCmd = Command{name: "operations/{id}", get: operationAPIGet, delete: operationAPIDelete}

func operationsAPIGet(d *Daemon, r *http.Request) Response {
	var md shared.Jmap

	recursion := d.isRecursionRequest(r)

	md = shared.Jmap{}

	operationLock.Lock()
	ops := operations
	operationLock.Unlock()

	for _, v := range ops {
		status := strings.ToLower(v.status.String())
		_, ok := md[status]
		if !ok {
			if recursion {
				md[status] = make([]*shared.Operation, 0)
			} else {
				md[status] = make([]string, 0)
			}
		}

		if !recursion {
			md[status] = append(md[status].([]string), v.url)
			continue
		}

		_, body, err := v.Render()
		if err != nil {
			continue
		}

		md[status] = append(md[status].([]*shared.Operation), body)
	}

	return SyncResponse(true, md)
}

var operationsCmd = Command{name: "operations", get: operationsAPIGet}

func operationAPIWaitGet(d *Daemon, r *http.Request) Response {
	timeout, err := shared.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return InternalError(err)
	}

	id := mux.Vars(r)["id"]
	op, err := operationGet(id)
	if err != nil {
		return NotFound
	}

	_, err = op.WaitFinal(timeout)
	if err != nil {
		return InternalError(err)
	}

	_, body, err := op.Render()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, body)
}

var operationWait = Command{name: "operations/{id}/wait", get: operationAPIWaitGet}

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

func operationAPIWebsocketGet(d *Daemon, r *http.Request) Response {
	id := mux.Vars(r)["id"]
	op, err := operationGet(id)
	if err != nil {
		return NotFound
	}

	return &operationWebSocket{r, op}
}

var operationWebsocket = Command{name: "operations/{id}/websocket", untrustedGet: true, get: operationAPIWebsocketGet}
