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
	"strings"
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

const (
	// OperationRetentionDuration is the duration after an operation is finalized for which it cannot be deleted (for non-bulk operations).
	OperationRetentionDuration = 5 * time.Second

	// OperationRetentionDurationBulk is the duration after a bulk operation is finalized for which it cannot be deleted.
	OperationRetentionDurationBulk = 24 * time.Hour
)

const (
	danglingOperationFinalizationErrorText  = "Operation exited uncleanly or was unable to report its status"
	danglingOperationFinalizationErrorCode  = int64(http.StatusInternalServerError)
	danglingOperationFinalizationStatusCode = int64(api.Failure)
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
			Stage:             int64(op.stage),
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
		op.lastPersistenceAttempt = op.updatedAt

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

	// Save the lastPersistenceAttempt field as the current updatedAt value.
	// If persistence fails, the updated_at row in the database will be behind this value.
	op.lastPersistenceAttempt = op.updatedAt

	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, tx.GetNodeID(), op.updatedAt, op.status, op.metadata, op.err, op.errCode)
	})
	if err != nil {
		// cluster.UpdateOperation returns a conflict error if the operation exists but is not present on the given node ID (this node).
		// This means the operation has been relocated. Cancel the operation if it is durable so that it is not running in two places at once.
		if api.StatusErrorCheck(err, http.StatusConflict) && op.Class() == operationtype.OperationClassDurable {
			cancelInternal(op)
		}

		return fmt.Errorf("Failed updating operation %q record: %w", op.id, err)
	}

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
		dbID:                   dbOp.Row.ID,
		projectName:            dbOp.ProjectName,
		id:                     dbOp.Row.UUID,
		class:                  operationtype.Class(dbOp.Row.Class),
		createdAt:              dbOp.Row.CreatedAt,
		updatedAt:              dbOp.Row.UpdatedAt,
		lastPersistenceAttempt: dbOp.Row.UpdatedAt,
		status:                 api.StatusCode(dbOp.Row.StatusCode),
		url:                    api.NewURL().Path(version.APIVersion, "operations", dbOp.Row.UUID).String(),
		description:            dbOp.Row.Type.Description(),
		dbOpType:               dbOp.Row.Type,
		finished:               cancel.New(),
		running:                cancel.New(),
		state:                  s,
		location:               dbOp.NodeName,
		err:                    dbOp.Row.Error,
		errCode:                dbOp.Row.ErrorCode,
		conflictReference:      dbOp.Row.ConflictReference,
		requestor:              dbOp.Requestor(),
		stage:                  uint16(dbOp.Row.Stage),
	}

	// If server is not clustered, the DB contains 'none' as the node name. In that case we use the server name as the location.
	if !s.ServerClustered {
		op.location = s.ServerName
	} else {
		op.location = dbOp.NodeName
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

	// Set the finalization function.
	setDoneFunc(&op)

	// We have just reconstructed the operation from the database, generally this is for inspection only, so call done and cancel running context.
	// However, durable operations may be reloaded and re-run, only finalize these operations if they have a final or cancelling status.
	if op.class != operationtype.OperationClassDurable || op.status.IsFinal() || op.status == api.Cancelling {
		op.running.Cancel()
		op.done()
	}

	return &op, nil
}

// Synchronize is run on database start up and shutdown, and as a recurring task every minute.
// It first checks the local map for any operations that need to be retained. These are:
// - Bulk operations that completed less than 24 hours ago.
// - Other operations that completed less than 5 seconds ago.
// It then checks all operations registered to this cluster member in the database:
// - Any bulk operations that completed less than 24 hours ago are retained.
// - Any other operations that completed less than 5 seconds ago are retained.
// Retained operations that are present in the database and not present in the local map are reconstructed and added to
// the local map. All other operations in the local map and registered to this member in the database are deleted.
// All retained operations are checked for consistency between the local map and the database:
//   - If an operation was reconstructed but its status is not final, then it was not properly cleaned up. The status is
//     set to an internal error.
//   - If an operations "updated_at" timestamp in the database does not match the local operation. This indicates that the
//     operation failed to store its state. It is updated to match what is present in the local map.
//
// Finally, any durable operations that running on this member but are not registered to this member in the database are
// internally cancelled, and any durable operations that are registered to this member but are not present in the local
// map are reconstructed and restarted.
func Synchronize(ctx context.Context, s *state.State) error {
	// Get a set of operation database IDs to retain and a list of operation UUIDs to delete.
	syncStartedAt, maxID, localOps, operationIDRetentionSet, operationUUIDsToInternallyDelete := sortLocalOperationsForSynchronization()

	// Delete from the internal map. These operations should no longer be present. If the upcoming transaction fails,
	// all operations that are not present in the map will still be deleted on the next task run.
	deleteInternal(operationUUIDsToInternallyDelete...)

	var reconstructedOps []*Operation
	var durableOperationUUIDsToRestart, durableOperationUUIDsToInternallyCancel map[string]struct{}
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		var allLocallyRegisteredDBOps []cluster.Operation
		var operationsToReconstruct map[string]cluster.Operation

		// Query for all operations on this member and find operations that should be in the local map but are not
		// (e.g. bulk operations that are less than 24 hours old that need to be persisted on daemon restart).
		allLocallyRegisteredDBOps, operationsToReconstruct, durableOperationUUIDsToRestart, durableOperationUUIDsToInternallyCancel, err = loadOperationsToSynchronize(ctx, tx, maxID, syncStartedAt, localOps, operationIDRetentionSet)
		if err != nil {
			return fmt.Errorf("Failed querying for locally registered operations: %w", err)
		}

		// Delete any operations registered to this member that are not in the retention set.
		nDeleted, err := deleteLocallyRegisteredOperationsNotInSet(ctx, tx, operationIDRetentionSet, maxID)
		if err != nil {
			return fmt.Errorf("Failed pruning expired operations: %w", err)
		}

		logger.Debug("Pruned expired operations", logger.Ctx{"count": nDeleted})

		// If the daemon is killed while an operation is running, it should not be reconstructed in a running state.
		// Ensure that any such operations are updated with an appropriate error status.
		err = finalizeUnfinishedDBOperations(ctx, tx, syncStartedAt, operationsToReconstruct, operationIDRetentionSet, durableOperationUUIDsToRestart)
		if err != nil {
			return fmt.Errorf("Failed finalizing dangling operations: %w", err)
		}

		// Reconstruct the operations.
		reconstructedOps, err = ConstructOperationsFromDB(ctx, tx.Tx(), s, slices.Collect(maps.Values(operationsToReconstruct)))
		if err != nil {
			return fmt.Errorf("Failed reconstructing operations during synchronization: %w", err)
		}

		// Check operation and database update timestamps to verify that all local operations are accurately represented.
		err = ensureLocalOperationsAreSynchronized(ctx, tx, allLocallyRegisteredDBOps, operationIDRetentionSet, localOps)
		if err != nil {
			return fmt.Errorf("Failed synchronizing local operations: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed synchronizing operations: %w", err)
	}

	// Add the reconstructed operations back to the local map. Restart any durable operations that need to be restarted,
	// and internally cancel any durable operations that need to be cancelled.
	// Only lock if necessary.
	if len(reconstructedOps) > 0 || len(durableOperationUUIDsToRestart) > 0 || len(durableOperationUUIDsToInternallyCancel) > 0 {
		operationsLock.Lock()
		for _, reconstructedOp := range reconstructedOps {
			operations[reconstructedOp.id] = reconstructedOp
			for _, child := range reconstructedOp.children {
				operations[child.id] = child
			}
		}

		for opUUIDToCancelInternal := range durableOperationUUIDsToInternallyCancel {
			op, ok := operations[opUUIDToCancelInternal]
			if !ok {
				// Should never get here. Operations are only ever deleted from the internal map within this function
				// and the operation UUID was present when the map was cloned. Log a warning in case a regression causes
				// this logic to break.
				logger.Warn("Found a durable operation to cancel but it is no longer present in the internal map", logger.Ctx{"uuid": opUUIDToCancelInternal})
				continue
			}

			cancelInternal(op)
		}

		for opUUIDToRestart := range durableOperationUUIDsToRestart {
			op, ok := operations[opUUIDToRestart]
			if !ok {
				// Should never get here. The durable operation should have been reconstructed because it is registered
				// to this member and is not finished. We've just added any reconstructed operations to the local map, so
				// if it is not present there is a logic mismatch when calculating the restartable operations list.
				logger.Warn("Found a durable operation to restart but it was not reconstructed", logger.Ctx{"uuid": opUUIDToRestart})
				continue
			}

			restartOperation(op)
		}

		operationsLock.Unlock()
	}

	return nil
}

// ensureLocalOperationsAreSynchronized looks at the local operations map and compares the Operation.lastPersistenceAttempt timestamp against the
// updated_at column of its database counterpart. If there was an attempt to persist the operation that did not succeed, these values will be mismatched.
// The operation is then persisted in its current state.
func ensureLocalOperationsAreSynchronized(ctx context.Context, tx *db.ClusterTx, locallyRegisteredDBOps []cluster.Operation, operationIDRetentionSet map[int64]struct{}, localOps map[string]*Operation) error {
	errs := make([]error, 0, len(locallyRegisteredDBOps))

	// At this point, all operations in the retained operations map are either present locally, or are bulk operations that are not present locally
	// but are in the database and finished less than 24 hours ago. We now range over all database operations and check that they are in sync.
	for _, dbOp := range locallyRegisteredDBOps {
		// Skip the ones we've just deleted.
		_, ok := operationIDRetentionSet[dbOp.Row.ID]
		if !ok {
			continue
		}

		op, ok := localOps[dbOp.Row.UUID]
		if !ok {
			// If the row is retained but is not present in the local map, it is one of the reconstructed operations.
			continue
		}

		// If Operation.lastPersistenceAttempt is equal to operations.updated_at, then the operation is in sync with the database.
		lastPersistenceAttemptUnixMillis := op.lastPersistenceAttempt.UnixMilli()
		dbUpdatedAtUnixMillis := dbOp.Row.UpdatedAt.UnixMilli()
		if lastPersistenceAttemptUnixMillis <= dbUpdatedAtUnixMillis {
			// The lastPersistenceAttempt should never be behind operations.updated_at.
			// If it is, update it to the current database value and log a warning.
			if lastPersistenceAttemptUnixMillis < dbUpdatedAtUnixMillis {
				op.lastPersistenceAttempt = dbOp.Row.UpdatedAt
				op.updatedAt = dbOp.Row.UpdatedAt
				logger.Warn("Operation persistence tracking mismatch", logger.Ctx{"uuid": op.id})
			}

			continue
		}

		// If the operation is not in sync, it could be due to a misbehaving database when the operation attempted to persist its data.
		// Synchronize the operation now.
		op.logger.Info("Resynchronizing operation")
		op.lastPersistenceAttempt = op.updatedAt
		err := cluster.UpdateOperation(ctx, tx.Tx(), op.id, tx.GetNodeID(), op.updatedAt, op.status, op.metadata, op.err, op.errCode)
		if err != nil {
			errs = append(errs, err)
			op.logger.Warn("Failed synchronizing operation with database", logger.Ctx{"err": err})
		}
	}

	return errors.Join(errs...)
}

// finalizeUnfinishedDBOperations accepts a list of operations that are not in the local map but should be.
// If any operation statuses are not final, something went wrong before the operations were able to persist their final status.
// This function writes an error status for those operations. Note that durable operations that are to be restarted are skipped,
// because they will be finalized when the restarted operation completes.
func finalizeUnfinishedDBOperations(ctx context.Context, tx *db.ClusterTx, syncStartedAt time.Time, operationsToReconstruct map[string]cluster.Operation, operationIDRetentionSet map[int64]struct{}, durableOperationUUIDsToRestart map[string]struct{}) error {
	unfinishedReconstructedOperationIDs := filterReconstructCandidatesForFinalization(syncStartedAt, operationsToReconstruct, operationIDRetentionSet, durableOperationUUIDsToRestart)
	if len(unfinishedReconstructedOperationIDs) == 0 {
		return nil
	}

	stmt := "UPDATE operations SET status_code = ?, error = ?, error_code = ?, updated_at = ? WHERE id IN " + query.IntParams(unfinishedReconstructedOperationIDs...)
	res, err := tx.Tx().ExecContext(ctx, stmt, danglingOperationFinalizationStatusCode, danglingOperationFinalizationErrorText, danglingOperationFinalizationErrorCode, syncStartedAt)
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

	return nil
}

// filterReconstructCandidatesForFinalization inspects the operations that are to be reconstructed.
// If their status is not final, something went wrong. Their status needs to be changed now as their run hook will not be re-run.
// The input map is modified to reflect this (so that any operations that are reconstructed are up-to-date) and IDs the operations to finalize are returned for update in a single query.
func filterReconstructCandidatesForFinalization(syncStartedAt time.Time, operationsToReconstruct map[string]cluster.Operation, operationIDRetentionSet map[int64]struct{}, durableOperationUUIDsToRestart map[string]struct{}) []int64 {
	unfinishedReconstructedOperationIDs := make([]int64, 0, len(operationsToReconstruct))
	for opUUID, operation := range operationsToReconstruct {
		if api.StatusCode(operation.Row.StatusCode).IsFinal() {
			continue
		}

		// If we are a child operation that is being reconstructed, then we are not in the local map and we are present
		// in the database, and our status is not final. If the parent of this operation is not retained, something went
		// wrong, but this child has already been deleted from the database, so there is no update to do (so skip it).
		if operation.Row.Parent != nil {
			_, ok := operationIDRetentionSet[*operation.Row.Parent]
			if !ok {
				continue
			}

			// Also we don't want to reconstruct it because the parent has gone, so we need to delete it from the reconstruction set.
			delete(operationsToReconstruct, operation.Row.UUID)
		}

		// Don't set status on durable operations that are to be restarted.
		_, ok := durableOperationUUIDsToRestart[operation.Row.UUID]
		if ok {
			continue
		}

		// Overwrite the value in the reconstructed operations map so that the operation is reconstructed with a finalized status.
		operation.Row.StatusCode = danglingOperationFinalizationStatusCode
		operation.Row.Error = danglingOperationFinalizationErrorText
		operation.Row.ErrorCode = danglingOperationFinalizationErrorCode
		operation.Row.UpdatedAt = syncStartedAt
		operationsToReconstruct[opUUID] = operation

		// Append the operation ID so that we can update all rows in one query.
		unfinishedReconstructedOperationIDs = append(unfinishedReconstructedOperationIDs, operation.Row.ID)
	}

	return unfinishedReconstructedOperationIDs
}

// deleteLocallyRegisteredOperationsNotInSet deletes any operations that are registered to this cluster member, are less
// than or equal to the given max ID, and are not in the given set.
func deleteLocallyRegisteredOperationsNotInSet(ctx context.Context, tx *db.ClusterTx, opsToRetain map[int64]struct{}, maxID int64) (int64, error) {
	var b strings.Builder
	b.WriteString("WHERE node_id = ? AND id <= ?")
	if len(opsToRetain) > 0 {
		b.WriteString(" AND id NOT IN ")
		b.WriteString(query.IntParams(slices.Collect(maps.Keys(opsToRetain))...))
	}

	nDeleted, err := query.DeleteMany[cluster.OperationsRow](ctx, tx.Tx(), b.String(), tx.GetNodeID(), maxID)
	if err != nil {
		return 0, fmt.Errorf("Failed deleting locally registered operations not in given set: %w", err)
	}

	return nDeleted, nil
}

// loadOperationsToSynchronize returns a list of all locally registered operations, a list of
// operations that are candidates for reconstruction, a list of durable operation UUIDs that need to be restarted, and a
// list of durable operation UUIDs that need to be internally cancelled. The lists for durable operations are obtained by
// comparing durable operations in the local map against database contents. This is fallback behaviour for scenarios in
// which a non-leader receives a heartbeat, but the leader does not receive the reply. The leader will take ownership of
// the operation, but it will be left running on this member, so synchronization here will cancel it.
// Any operations that are to be reconstructed also need to be retained, so the operationIDRetentionSet is passed in and modified.
func loadOperationsToSynchronize(ctx context.Context, tx *db.ClusterTx, maxID int64, syncStartedAt time.Time, localOps map[string]*Operation, operationIDRetentionSet map[int64]struct{}) (allMemberOperations []cluster.Operation, operationsToReconstruct map[string]cluster.Operation, durableOperationUUIDsToRestart map[string]struct{}, durableOperationUUIDsToInternallyCancel map[string]struct{}, err error) {
	operationsToReconstruct = make(map[string]cluster.Operation)
	durableOperationUUIDsToRestart = make(map[string]struct{})

	// Anonymous function to append to our list of operations to reconstruct.
	// We don't want to reconstruct operations that are already present (the local map is the source of truth, so
	// overwriting it with an operation reconstructed from the database is incorrect).
	// We also don't want to reconstruct operations whose database ID is greater than the maximum ID at the time of snapshotting the
	// local operations map. These should now be present in the local map but not in our copy.
	addReconstructCandidate := func(dbOp cluster.Operation) {
		_, ok := localOps[dbOp.Row.UUID]
		if ok {
			return
		}

		if dbOp.Row.ID > maxID {
			return
		}

		operationsToReconstruct[dbOp.Row.UUID] = dbOp

		// If the operation is durable and is not finalized, and is not present in the local map, we need to restart it once reconstructed.
		if operationtype.Class(dbOp.Row.Class) == operationtype.OperationClassDurable && !api.StatusCode(dbOp.Row.StatusCode).IsFinal() {
			durableOperationUUIDsToRestart[dbOp.Row.UUID] = struct{}{}
		}
	}

	// Order by parent in ascending order with nulls last so that child operations are scanned first.
	selectClause := "WHERE operations.node_id = ? ORDER BY operations.parent ASC NULLS LAST"
	parentIDs := make(map[int64]struct{})
	durableOperationUUIDs := make(map[string]struct{})
	err = query.SelectFunc[cluster.Operation](ctx, tx.Tx(), selectClause, func(operation cluster.Operation) error {
		allMemberOperations = append(allMemberOperations, operation)
		if operation.Row.Class == int64(operationtype.OperationClassDurable) {
			durableOperationUUIDs[operation.Row.UUID] = struct{}{}
		}

		// Figure out if the operation is parent operation. Child operations are scanned first, so this populates the
		// parentIDs map before parents are scanned.
		var isParent bool
		if operation.Row.Parent != nil {
			parentIDs[*operation.Row.Parent] = struct{}{}
		} else {
			_, isParent = parentIDs[operation.Row.ID]
		}

		// If the operation is retained, then it is added to the retention set and added as a reconstruct candidate.
		if isRetentionCandidate(syncStartedAt, operation, isParent) {
			operationIDRetentionSet[operation.Row.ID] = struct{}{}
			addReconstructCandidate(operation)
		}

		return nil
	}, tx.GetNodeID())
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("Failed getting operations to synchronize: %w", err)
	}

	// If any durable operations in the local map are not in the database, they need to be internally cancelled.
	durableOperationUUIDsToInternallyCancel = make(map[string]struct{})
	for _, op := range localOps {
		if op.class != operationtype.OperationClassDurable {
			continue
		}

		if !op.IsRunning() {
			// Operation is in a final state, no need to cancel it.
			continue
		}

		// We expect the local operation to be written to the database.
		_, ok := durableOperationUUIDs[op.id]
		if ok {
			continue
		}

		// If it is not present in the database, internally cancel it. It has been rescheduled by another member.
		durableOperationUUIDsToInternallyCancel[op.id] = struct{}{}
	}

	return allMemberOperations, operationsToReconstruct, durableOperationUUIDsToRestart, durableOperationUUIDsToInternallyCancel, nil
}

// sortLocalOperationsForSynchronization inspects the local operations map and returns:
//   - The time at which the map snapshot is taken.
//   - The maximum database ID present in the map.
//   - A copy of the local map.
//   - A set of operation database IDs to retain.
//   - A list of operation UUIDs to locally delete.
func sortLocalOperationsForSynchronization() (syncStartedAt time.Time, maxID int64, localOps map[string]*Operation, operationIDRetentionSet map[int64]struct{}, opsToDeleteInternal []string) {
	localOps = make(map[string]*Operation)
	operationIDRetentionSet = make(map[int64]struct{})

	operationsLock.Lock()
	defer operationsLock.Unlock()

	syncStartedAt = time.Now()
	for _, op := range operations {
		// Add operation to map copy.
		localOps[op.id] = op

		// Update the max ID.
		if op.dbID > maxID {
			maxID = op.dbID
		}

		if isRetentionCandidate(syncStartedAt, op, len(op.children) > 0) {
			operationIDRetentionSet[op.dbID] = struct{}{}
			continue
		}

		// Delete all other operations from the local map.
		opsToDeleteInternal = append(opsToDeleteInternal, op.id)
	}

	return syncStartedAt, maxID, localOps, operationIDRetentionSet, opsToDeleteInternal
}

// isRetentionCandidate returns true if the given [*Operation] or [cluster.Operation] should be retained (i.e. it should
// remain present both in the local map and in the database).
func isRetentionCandidate[O interface {
	IsFinished() bool
	IsChild() bool
	UpdatedAt() time.Time
	*Operation | cluster.Operation
}](now time.Time, op O, isParent bool) bool {
	// Retain all children, because we will delete them by foreign key in the database, and they will be equivalently
	// deleted from the internal map via deleteInternal.
	if op.IsChild() {
		return true
	}

	// Retain operations that have not completed. We can't delete operations that haven't finished yet.
	if !op.IsFinished() {
		return true
	}

	// Retain any operations that completed less than 5 seconds ago. We retain these for API inspection.
	updatedAt := op.UpdatedAt()
	if now.Before(updatedAt.Add(OperationRetentionDuration)) {
		return true
	}

	// Retain bulk operations that completed less than 24 hours ago. We retain these for the API inspection.
	// Otherwise, the operation can be discarded.
	return isParent && now.Before(updatedAt.Add(OperationRetentionDurationBulk))
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

// RelocateAndReconstructRunningDurableOperationsFromNode loads all durable operations on the given node, relocates them to this
// cluster member (by changing their node ID), and reconstructs them.
func RelocateAndReconstructRunningDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) ([]*Operation, error) {
	var ops []*Operation
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// See if there are any durable operations running on the node which need to be restarted.
		dbOps, err := cluster.GetOperationsByNodeIDAndClass(ctx, tx.Tx(), nodeID, operationtype.OperationClassDurable)
		if err != nil {
			return fmt.Errorf("Failed loading durable operations for node ID %d: %w", nodeID, err)
		}

		// Check which are running and which have already completed.
		var nRunningOps int
		for _, dbOp := range dbOps {
			if api.StatusCode(dbOp.Row.StatusCode).IsFinal() {
				continue
			}

			nRunningOps++
		}

		// If none were running, then there are none to restart.
		if nRunningOps == 0 {
			return nil
		}

		// Move the operation that have not completed to this member.
		nMovedOps, err := relocateUnfinishedDurableOperationsFromNode(ctx, tx, nodeID)
		if err != nil {
			return fmt.Errorf("Failed relocating durable operations from node ID %d: %w", nodeID, err)
		}

		if int(nMovedOps) != nRunningOps {
			return fmt.Errorf("Failed relocating durable operations from node ID %d: Expected to move %d operations but moved %d", nodeID, nRunningOps, nMovedOps)
		}

		// Update the records of operations we moved to match what is now in the database.
		currentNodeID := tx.GetNodeID()
		for _, dbOp := range dbOps {
			if api.StatusCode(dbOp.Row.StatusCode).IsFinal() {
				continue
			}

			dbOp.Row.NodeID = currentNodeID
			dbOp.NodeName = s.ServerName
			dbOp.NodeAddress = s.Endpoints.NetworkAddress()
		}

		// Reconstruct all durable operations. Even those that have finished.
		// This is so that bulk operations still have access to their completed children.
		ops, err = ConstructOperationsFromDB(ctx, tx.Tx(), s, dbOps)
		if err != nil {
			return fmt.Errorf("Failed reconstructing durable operations: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Reconstructed operations are a list of parent operations.
	// If the parent operations have completed, there is no need to restart them.
	ops = slices.DeleteFunc(ops, func(op *Operation) bool {
		return op.finished.Err() != nil
	})

	return ops, nil
}

// relocateUnfinishedDurableOperationsFromNode moves durable operations from the given nodeID to this cluster member.
// It is defined in this package (rather than in the db/cluster package) and unexported to prevent usage elsewhere.
// It returns the number of affected rows.
func relocateUnfinishedDurableOperationsFromNode(ctx context.Context, tx *db.ClusterTx, fromNodeID int64) (int64, error) {
	res, err := tx.Tx().ExecContext(ctx, "UPDATE operations SET node_id = ?, updated_at = ? WHERE node_id = ? AND class = ? AND status_code < ?", tx.GetNodeID(), time.Now(), fromNodeID, operationtype.OperationClassDurable, api.Success)
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

// restartOperation registers the operation and all of its children to the in-memory operations map and then
// starts the parent (which will handle starting the children).
func restartOperation(op *Operation) {
	// Don't restart operations which are already in a final state.
	if !op.IsRunning() {
		return
	}

	operationsLock.Lock()
	operations[op.id] = op
	for _, childOp := range op.children {
		operations[childOp.id] = childOp
	}

	operationsLock.Unlock()

	op.logger.Debug("Restarting operation", logger.Ctx{"id": op.id})
	op.start()
}

// RestartDurableOperationsFromNode restarts all durable operations that were running on the node
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	operations, err := RelocateAndReconstructRunningDurableOperationsFromNode(ctx, s, nodeID)
	if err != nil {
		return err
	}

	if len(operations) == 0 {
		return nil
	}

	logger.Warn("Restarting durable operations", logger.Ctx{"count": len(operations)})
	for _, op := range operations {
		restartOperation(op)
	}

	return nil
}
