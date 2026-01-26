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
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

func registerDBOperation(ctx context.Context, op *Operation) error {
	if op.state == nil {
		return errors.New("Failed registering operation: No state available")
	}

	registerSingleOperation := func(ctx context.Context, tx *db.ClusterTx, op *Operation, parentOpID *int64, projectID *int64) (int64, error) {
		// Conflict reference should only be provided for operation types that support conflicts.
		if op.dbOpType.ConflictAction() == operationtype.ConflictActionNone && op.conflictReference != "" {
			return 0, fmt.Errorf("Conflict reference %q provided for operation type %q that does not support conflicts", op.conflictReference, op.dbOpType.Description())
		}

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

		inputsJSON, err := json.Marshal(op.inputs)
		if err != nil {
			return 0, fmt.Errorf("Failed marshalling operation inputs: %w", err)
		}

		operationsRow.Inputs = string(inputsJSON)

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

// ConstructOperationFromDB is a constructor of a single Operation object based on its database representation.
// ConstructOperationFromDB doesn't populate the parent field, as that would require loading all other operations from the DB. Instead,
// the caller is expected to set the parent field on the returned Operation object based on other loaded operations, if needed.
func ConstructOperationFromDB(ctx context.Context, tx *sql.Tx, s *state.State, dbOp *cluster.Operation) (*Operation, error) {
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

	// Load operation inputs.
	var inputs map[string]any
	var err error
	err = json.Unmarshal([]byte(dbOp.Row.Inputs), &inputs)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation inputs for operation %d: %w", dbOp.Row.ID, err)
	}

	op.inputs = inputs

	// Load operation entity URL.
	// Note that we rely on the entity_type of the operation and the entityURL being the same. This is enforced by a check in the initOperation().
	entityURL, err := cluster.GetEntityURL(ctx, tx, dbOp.Row.Type.EntityType(), int(dbOp.Row.EntityID))
	if err == nil {
		op.entityURL = entityURL
	} else {
		// Fail for all errors other than the not found (see below)
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, fmt.Errorf("Failed loading entity URL for operation: %w", err)
		}

		// For various delete operations, these operations actually delete the entity somewhere in the operation code, which means that we might not be able to load the entity URL after the entity was deleted.
		// If this is the case, the entity URL will simply be empty.
		logger.Debug("Failed loading entity URL for operation, leaving it empty on the operation struct", logger.Ctx{"operationID": dbOp.Row.UUID, "entityType": dbOp.Row.Type.EntityType(), "entityID": dbOp.Row.EntityID})
	}

	// Load operation resources.
	op.resources, err = cluster.GetOperationResources(ctx, tx, dbOp.Row.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed loading operation resources for operation %d: %w", dbOp.Row.ID, err)
	}

	// Load operation metadata.
	var metadata map[string]any
	err = json.Unmarshal([]byte(dbOp.Row.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation metadata for operation %d: %w", dbOp.Row.ID, err)
	}

	op.metadata = metadata

	// Load the requestor identity if a protocol was set.
	// Note that the origin address is not saved, so cannot be set on the reconstructed operation.
	if dbOp.Row.RequestorProtocol != nil && *dbOp.Row.RequestorProtocol != "" {
		op.requestor = &request.RequestorAuditor{
			Protocol: string(*dbOp.Row.RequestorProtocol),
		}

		if dbOp.Row.RequestorIdentityID != nil {
			identity, err := cluster.GetIdentityByID(ctx, tx, *dbOp.Row.RequestorIdentityID)
			if err != nil {
				return nil, fmt.Errorf("Failed loading identity for operation %d: %w", dbOp.Row.ID, err)
			}

			op.requestor.IdentityID = &identity.ID
			op.requestor.Username = identity.Identifier
		}
	}

	if op.class == operationtype.OperationClassDurable {
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

// loadDurableOperationFromDB reloads a durable operation from the database based on its UUID.
// This is only used to ease debugging to ensure that the reloaded operation is identical to the one originally created.
func loadDurableOperationFromDB(op *Operation) (*Operation, error) {
	var result *Operation
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbOp, err := cluster.GetOperation(ctx, tx.Tx(), op.id)
		if err != nil {
			return fmt.Errorf("Failed loading operation %q from database: %w", op.id, err)
		}

		op, err := ConstructOperationFromDB(ctx, tx.Tx(), op.state, dbOp)
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
