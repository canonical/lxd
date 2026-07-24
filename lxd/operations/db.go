package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
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

		// Save the time at which the operation was persisted
		op.persistedAt = op.updatedAt

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

		op.dbID = parentOpID

		// Create child operation records, if any.
		for _, childOp := range op.children {
			if childOp.projectName != op.projectName {
				return errors.New("Child operations cannot have a different project to the parent operation")
			}

			childOpID, err := registerSingleOperation(ctx, tx, childOp, &parentOpID, projectIDPtr)
			if err != nil {
				return err
			}

			childOp.dbID = childOpID
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return nil
}

// persistOperation saves the operation to the database in its current state. The [cluster.OperationsRow.UpdatedAt]
// column is only set to the value of Operation.updatedAt, not necessarily the current time.
func persistOperation(ctx context.Context, op *Operation) error {
	if op.state == nil {
		return errors.New("Failed updating operation: No state available")
	}

	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, tx.GetNodeID(), op.updatedAt, op.status, op.metadata, op.err, op.errCode)
	})
	if err != nil {
		return fmt.Errorf("Failed updating operation %q record: %w", op.id, err)
	}

	// Save the time at which the last update of the operation was persisted.
	op.persistedAt = op.updatedAt
	return nil
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
		dbID:              dbOp.Row.ID,
		projectName:       dbOp.ProjectName,
		id:                dbOp.Row.UUID,
		class:             operationtype.Class(dbOp.Row.Class),
		createdAt:         dbOp.Row.CreatedAt,
		updatedAt:         dbOp.Row.UpdatedAt,
		persistedAt:       dbOp.Row.UpdatedAt,
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

		// Reconstructed operations are readonly.
		readonly: true,
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

	return &op, nil
}

// Synchronize is run on database start up and shutdown, and as a recurring task every minute.
// It first checks the local map for any operations that need to be retained. These are:
// - Bulk operations that less than 24 hours ago.
// - Other operations that completed less than 5 seconds ago.
// It then checks all operations registered to this cluster member in the database:
// - Any bulk operations that completed less than 24 hours ago are retained.
// - Any other operations that completed less than 5 seconds ago are retained.
// Retained operations that are present in the database and not present in the local map are reconstructed and added to
// the local map. All other operations in the local map and registered to this member in the database are deleted.
// Finally, all retained operations are checked for consistency between the local map and the database:
//   - If an operation was reconstructed but its status is not final, then it was not properly cleaned up. The status is
//     set to an internal error.
//   - If an operations "updated_at" timestamp in the database does not match the local operation. This indicates that the
//     operation failed to store its state. It is updated to match what is present in the local map.
func Synchronize(ctx context.Context, s *state.State) error {
	now := time.Now()
	ops := Clone()

	opsToRetain := make(map[int64]struct{}, len(ops))
	opsToDeleteInternal := make([]string, 0, len(ops))
	for _, op := range ops {
		// Retain all children, because we will delete them by foreign key in the database, and they will be equivalently
		// deleted from the internal map via deleteInternal.
		if op.parent != nil {
			opsToRetain[op.dbID] = struct{}{}
			continue
		}

		// Retain operations that have not completed.
		if op.finished.Err() == nil {
			opsToRetain[op.dbID] = struct{}{}
			continue
		}

		// Retain any operations that completed less than 5 seconds ago.
		if now.Before(op.updatedAt.Add(5 * time.Second)) {
			opsToRetain[op.dbID] = struct{}{}
			continue
		}

		// Retain bulk operations that completed less than 24 hours ago.
		if len(op.children) > 0 && now.Before(op.updatedAt.Add(24*time.Hour)) {
			opsToRetain[op.dbID] = struct{}{}
			continue
		}

		// Delete all other operations from the local map.
		opsToDeleteInternal = append(opsToDeleteInternal, op.id)
	}

	// Delete from the internal map. These operations should no longer be present. If the upcoming transaction fails,
	// all operations that are not present in the map will still be deleted on the next task run.
	deleteInternal(opsToDeleteInternal...)

	var reconstructedOps []*Operation
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Collect:
		// - A list of all operations registered to this member (for later synchronization).
		// - A map of parent operations, this is interim for calculating operations to reconstruct.
		// - A list of operations to reconstruct.
		var allMemberOperations []cluster.Operation
		parents := make(map[int64]cluster.Operation)
		var operationsToReconstruct []cluster.Operation

		// Anonymous function to append to our list of operations to reconstruct.
		// We don't want to reconstruct operations that are already present (the local map is the source of truth, so
		// overwriting it with an operation reconstructed from the database is incorrect).
		addReconstructCandidate := func(dbOp cluster.Operation) {
			_, ok := ops[dbOp.Row.UUID]
			if ok {
				return
			}

			operationsToReconstruct = append(operationsToReconstruct, dbOp)
		}

		// Order by parent in ascending order with nulls first so that parent operations are scanned first.
		selectClause := "WHERE operations.node_id = ? ORDER BY operations.parent ASC NULLS FIRST"
		err := query.SelectFunc[cluster.Operation](ctx, tx.Tx(), selectClause, func(operation cluster.Operation) error {
			allMemberOperations = append(allMemberOperations, operation)

			// Process parent operations.
			if operation.Row.Parent == nil {
				// Retain and reconstruct parent operations that completed less than 5 seconds ago.
				if now.Before(operation.Row.UpdatedAt.Add(5 * time.Second)) {
					opsToRetain[operation.Row.ID] = struct{}{}
					addReconstructCandidate(operation)
				}

				parents[operation.Row.ID] = operation
				return nil
			}

			// Retain all children as they will be deleted by foreign key.
			opsToRetain[operation.Row.ID] = struct{}{}

			// Check if the parent of this child completed more than 24 hours ago.
			if now.After(parents[*operation.Row.Parent].Row.UpdatedAt.Add(24 * time.Hour)) {
				// If so, there is nothing to retain or reconstruct, so return.
				return nil
			}

			// If the parent is already retained, a previous child has already added it.
			// Just add the child to the list of operations to reconstruct.
			_, ok := opsToRetain[*operation.Row.Parent]
			if ok {
				addReconstructCandidate(operation)
				return nil
			}

			// Retain the parent and add both the parent and child to the list of operations to reconstruct.
			opsToRetain[*operation.Row.Parent] = struct{}{}
			addReconstructCandidate(parents[*operation.Row.Parent])
			addReconstructCandidate(operation)

			return nil
		}, tx.GetNodeID())
		if err != nil {
			return fmt.Errorf("Failed getting operations to synchronize: %w", err)
		}

		// We now have a complete list of operations to retain. Delete any operations that are registered to this cluster member
		// and are not in this set.
		deleteClause := "WHERE node_id = ? AND id NOT IN " + query.IntParams(slices.Collect(maps.Keys(opsToRetain))...)
		nDeleted, err := query.DeleteMany[cluster.OperationsRow](ctx, tx.Tx(), deleteClause, tx.GetNodeID())
		if err != nil {
			return fmt.Errorf("Failed pruning expired operations: %w", err)
		}

		logger.Info("Pruned expired operations", logger.Ctx{"count": nDeleted})

		// Inspect operations that are to be reconstructed.
		// If their status is not final, something went wrong. Their status needs to be changed now as their run hook will
		// not be re-run.
		unfinishedReconstructedOperationIDs := make([]int64, 0, len(operationsToReconstruct))
		for _, operation := range operationsToReconstruct {
			if api.StatusCode(operation.Row.StatusCode).IsFinal() {
				continue
			}

			// Update the row so that it is reconstructed correctly.
			operation.Row.StatusCode = int64(api.Failure)
			operation.Row.Error = "Operation exited uncleanly or was unable to report its status"
			operation.Row.ErrorCode = int64(http.StatusInternalServerError)
			operation.Row.UpdatedAt = now

			// Append the operation ID so that we can update all rows in one query.
			unfinishedReconstructedOperationIDs = append(unfinishedReconstructedOperationIDs, operation.Row.ID)
		}

		if len(unfinishedReconstructedOperationIDs) > 0 {
			stmt := "UPDATE operations SET status_code = ?, error = ?, error_code = ?, updated_at = ? WHERE id IN " + query.IntParams(unfinishedReconstructedOperationIDs...)
			res, err := tx.Tx().ExecContext(ctx, stmt, api.Failure, "Operation exited uncleanly or was unable to report its status", http.StatusInternalServerError, now)
			if err != nil {
				return fmt.Errorf("Failed writing error status for unfinished orphaned operations: %w", err)
			}

			nAffected, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("Failed validating write for unfinished orphaned operations: %w", err)
			}

			if int(nAffected) != len(unfinishedReconstructedOperationIDs) {
				return fmt.Errorf("Failed validating write for unfinished orphaned operations: Expected %d writes, got %d", len(unfinishedReconstructedOperationIDs), nAffected)
			}
		}

		// Reconstruct the operations.
		reconstructedOps, err = ConstructOperationsFromDB(ctx, tx.Tx(), s, operationsToReconstruct)
		if err != nil {
			return fmt.Errorf("Failed reconstructing operations on synchronization: %w", err)
		}

		// At this point, all operations in the retained operations map are either present locally, or are bulk operations that are not present locally
		// but are in the database and finished less than 24 hours ago. Range over all database operations and check they correspond.
		for _, dbOp := range allMemberOperations {
			// Skip the ones we've just deleted.
			_, ok := opsToRetain[dbOp.Row.ID]
			if !ok {
				continue
			}

			op, ok := ops[dbOp.Row.UUID]
			if !ok {
				// If the row is retained but is not present in the local map, it is one of the reconstructed operations.
				continue
			}

			// If the updated_at timestamp is identical, consider the operation synchronized.
			if op.persistedAt.UnixMilli() == dbOp.Row.UpdatedAt.UnixMilli() {
				continue
			}

			// Otherwise, synchronize the operation.
			op.logger.Info("Resynchronizing operation")
			err = cluster.UpdateOperation(ctx, tx.Tx(), op.id, tx.GetNodeID(), op.updatedAt, op.status, op.metadata, op.err, op.errCode)
			if err != nil {
				op.logger.Warn("Failed synchronizing operation with database", logger.Ctx{"err": err})
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed synchonizing operations: %w", err)
	}

	// Add the reconstructed operations back to the local map.
	// Only lock if necessary.
	if len(reconstructedOps) > 0 {
		operationsLock.Lock()
		for _, reconstructedOp := range reconstructedOps {
			operations[reconstructedOp.id] = reconstructedOp
			for _, child := range reconstructedOp.children {
				operations[child.id] = child
			}
		}

		operationsLock.Unlock()
	}

	return nil
}
