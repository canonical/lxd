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
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
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
	dbOpType    operationtype.Type
	requestor   *request.Requestor
	logger      logger.Logger

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(context.Context, *Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error
	onDone    func(*Operation)

	// Inputs for the operation, which are stored in the database.
	inputs map[string]any

	// If this operation is part of a bulk operation, parent will point to the parent operation.
	parent   *Operation
	children []*Operation

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

// RunHook represents the function signature for an operation run hook.
type RunHook func(ctx context.Context, op *Operation) error

// OperationArgs contains all the arguments for operation creation.
type OperationArgs struct {
	ProjectName string
	Type        operationtype.Type
	Class       OperationClass
	Resources   map[string][]api.URL
	Metadata    map[string]any
	RunHook     RunHook
	ConnectHook func(op *Operation, r *http.Request, w http.ResponseWriter) error
	// ConflictReference allows to create the operation only if no other operation with the same conflict reference is running.
	// Empty ConflictReference means the operation can be started anytime.
	ConflictReference string
	Inputs            map[string]any
	Children          []*OperationArgs
}

// CreateUserOperation creates a new [Operation]. The [request.Requestor] argument must be non-nil, as this is required for auditing.
func CreateUserOperation(s *state.State, requestor *request.Requestor, args OperationArgs) (*Operation, error) {
	if requestor == nil {
		return nil, errors.New("Cannot create user operation, the requestor must be set")
	}

	return operationCreate(s, requestor, args)
}

// CreateServerOperation creates a new [Operation] that runs as a server background task.
func CreateServerOperation(s *state.State, args OperationArgs) (*Operation, error) {
	return operationCreate(s, nil, args)
}

// DurableOperationTable represents the table of durable operation hooks.
type DurableOperationTable map[operationtype.Type]RunHook

var (
	durableOperations     DurableOperationTable
	requestorHook         request.RequestorHook
	durableOperationsOnce sync.Once
)

// InitDurableOperations initializes the durable operations table.
// As durable operations can be restarted on other nodes, the durable operation handlers cannot be defined only in the memory of the node.
// Therefore we maintain a static map of durable operation handlers based on operation type.
// As this map contains handlers from across many packages, the table itself is defined in the main package.
// Because this table needs to be accessible from the operations package, we provide this Init function to set it.
func InitDurableOperations(opTable DurableOperationTable, hook request.RequestorHook) {
	durableOperationsOnce.Do(func() {
		durableOperations = opTable
		requestorHook = hook
	})
}

// initOperation initializes a new operation structure. It does not register it in the database.
func initOperation(s *state.State, requestor *request.Requestor, args OperationArgs) (*Operation, error) {
	// Don't allow new operations when LXD is shutting down.
	if s != nil && s.ShutdownCtx.Err() == context.Canceled {
		return nil, errors.New("LXD is shutting down")
	}

	// Quick check.
	if args.Class == OperationClassDurable {
		if args.RunHook != nil || args.ConnectHook != nil {
			return nil, errors.New("Durable operations cannot have Run or Connect hooks provided directly")
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

	// Use a v7 UUID for the operation ID.
	uuid, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Failed to generate operation UUID: %w", err)
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
	op.status = api.OperationCreated
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

	op.metadata, err = validateMetadata(args.Metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed to validate operation metadata: %w", err)
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

// operationCreate creates a new operation and returns it. If it cannot be created, it returns an error.
func operationCreate(s *state.State, requestor *request.Requestor, args OperationArgs) (*Operation, error) {
	// If this is a bulk operation, don't allow more than one level of nesting.
	if args.Children != nil {
		for _, child := range args.Children {
			if child.Children != nil {
				return nil, errors.New("Bulk operations cannot have nested bulk operations")
			}
		}
	}

	// Create the parent operation
	op, err := initOperation(s, requestor, args)
	if err != nil {
		return nil, err
	}

	// Create the child operations, if any.
	op.children = make([]*Operation, 0, len(args.Children))
	for _, childArgs := range args.Children {
		childOp, err := initOperation(s, requestor, *childArgs)
		if err != nil {
			return nil, fmt.Errorf("Failed to create child operation: %w", err)
		}

		childOp.parent = op
		op.children = append(op.children, childOp)
	}

	err = registerDBOperation(op, args.ConflictReference)
	if err != nil {
		return nil, err
	}

	// Durable operations need to be able to be reloaded from the database.
	// To ease debugging in case of issues, we want to ensure the reloaded operation will be identical to the one originally created.
	// Therefore, reload the operation from the database here to ensure everything is properly persisted and can be reloaded correctly.
	// Notably, when unix socket is used for auth, the op.requestor.OriginAddress is set to '@'. This is not persisted in the database,
	// so reloading the operation ensures we work with empty ("") OriginAddress instead of "@".
	if op.class == OperationClassDurable {
		reconstructedChildOps := make([]*Operation, 0, len(op.children))
		for _, childOp := range op.children {
			reconstructedChildOp, err := loadDurableOperationFromDB(childOp)
			if err != nil {
				return nil, fmt.Errorf("Failed reloading durable child operation from database: %w", err)
			}

			reconstructedChildOps = append(reconstructedChildOps, reconstructedChildOp)
		}

		op, err = loadDurableOperationFromDB(op)
		if err != nil {
			return nil, fmt.Errorf("Failed reloading durable operation from database: %w", err)
		}

		op.children = reconstructedChildOps
	}

	op.logger.Debug("New operation")
	_, md, _ := op.Render()

	operationsLock.Lock()
	operations[op.id] = op
	for _, childOp := range op.children {
		operations[childOp.id] = childOp
	}

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

	// If we are a child operation, we're done. The parent operation will clean all the child entries when it finishes.
	if op.parent != nil {
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

		// Clear the child operations
		for _, childOp := range op.children {
			_, ok := operations[childOp.id]
			if ok {
				delete(operations, childOp.id)
			}
		}

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

// Start a pending operation. It returns an error if the operation cannot be started.
func (op *Operation) Start() error {
	op.lock.Lock()
	if op.status.IsFinal() {
		op.lock.Unlock()
		return errors.New("Only operations in running states can be started")
	}

	runCtx := context.Context(op.running)
	err := op.updateStatus(runCtx, api.Running)
	if err != nil {
		op.lock.Unlock()
		return fmt.Errorf("Failed updating Operation %q (%q) status: %w", op.id, op.description, err)
	}

	// Start child operations
	for _, childOp := range op.children {
		err := childOp.Start()
		if err != nil {
			op.lock.Unlock()
			return fmt.Errorf("Failed to start child operation: %w", err)
		}
	}

	if op.onRun != nil {
		// The operation context is the "running" context plus the requestor.
		// The requestor is available directly on the operation, but we should still put it in the context.
		// This is so that, if an operation queries another cluster member, the requestor information will be set
		// in the request headers.
		if op.requestor != nil {
			runCtx = request.WithRequestor(runCtx, op.requestor)
		}

		go func(ctx context.Context, op *Operation) {
			err := op.onRun(ctx, op)

			// If we're a parent operation with children, wait until all of our child operations are done.
			// This is to ensure that the parent operation remains visible in the API until all child operations have completed,
			// which is important for operations that spawn multiple child operations (e.g. bulk operations),
			// so that the user can see the overall progress of the operation until everything is done.
			if op.parent == nil && len(op.children) > 0 {
				for _, childOp := range op.children {
					// Ignore the child error here, the overal result of the bulk operation should be determined by the parent operation, not the child operations.
					_ = childOp.Wait(context.Background())
				}
			}

			if err != nil {
				op.lock.Lock()

				op.err = err

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
				_, md, _ := op.Render()

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

	// Signal the child operations to stop as well.
	for _, childOp := range op.children {
		childOp.Cancel()
	}

	// If the operation has a run hook, then set the status to cancelling.
	// When the hook returns, the status will be set to cancelled because the run context is cancelled.
	// The allows an operation to emit a cancelling status if it is in the middle of something that could take a while to clean up.
	//
	// If the operation does not have a run hook, immediately set the status to cancelled because there is nothing to clean up.
	// We cannot use the operation context here because it has already been cancelled above.
	if op.onRun != nil {
		updateStatus(op, api.Cancelling)
	} else {
		op.lock.Unlock()

		// If we're a parent operation with children, wait until all of our child operations are done.
		// This is to ensure that the parent operation remains visible in the API until all child operations have completed,
		// which is important for operations that spawn multiple child operations (e.g. bulk operations),
		// so that the user can see the overall progress of the operation until everything is done.
		if op.parent == nil && len(op.children) > 0 {
			for _, childOp := range op.children {
				// Ignore the child error here, the overal result of the bulk operation should be determined by the parent operation, not the child operations.
				_ = childOp.Wait(context.Background())
			}
		}

		op.lock.Lock()

		updateStatus(op, api.Cancelled)
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
		return fmt.Errorf("Failed to update operation metadata: %w", err)
	}

	op.lock.Lock()
	if op.finished.Err() != nil {
		op.lock.Unlock()
		return api.NewStatusError(http.StatusBadRequest, "Operations cannot be updated after they have completed")
	}

	if op.readonly {
		op.lock.Unlock()
		return errors.New("Read-only operations can't be updated")
	}

	op.updatedAt = time.Now()
	op.metadata = opMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
	_, md, _ := op.Render()

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
		return errors.New("Read-only operations can't be updated")
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
		return fmt.Errorf("Failed to extend operation metadata: %w", err)
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

// validateMetadata is used to enforce some consistency in operation metadata.
func validateMetadata(metadata map[string]any) (map[string]any, error) {
	// Ensure metadata is never nil.
	if metadata == nil {
		metadata = make(map[string]any)
	}

	// If the entity_url field is used, it must always be a string and must always be a valid URL.
	entityURLAny, ok := metadata["entity_url"]
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
