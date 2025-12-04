//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

func registerDBOperation(ctx context.Context, op *Operation) error {
	if op.state == nil {
		return nil
	}

	registerSingleOperation := func(ctx context.Context, tx *db.ClusterTx, op *Operation, parentOpID *int64) (int64, error) {
		// Conflict reference should only be provided for operation types that support conflicts.
		if op.dbOpType.ConflictAction() == operationtype.ConflictActionNone && op.conflictReference != "" {
			return 0, fmt.Errorf("Conflict reference %q provided for operation type %q that does not support conflicts", op.conflictReference, op.dbOpType.Description())
		}

		opInfo := cluster.Operation{
			UUID:              op.id,
			Type:              op.dbOpType,
			NodeID:            tx.GetNodeID(),
			Class:             int64(op.class),
			CreatedAt:         op.createdAt,
			UpdatedAt:         op.updatedAt,
			Status:            int64(op.Status()),
			Parent:            parentOpID,
			ConflictReference: op.conflictReference,
		}

		if op.projectName != "" {
			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
			if err != nil {
				return 0, fmt.Errorf("Failed fetching project ID: %w", err)
			}

			opInfo.ProjectID = &projectID
		}

		if op.requestor != nil {
			// If there is no requestor (eg. for server operations), we leave the requestor_protocol
			// and requestor_identity_id fields `null` in the database.
			// If there's an untrusted requestor with empty protocol and no identity, we set the
			// requestor_protocol to `requestorProtocolNone` and leave the requestor_identity_id `null`.
			// The untrusted requestor is provided eg. in a local image upload operation run as part of an image copy operation.
			value := cluster.RequestorProtocol(op.requestor.CallerProtocol())
			opInfo.RequestorProtocol = &value

			requestorCallerIdentityID := op.requestor.CallerIdentityID()
			if requestorCallerIdentityID != 0 {
				identityID := int64(requestorCallerIdentityID)
				opInfo.RequestorIdentityID = &identityID
			}
		}

		if op.entityURL != nil {
			entityReference, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), op.entityURL)
			if err != nil {
				return 0, fmt.Errorf("Failed fetching entity reference: %w", err)
			}

			if entityReference.EntityType != cluster.EntityType(op.dbOpType.EntityType()) {
				return 0, fmt.Errorf("Entity type %q does not match operation type's entity type %q", entityReference.EntityType, op.dbOpType.EntityType())
			}

			opInfo.EntityID = entityReference.EntityID
		}

		inputsJSON, err := json.Marshal(op.inputs)
		if err != nil {
			return 0, fmt.Errorf("Failed marshalling operation inputs: %w", err)
		}

		opInfo.Inputs = string(inputsJSON)

		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return 0, fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		opInfo.Metadata = string(metadataJSON)

		dbOpID, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
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
		// Create parent operation record.
		parentOpID, err := registerSingleOperation(ctx, tx, op, nil)
		if err != nil {
			return err
		}

		// Create child operation records, if any.
		for _, childOp := range op.children {
			_, err := registerSingleOperation(ctx, tx, childOp, &parentOpID)
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
	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, op.updatedAt, op.status, string(metadataJSON), op.err, op.errCode)
	})
	if err != nil {
		return fmt.Errorf("Failed updating operation %q record: %w", op.id, err)
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteOperation(ctx, tx.Tx(), op.id)
	})

	return err
}

func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		return
	}

	_ = op.events.Send(op.projectName, api.EventTypeOperation, eventMessage)
}

// ConstructOperationFromDB is a constructor of a single Operation object based on its database representation.
// ConstructOperationFromDB doesn't populate the parent field, as that would require loading all other operations from the DB. Instead,
// the caller is expected to set the parent field on the returned Operation object based on other loaded operations, if needed.
func ConstructOperationFromDB(ctx context.Context, tx *sql.Tx, s *state.State, dbOp *cluster.Operation, projectName string) (*Operation, error) {
	op := Operation{
		projectName:       projectName,
		id:                dbOp.UUID,
		class:             OperationClass(dbOp.Class),
		createdAt:         dbOp.CreatedAt,
		updatedAt:         dbOp.UpdatedAt,
		status:            api.StatusCode(dbOp.Status),
		url:               api.NewURL().Path(version.APIVersion, "operations", dbOp.UUID).String(),
		description:       dbOp.Type.Description(),
		dbOpType:          dbOp.Type,
		finished:          cancel.New(),
		running:           cancel.New(),
		state:             s,
		location:          dbOp.Location,
		err:               dbOp.Error,
		errCode:           dbOp.ErrorCode,
		conflictReference: dbOp.ConflictReference,
	}

	// If server is not clustered, the DB contains 'none' as the node name. In that case we use the server name as the location.
	if !s.ServerClustered {
		op.location = s.ServerName
	} else {
		op.location = dbOp.Location
	}

	// If operation is already in final state, cancel both contexts, there's no point in running any hook.
	if op.status.IsFinal() {
		op.running.Cancel()
		op.finished.Cancel()
	}

	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})

	if s != nil {
		op.SetEventServer(s.Events)
	}

	// Load operation inputs.
	var inputs map[string]any
	var err error
	err = json.Unmarshal([]byte(dbOp.Inputs), &inputs)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation inputs for operation %d: %w", dbOp.ID, err)
	}

	op.inputs = inputs

	// Load operation entity URL.
	// Note that we rely on the entity_type of the operation and the entityURL being the same. This is enforced by a check in the initOperation().
	entityURL, err := cluster.GetEntityURL(ctx, tx, dbOp.Type.EntityType(), dbOp.EntityID)
	if err == nil {
		op.entityURL = entityURL
	} else {
		// Fail for all errors other than the not found (see below)
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, fmt.Errorf("Failed loading entity URL for operation: %w", err)
		}

		// For various delete operations, these operations actually delete the entity somewhere in the operation code, which means that we might not be able to load the entity URL after the entity was deleted.
		// If this is the case, the entity URL will simply be empty.
		logger.Debug("Failed loading entity URL for operation, leaving it empty on the operation struct", logger.Ctx{"operationID": dbOp.UUID, "entityType": dbOp.Type.EntityType(), "entityID": dbOp.EntityID})
	}

	// Load operation resources.
	op.resources, err = cluster.GetOperationResources(ctx, tx, dbOp.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed loading operation resources for operation %d: %w", dbOp.ID, err)
	}

	// Load operation metadata.
	var metadata map[string]any
	err = json.Unmarshal([]byte(dbOp.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation metadata for operation %d: %w", dbOp.ID, err)
	}

	op.metadata = metadata

	// Load the requestor identity if provided.
	if dbOp.RequestorIdentityID != nil {
		identity, err := cluster.GetIdentityByID(ctx, tx, *dbOp.RequestorIdentityID)
		if err != nil {
			return nil, fmt.Errorf("Failed loading identity for operation %d: %w", dbOp.ID, err)
		}

		// Reconstruct the requestor.
		protocol := ""
		if dbOp.RequestorProtocol != nil {
			protocol = string(*dbOp.RequestorProtocol)
		}

		op.requestor = &opRequestor{
			identityID: *dbOp.RequestorIdentityID,
			r: &api.OperationRequestor{
				Username: identity.Identifier,
				Protocol: protocol,
			},
		}
	}

	if op.class == OperationClassDurable {
		runHook, ok := durableOperations[op.dbOpType]
		if !ok {
			return nil, fmt.Errorf("No durable operation handlers defined for operation type %d (%q)", op.dbOpType, op.dbOpType.Description())
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
		dbOps, err := cluster.GetOperations(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading operations: %w", err)
		}

		for _, dbOp := range dbOps {
			// Don't prune operations which are still running.
			if !api.StatusCode(dbOp.Status).IsFinal() {
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

// loadDurableOperationFromDB reloads a durable operation from the database based on its UUID.
// This is only used to ease debugging to ensure that the reloaded operation is identical to the one originally created.
func loadDurableOperationFromDB(op *Operation) (*Operation, error) {
	var result *Operation
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.OperationFilter{UUID: &op.id}
		dbOps, err := cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading operation %q from database: %w", op.id, err)
		}

		if len(dbOps) != 1 {
			return fmt.Errorf("Operation %q not found in database", op.id)
		}

		dbOp := dbOps[0]
		projectName := ""
		if dbOp.ProjectID != nil {
			projectID := (int)(*dbOp.ProjectID)
			filter := cluster.ProjectFilter{ID: &projectID}
			projects, err := cluster.GetProjects(ctx, tx.Tx(), filter)
			if err != nil {
				return fmt.Errorf("Failed loading project for operation %q from database: %w", op.id, err)
			}

			if len(projects) != 1 {
				return fmt.Errorf("Project ID %d for operation %q not found in database", *dbOp.ProjectID, op.id)
			}

			projectName = projects[0].Name
		}

		op, err := ConstructOperationFromDB(ctx, tx.Tx(), op.state, &dbOp, projectName)
		if err != nil {
			return fmt.Errorf("Failed constructing operation %q from database: %w", op.id, err)
		}

		result = op
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// LoadDurableOperationsFromNode returns all durable operations from the db that exist on given node.
func LoadDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) ([]*Operation, error) {
	var dbOps []cluster.Operation
	opsMap := map[int64]struct {
		op   *Operation
		dbOp *cluster.Operation
	}{}

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjectIDsToNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading project IDs to names: %w", err)
		}

		// See if there are any durable operations running on this node which need to be restarted.
		durableClass := (int64)(OperationClassDurable)
		filter := cluster.OperationFilter{NodeID: &nodeID, Class: &durableClass}
		dbOps, err = cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading durable operations for node ID %d: %w", nodeID, err)
		}

		// We'll put both the operations.Operations and the DB operations in a map keyed by their DB ID.
		// This is needed to set the parent-child relationships between operations.
		for _, dbOp := range dbOps {
			var projectName string

			// Load the project name if provided.
			if dbOp.ProjectID != nil {
				var ok bool
				projectName, ok = projects[*dbOp.ProjectID]
				if !ok {
					logger.Warn("Project ID not found in the map of projects", logger.Ctx{"projectID": *dbOp.ProjectID})
					continue
				}
			}

			// Update the operation nodeID to this node if needed.
			if dbOp.NodeID != tx.GetNodeID() {
				dbOp.NodeID = tx.GetNodeID()
				dbOp.UpdatedAt = time.Now()
				err = cluster.UpdateOperationNodeID(ctx, tx.Tx(), dbOp.UUID, dbOp.NodeID, dbOp.UpdatedAt)
				if err != nil {
					return fmt.Errorf("Failed updating existing operation %q node ID: %w", dbOp.UUID, err)
				}
			}

			// Create an Operation object for each DB entry.
			op, err := ConstructOperationFromDB(ctx, tx.Tx(), s, &dbOp, projectName)
			if err != nil {
				return fmt.Errorf("Failed creating durable operation from DB entry: %w", err)
			}

			opsMap[dbOp.ID] = struct {
				op   *Operation
				dbOp *cluster.Operation
			}{op: op, dbOp: &dbOp}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Now that we have all operations created, we can set the parent-child relationships.
	var result []*Operation
	for _, opPair := range opsMap {
		// If the operation has no parent, we'll return it as a top-level operation.
		if opPair.dbOp.Parent == nil {
			result = append(result, opPair.op)
			continue
		}

		// Otherwise find the parent operation.
		parentOpPair, ok := opsMap[*opPair.dbOp.Parent]
		if !ok {
			logger.Warn("Parent operation not found in the map of operations when setting parent-child relationships", logger.Ctx{"operationID": opPair.dbOp.UUID, "parentID": *opPair.dbOp.Parent})
			continue
		}

		// And set the parent-child relationship.
		parentOpPair.op.AddChildren(opPair.op)
	}

	// Clear run hooks for parent operations. These should not be set from the run hook table per operation type.
	for _, op := range result {
		if len(op.children) > 0 {
			op.onRun = nil
		}
	}

	// Return values of the operations map as a slice.
	return result, nil
}

// RestartDurableOperation creates and starts a new durable operation based on the provided Operation object.
// This is used to restart operations that were running on a node which failed to respond to heartbeats on other node.
func RestartDurableOperation(s *state.State, op *Operation) {
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

	op.logger.Debug("Restarting durable operation", logger.Ctx{"id": op.id})
	op.start()
}

// RestartDurableOperationsFromNode restarts all durable operations that were running on the node
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	operations, err := LoadDurableOperationsFromNode(ctx, s, nodeID)
	if err != nil {
		return err
	}

	for _, op := range operations {
		RestartDurableOperation(s, op)
	}

	return nil
}
