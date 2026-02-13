//go:build linux && cgo && !agent

package operations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
)

func registerOperation(ctx context.Context, tx *db.ClusterTx, op *Operation, conflictReference string, parentOpID *int64) (int64, error) {
	// If a conflict reference is provided, check if any conflicting operation is already running before creating the new operation record.
	if op.dbOpType.ConflictAction() == operationtype.ConflictActionFail && conflictReference != "" {
		conflict, err := conflictingOperationExists(ctx, tx, conflictReference)
		if err != nil {
			return 0, err
		}

		if conflict {
			return 0, fmt.Errorf("Conflicting operation with conflict reference %q already exists", conflictReference)
		}
	}

	opInfo := cluster.Operation{
		UUID:      op.id,
		Type:      op.dbOpType,
		NodeID:    tx.GetNodeID(),
		Class:     (int64)(op.class),
		CreatedAt: op.createdAt,
		UpdatedAt: op.updatedAt,
		Status:    int64(op.Status()),
		Parent:    parentOpID,
	}

	if op.projectName != "" {
		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
		if err != nil {
			return 0, fmt.Errorf("Fetch project ID: %w", err)
		}

		opInfo.ProjectID = &projectID
	}

	if op.requestor != nil {
		value := cluster.RequestorProtocol(op.requestor.CallerProtocol())
		opInfo.RequestorProtocol = &value

		requestorCallerIdentityID := op.requestor.CallerIdentityID()
		if requestorCallerIdentityID != 0 {
			identityID := int64(requestorCallerIdentityID)
			opInfo.RequestorIdentityID = &identityID
		}
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

	return cluster.CreateOperation(ctx, tx.Tx(), opInfo)
}

func registerDBOperation(op *Operation, conflictReference string) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create parent operation record.
		parentOpID, err := registerOperation(ctx, tx, op, conflictReference, nil)
		if err != nil {
			return err
		}

		for _, childOp := range op.children {
			_, err := registerOperation(ctx, tx, childOp, conflictReference, &parentOpID)
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

		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, op.updatedAt, op.status, string(metadataJSON), op.err)
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

func conflictingOperationExists(ctx context.Context, tx *db.ClusterTx, conflictReference string) (bool, error) {
	var ops []cluster.Operation
	filter := cluster.OperationFilter{ConflictReference: &conflictReference}
	var err error
	ops, err = cluster.GetOperations(ctx, tx.Tx(), filter)
	if err != nil {
		return false, fmt.Errorf("Failed fetching operations with conflict reference %q: %w", conflictReference, err)
	}

	// Detect conflict only if any of the operations of conflicting type (and entity ID if applicable)
	// is still running.
	for _, existingOp := range ops {
		if !api.StatusCode(existingOp.Status).IsFinal() {
			return true, nil
		}
	}

	return false, nil
}
