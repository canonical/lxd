//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
)

func registerDBOperation(op *Operation, opType operationtype.Type) error {
	if op.transaction == nil {
		return nil
	}

	err := op.transaction(context.TODO(), func(ctx context.Context, tx *sql.Tx) error {
		nodeID, err := cluster.GetNodeID(ctx, tx, op.location)
		if err != nil {
			return fmt.Errorf("Failed getting node ID: %w", err)
		}

		opInfo := cluster.Operation{
			UUID:   op.id,
			Type:   opType,
			NodeID: nodeID,
		}

		if op.projectName != "" {
			projectID, err := cluster.GetProjectID(ctx, tx, op.projectName)
			if err != nil {
				return fmt.Errorf("Fetch project ID: %w", err)
			}

			opInfo.ProjectID = &projectID
		}

		_, err = cluster.CreateOrReplaceOperation(ctx, tx, opInfo)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to add %q Operation %s to database: %w", opType.Description(), op.id, err)
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.transaction == nil {
		return nil
	}

	err := op.transaction(context.TODO(), func(ctx context.Context, tx *sql.Tx) error {
		return cluster.DeleteOperation(ctx, tx, op.id)
	})

	return err
}

func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		return
	}

	_ = op.events.Send(op.projectName, api.EventTypeOperation, eventMessage)
}
