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

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var debug bool

var operationsLock sync.Mutex
var operations = make(map[string]*Operation)

// OperationClass represents the OperationClass type.
type OperationClass int

const (
	// OperationClassTask represents the Task OperationClass.
	OperationClassTask OperationClass = 1
	// OperationClassWebsocket represents the Websocket OperationClass.
	OperationClassWebsocket OperationClass = 2
	// OperationClassToken represents the Token OperationClass.
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

// Clone returns a clone of the internal operations map containing references to the actual operations.
func Clone() map[string]*Operation {
	operationsLock.Lock()
	defer operationsLock.Unlock()

	localOperations := make(map[string]*Operation, len(operations))
	maps.Copy(localOperations, operations)

	return localOperations
}

// OperationGetInternal returns the operation with the given id. It returns an
// error if it doesn't exist.
func OperationGetInternal(id string) (*Operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation %q doesn't exist", id)
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
	resources   map[string][]api.URL
	metadata    map[string]any
	err         error
	readonly    bool
	description string
	entityType  entity.Type
	entitlement auth.Entitlement
	dbOpType    operationtype.Type
	requestor   *request.Requestor
	logger      logger.Logger

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(context.Context, *Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error
	onDone    func(*Operation)

	// inputs holds the JSON encoded inputs for the operation.
	// These are stored in the database.
	inputs string

	// finished is cancelled when the operation has finished executing all configured hooks.
	// It is used by Wait, to wait on the operation to be fully completed.
	finished cancel.Canceller

	// running is the basis of the [context.Context] passed into the onRun hook.
	// It is cancelled when the onRun hook completes or when Cancel is called (on operation deletion).
	running cancel.Canceller

	// Locking for concurent access to the Operation
	lock sync.Mutex

	state  *state.State
	events *events.Server
}

// OperationArgs contains all the arguments for operation creation.
type OperationArgs struct {
	ProjectName string
	Type        operationtype.Type
	Class       OperationClass
	Resources   map[string][]api.URL
	Metadata    any
	RunHook     func(ctx context.Context, op *Operation) error
	ConnectHook func(op *Operation, r *http.Request, w http.ResponseWriter) error
	Inputs      string
}

// CreateUserOperation creates a new [Operation]. The [request.Requestor] argument must be non-nil, as this is required for auditing.
func CreateUserOperation(s *state.State, requestor *request.Requestor, args OperationArgs) (*Operation, error) {
	if requestor == nil || requestor.OriginAddress() == "" {
		return nil, errors.New("Cannot create user operation, the requestor must be set")
	}

	return operationCreate(s, requestor, args)
}

// CreateServerOperation creates a new [Operation] that runs as a server background task.
func CreateServerOperation(s *state.State, args OperationArgs) (*Operation, error) {
	return operationCreate(s, nil, args)
}

// initOperation initializes a new operation structure. It does not register it in the database.
func initOperation(s *state.State, requestor *request.Requestor, args OperationArgs) (*Operation, error) {
	// Don't allow new operations when LXD is shutting down.
	if s != nil && s.ShutdownCtx.Err() == context.Canceled {
		return nil, errors.New("LXD is shutting down")
	}

	// Main attributes
	op := Operation{}
	op.projectName = args.ProjectName
	op.id = uuid.New().String()
	op.description = args.Type.Description()
	op.entityType, op.entitlement = args.Type.Permission()
	op.dbOpType = args.Type
	op.class = args.Class
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = api.Pending
	op.url = api.NewURL().Path(version.APIVersion, "operations", op.id).String()
	op.resources = args.Resources
	op.finished = cancel.New()
	op.running = cancel.New()
	op.state = s
	op.requestor = requestor
	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})
	op.inputs = args.Inputs

	if s != nil {
		op.SetEventServer(s.Events)
	}

	newMetadata, err := shared.ParseMetadata(args.Metadata)
	if err != nil {
		return nil, err
	}

	op.metadata = newMetadata

	// Callback functions
	op.onRun = args.RunHook
	op.onConnect = args.ConnectHook

	// Quick check.
	if op.class != OperationClassWebsocket && op.onConnect != nil {
		return nil, errors.New("Only websocket operations can have a Connect hook")
	}

	if op.class == OperationClassWebsocket && op.onConnect == nil {
		return nil, errors.New("Websocket operations must have a Connect hook")
	}

	if op.class == OperationClassToken && op.onRun != nil {
		return nil, errors.New("Token operations cannot have a Run hook")
	}

	return &op, nil
}

// operationCreate creates a new operation and returns it. If it cannot be created, it returns an error.
func operationCreate(s *state.State, requestor *request.Requestor, args OperationArgs) (*Operation, error) {
	op, err := initOperation(s, requestor, args)
	if err != nil {
		return nil, err
	}

	err = registerDBOperation(op)
	if err != nil {
		return nil, err
	}

	op.logger.Debug("New operation")
	_, md, _ := op.Render()

	operationsLock.Lock()
	operations[op.id] = op
	op.sendEvent(md)
	operationsLock.Unlock()

	return op, nil
}

// SetEventServer allows injection of event server.
func (op *Operation) SetEventServer(events *events.Server) {
	op.events = events
}

// CheckRequestor checks that the requestor of a given HTTP request is equal to the requestor of the operation.
func (op *Operation) CheckRequestor(r *http.Request) error {
	opRequestor := op.Requestor()
	if opRequestor == nil {
		return errors.New("Operation does not contain a requestor")
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return fmt.Errorf("Failed to verify operation requestor: %w", err)
	}

	if !opRequestor.CallerIsEqual(requestor) {
		return api.StatusErrorf(http.StatusForbidden, "Operation requestor mismatch")
	}

	return nil
}

// SetOnDone sets the operation onDone function that is called after the operation completes.
func (op *Operation) SetOnDone(f func(*Operation)) {
	op.onDone = f
}

// Requestor returns the initial requestor for this operation.
func (op *Operation) Requestor() *request.Requestor {
	return op.requestor
}

// EventLifecycleRequestor returns the [api.EventLifecycleRequestor] for the operation.
func (op *Operation) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	if op.requestor == nil {
		return &api.EventLifecycleRequestor{}
	}

	return op.requestor.EventLifecycleRequestor()
}

func (op *Operation) done() {
	if op.onDone != nil {
		// This can mark the request that spawned this operation as completed for the API metrics.
		op.onDone(op)
	}

	if op.readonly {
		return
	}

	op.lock.Lock()
	op.readonly = true
	op.onRun = nil
	op.onConnect = nil
	op.finished.Cancel()
	op.lock.Unlock()

	go func() {
		shutdownCtx := context.Background()
		if op.state != nil {
			shutdownCtx = op.state.ShutdownCtx
		}

		select {
		case <-shutdownCtx.Done():
			return // Expect all operation records to be removed by daemon.Stop in one query.
		case <-time.After(time.Second * 5): // Wait 5s before removing from internal map and database.
		}

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
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			// Operations can be deleted from the database before the operation clean up go routine has
			// run in cases where the project that the operation(s) are associated to is deleted first.
			// So don't log warning if operation not found.
			op.logger.Warn("Failed to delete operation", logger.Ctx{"status": op.status, "err": err})
		}
	}()
}

func updateStatusWithWarning(op *Operation, newStatus api.StatusCode) {
	oldStatus := op.status
	err := op.updateStatus(newStatus)
	if err != nil {
		op.logger.Warn("Failed updating operation status", logger.Ctx{
			"operation": op.id,
			"err":       err,
			"oldStatus": oldStatus,
			"newStatus": newStatus,
		})
	}
}

// Start a pending operation. It returns an error if the operation cannot be started.
func (op *Operation) Start() error {
	op.lock.Lock()
	if op.status != api.Pending {
		op.lock.Unlock()
		return errors.New("Only pending operations can be started")
	}

	err := op.updateStatus(api.Running)
	if err != nil {
		op.lock.Unlock()
		return fmt.Errorf("Failed updating Operation %q (%q) status: %w", op.id, op.description, err)
	}

	if op.onRun != nil {
		// The operation context is the "running" context plus the requestor.
		// The requestor is available directly on the operation, but we should still put it in the context.
		// This is so that, if an operation queries another cluster member, the requestor information will be set
		// in the request headers.
		runCtx := context.Context(op.running)
		if op.requestor != nil {
			runCtx = request.WithRequestor(runCtx, op.requestor)
		}

		go func(ctx context.Context, op *Operation) {
			err := op.onRun(ctx, op)
			if err != nil {
				op.lock.Lock()

				op.err = err

				// If the run context was cancelled, the previous state should be "cancelling", and the final state should be "cancelled".
				if errors.Is(err, context.Canceled) {
					updateStatusWithWarning(op, api.Cancelled)
				} else {
					updateStatusWithWarning(op, api.Failure)
				}

				// Always call cancel. This is a no-op if already cancelled.
				op.running.Cancel()

				op.lock.Unlock()
				op.done()

				op.logger.Warn("Failure for operation", logger.Ctx{"err": err})
				_, md, _ := op.Render()

				op.lock.Lock()
				op.sendEvent(md)
				op.lock.Unlock()

				return
			}

			op.lock.Lock()
			updateStatusWithWarning(op, api.Success)
			op.running.Cancel()
			op.lock.Unlock()
			op.done()

			op.logger.Debug("Success for operation")
			_, md, _ := op.Render()

			op.lock.Lock()
			op.sendEvent(md)
			op.lock.Unlock()
		}(runCtx, op)
	}

	op.lock.Unlock()

	op.logger.Debug("Started operation")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// IsRunning returns true if the operation run hook is still in progress.
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

	// If the operation has a run hook, then set the status to cancelling.
	// When the hook returns, the status will be set to cancelled because the run context is cancelled.
	// The allows an operation to emit a cancelling status if it is in the middle of something that could take a while to clean up.
	//
	// If the operation does not have a run hook, immediately set the status to cancelled because there is nothing to clean up.
	if op.onRun != nil {
		updateStatusWithWarning(op, api.Cancelling)
	} else {
		updateStatusWithWarning(op, api.Cancelled)
	}

	op.lock.Unlock()

	op.logger.Debug("Cancelling operation")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	// If the operation does not have a run hook (e.g. a token operation) we need to call op.done(), because it won't be
	// called automatically when the run hook completes.
	if op.onRun == nil {
		op.done()
	}
}

// Connect connects a websocket operation. If the operation is not a websocket
// operation or the operation is not running, it returns an error.
func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	op.lock.Lock()
	if op.class != OperationClassWebsocket {
		op.lock.Unlock()
		return nil, errors.New("Only websocket operations can be connected")
	}

	if op.running.Err() != nil {
		op.lock.Unlock()
		return nil, api.NewStatusError(http.StatusBadRequest, "Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	go func(op *Operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			op.logger.Debug("Failed to connect to operation", logger.Ctx{"err": err})
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
func (op *Operation) Render() (string, *api.Operation, error) {
	// Setup the resource URLs
	renderedResources := make(map[string][]string)
	resources := op.resources
	if resources != nil {
		tmpResources := make(map[string][]string)
		for key, value := range resources {
			var values = make([]string, 0, len(value))
			for _, c := range value {
				values = append(values, c.Project(op.Project()).String())
			}

			tmpResources[key] = values
		}

		renderedResources = tmpResources
	}

	op.lock.Lock()

	// Make a copy of the metadata to avoid concurrent reads/writes.
	metadata := make(map[string]any, len(op.metadata))
	maps.Copy(metadata, op.metadata)

	// Put together the response struct.
	retOp := &api.Operation{
		ID:          op.id,
		Class:       op.class.String(),
		Description: op.description,
		CreatedAt:   op.createdAt,
		UpdatedAt:   op.updatedAt,
		Status:      op.status.String(),
		StatusCode:  op.status,
		Resources:   renderedResources,
		Metadata:    metadata,
		MayCancel:   true,
	}

	requestor := op.Requestor()
	if requestor != nil {
		retOp.Requestor = requestor.OperationRequestor()
	}

	if op.state != nil {
		retOp.Location = op.state.ServerName
	}

	if op.err != nil {
		retOp.Err = response.SmartError(op.err).String()
	}

	op.lock.Unlock()

	return op.url, retOp, nil
}

// Wait for the operation to be done.
// Returns non-nil error if operation failed or context was cancelled.
func (op *Operation) Wait(ctx context.Context) error {
	select {
	case <-op.finished.Done():
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (op *Operation) updateStatus(newStatus api.StatusCode) error {
	op.status = newStatus
	return updateDBOperationStatus(op)
}

// UpdateMetadata updates the metadata of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateMetadata(opMetadata any) error {
	op.lock.Lock()
	if op.finished.Err() != nil {
		op.lock.Unlock()
		return api.NewStatusError(http.StatusBadRequest, "Operations cannot be updated after they have completed")
	}

	if op.readonly {
		op.lock.Unlock()
		return errors.New("Read-only operations can't be updated")
	}

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return err
	}

	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// ExtendMetadata updates the metadata of the operation with the additional data provided.
// It returns an error if the operation is not pending or running, or the operation is read-only.
func (op *Operation) ExtendMetadata(metadata any) error {
	op.lock.Lock()

	// Quick checks.
	if op.finished.Err() != nil {
		op.lock.Unlock()
		return api.NewStatusError(http.StatusBadRequest, "Operations cannot be updated after they have completed")
	}

	if op.readonly {
		op.lock.Unlock()
		return errors.New("Read-only operations can't be updated")
	}

	// Parse the new metadata.
	extraMetadata, err := shared.ParseMetadata(metadata)
	if err != nil {
		return err
	}

	// Get current metadata.
	newMetadata := op.metadata
	op.lock.Unlock()

	// Merge with current one.
	if op.metadata == nil {
		newMetadata = extraMetadata
	} else {
		maps.Copy(newMetadata, extraMetadata)
	}

	// Update the operation.
	op.lock.Lock()
	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
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
func (op *Operation) Metadata() map[string]any {
	return op.metadata
}

// URL returns the operation URL.
func (op *Operation) URL() string {
	return op.url
}

// Resources returns the operation resources.
func (op *Operation) Resources() map[string][]api.URL {
	return op.resources
}

// Permission returns the operations entity.Type and auth.Entitlement.
func (op *Operation) Permission() (entity.Type, auth.Entitlement) {
	return op.entityType, op.entitlement
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
func (op *Operation) Type() operationtype.Type {
	return op.dbOpType
}

// Inputs returns the operation inputs from the database.
func (op *Operation) Inputs() string {
	return op.inputs
}
