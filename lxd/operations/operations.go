package operations

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var debug bool

var operationsLock sync.Mutex
var operations = make(map[string]*Operation)

// OperationClass represents the OperationClass type
type OperationClass int

const (
	// OperationClassTask represents the Task OperationClass
	OperationClassTask OperationClass = 1
	// OperationClassWebsocket represents the Websocket OperationClass
	OperationClassWebsocket OperationClass = 2
	// OperationClassToken represents the Token OperationClass
	OperationClassToken OperationClass = 3
)

func (t OperationClass) String() string {
	return map[OperationClass]string{
		OperationClassTask:      api.OperationClassTask,
		OperationClassWebsocket: api.OperationClassWebsocket,
		OperationClassToken:     api.OperationClassToken,
	}[t]
}

// Init sets the debug value for the operations package.
func Init(d bool) {
	debug = d
}

// Lock locks the operations mutex.
func Lock() {
	operationsLock.Lock()
}

// Unlock unlocks the operations mutex.
func Unlock() {
	operationsLock.Unlock()
}

// Clone returns a clone of the internal operations map containing references to the actual operations.
func Clone() map[string]*Operation {
	operationsLock.Lock()
	defer operationsLock.Unlock()

	localOperations := make(map[string]*Operation, len(operations))
	for k, v := range operations {
		localOperations[k] = v
	}

	return localOperations
}

// OperationGetInternal returns the operation with the given id. It returns an
// error if it doesn't exist.
func OperationGetInternal(id string) (*Operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}

// Operation represents an operation.
type Operation struct {
	projectName string
	id          string
	class       OperationClass
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
	permission  string
	dbOpType    db.OperationType
	requestor   *api.EventLifecycleRequestor

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(*Operation) error
	onCancel  func(*Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

	// Channels used for error reporting and state tracking of background actions
	chanDone chan error

	// Locking for concurent access to the Operation
	lock sync.Mutex

	state  *state.State
	events *events.Server
}

// OperationCreate creates a new operation and returns it. If it cannot be
// created, it returns an error.
func OperationCreate(s *state.State, projectName string, opClass OperationClass, opType db.OperationType, opResources map[string][]string, opMetadata interface{}, onRun func(*Operation) error, onCancel func(*Operation) error, onConnect func(*Operation, *http.Request, http.ResponseWriter) error, r *http.Request) (*Operation, error) {
	// Don't allow new operations when LXD is shutting down.
	if s != nil && s.Context.Err() == context.Canceled {
		return nil, fmt.Errorf("LXD is shutting down")
	}

	// Main attributes
	op := Operation{}
	op.projectName = projectName
	op.id = uuid.NewRandom().String()
	op.description = opType.Description()
	op.permission = opType.Permission()
	op.dbOpType = opType
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = api.Pending
	op.url = fmt.Sprintf("/%s/operations/%s", version.APIVersion, op.id)
	op.resources = opResources
	op.chanDone = make(chan error)
	op.state = s

	if s != nil {
		op.SetEventServer(s.Events)
	}

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return nil, err
	}
	op.metadata = newMetadata

	// Callback functions
	op.onRun = onRun
	op.onCancel = onCancel
	op.onConnect = onConnect

	// Quick check.
	if op.class != OperationClassWebsocket && op.onConnect != nil {
		return nil, fmt.Errorf("Only websocket operations can have a Connect hook")
	}

	if op.class == OperationClassWebsocket && op.onConnect == nil {
		return nil, fmt.Errorf("Websocket operations must have a Connect hook")
	}

	if op.class == OperationClassToken && op.onRun != nil {
		return nil, fmt.Errorf("Token operations can't have a Run hook")
	}

	if op.class == OperationClassToken && op.onCancel != nil {
		return nil, fmt.Errorf("Token operations can't have a Cancel hook")
	}

	// Set requestor if request was provided.
	if r != nil {
		ctx := r.Context()
		requestor := api.EventLifecycleRequestor{}

		username, ok := ctx.Value("username").(string)
		if ok {
			requestor.Username = username
		}

		protocol, ok := ctx.Value("protocol").(string)
		if ok {
			requestor.Protocol = protocol
		}

		op.requestor = &requestor
	}

	operationsLock.Lock()
	operations[op.id] = &op
	operationsLock.Unlock()

	err = registerDBOperation(&op, opType)
	if err != nil {
		return nil, err
	}

	logger.Debugf("New %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()

	operationsLock.Lock()
	op.sendEvent(md)
	operationsLock.Unlock()

	return &op, nil
}

// SetEventServer allows injection of event server.
func (op *Operation) SetEventServer(events *events.Server) {
	op.events = events
}

// Requestor returns the initial requestor for this operation.
func (op *Operation) Requestor() *api.EventLifecycleRequestor {
	return op.requestor
}

func (op *Operation) done() {
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

		if op.state == nil {
			return
		}

		err := removeDBOperation(op)
		if err != nil {
			logger.Warnf("Failed to delete operation %s: %s", op.id, err)
		}
	})
}

// Run runs a pending operation. It returns an error if the operation cannot
// be started.
func (op *Operation) Run() (chan error, error) {
	if op.status != api.Pending {
		return nil, fmt.Errorf("Only pending operations can be started")
	}

	chanRun := make(chan error, 1)

	op.lock.Lock()
	op.status = api.Running

	if op.onRun != nil {
		go func(op *Operation, chanRun chan error) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.status = api.Failure
				op.err = response.SmartError(err).String()
				op.lock.Unlock()
				op.done()
				chanRun <- err

				logger.Debugf("Failure for %s operation: %s: %s", op.class.String(), op.id, err)
				_, md, _ := op.Render()

				op.lock.Lock()
				op.sendEvent(md)
				op.lock.Unlock()

				return
			}

			op.lock.Lock()
			op.status = api.Success
			op.lock.Unlock()
			op.done()
			chanRun <- nil

			logger.Debugf("Success for %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()

			op.lock.Lock()
			op.sendEvent(md)
			op.lock.Unlock()
		}(op, chanRun)
	}

	op.lock.Unlock()

	logger.Debugf("Started %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return chanRun, nil
}

// Cancel cancels a running operation. If the operation cannot be cancelled, it
// returns an error.
func (op *Operation) Cancel() (chan error, error) {
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

	hasOnCancel := op.onCancel != nil

	if hasOnCancel {
		go func(op *Operation, oldStatus api.StatusCode, chanCancel chan error) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.status = oldStatus
				op.lock.Unlock()
				chanCancel <- err

				logger.Debug("Failed to cancel operation", log.Ctx{"operation": op.id, "class": op.class.String(), "err": err})
				_, md, _ := op.Render()

				op.lock.Lock()
				op.sendEvent(md)
				op.lock.Unlock()

				return
			}

			op.lock.Lock()
			op.status = api.Cancelled
			op.lock.Unlock()
			op.done()
			chanCancel <- nil

			logger.Debug("Cancelled operation", log.Ctx{"operation": op.ID(), "class": op.class.String()})
			_, md, _ := op.Render()

			op.lock.Lock()
			op.sendEvent(md)
			op.lock.Unlock()

		}(op, oldStatus, chanCancel)
	}

	logger.Debug("Cancelling operation", log.Ctx{"operation": op.ID(), "class": op.class.String()})
	_, md, _ := op.Render()
	op.sendEvent(md)

	if op.canceler != nil {
		err := op.canceler.Cancel()
		if err != nil {
			return nil, err
		}
	}

	if !hasOnCancel {
		op.lock.Lock()
		op.status = api.Cancelled
		op.lock.Unlock()
		op.done()
		chanCancel <- nil
	}

	logger.Debug("Cancelled operation", log.Ctx{"operation": op.ID(), "class": op.class.String()})
	_, md, _ = op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return chanCancel, nil
}

// Connect connects a websocket operation. If the operation is not a websocket
// operation or the operation is not running, it returns an error.
func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	if op.class != OperationClassWebsocket {
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.status != api.Running {
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	op.lock.Lock()

	go func(op *Operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			logger.Debugf("Failed to handle %s Operation: %s: %s", op.class.String(), op.id, err)
			return
		}

		chanConnect <- nil

		logger.Debugf("Handled %s Operation: %s", op.class.String(), op.id)
	}(op, chanConnect)
	op.lock.Unlock()

	logger.Debugf("Connected %s Operation: %s", op.class.String(), op.id)

	return chanConnect, nil
}

func (op *Operation) mayCancel() bool {
	if op.class == OperationClassToken {
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

// Render renders the operation structure.
// Returns URL of operation and operation info.
func (op *Operation) Render() (string, *api.Operation, error) {
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

	// Local server name
	var err error
	serverName, err := getServerName(op)
	if err != nil {
		return "", nil, err
	}

	op.lock.Lock()
	retOp := &api.Operation{
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
		Location:    serverName,
	}
	op.lock.Unlock()

	return op.url, retOp, nil
}

// WaitFinal waits for the operation to be done. If timeout is -1, it will wait
// indefinitely otherwise it will timeout after {timeout} seconds.
func (op *Operation) WaitFinal(timeout int) (bool, error) {
	// Check current state
	op.lock.Lock()
	if op.status.IsFinal() {
		op.lock.Unlock()
		return true, nil
	}
	op.lock.Unlock()

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

// UpdateResources updates the resources of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateResources(opResources map[string][]string) error {
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

	logger.Debugf("Updated resources for %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// UpdateMetadata updates the metadata of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateMetadata(opMetadata interface{}) error {
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

	logger.Debugf("Updated metadata for %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// ID returns the operation ID.
func (op *Operation) ID() string {
	return op.id
}

// Metadata returns the operation Metadata.
func (op *Operation) Metadata() map[string]interface{} {
	return op.metadata
}

// URL returns the operation URL.
func (op *Operation) URL() string {
	return op.url
}

// Resources returns the operation resources.
func (op *Operation) Resources() map[string][]string {
	return op.resources
}

// SetCanceler sets a canceler.
func (op *Operation) SetCanceler(canceler *cancel.Canceler) {
	op.canceler = canceler
}

// Permission returns the operation permission.
func (op *Operation) Permission() string {
	return op.permission
}

// Project returns the operation project.
func (op *Operation) Project() string {
	return op.projectName
}

// Status returns the operation status.
func (op *Operation) Status() api.StatusCode {
	return op.status
}

// Class returns the operation class.
func (op *Operation) Class() OperationClass {
	return op.class
}

// Type returns the db operation type.
func (op *Operation) Type() db.OperationType {
	return op.dbOpType
}
