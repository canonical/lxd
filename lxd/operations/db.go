package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

func registerDBOperation(ctx context.Context, op *Operation) error {
	if op.state == nil {
		return errors.New("Failed registering operation: No state available")
	}

	registerSingleOperation := func(ctx context.Context, tx *db.ClusterTx, op *Operation, parentOpID *int64, projectID *int64) (int64, error) {
		operationsRow := cluster.OperationsRow{
			UUID:              op.id,
			Type:              op.dbOpType,
			NodeID:            tx.GetNodeID(),
			ProjectID:         projectID,
			Class:             int64(op.class),
			CreatedAt:         op.createdAt,
			UpdatedAt:         op.updatedAt,
			StatusCode:        int64(op.Status()),
			Parent:            parentOpID,
			ConflictReference: op.conflictReference,
			Stage:             op.stage,
		}

		if op.requestor != nil {
			// If there is no requestor (eg. for server operations), we leave the requestor_protocol
			// and requestor_identity_id fields `null` in the database.
			// If there's an untrusted requestor with empty protocol and no identity, we set the
			// requestor_protocol to `requestorProtocolNone` and leave the requestor_identity_id `null`.
			// The untrusted requestor is provided eg. in a local image upload operation run as part of an image copy operation.
			value := cluster.RequestorProtocol(op.requestor.Protocol)
			operationsRow.RequestorProtocol = &value
			operationsRow.RequestorIdentityID = op.requestor.IdentityID
		}

		if op.entityURL != nil {
			entityReference, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), op.entityURL)
			if err != nil {
				return 0, fmt.Errorf("Failed fetching entity reference: %w", err)
			}

			if entityReference.EntityType != cluster.EntityType(op.dbOpType.EntityType()) {
				return 0, fmt.Errorf("Entity type %q does not match operation type's entity type %q", entityReference.EntityType, op.dbOpType.EntityType())
			}

			operationsRow.EntityID = int64(entityReference.EntityID)
		}

		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return 0, fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		operationsRow.Metadata = string(metadataJSON)

		dbOpID, err := query.Create(ctx, tx.Tx(), operationsRow)
		if err != nil {
			// The operations table has unique index on uuid, and conditional unique index on conflict_reference.
			// Conflict on generated uuid is highly unlikely, so conflicts will most likely happen due to conflict on conflict_reference.
			// If that is the case, we return a more specific error message.
			if op.conflictReference != "" && api.StatusErrorCheck(err, http.StatusConflict) {
				return 0, api.NewStatusError(http.StatusConflict, "An operation with this conflict reference is already running")
			}

			return 0, err
		}

		err = cluster.CreateOperationResources(ctx, tx.Tx(), dbOpID, op.resources)
		if err != nil {
			return 0, err
		}

		return dbOpID, nil
	}

	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var projectIDPtr *int64
		if op.projectName != "" {
			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
			if err != nil {
				return fmt.Errorf("Failed fetching project ID: %w", err)
			}

			projectIDPtr = &projectID
		}

		// Create parent operation record.
		parentOpID, err := registerSingleOperation(ctx, tx, op, nil, projectIDPtr)
		if err != nil {
			return err
		}

		// Create child operation records, if any.
		for _, childOp := range op.children {
			if childOp.projectName != op.projectName {
				return errors.New("Child operations cannot have a different project to the parent operation")
			}

			_, err := registerSingleOperation(ctx, tx, childOp, &parentOpID, projectIDPtr)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return nil
}

func updateDBOperation(ctx context.Context, op *Operation) error {
	if op.state == nil {
		return errors.New("Failed updating operation: No state available")
	}

	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, tx.GetNodeID(), op.updatedAt, op.status, op.metadata, op.err, op.errCode)
	})
	if err != nil {
		return fmt.Errorf("Failed updating operation %q record: %w", op.id, err)
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state == nil {
		return errors.New("Failed deleting operation: No state available")
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteOperation(ctx, tx.Tx(), op.id)
	})

	return err
}

// ConstructOperationsFromDB is a constructs a list of Operation objects based on their database representation.
func ConstructOperationsFromDB(ctx context.Context, tx *sql.Tx, s *state.State, dbOps []cluster.Operation) ([]*Operation, error) {
	if len(dbOps) == 0 {
		return []*Operation{}, nil
	}

	// Get a list of all entity types and IDs to resolve.
	allEntities := make(map[entity.Type][]int64)

	// Construct a list of parents.
	parents := make([]cluster.Operation, 0, len(dbOps))

	// Construct a map of parent operation IDs to their children.
	children := make(map[int64][]cluster.Operation, len(dbOps))

	// Get a list of all operations to get resources for.
	opIDs := make([]int64, 0, len(dbOps))

	for _, dbOp := range dbOps {
		opIDs = append(opIDs, dbOp.Row.ID)
		opEntityType := dbOp.Row.Type.EntityType()
		allEntities[opEntityType] = append(allEntities[opEntityType], dbOp.Row.EntityID)
		if dbOp.Row.Parent == nil {
			parents = append(parents, dbOp)
			continue
		}

		children[*dbOp.Row.Parent] = append(children[*dbOp.Row.Parent], dbOp)
	}

	// Preload operation resource IDs.
	resources := make(map[int64][]cluster.OperationsResourcesRow)
	err := query.SelectFunc[cluster.OperationsResourcesRow](ctx, tx, "WHERE operations_resources.operation_id IN "+query.IntParams(opIDs...), func(resource cluster.OperationsResourcesRow) error {
		resourceEntityType := entity.Type(resource.EntityType)
		allEntities[resourceEntityType] = append(allEntities[resourceEntityType], resource.EntityID)
		resources[resource.OperationID] = append(resources[resource.OperationID], resource)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Get URLs for all operations and their resources.
	entityURLs, err := cluster.GetEntityURLsByEntityTypeAndID(ctx, tx, allEntities)
	if err != nil {
		return nil, fmt.Errorf("Failed getting operation entity URLs: %w", err)
	}

	ops := make([]*Operation, 0, len(dbOps))
	for _, parent := range parents {
		op, err := constructSingleOperation(s, parent, resources, entityURLs)
		if err != nil {
			return nil, fmt.Errorf("Failed constructing operation: %w", err)
		}

		for _, child := range children[parent.Row.ID] {
			childOp, err := constructSingleOperation(s, child, resources, entityURLs)
			if err != nil {
				return nil, fmt.Errorf("Failed constructing child operation: %w", err)
			}

			op.addChild(childOp)
		}

		ops = append(ops, op)
	}

	return ops, nil
}

func constructSingleOperation(s *state.State, dbOp cluster.Operation, resources map[int64][]cluster.OperationsResourcesRow, entityURLs map[entity.Type]map[int64]*api.URL) (*Operation, error) {
	getURL := func(p entity.Type, id int64) *api.URL {
		urlsOfType, ok := entityURLs[p]
		if !ok {
			return nil
		}

		return urlsOfType[id]
	}

	op := Operation{
		projectName:       dbOp.ProjectName,
		id:                dbOp.Row.UUID,
		class:             operationtype.Class(dbOp.Row.Class),
		createdAt:         dbOp.Row.CreatedAt,
		updatedAt:         dbOp.Row.UpdatedAt,
		status:            api.StatusCode(dbOp.Row.StatusCode),
		url:               api.NewURL().Path(version.APIVersion, "operations", dbOp.Row.UUID).String(),
		description:       dbOp.Row.Type.Description(),
		dbOpType:          dbOp.Row.Type,
		finished:          cancel.New(),
		running:           cancel.New(),
		state:             s,
		location:          dbOp.NodeName,
		err:               dbOp.Row.Error,
		errCode:           dbOp.Row.ErrorCode,
		conflictReference: dbOp.Row.ConflictReference,
		requestor:         dbOp.Requestor(),
		stage:             dbOp.Row.Stage,
	}

	// If server is not clustered, the DB contains 'none' as the node name. In that case we use the server name as the location.
	if !s.ServerClustered {
		op.location = s.ServerName
	} else {
		op.location = dbOp.NodeName
	}

	// If operation is already in final state, cancel both contexts, there's no point in running any hook.
	if op.status.IsFinal() {
		op.running.Cancel()
		op.finished.Cancel()
	}

	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})

	op.events = s.Events

	op.entityURL = getURL(dbOp.Row.Type.EntityType(), dbOp.Row.EntityID)
	if op.entityURL == nil {
		// Various operations (e.g. deletion operations) actually delete the entity within the operation run hook.
		// The entity URL is saved to the operation metadata at create time for this reason.
		// Log a debug message for inspection in case this is not the intended behaviour.
		op.logger.Debug("Failed loading entity URL for operation, leaving it empty on the operation struct", logger.Ctx{"entityType": dbOp.Row.Type.EntityType(), "entityID": dbOp.Row.EntityID})
	}

	// Load operation resources.
	if len(resources[dbOp.Row.ID]) > 0 {
		op.resources = make(map[entity.Type][]api.URL)
		for _, resource := range resources[dbOp.Row.ID] {
			resourceEntityType := entity.Type(resource.EntityType)
			resourceURL := getURL(resourceEntityType, resource.EntityID)
			if resourceURL == nil {
				op.logger.Debug("Failed loading resource URL for operation", logger.Ctx{"entityType": resourceEntityType, "entityID": resource.EntityID})
				continue
			}

			op.resources[resourceEntityType] = append(op.resources[resourceEntityType], *resourceURL)
		}
	}

	// Load operation metadata.
	var metadata map[string]any
	err := json.Unmarshal([]byte(dbOp.Row.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation metadata for operation %d: %w", dbOp.Row.ID, err)
	}

	op.metadata = metadata

	// If the operation is durable, load the run hook.
	if op.class == operationtype.OperationClassDurable {
		runHook, ok := getDurableOperationRunHook(op.dbOpType)
		if !ok {
			return nil, fmt.Errorf("No run hook is defined for durable operation %q", op.dbOpType.Description())
		}

		op.onRun = runHook
	}

	return &op, nil
}

// PruneExpiredOperations deletes database entries of all operations which finished more than 24 hours ago.
// Normally, operations are cleared 5 seconds after they finish. However, more complex operations, such as bulk operations,
// are only cleared by this task, to allow more time to inspect their results.
func PruneExpiredOperations(ctx context.Context, s *state.State) error {
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbOps, err := query.Select[cluster.OperationsRow](ctx, tx.Tx(), "")
		if err != nil {
			return fmt.Errorf("Failed loading operations: %w", err)
		}

		for _, dbOp := range dbOps {
			// Don't prune operations which are still running.
			if !api.StatusCode(dbOp.StatusCode).IsFinal() {
				continue
			}

			// Don't delete child operations. These will be deleted by the foreign key constraint when the parent operation is deleted.
			if dbOp.Parent != nil {
				continue
			}

			// Prune operations which were last updated more than 24 hours ago.
			if dbOp.UpdatedAt.Add(24 * time.Hour).After(time.Now()) {
				continue
			}

			err = cluster.DeleteOperation(ctx, tx.Tx(), dbOp.UUID)
			if err != nil {
				return fmt.Errorf("Failed deleting expired operation: %w", err)
			}

			logger.Info("Pruned expired operation", logger.Ctx{"operation": dbOp.UUID})
		}

		return nil
	})
	if err != nil {
		return err
	}

	logger.Debug("Done pruning expired operations")

	return nil
}

// loadAndConstructOperationFromDB queries for an operation and all of its children, then reconstructs the result into an [Operation].
func loadAndConstructOperationFromDB(ctx context.Context, s *state.State, opID string) (*Operation, error) {
	var ops []*Operation
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbOps, err := cluster.GetOperationWithChildren(ctx, tx.Tx(), opID)
		if err != nil {
			return fmt.Errorf("Failed getting operation records: %w", err)
		}

		ops, err = ConstructOperationsFromDB(ctx, tx.Tx(), s, dbOps)
		if err != nil {
			return fmt.Errorf("Failed constructing operation: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(ops) != 1 {
		return nil, fmt.Errorf(`Expected to construct 1 operation but constructed "%d"`, len(ops))
	}

	return ops[0], nil
}
