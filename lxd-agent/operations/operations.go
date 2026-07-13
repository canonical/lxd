package operations

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var operationsLock sync.Mutex
var operations = make(map[string]*Operation)

// Clone returns a clone of the internal operations map containing references to the actual operations.
func Clone() map[string]*Operation {
	operationsLock.Lock()
	defer operationsLock.Unlock()

	localOperations := make(map[string]*Operation, len(operations))
	maps.Copy(localOperations, operations)

	return localOperations
}

// Get returns the operation with the given id. It returns an
// error if it doesn't exist.
func Get(id string) (*Operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation %q does not exist", id)
	}

	return op, nil
}

// Operation represents an operation.
type Operation struct {
	id          string
	opType      operationtype.Type
	class       operationtype.Class
	createdAt   time.Time
	updatedAt   time.Time
	status      api.StatusCode
	metadata    map[string]any
	err         string
	errCode     int64
	readonly    bool
	description string
	logger      logger.Logger

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(context.Context, *Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

	// finished is cancelled when the operation has finished executing all configured hooks.
	// It is used by Wait, to wait on the operation to be fully completed.
	finished cancel.Canceller

	// running is the basis of the [context.Context] passed into the onRun hook.
	// It is cancelled when the onRun hook completes or when Cancel is called (on operation deletion).
	running cancel.Canceller

	// Locking for concurrent access to the Operation
	lock sync.Mutex

	events *events.Server
}

// OperationArgs contains all the arguments for operation creation.
type OperationArgs struct {
	Type        operationtype.Type
	Class       operationtype.Class
	Metadata    map[string]any
	RunHook     func(ctx context.Context, op *Operation) error
	ConnectHook func(op *Operation, r *http.Request, w http.ResponseWriter) error
}

// ScheduleOperation schedules a new operation and returns it. If it cannot be created, it returns an error.
func ScheduleOperation(eventServer *events.Server, args OperationArgs) (*Operation, error) {
	if args.Class != operationtype.OperationClassWebsocket {
		return nil, fmt.Errorf("Operation class %q not supported", args.Class.String())
	}

	if args.Type != operationtype.CommandExec {
		return nil, fmt.Errorf("Operation type %q not supported", args.Type.Description())
	}

	if args.RunHook == nil {
		return nil, errors.New("Operation type requires a run hook")
	}

	if args.ConnectHook == nil {
		return nil, errors.New("Operation type requires a connect hook")
	}

	// Use a v7 UUID for the operation ID.
	uuid, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Failed generating operation UUID: %w", err)
	}

	// Main attributes
	op := Operation{}
	op.id = uuid.String()
	op.opType = args.Type
	op.class = args.Class
	op.description = op.opType.Description()
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = api.Running
	op.finished = cancel.New()
	op.running = cancel.New()
	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "class": api.OperationClassWebsocket, "description": op.description})
	op.metadata = args.Metadata
	op.events = eventServer

	// Callback functions
	op.onRun = args.RunHook
	op.onConnect = args.ConnectHook

	op.logger.Debug("New operation")

	operationsLock.Lock()
	operations[op.id] = &op
	operationsLock.Unlock()

	op.start()
	return &op, nil
}

func (op *Operation) done() {
	op.lock.Lock()
	if op.readonly {
		op.lock.Unlock()
		return
	}

	op.readonly = true
	op.onRun = nil
	op.onConnect = nil
	op.finished.Cancel()
	op.lock.Unlock()

	go func() {
		<-time.After(time.Second * 5) // Wait 5s before removing from internal map.

		operationsLock.Lock()
		delete(operations, op.id)
		operationsLock.Unlock()
	}()
}

func (op *Operation) updateStatus(newStatus api.StatusCode) {
	op.status = newStatus
	op.updatedAt = time.Now()
}

// start a pending operation.
func (op *Operation) start() {
	op.lock.Lock()

	// If there's a run hook, we need to run it and get the final status from it.
	// If there are child operations, we need to start and wait for them to finish before we can get the final status of the parent operation.
	if op.onRun != nil {
		runCtx := context.Context(op.running)

		go func(ctx context.Context, op *Operation) {
			err := op.onRun(ctx, op)
			if err != nil {
				op.lock.Lock()

				// Set the error and error code. We use either the error code from the error, or default to internal server error.
				op.err = err.Error()
				statusCode, found := api.StatusErrorMatch(err)
				if found {
					op.errCode = int64(statusCode)
				} else {
					op.errCode = http.StatusInternalServerError
				}

				// If the run context was cancelled, the previous state should be "cancelling", and the final state should be "cancelled".
				if errors.Is(err, context.Canceled) {
					op.updateStatus(api.Cancelled)
				} else {
					op.updateStatus(api.Failure)
				}

				// Always call cancel. This is a no-op if already cancelled.
				op.running.Cancel()

				op.lock.Unlock()
				op.done()

				op.logger.Warn("Failure for operation", logger.Ctx{"err": err})
				_, md := op.Render()

				op.lock.Lock()
				op.sendEvent(md)
				op.lock.Unlock()

				return
			}

			op.lock.Lock()
			op.updateStatus(api.Success)
			op.running.Cancel()
			op.lock.Unlock()
			op.done()

			op.logger.Debug("Success for operation")
			_, md := op.Render()

			op.lock.Lock()
			op.sendEvent(md)
			op.lock.Unlock()
		}(runCtx, op)
	}

	op.lock.Unlock()

	op.logger.Debug("Started operation")
	_, md := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()
}

// IsRunning returns true while the operation context has not been cancelled.
// It returns false once the operation completes or cancellation is requested.
func (op *Operation) IsRunning() bool {
	return op.running.Err() == nil
}

// Cancel cancels a running operation. If the operation cannot be cancelled, it
// returns an error.
func (op *Operation) Cancel() {
	op.lock.Lock()
	if op.running.Err() != nil {
		// Already cancelled, nothing to do.
		op.lock.Unlock()
		return
	}

	// Signal the operation to stop.
	op.running.Cancel()
	op.updateStatus(api.Cancelling)
	op.lock.Unlock()

	op.logger.Debug("Cancelling operation")
	_, md := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()
}

// Connect connects a websocket operation. If the operation is not a websocket
// operation or the operation is not running, it returns an error.
func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	op.lock.Lock()
	if op.onConnect == nil {
		op.lock.Unlock()
		return nil, errors.New("Only websocket operations can be connected")
	}

	if op.running.Err() != nil {
		errMsg := op.err
		errCode := op.errCode
		op.lock.Unlock()

		if errMsg != "" {
			return nil, api.NewStatusError(int(errCode), "Failed connecting to operation: "+errMsg)
		}

		return nil, api.NewStatusError(http.StatusBadRequest, "Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	go func(op *Operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			op.logger.Debug("Failed connecting to operation", logger.Ctx{"err": err})
			return
		}

		chanConnect <- nil

		op.logger.Debug("Connected to operation")
	}(op, chanConnect)
	op.lock.Unlock()

	op.logger.Debug("Connecting to operation")

	return chanConnect, nil
}

// Render renders the operation structure.
// Returns URL of operation and operation info.
func (op *Operation) Render() (string, *api.Operation) {
	op.lock.Lock()

	// Make a copy of the metadata to avoid concurrent reads/writes.
	metadata := make(map[string]any, len(op.metadata))
	maps.Copy(metadata, op.metadata)

	// Put together the response struct.
	retOp := &api.Operation{
		ID:          op.id,
		Class:       api.OperationClassWebsocket,
		Description: op.description,
		CreatedAt:   op.createdAt,
		UpdatedAt:   op.updatedAt,
		Status:      op.status.String(),
		StatusCode:  op.status,
		Metadata:    metadata,
		MayCancel:   true,
		Err:         op.err,
		ErrCode:     op.errCode,
	}

	op.lock.Unlock()

	return api.NewURL().Path(version.APIVersion, "operations", op.id).String(), retOp
}

// Wait for the operation to be done.
// Returns non-nil error if operation failed or context was cancelled.
func (op *Operation) Wait(ctx context.Context) error {
	select {
	case <-op.finished.Done():
		op.lock.Lock()
		errMsg := op.err
		errCode := op.errCode
		status := op.status
		op.lock.Unlock()

		if errMsg != "" {
			// Custom error types can contain additional information about the failure.
			// To ensure the error returned from the database is the same as error returned
			// directly from the operation code, we return a new error object consisting
			// only of the error message and error code.

			// If the operation was cancelled, return fresh context.Cancelled error.
			if status == api.Cancelled {
				return context.Canceled
			}

			// For other errors, return a new error with the same message and code.
			return api.NewStatusError(int(errCode), errMsg)
		}

		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// UpdateMetadata updates the metadata of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateMetadata(opMetadata map[string]any) error {
	op.lock.Lock()
	if op.finished.Err() != nil {
		op.lock.Unlock()
		return api.NewStatusError(http.StatusBadRequest, "Operations cannot be updated after they have completed")
	}

	if op.readonly {
		op.lock.Unlock()
		return errors.New("Read-only operations cannot be updated")
	}

	op.updatedAt = time.Now()
	op.metadata = opMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
	_, md := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// URL returns the operation URL.
func (op *Operation) URL() string {
	return api.NewURL().Path(version.APIVersion, "operations", op.id).String()
}

// Status returns the operation status.
func (op *Operation) Status() api.StatusCode {
	op.lock.Lock()
	status := op.status
	op.lock.Unlock()

	return status
}

func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		return
	}

	_ = op.events.Send("", api.EventTypeOperation, eventMessage)
}
