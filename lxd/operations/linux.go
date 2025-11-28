//go:build linux && cgo && !agent

package operations

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

func registerDBOperation(op *Operation, opType operationtype.Type) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		opInfo := cluster.Operation{
			UUID:        op.id,
			Type:        opType,
			NodeID:      tx.GetNodeID(),
			Description: op.description,
			Class:       (int64)(op.class),
			CreatedAt:   op.createdAt,
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
			callerIdentity := op.requestor.CallerIdentity()
			if callerIdentity != nil {
				identityID, err := cluster.GetIdentityID(ctx, tx.Tx(), cluster.AuthMethod(callerIdentity.AuthenticationMethod), callerIdentity.Identifier)
				if err != nil {
					return fmt.Errorf("failed to add %q Operation %s to database: %w", opType.Description(), op.id, err)
				}

				opInfo.IdentityID = &identityID
			}
		}

		_, err := cluster.CreateOrReplaceOperation(ctx, tx.Tx(), opInfo)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to add %q Operation %s to database: %w", opType.Description(), op.id, err)
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

// RestartDurableOperationsFromNode restarts all durable operations that were running on node,
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, localNodeID int64, dbOps []cluster.Operation) error {
	var projects map[int64]string
	var err error
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err = cluster.GetProjectIDsToNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to load project IDs to names: %w", err)
	}

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

		op, err := CreateDurableOperation(ctx, s, dbOp.UUID, projectName, dbOp.Type, nil, nil)

		if err != nil {
			logger.Warn("Failed to create durable operation", logger.Ctx{"err": err})
		}

		// TODO insert the onDone() function to update metrics

		err = op.Start()
		if err != nil {
			logger.Warn("Failed to start durable operation", logger.Ctx{"err": err})
		}
	}

	return nil
}
