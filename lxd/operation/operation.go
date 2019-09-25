package operation

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/daemon"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var SmartError func(err error) daemon.Response

type OperationClass int

const (
	OperationClassTask      OperationClass = 1
	OperationClassWebsocket OperationClass = 2
	OperationClassToken     OperationClass = 3
)

var OperationsLock sync.Mutex
var Operations map[string]*Operation = make(map[string]*Operation)

func (t OperationClass) String() string {
	return map[OperationClass]string{
		OperationClassTask:      "task",
		OperationClassWebsocket: "websocket",
		OperationClassToken:     "token",
	}[t]
}

type Operation struct {
	Project     string
	ID          string
	class       OperationClass
	createdAt   time.Time
	updatedAt   time.Time
	Status      api.StatusCode
	URL         string
	Resources   map[string][]string
	Metadata    map[string]interface{}
	err         string
	readonly    bool
	Canceler    *cancel.Canceler
	description string
	Permission  string

	// Those functions are called at various points in the operation lifecycle
	onRun     func(*Operation) error
	onCancel  func(*Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

	// Channels used for error reporting and state tracking of background actions
	chanDone chan error

	// Locking for concurent access to the operation
	lock sync.Mutex

	cluster *db.Cluster
}

func OperationCreate(cluster *db.Cluster, project string, opClass OperationClass, opType db.OperationType, opResources map[string][]string, opMetadata interface{}, onRun func(*Operation) error, onCancel func(*Operation) error, onConnect func(*Operation, *http.Request, http.ResponseWriter) error) (*Operation, error) {
	// Main attributes
	op := Operation{}
	op.Project = project
	op.ID = uuid.NewRandom().String()
	op.description = opType.Description()
	op.Permission = opType.Permission()
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.Status = api.Pending
	op.URL = fmt.Sprintf("/%s/operations/%s", version.APIVersion, op.ID)
	op.Resources = opResources
	op.chanDone = make(chan error)
	op.cluster = cluster

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

	OperationsLock.Lock()
	Operations[op.ID] = &op
	OperationsLock.Unlock()

	err = op.cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.OperationAdd(project, op.ID, opType)
		return err
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to add operation %s to database", op.ID)
	}

	logger.Debugf("New %s operation: %s", op.class.String(), op.ID)
	_, md, _ := op.Render()
	events.Send(op.Project, "operation", md)

	return &op, nil
}

func OperationGetInternal(id string) (*Operation, error) {
	OperationsLock.Lock()
	op, ok := Operations[id]
	OperationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}

func (op *Operation) UpdateMetadata(opMetadata interface{}) error {
	if op.Status != api.Pending && op.Status != api.Running {
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

	logger.Debugf("Updated metadata for %s operation: %s", op.class.String(), op.ID)
	_, md, _ := op.Render()
	events.Send(op.Project, "operation", md)

	return nil
}

func (op *Operation) Render() (string, *api.Operation, error) {
	// Setup the resource URLs
	resources := op.Resources
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
	var serverName string
	err = op.cluster.Transaction(func(tx *db.ClusterTx) error {
		serverName, err = tx.NodeName()
		return err
	})
	if err != nil {
		return "", nil, err
	}

	return op.URL, &api.Operation{
		ID:          op.ID,
		Class:       op.class.String(),
		Description: op.description,
		CreatedAt:   op.createdAt,
		UpdatedAt:   op.updatedAt,
		Status:      op.Status.String(),
		StatusCode:  op.Status,
		Resources:   resources,
		Metadata:    op.Metadata,
		MayCancel:   op.mayCancel(),
		Err:         op.err,
		Location:    serverName,
	}, nil
}

func (op *Operation) mayCancel() bool {
	if op.class == OperationClassToken {
		return true
	}

	if op.onCancel != nil {
		return true
	}

	if op.Canceler != nil && op.Canceler.Cancelable() {
		return true
	}

	return false
}

func (op *Operation) WaitFinal(timeout int) (bool, error) {
	// Check current state
	if op.Status.IsFinal() {
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

func (op *Operation) UpdateResources(opResources map[string][]string) error {
	if op.Status != api.Pending && op.Status != api.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.Resources = opResources
	op.lock.Unlock()

	logger.Debugf("Updated resources for %s operation: %s", op.class.String(), op.ID)
	_, md, _ := op.Render()
	events.Send(op.Project, "operation", md)

	return nil
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
		OperationsLock.Lock()
		_, ok := Operations[op.ID]
		if !ok {
			OperationsLock.Unlock()
			return
		}

		delete(Operations, op.ID)
		OperationsLock.Unlock()

		err := op.cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.OperationRemove(op.ID)
		})
		if err != nil {
			logger.Warnf("Failed to delete operation %s: %s", op.ID, err)
		}
	})
}

func (op *Operation) Run() (chan error, error) {
	if op.Status != api.Pending {
		return nil, fmt.Errorf("Only pending operations can be started")
	}

	chanRun := make(chan error, 1)

	op.lock.Lock()
	op.Status = api.Running

	if op.onRun != nil {
		go func(op *Operation, chanRun chan error) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.Status = api.Failure
				op.err = SmartError(err).String()
				op.lock.Unlock()
				op.done()
				chanRun <- err

				logger.Debugf("Failure for %s operation: %s: %s", op.class.String(), op.ID, err)

				_, md, _ := op.Render()
				events.Send(op.Project, "operation", md)
				return
			}

			op.lock.Lock()
			op.Status = api.Success
			op.lock.Unlock()
			op.done()
			chanRun <- nil

			op.lock.Lock()
			logger.Debugf("Success for %s operation: %s", op.class.String(), op.ID)
			_, md, _ := op.Render()
			events.Send(op.Project, "operation", md)
			op.lock.Unlock()
		}(op, chanRun)
	}
	op.lock.Unlock()

	logger.Debugf("Started %s operation: %s", op.class.String(), op.ID)
	_, md, _ := op.Render()
	events.Send(op.Project, "operation", md)

	return chanRun, nil
}

func (op *Operation) Cancel() (chan error, error) {
	if op.Status != api.Running {
		return nil, fmt.Errorf("Only running operations can be cancelled")
	}

	if !op.mayCancel() {
		return nil, fmt.Errorf("This operation can't be cancelled")
	}

	chanCancel := make(chan error, 1)

	op.lock.Lock()
	oldStatus := op.Status
	op.Status = api.Cancelling
	op.lock.Unlock()

	if op.onCancel != nil {
		go func(op *Operation, oldStatus api.StatusCode, chanCancel chan error) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.Status = oldStatus
				op.lock.Unlock()
				chanCancel <- err

				logger.Debugf("Failed to cancel %s operation: %s: %s", op.class.String(), op.ID, err)
				_, md, _ := op.Render()
				events.Send(op.Project, "operation", md)
				return
			}

			op.lock.Lock()
			op.Status = api.Cancelled
			op.lock.Unlock()
			op.done()
			chanCancel <- nil

			logger.Debugf("Cancelled %s operation: %s", op.class.String(), op.ID)
			_, md, _ := op.Render()
			events.Send(op.Project, "operation", md)
		}(op, oldStatus, chanCancel)
	}

	logger.Debugf("Cancelling %s operation: %s", op.class.String(), op.ID)
	_, md, _ := op.Render()
	events.Send(op.Project, "operation", md)

	if op.Canceler != nil {
		err := op.Canceler.Cancel()
		if err != nil {
			return nil, err
		}
	}

	if op.onCancel == nil {
		op.lock.Lock()
		op.Status = api.Cancelled
		op.lock.Unlock()
		op.done()
		chanCancel <- nil
	}

	logger.Debugf("Cancelled %s operation: %s", op.class.String(), op.ID)
	_, md, _ = op.Render()
	events.Send(op.Project, "operation", md)

	return chanCancel, nil
}

func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	if op.class != OperationClassWebsocket {
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.Status != api.Running {
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	op.lock.Lock()

	go func(op *Operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			logger.Debugf("Failed to handle %s operation: %s: %s", op.class.String(), op.ID, err)
			return
		}

		chanConnect <- nil

		logger.Debugf("Handled %s operation: %s", op.class.String(), op.ID)
	}(op, chanConnect)
	op.lock.Unlock()

	logger.Debugf("Connected %s operation: %s", op.class.String(), op.ID)

	return chanConnect, nil
}
