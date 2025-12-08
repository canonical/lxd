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
