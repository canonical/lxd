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
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
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
	class           operationtype.Class
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
	requestor       *request.RequestorAuditor
	metricsCallback func(metrics.RequestResult)
	logger          logger.Logger
	location        string
	stage           int64 // OperationArgs has uint16 but this is an int64 to correspond to the database value.

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(context.Context, *Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

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

	// Locking for concurrent access to the Operation
	lock sync.Mutex

	state  *state.State
	events *events.Server
}

// OperationScheduler is a signature used in function arguments where the function is used to deduplicate operation
// argument initialisation logic where the operation can be scheduled within an HTTP request or within an operation.
type OperationScheduler func(s *state.State, args OperationArgs) (*Operation, error)

// ScheduleUserOperationFromRequest schedules a new [Operation] from the given HTTP request.
// The request context must contain the requestor as that is used for auditing.
// The operation will keep a reference to the parent HTTP request until it completes so that it can report success or
// failure for API metrics.
func ScheduleUserOperationFromRequest(s *state.State, r *http.Request, args OperationArgs) (*Operation, error) {
	var err error
	args.requestor, err = request.GetRequestorAuditor(r.Context())
	if err != nil {
		return nil, fmt.Errorf("Cannot create user operation: %w", err)
	}

	args.metricsCallback, err = request.GetContextValue[func(metrics.RequestResult)](r.Context(), request.CtxMetricsCallbackFunc)
	if err != nil {
		return nil, fmt.Errorf("Cannot create user operation: %w", err)
	}

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

// scheduleOperation schedules a new operation and returns it. If it cannot be created, it returns an error.
func scheduleOperation(s *state.State, args OperationArgs) (*Operation, error) {
	if s == nil {
		return nil, errors.New("State must be provided")
	}

	err := args.validate(false)
	if err != nil {
		return nil, fmt.Errorf("Failed validating operation arguments: %w", err)
	}

	// initOperation initializes a single operation structure.
	initOperation := func(s *state.State, args OperationArgs) (*Operation, error) {
		// Don't allow new operations when LXD is shutting down.
		if s.ShutdownCtx.Err() != nil {
			return nil, errors.New("LXD is shutting down")
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
		op.url = api.NewURL().Path(version.APIVersion, "operations", op.id).String()
		op.entityURL = args.EntityURL
		op.resources = args.Resources
		op.finished = cancel.New()
		op.running = cancel.New()
		op.state = s
		op.requestor = args.requestor
		op.metricsCallback = args.metricsCallback
		op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})
		op.conflictReference = args.ConflictReference
		op.events = s.Events
		op.location = s.ServerName
		op.stage = int64(args.Stage)

		// The call to args.validate already validated the entity URL. If it is nil, then it should be set to the
		// server URL (/1.0).
		entityURL := args.EntityURL
		if entityURL == nil {
			entityURL = entity.ServerURL()
		}

		op.entityURL = entityURL

		metadata := args.Metadata
		if metadata == nil {
			metadata = make(map[string]any)
		}

		// If the entity_url field is not already populated, populate it with the entity url of the operation.
		// This allows the caller to override the entity URL if e.g. creating a new entity but ensures the field is populated.
		// Skip if the entity type is "server". This doesn't give any useful information to the requestor (since the url will just be "/1.0").
		operationEntityType := args.Type.EntityType()
		_, ok := metadata[api.MetadataEntityURL]
		if !ok && operationEntityType != entity.TypeServer {
			// The project that is present in the operation entity URL is always the effective project (e.g. the actual
			// project where the resource lives in the database). This means that if a user is updating a network within
			// a project that has `features.networks=false`, the auto-generated metadata entity URL would incorrectly have
			// `project=default`. For this reason, we always overwrite the project to be the project that the operation
			// is contained within, which should always be the requested project.
			requiresProject, _ := operationEntityType.RequiresProject()
			metadataURL := *op.entityURL
			if requiresProject {
				metadataURL.Project(args.ProjectName)
			}

			metadata[api.MetadataEntityURL] = metadataURL.String()
		}

		err = validateMetadata(metadata)
		if err != nil {
			return nil, fmt.Errorf("Failed validating operation metadata: %w", err)
		}

		op.metadata = metadata

		// Only operations in stage zero are initially running.
		// OperationArgs validation ensures that parent operations are in stage zero.
		// If all children have stage zero, then they are all spawned at once.
		op.status = api.Running
		if op.stage > 0 {
			op.status = api.Pending
		}

		// Callback functions
		op.onRun = args.RunHook
		op.onConnect = args.ConnectHook

		return &op, nil
	}

	// Create the parent operation
	op, err := initOperation(s, args)
	if err != nil {
		return nil, err
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

		op.addChild(childOp)
	}

	shutdownCtx := context.TODO()
	if op.state != nil {
		shutdownCtx = op.state.ShutdownCtx
	}

	err = registerDBOperation(shutdownCtx, op)
	if err != nil {
		return nil, err
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

// addChild adds a child operation to the parent operation. It also sets the parent of the child operation to the parent operation.
func (op *Operation) addChild(child *Operation) {
	op.lock.Lock()
	op.children = append(op.children, child)
	op.lock.Unlock()
	child.lock.Lock()
	child.parent = op
	child.lock.Unlock()
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

	if !requestor.CallerIsEqual(opRequestor) {
		return api.StatusErrorf(http.StatusForbidden, "Operation requestor mismatch")
	}

	return nil
}

// Requestor returns the initial requestor for this operation.
func (op *Operation) Requestor() *request.RequestorAuditor {
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

	// If this is a parent operation of a bulk operation, we clear the entries from the internal map, but leave the database records in place.
	// The database records will be cleared later by the pruneExpiredOperationsTask().
	if len(op.children) > 0 {
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

	// Operations that have already been cancelled should not be started.
	if op.running.Err() != nil {
		op.lock.Unlock()
		return
	}

	// Pending operations have their status set to [api.Running] before invoking the run hook.
	if op.status == api.Pending {
		updateStatus(op, api.Running)
	}

	// If there's a run hook, we need to run it and get the final status from it.
	// If there are child operations, we need to start and wait for them to finish before we can get the final status of the parent operation.
	if op.onRun != nil || len(op.children) > 0 {
		// The operation context is the "running" context plus the requestor.
		// The requestor is available directly on the operation, but we should still put it in the context.
		// This is so that, if an operation queries another cluster member, the requestor information will be set
		// in the request headers.
		runCtx := context.Context(op.running)
		if op.requestor != nil {
			runCtx = request.WithRequestorAuditor(runCtx, op.requestor)
		}

		go func(ctx context.Context, op *Operation) {
			var err error
			if op.parent == nil && len(op.children) > 0 {
				err = runBulkOperation(op)
			} else if op.onRun != nil {
				// Single-task operation: just run the hook.
				err = op.onRun(ctx, op)
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

// runBulkOperation runs a bulk operation. It sorts child operations into stages and runs each stage, waiting for it to
// complete before starting the next stage. If a stage fails, operations in subsequent stages are cancelled. The run
// context is not passed in here because there is no run hook on the parent to pass it to. Cancellation of the parent is
// propagated to children via [Operation.Cancel].
func runBulkOperation(op *Operation) error {
	// Get a shallow clone of the child operations.
	op.lock.Lock()
	children := slices.Clone(op.children)
	op.lock.Unlock()

	// Sort the list of children
	slices.SortFunc(children, func(a, b *Operation) int {
		return int(a.stage - b.stage)
	})

	// Categorize into batches. There will just be one batch if no stages are set.
	var batches [][]*Operation
	var stage int64
	for i, childOp := range children {
		// On the first iteration we always create the first batch.
		// Subsequently, we only change batch if the stage changes.
		if i == 0 || childOp.stage != stage {
			batches = append(batches, []*Operation{childOp})
			stage = childOp.stage
			continue
		}

		batches[len(batches)-1] = append(batches[len(batches)-1], childOp)
	}

	// Track the first error returned by one of the children
	var firstChildError error

	// Function to run or cancel a child operation.
	runChildOp := func(op *Operation) {
		// Start if there are no previous errors.
		if firstChildError == nil {
			op.start()
			return
		}

		// Start if the operation type must run regardless of previous errors.
		if op.dbOpType.MustRun() {
			op.start()
			return
		}

		// Otherwise cancel.
		op.Cancel()
	}

	// Process each batch.
	for _, batch := range batches {
		for _, childOp := range batch {
			runChildOp(childOp)
		}

		// Wait on any operations that have been started or cancelled in this batch.
		for _, childOp := range batch {
			// Use the parents' finished context so that the parent waits for every child
			// to finish even if the parent's context has been cancelled. The
			// parent must observe all child outcomes before reporting its own result.
			err := childOp.Wait(op.finished)
			if err != nil && firstChildError == nil {
				// Capture the first child error.
				firstChildError = err
			}
		}
	}

	if firstChildError != nil {
		if errors.Is(firstChildError, context.Canceled) {
			return context.Canceled
		}

		return errors.New("One or more child operations failed")
	}

	return nil
}

// IsRunning returns true if the operation run hook is still in progress.
func (op *Operation) IsRunning() bool {
	return op.running.Err() == nil
}

// Cancel cancels an operation.
//   - All operations whose run context has not yet been cancelled have their run context cancelled.
//   - Operations with the [api.Pending] status are set to [api.Cancelled] immediately.
//   - Operations without a run hook or any children (e.g. tokens) are set to [api.Cancelled] immediately.
//   - Operations with a run hook or children have their status set to [api.Cancelling] (including all children).
//     The go routine that is running the run hook will detect a [context.Canceled] error when the run hook exits and set
//     the status to [api.Cancelled].
func (op *Operation) Cancel() {
	op.lock.Lock()
	if op.running.Err() != nil {
		// Already cancelled, nothing to do.
		op.lock.Unlock()
		return
	}

	// Signal the operation to stop.
	op.running.Cancel()

	var awaitRunHook bool
	if (op.onRun != nil || len(op.children) > 0) && op.status != api.Pending {
		awaitRunHook = true
	}

	if awaitRunHook {
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

	// If the operation was immediately cancelled, either its run hook was never executed or it doesn't have a run hook.
	// In this case we need to call op.done to clean it up. Other operations will be cleaned up when their run hook exits.
	if !awaitRunHook {
		op.done()
	}
}

// Connect connects a websocket operation. If the operation is not a websocket
// operation or the operation is not running, it returns an error.
func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	op.lock.Lock()
	if op.class != operationtype.OperationClassWebsocket {
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
		ChildCount:  int64(len(op.children)),
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

	retOp.Children = make([]api.Operation, 0, len(op.children))
	for _, childOp := range op.children {
		_, child := childOp.RenderWithoutProgress()
		retOp.Children = append(retOp.Children, *child)
	}

	// Sort operations by UUID. Since we use UUIDv7, this will also sort operations by creation time.
	slices.SortFunc(retOp.Children, func(a, b api.Operation) int {
		return strings.Compare(a.ID, b.ID)
	})

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

// UpdateMetadata updates the metadata of the operation. It returns an error if the operation has completed.
// The api.MetadataEntityURL field is retained unless the caller sets api.MetadataEntityURL in the input map.
// If a nil map is passed in, metadata is set to an empty map.
func (op *Operation) UpdateMetadata(opMetadata map[string]any) error {
	err := validateMetadata(opMetadata)
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

	if opMetadata == nil {
		opMetadata = make(map[string]any)
	}

	// Retain entity URL unless it is set in the input map.
	// This is to prevent the caller inadvertently overwriting it.
	oldEntityURL, ok := op.metadata[api.MetadataEntityURL]
	if ok {
		_, ok := opMetadata[api.MetadataEntityURL]
		if !ok {
			opMetadata[api.MetadataEntityURL] = oldEntityURL
		}
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

	// Nothing to do.
	if len(metadata) == 0 {
		op.lock.Unlock()
		return nil
	}

	// Get current metadata.
	newMetadata := maps.Clone(op.metadata)

	// Merge with current one.
	if op.metadata == nil {
		newMetadata = metadata
	} else {
		maps.Copy(newMetadata, metadata)
	}

	err := validateMetadata(newMetadata)
	if err != nil {
		op.lock.Unlock()
		return fmt.Errorf("Failed extending operation metadata: %w", err)
	}

	// Update the operation.
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
func (op *Operation) Class() operationtype.Class {
	return op.class
}

// Type returns the db operation type.
func (op *Operation) Type() operationtype.Type {
	return op.dbOpType
}

// Parent returns the parent operation if this operation is a child operation, or nil if this operation is not a child operation.
func (op *Operation) Parent() *Operation {
	return op.parent
}

// Children returns the child operations if this operation is a parent operation, or an empty slice if this operation is not a parent operation.
func (op *Operation) Children() []*Operation {
	return op.children
}

// validateMetadata returns an error if the metadata contains a known key with an invalid value (such as
// [api.MetadataEntityURL] with a non-url value).
func validateMetadata(metadata map[string]any) error {
	if metadata == nil {
		return nil
	}

	// If any url fields are used, they must always be a string and must always be a valid URL.
	urlFields := []string{api.MetadataEntityURL, api.MetadataOriginalEntityURL}
	for _, urlField := range urlFields {
		urlAny, ok := metadata[urlField]
		if ok {
			urlString, ok := urlAny.(string)
			if !ok {
				return fmt.Errorf("Operation metadata field %q must be a string (got %T)", urlField, urlAny)
			}

			err := validate.IsRequestURL(urlString)
			if err != nil {
				return fmt.Errorf("Operation metadata field %q must be a valid request URL: %w", urlField, err)
			}
		}
	}

	return nil
}

// ProgressHandler implements [ioprogress.ProgressReporter]. This is used by instance and storage drivers to
// report I/O progress as they perform different actions (migration, download, image unpack, etc.).
func (op *Operation) ProgressHandler(action string) ioprogress.ProgressHandler {
	return func(data ioprogress.ProgressData) {
		_ = op.updateProgress(action, data)
	}
}

// updateProgress updates the operation metadata with progress information for a specific action.
func (op *Operation) updateProgress(action string, data ioprogress.ProgressData) error {
	// Copy current metadata and ensure it is non-nil.
	metadata := op.Metadata()
	if metadata == nil {
		metadata = make(map[string]any)
	}

	// Delete any keys that end in "_progress", we rely on there only being one.
	for k := range metadata {
		if strings.HasSuffix(k, "_progress") {
			delete(metadata, k)
		}
	}

	progress := make(map[string]string)
	progress["stage"] = action

	if data.TransferredBytes > 0 {
		progress["processed"] = strconv.FormatInt(data.TransferredBytes, 10)
	}

	if data.Percentage > 0 {
		progress["percent"] = strconv.Itoa(data.Percentage)
	}

	if data.BytesPerSecond > 0 {
		progress["speed"] = strconv.FormatInt(data.BytesPerSecond, 10)
	}

	metadata[action+"_progress"] = data.Text
	metadata["progress"] = progress

	// Write the updated metadata.
	return op.UpdateMetadata(metadata)
}

func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		logger.Error("No event server configured for operation", logger.Ctx{"id": op.id, "type": op.dbOpType.Description(), "class": op.class.String()})
		return
	}

	_ = op.events.Send(op.projectName, api.EventTypeOperation, eventMessage)
}
