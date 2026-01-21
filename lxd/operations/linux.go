//go:build linux && cgo && !agent

package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
)

func registerDBOperation(op *Operation) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		opInfo := cluster.Operation{
			Reference: op.id,
			Type:      op.dbOpType,
			NodeID:    tx.GetNodeID(),
			Class:     (int64)(op.class),
			CreatedAt: op.createdAt,
			UpdatedAt: op.updatedAt,
			Inputs:    op.inputs,
			Status:    int64(op.Status()),
		}

		if op.projectName != "" {
			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
			if err != nil {
				return fmt.Errorf("Fetch project ID: %w", err)
			}

			opInfo.ProjectID = &projectID
		}

		if op.requestor != nil {
			opInfo.RequestorProtocol = op.requestor.CallerProtocol()

			requestorCallerIdentityID := op.requestor.CallerIdentityID()
			if requestorCallerIdentityID != 0 {
				identityID := int64(requestorCallerIdentityID)
				opInfo.RequestorIdentityID = &identityID
			}
		}

		_, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return nil
}

func updateDBOperationStatus(op *Operation) error {
	op.updatedAt = time.Now()

	var opErr *string
	if op.err != nil {
		tempErr := op.err.Error()
		opErr = &tempErr
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateOperationStatus(ctx, tx.Tx(), op.id, op.Status(), op.updatedAt, opErr)
	})
	if err != nil {
		return fmt.Errorf("Failed updating Operation %s status in database: %w", op.id, err)
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

func conflictingOperationExists(op *Operation, constraint OperationUniquenessConstraint) (bool, error) {
	var entityURL *api.URL
	// If the constraint is restricting also the entity ID on which the operation operates,
	// use the first resource's URL to check for conflicts.
	// operationCreate() has already verified there is already only one resource type in the list.
	if constraint == OperationUniquenessConstraintEntityID {
		for _, resources := range op.resources {
			entityURL = &resources[0]
		}
	}

	var ops []cluster.Operation
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		opType := op.dbOpType
		filter := cluster.OperationFilter{Type: &opType}
		if entityURL != nil {
			// Get the entity ID from the resource URL.
			entityReference, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), entityURL)
			if err != nil {
				return fmt.Errorf("Failed getting entity ID from resource URL %q: %w", entityURL.String(), err)
			}

			filter.EntityID = &entityReference.EntityID
		}

		var err error
		ops, err = cluster.GetOperations(ctx, tx.Tx(), filter)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("Failed checking for conflicting operations: %w", err)
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
