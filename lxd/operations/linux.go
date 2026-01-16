//go:build linux && cgo && !agent

package operations

import (
	"context"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
)

func registerDBOperation(op *Operation, opType operationtype.Type) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Fixed references use the unique DB constraint to enforce cluster-wide exclusivity.
		opInfo := cluster.Operation{
			UUID:   op.id,
			Type:   opType,
			NodeID: tx.GetNodeID(),
		}

		if op.projectName != "" {
			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
			if err != nil {
				return fmt.Errorf("Fetch project ID: %w", err)
			}

			opInfo.ProjectID = &projectID
		}

		// Uniqueness conflicts are surfaced to callers.
		_, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return api.StatusErrorf(http.StatusConflict, "Another operation with reference %q already exists", op.id)
			}

			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", opType.Description(), err)
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
