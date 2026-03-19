package operations

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
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
	// OperationClassDurable represents the Durable OperationClass.
	OperationClassDurable OperationClass = 4
)

func (t OperationClass) String() string {
	return map[OperationClass]string{
		OperationClassTask:      api.OperationClassTask,
		OperationClassWebsocket: api.OperationClassWebsocket,
		OperationClassToken:     api.OperationClassToken,
		OperationClassDurable:   api.OperationClassDurable,
	}[t]
}

const (
	// EntityURL is set in the operation metadata if the caller requests a resource that might have a generated name.
	// For example, EntityURL is set on instance creation because a name is generated if one is not provided by the client.
	// Whereas EntityURL is not set on creation of a custom storage volume, because a name must be provided.
	// The value corresponding to EntityURL must be a string.
	EntityURL = "entity_url"
)

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
		return nil, fmt.Errorf("Operation %q does not exist", id)
	}

	return op, nil
}

// Operation represents an operation.
type Operation struct {
	projectName     string
	id              string
	class           OperationClass
	createdAt       time.Time
	updatedAt       time.Time
	status          api.StatusCode
	url             string
	resources       map[entity.Type][]api.URL
	entityURL       *api.URL
	metadata        map[string]any
	err             string
	errCode         int64
	readonly        bool
	description     string
	dbOpType        operationtype.Type
	requestor       *opRequestor
	metricsCallback func(metrics.RequestResult)
	logger          logger.Logger
	location        string

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(context.Context, *Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

	// Inputs for the operation, which are stored in the database.
	inputs map[string]any

	// Operations which conflict with each other share the same conflict reference.
	conflictReference string

	// If this operation is part of a bulk operation, parent will point to the parent operation.
	parent   *Operation
	children []*Operation

	// finished is cancelled when the operation has finished executing all configured hooks.
	// It is used by Wait, to wait on the operation to be fully completed.
	finished cancel.Canceller

	// running is the basis of the [context.Context] passed into the onRun hook.
	// It is cancelled when the onRun hook completes or when Cancel is called (on operation deletion).
	running cancel.Canceller

	// heartbeatMissed is set to true if the heartbeat was not received on this node.
	// In such case we stop all the durable operations running on this node, these will be restarted on the cluster leader.
	heartbeatMissed bool

	// Locking for concurent access to the Operation
	lock sync.Mutex

	state  *state.State
	events *events.Server
}

// RunHook represents the function signature for an operation run hook.
type RunHook func(ctx context.Context, op *Operation) error

// OperationArgs contains all the arguments for operation creation.
type OperationArgs struct {
	ProjectName     string
	Type            operationtype.Type
	Class           OperationClass
	EntityURL       *api.URL
	Resources       map[entity.Type][]api.URL
	Metadata        map[string]any
	RunHook         RunHook
	ConnectHook     func(op *Operation, r *http.Request, w http.ResponseWriter) error
	requestor       *opRequestor
	metricsCallback func(result metrics.RequestResult)
	Inputs          map[string]any
	// ConflictReference allows to create the operation only if no other operation with the same conflict reference is running.
	// Empty ConflictReference means the operation can be started anytime.
	ConflictReference string
	Children          []*OperationArgs
}

// OperationScheduler is a signature used in function arguments where the function is used to deduplicate operation
// argument initialisation logic where the operation can be scheduled within an HTTP request or within an operation.
type OperationScheduler func(s *state.State, args OperationArgs) (*Operation, error)

// ScheduleUserOperationFromRequest schedules a new [Operation] from the given HTTP request.
// The request context must contain the requestor as that is used for auditing.
// The operation will keep a reference to the parent HTTP request until it completes so that it can report success or
// failure for API metrics.
func ScheduleUserOperationFromRequest(s *state.State, r *http.Request, args OperationArgs) (*Operation, error) {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return nil, fmt.Errorf("Cannot create user operation: %w", err)
	}

	metricsCallback, err := request.GetContextValue[func(metrics.RequestResult)](r.Context(), request.CtxMetricsCallbackFunc)
	if err != nil {
		return nil, fmt.Errorf("Cannot create user operation: %w", err)
	}

	args.requestor = &opRequestor{
		identityID: requestor.CallerIdentityID(),
		r:          requestor.OperationRequestor(),
	}

	args.metricsCallback = metricsCallback
	return scheduleOperation(s, args)
}

// ScheduleUserOperationFromOperation schedules a new [Operation] from the given operation.
// The operation must have a requestor as that is used for auditing.
func ScheduleUserOperationFromOperation(s *state.State, op *Operation, args OperationArgs) (*Operation, error) {
	requestor := op.Requestor()
	if requestor == nil {
		return nil, errors.New("Cannot create user operation: No requestor present in parent operation")
	}

	args.requestor = requestor
	return scheduleOperation(s, args)
}

// ScheduleServerOperation schedules a new [Operation] that runs as a server background task.
func ScheduleServerOperation(s *state.State, args OperationArgs) (*Operation, error) {
	return scheduleOperation(s, args)
}

// DurableOperationTable represents the table of durable operation hooks.
type DurableOperationTable map[operationtype.Type]RunHook

var (
	durableOperations     DurableOperationTable
	durableOperationsOnce sync.Once
)

// InitDurableOperations initializes the durable operations table.
// As durable operations can be restarted on other nodes, the durable operation handlers cannot be defined only in the memory of the node.
// Therefore we maintain a static map of durable operation handlers based on operation type.
// As this map contains handlers from across many packages, the table itself is defined in the main package.
// Because this table needs to be accessible from the operations package, we provide this Init function to set it.
func InitDurableOperations(opTable DurableOperationTable) {
	durableOperationsOnce.Do(func() {
		durableOperations = opTable
	})
}

// scheduleOperation schedules a new operation and returns it. If it cannot be created, it returns an error.
func scheduleOperation(s *state.State, args OperationArgs) (*Operation, error) {
	// initOperation initializes a single operation structure.
	initOperation := func(s *state.State, args OperationArgs) (*Operation, error) {
		// Don't allow new operations when LXD is shutting down.
		if s != nil && s.ShutdownCtx.Err() == context.Canceled {
			return nil, errors.New("LXD is shutting down")
		}

		// Quick check.
		if args.Class == OperationClassDurable {
			if args.RunHook != nil || args.ConnectHook != nil {
				return nil, errors.New("Durable operation run and connect hooks are statically defined")
			}
		}

		if args.Class != OperationClassWebsocket && args.ConnectHook != nil {
			return nil, errors.New("Only websocket operations can have a Connect hook")
		}

		if args.Class == OperationClassWebsocket && args.ConnectHook == nil {
			return nil, errors.New("Websocket operations must have a Connect hook")
		}

		if args.Class == OperationClassToken && args.RunHook != nil {
			return nil, errors.New("Token operations cannot have a Run hook")
		}

		// Validate that the primary entity URL matches the operation entity type to ensure that the operation entity URL
		// can be reconstructed from a database record (where it is saved as an entity ID).
		operationEntityType := args.Type.EntityType()
		if args.EntityURL != nil {
			entityType, _, _, _, err := entity.ParseURL(args.EntityURL.URL)
			if err != nil {
				return nil, fmt.Errorf("Invalid operation entity URL: %w", err)
			}

			if entityType != operationEntityType {
				return nil, fmt.Errorf("Entity type for URL %q does not match operation entity type %q", args.EntityURL, operationEntityType)
			}
		} else if operationEntityType != entity.TypeServer {
			return nil, errors.New("Operation entity URL required")
		} else {
			args.EntityURL = entity.ServerURL()
		}

		// Use a v7 UUID for the operation ID.
		uuid, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("Failed generating operation UUID: %w", err)
		}

		// Main attributes
		op := Operation{}
		op.projectName = args.ProjectName
		op.id = uuid.String()
		op.description = args.Type.Description()
		op.dbOpType = args.Type
		op.class = args.Class
		op.createdAt = time.Now()
		op.updatedAt = op.createdAt
		op.status = api.Running
		op.url = api.NewURL().Path(version.APIVersion, "operations", op.id).String()
		op.entityURL = args.EntityURL
		op.resources = args.Resources
		op.finished = cancel.New()
		op.running = cancel.New()
		op.state = s
		op.requestor = args.requestor
		op.metricsCallback = args.metricsCallback
		op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})
		op.inputs = args.Inputs
		op.conflictReference = args.ConflictReference

		if s != nil {
			op.SetEventServer(s.Events)
			op.location = s.ServerName
		}

		op.metadata, err = validateMetadata(args.Metadata)
		if err != nil {
			return nil, fmt.Errorf("Failed validating operation metadata: %w", err)
		}

		// Callback functions
		op.onRun = args.RunHook
		op.onConnect = args.ConnectHook

		if op.class == OperationClassDurable {
			// Durable operations must have their hooks defined in the durable operations table.
			runHook, ok := durableOperations[args.Type]
			if !ok {
				return nil, fmt.Errorf("No durable operation handlers defined for operation type %d (%q)", args.Type, args.Type.Description())
			}

			op.onRun = runHook
		}

		return &op, nil
	}

	// If this is a bulk operation, don't allow more than one level of nesting.
	for _, child := range args.Children {
		if len(child.Children) > 0 {
			return nil, errors.New("Bulk operations cannot have nested bulk operations")
		}
	}

	// If this is a bulk operation, ensure that parent has no run hook.
	// There's really no strong reason why parent could not have a run hook, but because this is currently unused (thus untested), ensure it doesn't happen.
	if len(args.Children) > 0 && args.RunHook != nil {
		return nil, errors.New("Bulk operation parent cannot have a Run hook")
	}

	// If this is a single task operation without children, it must have a run hook.
	if !slices.Contains([]OperationClass{OperationClassWebsocket, OperationClassToken, OperationClassDurable}, args.Class) && args.Children == nil && args.RunHook == nil {
		return nil, errors.New("Task operations must have a Run hook")
	}

	// Create the parent operation
	op, err := initOperation(s, args)
	if err != nil {
		return nil, err
	}

	// If this is a bulk durable operation, clear the parent run hook even if it's defined in the table per parent operation type.
	if op.class == OperationClassDurable && len(args.Children) > 0 {
		op.onRun = nil
	}

	// Create the child operations, if any.
	op.children = make([]*Operation, 0, len(args.Children))
	for _, childArgs := range args.Children {
		// Child operations inherit the requestor from the parent operation.
		// metricsCallback is set only on the parent operation, so that it's called only once for the whole bulk operation.
		childArgs.requestor = args.requestor
		childOp, err := initOperation(s, *childArgs)
		if err != nil {
			return nil, fmt.Errorf("Failed creating child operation: %w", err)
		}

		op.AddChildren(childOp)
	}

	shutdownCtx := context.TODO()
	if op.state != nil {
		shutdownCtx = op.state.ShutdownCtx
	}

	err = registerDBOperation(shutdownCtx, op)
	if err != nil {
		return nil, err
	}

	// Durable operations need to be able to be reloaded from the database.
	// To ease debugging in case of issues, we want to ensure the reloaded operation will be identical to the one originally created.
	// Therefore, reload the operation from the database here to ensure everything is properly persisted and can be reloaded correctly.
	// Notably, when unix socket is used for auth, the op.requestor.OriginAddress is set to '@'. This is not persisted in the database,
	// so reloading the operation ensures we work with empty ("") OriginAddress instead of "@".
	if op.class == OperationClassDurable {
		newOp, err := loadDurableOperationFromDB(op)
		if err != nil {
			return nil, fmt.Errorf("Failed reloading durable operation from database: %w", err)
		}

		// Clear the run hook if it's a parent operation.
		if len(op.children) > 0 {
			newOp.onRun = nil
		}

		newOp.children = make([]*Operation, 0, len(op.children))
		for _, childOp := range op.children {
			reconstructedChildOp, err := loadDurableOperationFromDB(childOp)
			if err != nil {
				return nil, fmt.Errorf("Failed reloading durable child operation from database: %w", err)
			}

			newOp.AddChildren(reconstructedChildOp)
		}

		op = newOp
	}

	op.logger.Debug("New operation")

	operationsLock.Lock()
	operations[op.id] = op
	for _, childOp := range op.children {
		operations[childOp.id] = childOp
	}

	operationsLock.Unlock()

	op.start()
	return op, nil
}

// AddChildren adds a child operation to the parent operation. It also sets the parent of the child operation to the parent operation.
func (op *Operation) AddChildren(children ...*Operation) {
	if op.children == nil {
		op.children = make([]*Operation, 0, len(children))
	}

	op.lock.Lock()
	op.children = append(op.children, children...)
	op.lock.Unlock()
	for _, child := range children {
		child.lock.Lock()
		child.parent = op
		child.lock.Unlock()
	}
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
		return fmt.Errorf("Failed verifying operation requestor: %w", err)
	}

	if !opRequestor.CallerIsEqual(requestor) {
		return api.StatusErrorf(http.StatusForbidden, "Operation requestor mismatch")
	}

	return nil
}

// Requestor returns the initial requestor for this operation.
func (op *Operation) Requestor() *opRequestor {
	return op.requestor
}

// EventLifecycleRequestor returns the [api.EventLifecycleRequestor] for the operation.
func (op *Operation) EventLifecycleRequestor() *api.EventLifecycleRequestor {
	if op.requestor == nil {
		return &api.EventLifecycleRequestor{}
	}

	return op.requestor.EventLifecycleRequestor()
}

// statusToMetricsResult converts the operation status to a [metrics.RequestResult].
func statusToMetricsResult(status api.StatusCode) metrics.RequestResult {
	switch status {
	case api.Success, api.Cancelled:
		return metrics.Success
	default:
		return metrics.ErrorServer
	}
}

func (op *Operation) done() {
	if op.metricsCallback != nil {
		op.metricsCallback(statusToMetricsResult(op.status))
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

	// If we are a child operation, we're done. The parent operation will clean all the child entries when it finishes.
	if op.parent != nil {
		return
	}

	// If this is a durable operation, or a parent operation of a bulk operation,
	// we clear the entries from the internal map, but leave the database records in place.
	// The database records will be cleared later by the pruneExpiredOperationsTask().
	if len(op.children) > 0 || op.class == OperationClassDurable {
		operationsLock.Lock()
		_, ok := operations[op.id]
		if !ok {
			operationsLock.Unlock()
			return
		}

		delete(operations, op.id)

		// Clear child operations
		for _, childOp := range op.children {
			_, ok := operations[childOp.id]
			if ok {
				delete(operations, childOp.id)
			}
		}

		operationsLock.Unlock()
		return
	}

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
			op.logger.Warn("Failed deleting operation", logger.Ctx{"status": op.status, "err": err})
		}
	}()
}

func updateStatus(op *Operation, newStatus api.StatusCode) {
	oldStatus := op.status
	// We cannot really use operation context as it was already cancelled.
	err := op.updateStatus(context.TODO(), newStatus)
	if err != nil {
		op.logger.Warn("Failed updating operation status", logger.Ctx{
			"operation": op.id,
			"err":       err,
			"oldStatus": oldStatus,
			"newStatus": newStatus,
		})
	}
}

// start a pending operation.
func (op *Operation) start() {
	op.lock.Lock()

	// If there's a run hook, we need to run it and get the final status from it.
	// If there are child operations, we need to start and wait for them to finish before we can get the final status of the parent operation.
	if op.onRun != nil || len(op.children) > 0 {
		// The operation context is the "running" context plus the requestor.
		// The requestor is available directly on the operation, but we should still put it in the context.
		// This is so that, if an operation queries another cluster member, the requestor information will be set
		// in the request headers.
		runCtx := context.Context(op.running)
		if op.requestor != nil {
			runCtx = request.WithRequestor(runCtx, op.requestor)
		}

		go func(ctx context.Context, op *Operation) {
			var err error
			// Run the run hook.
			// We don't allow bulk operations with run hook. So there's either run hook, or children, and we can just run the run hook with children serialized.
			if op.onRun != nil {
				err = op.onRun(ctx, op)
			}

			// If we're a parent operation with children, wait until all of our child operations are done.
			// This is to ensure that the parent operation remains visible in the API until all child operations have completed,
			// which is important for operations that spawn multiple child operations (eg. bulk operations),
			// so that the user can see the overall progress of the operation until everything is done.
			if op.parent == nil && len(op.children) > 0 {
				// Start child operations
				for _, childOp := range op.children {
					childOp.start()
				}

				var childFailed bool
				var childCancelled bool
				for _, childOp := range op.children {
					err := childOp.Wait(context.Background())

					if err != nil {
						if errors.Is(err, context.Canceled) {
							childCancelled = true
						} else {
							childFailed = true
						}
					}
				}

				// Parent operation inherits the error from its children if these failed or were cancelled.
				if childFailed {
					err = errors.New("One or more child operations failed")
				} else if childCancelled {
					err = context.Canceled
				}
			}

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

				// If the durable operation was cancelled locally because of missed heartbeat,
				// we only clear it from the local operations map and leave the database record intact.
				if op.heartbeatMissed {
					// Mark the operation as finished. running context is already cancelled.
					op.status = api.Cancelled
					op.finished.Cancel()
					op.lock.Unlock()

					// Remove the operation from the local operations map.
					operationsLock.Lock()
					_, ok := operations[op.id]
					if !ok {
						operationsLock.Unlock()
						return
					}

					delete(operations, op.id)
					operationsLock.Unlock()
					return
				}

				// If the run context was cancelled, the previous state should be "cancelling", and the final state should be "cancelled".
				if errors.Is(err, context.Canceled) {
					updateStatus(op, api.Cancelled)
				} else {
					updateStatus(op, api.Failure)
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
			updateStatus(op, api.Success)
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

	if op.onRun != nil || len(op.children) > 0 {
		// If the operation has a run hook, or this is a parent operation waiting for children, set the status to cancelling.
		// If there's a run hook, the status, error and error code will be set to cancelled by the start routine because the run context is cancelled.
		// The allows an operation to emit a cancelling status if it is in the middle of something that could take a while to clean up.
		// If we're a parent operation with children, the start routine is waiting for the children to finish,
		// and will set the final status, error and error code to cancelled.
		updateStatus(op, api.Cancelling)

		// Signal the child operations to stop as well.
		for _, childOp := range op.children {
			childOp.Cancel()
		}
	} else {
		// If the operation does not have any children or a run hook, set the status and error to cancelled because there is nothing to clean up.
		// We cannot use the operation context here because it has already been cancelled above.
		op.err = context.Canceled.Error()
		op.errCode = http.StatusInternalServerError
		updateStatus(op, api.Cancelled)
	}

	op.lock.Unlock()

	op.logger.Debug("Cancelling operation")
	_, md := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	// If the operation does not have a run hook (e.g. a token operation) we need to call op.done(), because it won't be
	// called automatically when the run hook completes.
	if op.onRun == nil {
		op.done()
	}
}

// CancelLocalDurableOperation stops a durable operation running on this node.
// This operation is only removed from the local operations map, the database record is left intact.
// The cluster leader will restart this operation.
func CancelLocalDurableOperation(op *Operation) {
	logger.Warn("Cancelling local durable operation", logger.Ctx{"operation": op.id})
	// Note that on purpose we don't lock the operation lock here. Both setting the heartbeatMissed flag and cancelling the operation are atomic operations,
	// so we don't need to lock the operation for that.
	// If we'd lock the operation here, we might end up waiting on the lock while the operation is trying to update its status, which would delay concelling the operation context.

	// Mark this operation as having missed the heartbeat.
	// It will tell the end routines not to clear the database record.
	op.heartbeatMissed = true
	// Signal the operation to stop.
	// The operation will be marked as finished and removed from the local operations map
	// by the rest of the Start() routine after it actually stops.
	op.running.Cancel()
}

// CancelLocalDurableOperations stops all durable operations running on this node.
// These operations are only removed from the local operations map, the database records are left intact.
// The cluster leader will later restart these operations.
func CancelLocalDurableOperations() {
	operationsLock.Lock()
	for _, op := range operations {
		if op.class != OperationClassDurable {
			continue
		}

		CancelLocalDurableOperation(op)
	}

	operationsLock.Unlock()
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
		if op.err != "" {
			return nil, api.NewStatusError(int(op.errCode), "Failed connecting to operation: "+op.err)
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
	// Setup the resource URLs
	renderedResources := make(map[string][]string)
	resources := op.resources
	if resources != nil {
		tmpResources := make(map[string][]string)
		for key, value := range resources {
			var values = make([]string, 0, len(value))
			for _, c := range value {
				values = append(values, c.String())
			}

			tmpResources[string(key)] = values
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
		Location:    op.location,
		Err:         op.err,
		ErrCode:     op.errCode,
	}

	requestor := op.Requestor()
	if requestor != nil {
		retOp.Requestor = requestor.OperationRequestor()
	}

	op.lock.Unlock()

	return op.url, retOp
}

// RenderWithoutProgress renders the operation structure without progress metadata.
// This is used when operation constructed from the database is returned via API, as database likely contains stale progress metadata.
// Progress should be consumed from the websocket events, so it doesn't need to be returned in the API response.
func (op *Operation) RenderWithoutProgress() (string, *api.Operation) {
	url, retOp := op.Render()

	for key := range retOp.Metadata {
		if strings.HasSuffix(key, "progress") {
			delete(retOp.Metadata, key)
		}
	}

	return url, retOp
}

// RenderFullWithoutProgress renders the operation structure, including child operations, without progress metadata.
func (op *Operation) RenderFullWithoutProgress() (string, *api.OperationFull) {
	url, baseOp := op.RenderWithoutProgress()

	op.lock.Lock()
	defer op.lock.Unlock()

	retOp := &api.OperationFull{
		Operation: *baseOp,
	}

	if len(op.children) > 0 {
		childAPIOps := make([]*api.Operation, 0, len(op.children))
		for _, childOp := range op.children {
			_, child := childOp.RenderWithoutProgress()
			childAPIOps = append(childAPIOps, child)
		}

		// Sort operations by UUID. Since we use UUIDv7, this will also sort operations by creation time.
		slices.SortFunc(childAPIOps, func(a, b *api.Operation) int {
			return strings.Compare(a.ID, b.ID)
		})

		retOp.Children = make([]api.Operation, 0, len(op.children))
		for _, childOp := range childAPIOps {
			retOp.Children = append(retOp.Children, *childOp)
		}
	}

	return url, retOp
}

// Wait for the operation to be done.
// Returns non-nil error if operation failed or context was cancelled.
func (op *Operation) Wait(ctx context.Context) error {
	select {
	case <-op.finished.Done():
		if op.err != "" {
			// Custom error types can contain additional information about the failure.
			// To ensure the error returned from the database is the same as error returned
			// directly from the operation code, we return a new error object consisting
			// only of the error message and error code.

			// If the operation was cancelled, return fresh context.Cancelled error.
			if op.status == api.Cancelled {
				return context.Canceled
			}

			// For other errors, return a new error with the same message and code.
			return api.NewStatusError(int(op.errCode), op.err)
		}

		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// EntityURL returns the primary entity URL for the Operation.
// This is used by the LXD shutdown process to determine if it should wait for any operations to complete.
func (op *Operation) EntityURL() *api.URL {
	return op.entityURL
}

func (op *Operation) updateStatus(ctx context.Context, newStatus api.StatusCode) error {
	op.status = newStatus
	op.updatedAt = time.Now()
	return updateDBOperation(ctx, op)
}

// UpdateMetadata updates the metadata of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateMetadata(opMetadata map[string]any) error {
	opMetadata, err := validateMetadata(opMetadata)
	if err != nil {
		return fmt.Errorf("Failed updating operation metadata: %w", err)
	}

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

// CommitMetadata commits the metadata and status of the operation to the database, and updates the updatedAt time.
func (op *Operation) CommitMetadata() error {
	op.lock.Lock()
	defer op.lock.Unlock()

	op.updatedAt = time.Now()
	// Use the operation context for the database update, so that if the operation is cancelled, the database update will be cancelled as well.
	return updateDBOperation(context.Context(op.running), op)
}

// ExtendMetadata updates the metadata of the operation with the additional data provided.
// It returns an error if the operation is not pending or running, or the operation is read-only.
func (op *Operation) ExtendMetadata(metadata map[string]any) error {
	op.lock.Lock()

	// Quick checks.
	if op.finished.Err() != nil {
		op.lock.Unlock()
		return api.NewStatusError(http.StatusBadRequest, "Operations cannot be updated after they have completed")
	}

	if op.readonly {
		op.lock.Unlock()
		return errors.New("Read-only operations cannot be updated")
	}

	// Get current metadata.
	newMetadata := op.metadata
	op.lock.Unlock()

	// Merge with current one.
	if op.metadata == nil {
		newMetadata = metadata
	} else {
		maps.Copy(newMetadata, metadata)
	}

	newMetadata, err := validateMetadata(newMetadata)
	if err != nil {
		return fmt.Errorf("Failed extending operation metadata: %w", err)
	}

	// Update the operation.
	op.lock.Lock()
	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
	_, md := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// ID returns the operation ID.
func (op *Operation) ID() string {
	return op.id
}

// Metadata returns a copy of the operation Metadata.
func (op *Operation) Metadata() map[string]any {
	op.lock.Lock()
	defer op.lock.Unlock()
	return maps.Clone(op.metadata)
}

// URL returns the operation URL.
func (op *Operation) URL() string {
	return op.url
}

// Resources returns the operation resources.
func (op *Operation) Resources() map[entity.Type][]api.URL {
	return op.resources
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
func (op *Operation) Inputs() map[string]any {
	return op.inputs
}

// Parent returns the parent operation if this operation is a child operation, or nil if this operation is not a child operation.
func (op *Operation) Parent() *Operation {
	return op.parent
}

// validateMetadata is used to enforce some consistency in operation metadata.
func validateMetadata(metadata map[string]any) (map[string]any, error) {
	// Ensure metadata is never nil.
	if metadata == nil {
		metadata = make(map[string]any)
	}

	// If the entity_url field is used, it must always be a string and must always be a valid URL.
	entityURLAny, ok := metadata[EntityURL]
	if ok {
		entityURL, ok := entityURLAny.(string)
		if !ok {
			return nil, fmt.Errorf("Operation metadata entity_url must be a string (got %T)", entityURLAny)
		}

		err := validate.IsRequestURL(entityURL)
		if err != nil {
			return nil, fmt.Errorf("Operation metadata entity_url must be a valid request URL: %w", err)
		}
	}

	return metadata, nil
}

// UpdateProgress updates the operation metadata with progress information, including
// the percentage complete, data processed, and speed. It formats and stores these values for
// both API callers and CLI display purposes.
func (op *Operation) UpdateProgress(stage string, displayPrefix string, percent int64, processed int64, speed int64) error {
	// Copy current metadata.
	metadata := op.Metadata()

	// Delete any keys that end in "_progress", we rely on there only being one.
	for k := range metadata {
		if strings.HasSuffix(k, "_progress") {
			delete(metadata, k)
		}
	}

	// Create a map for progress.
	// stage, percent, speed sent for API callers.
	progress := make(map[string]string)
	progress["stage"] = stage
	if processed > 0 {
		progress["processed"] = strconv.FormatInt(processed, 10)
	}

	if percent > 0 {
		progress["percent"] = strconv.FormatInt(percent, 10)
	}

	progress["speed"] = strconv.FormatInt(speed, 10)

	metadata["progress"] = progress

	// <stage>_progress with formatted text sent for lxc cli.
	activeStageProgress := stage + "_progress"
	if percent > 0 {
		if speed > 0 {
			metadata[activeStageProgress] = fmt.Sprintf("%s: %d%% (%s/s)", displayPrefix, percent, units.GetByteSizeString(speed, 2))
		} else {
			metadata[activeStageProgress] = fmt.Sprintf("%s: %d%%", displayPrefix, percent)
		}
	} else if processed > 0 {
		metadata[activeStageProgress] = displayPrefix + ": " + units.GetByteSizeString(processed, 2) + " (" + units.GetByteSizeString(speed, 2) + "/s)"
	} else {
		metadata[activeStageProgress] = displayPrefix + ": " + units.GetByteSizeString(speed, 2) + "/s"
	}

	// Write the updated metadata.
	return op.UpdateMetadata(metadata)
}
