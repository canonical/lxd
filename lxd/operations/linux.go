//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

func registerDBOperation(op *Operation, opType operationtype.Type) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		opInfo := cluster.Operation{
			Reference: op.id,
			Type:      opType,
			NodeID:    tx.GetNodeID(),
			Class:     (int64)(op.class),
			CreatedAt: op.createdAt,
			UpdatedAt: op.updatedAt,
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

// NewDurableOperation is a constructor of the Operation object based on its database representation.
func NewDurableOperation(ctx context.Context, tx *sql.Tx, s *state.State, dbOp *cluster.Operation, projectName string) (*Operation, error) {
	if dbOp.Class != int64(OperationClassDurable) {
		return nil, fmt.Errorf("Operation %s is not of durable class", dbOp.Reference)
	}

	op := Operation{}
	op.projectName = projectName
	op.id = dbOp.Reference
	op.description = dbOp.Type.Description()
	op.entityType, op.entitlement = dbOp.Type.Permission()
	op.dbOpType = dbOp.Type
	op.class = OperationClass(dbOp.Class)
	op.createdAt = dbOp.CreatedAt
	op.updatedAt = dbOp.CreatedAt
	op.status = api.StatusCode(dbOp.Status)
	op.url = api.NewURL().Path(version.APIVersion, "operations", dbOp.Reference).String()
	op.finished = cancel.New()
	op.running = cancel.New()
	op.state = s
	op.requestor = nil
	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})

	if s != nil {
		op.SetEventServer(s.Events)
	}

	op.resources = nil
	op.metadata = nil

	runHook, ok := durableOperations[op.dbOpType]
	if !ok {
		return nil, fmt.Errorf("No durable operation handlers defined for operation type %q", op.dbOpType)
	}

	op.onRun = runHook

	return &op, nil
}
