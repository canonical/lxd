package operation

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

var operationsLock sync.Mutex
var operations map[string]*Operation = make(map[string]*Operation)

type Class int

const (
	ClassTask      Class = 1
	ClassWebsocket Class = 2
	ClassToken     Class = 3
)

func (t Class) String() string {
	return map[Class]string{
		ClassTask:      "task",
		ClassWebsocket: "websocket",
		ClassToken:     "token",
	}[t]
}

func Operations() map[string]*Operation {
	operationsLock.Lock()
	ops := operations
	operationsLock.Unlock()
	return ops
}

type Operation struct {
	id        string
	class     Class
	createdAt time.Time
	updatedAt time.Time
	status    shared.StatusCode
	url       string
	Resources map[string][]string
	Metadata  map[string]interface{}
	err       string
	readonly  bool

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(*Operation) error
	onCancel  func(*Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

	// Channels used for error reporting and state tracking of background actions
	chanDone chan error

	// Locking for concurent access to the Operation
	lock sync.Mutex
}

func (op *Operation) Id() string {
	return op.id
}

func (op *Operation) Url() string {
	return op.url
}

func (op *Operation) Status() shared.StatusCode {
	return op.status
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

func (op *Operation) Run() (chan error, error) {
	if op.status != shared.Pending {
		return nil, fmt.Errorf("Only pending operations can be started")
	}

	chanRun := make(chan error, 1)

	op.lock.Lock()
	op.status = shared.Running

	if op.onRun != nil {
		go func(op *Operation, chanRun chan error) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.status = shared.Failure
				switch err {
				case os.ErrNotExist:
					op.err = "not found"
				case sql.ErrNoRows:
					op.err = "not found"
				case util.NoSuchObjectError:
					op.err = "not found"
				case os.ErrPermission:
					op.err = "not authorized"
				case util.DbErrAlreadyDefined:
					op.err = "already exists"
				case sqlite3.ErrConstraintUnique:
					op.err = "already exists"
				default:
					op.err = err.Error()
				}
				op.lock.Unlock()
				op.done()
				chanRun <- err

				shared.LogDebugf("Failure for %s Operation: %s: %s", op.class.String(), op.id, err)

				_, md, _ := op.Render()
				events.Send("Operation", md)
				return
			}

			op.lock.Lock()
			op.status = shared.Success
			op.lock.Unlock()
			op.done()
			chanRun <- nil

			op.lock.Lock()
			shared.LogDebugf("Success for %s Operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			events.Send("Operation", md)
			op.lock.Unlock()
		}(op, chanRun)
	}
	op.lock.Unlock()

	shared.LogDebugf("Started %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	events.Send("Operation", md)

	return chanRun, nil
}

func (op *Operation) Cancel() (chan error, error) {
	if op.status != shared.Running {
		return nil, fmt.Errorf("Only running operations can be cancelled")
	}

	if !op.mayCancel() {
		return nil, fmt.Errorf("This Operation can't be cancelled")
	}

	chanCancel := make(chan error, 1)

	op.lock.Lock()
	oldStatus := op.status
	op.status = shared.Cancelling
	op.lock.Unlock()

	if op.onCancel != nil {
		go func(op *Operation, oldStatus shared.StatusCode, chanCancel chan error) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.status = oldStatus
				op.lock.Unlock()
				chanCancel <- err

				shared.LogDebugf("Failed to cancel %s Operation: %s: %s", op.class.String(), op.id, err)
				_, md, _ := op.Render()
				events.Send("Operation", md)
				return
			}

			op.lock.Lock()
			op.status = shared.Cancelled
			op.lock.Unlock()
			op.done()
			chanCancel <- nil

			shared.LogDebugf("Cancelled %s Operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			events.Send("Operation", md)
		}(op, oldStatus, chanCancel)
	}

	shared.LogDebugf("Cancelling %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	events.Send("Operation", md)

	if op.onCancel == nil {
		op.lock.Lock()
		op.status = shared.Cancelled
		op.lock.Unlock()
		op.done()
		chanCancel <- nil
	}

	shared.LogDebugf("Cancelled %s Operation: %s", op.class.String(), op.id)
	_, md, _ = op.Render()
	events.Send("Operation", md)

	return chanCancel, nil
}

func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	if op.class != ClassWebsocket {
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.status != shared.Running {
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	op.lock.Lock()

	go func(op *Operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			shared.LogDebugf("Failed to handle %s Operation: %s: %s", op.class.String(), op.id, err)
			return
		}

		chanConnect <- nil

		shared.LogDebugf("Handled %s Operation: %s", op.class.String(), op.id)
	}(op, chanConnect)
	op.lock.Unlock()

	shared.LogDebugf("Connected %s Operation: %s", op.class.String(), op.id)

	return chanConnect, nil
}

func (op *Operation) mayCancel() bool {
	return op.onCancel != nil || op.class == ClassToken
}

func (op *Operation) Render() (string, *shared.Operation, error) {
	// Setup the resource URLs
	resources := op.Resources
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

	md := shared.Jmap(op.Metadata)

	return op.url, &shared.Operation{
		Id:         op.id,
		Class:      op.class.String(),
		CreatedAt:  op.createdAt,
		UpdatedAt:  op.updatedAt,
		Status:     op.status.String(),
		StatusCode: op.status,
		Resources:  resources,
		Metadata:   &md,
		MayCancel:  op.mayCancel(),
		Err:        op.err,
	}, nil
}

func (op *Operation) WaitFinal(timeout int) (bool, error) {
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
				return false, nil
			}
		}
	}

	return false, nil
}

func (op *Operation) UpdateResources(opResources map[string][]string) error {
	if op.status != shared.Pending && op.status != shared.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.Resources = opResources
	op.lock.Unlock()

	shared.LogDebugf("Updated resources for %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	events.Send("Operation", md)

	return nil
}

func (op *Operation) UpdateMetadata(opMetadata interface{}) error {
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
	op.Metadata = newMetadata
	op.lock.Unlock()

	shared.LogDebugf("Updated metadata for %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	events.Send("Operation", md)

	return nil
}

func Create(opClass Class, opResources map[string][]string, opMetadata interface{},
	onRun func(*Operation) error,
	onCancel func(*Operation) error,
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error) (*Operation, error) {

	// Main attributes
	op := Operation{}
	op.id = uuid.NewRandom().String()
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = shared.Pending
	op.url = fmt.Sprintf("/%s/operations/%s", shared.APIVersion, op.id)
	op.Resources = opResources
	op.chanDone = make(chan error)

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return nil, err
	}
	op.Metadata = newMetadata

	// Callback functions
	op.onRun = onRun
	op.onCancel = onCancel
	op.onConnect = onConnect

	// Sanity check
	if op.class != ClassWebsocket && op.onConnect != nil {
		return nil, fmt.Errorf("Only websocket operations can have a Connect hook")
	}

	if op.class == ClassWebsocket && op.onConnect == nil {
		return nil, fmt.Errorf("Websocket operations must have a Connect hook")
	}

	if op.class == ClassToken && op.onRun != nil {
		return nil, fmt.Errorf("Token operations can't have a Run hook")
	}

	if op.class == ClassToken && op.onCancel != nil {
		return nil, fmt.Errorf("Token operations can't have a Cancel hook")
	}

	operationsLock.Lock()
	operations[op.id] = &op
	operationsLock.Unlock()

	shared.LogDebugf("New %s Operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	events.Send("Operation", md)

	return &op, nil
}

func Get(id string) (*Operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}
