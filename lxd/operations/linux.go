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
			Reference:   op.id,
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
					return fmt.Errorf("Failed adding %q Operation %s to database: %w", opType.Description(), op.id, err)
				}

				opInfo.RequestorIdentityID = &identityID
			}
		}

		opID, err := cluster.CreateOrReplaceOperation(ctx, tx.Tx(), opInfo)
		if err != nil {
			return err
		}

		// For durable operations we need to register metadata and resources in the database.
		if op.class == OperationClassDurable {
			err = cluster.CreateOrInsertDurableOperationMetadata(ctx, tx.Tx(), opID, op.metadata)
			if err != nil {
				return fmt.Errorf("Failed adding operation %s metadata to database: %w", op.id, err)
			}
		}

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
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	var projects map[int64]string
	var err error
	var dbOps []cluster.Operation
	metadata := make(map[int64]map[string]string)

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// See if there are any durable operations running on this node which need to be restarted.
		durableClass := (int64)(OperationClassDurable)
		filter := cluster.OperationFilter{NodeID: &nodeID, Class: &durableClass}
		dbOps, err = cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading durable operations for the node %d: %w", nodeID, err)
		}

		for _, dbOp := range dbOps {
			metadata[dbOp.ID], err = cluster.GetDurableOperationMetadata(ctx, tx.Tx(), dbOp.ID)
			if err != nil {
				return fmt.Errorf("Failed loading durable operation metadata for operation %d: %w", dbOp.ID, err)
			}
		}

		projects, err = cluster.GetProjectIDsToNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading project IDs to names: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
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

		args := OperationArgs{
			ProjectName: projectName,
			Type:        dbOp.Type,
			Class:       OperationClass(dbOp.Class),
			Resources:   nil,
			Metadata:    metadata[dbOp.ID],
			Reference:   dbOp.Reference,
		}

		op, err := CreateServerOperation(s, args)

		if err != nil {
			logger.Warn("Failed creating durable operation", logger.Ctx{"err": err})
			continue
		}

		err = op.Start()
		if err != nil {
			logger.Warn("Failed starting durable operation", logger.Ctx{"err": err})
		}
	}

	return nil
}
