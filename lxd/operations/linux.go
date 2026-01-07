//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

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
		// Fixed references use the unique DB constraint to enforce cluster-wide exclusivity.
		nodeID := tx.GetNodeID()
		opInfo := cluster.Operation{
			Reference: op.id,
			Type:      opType,
			NodeID:    &nodeID,
			Class:     (int64)(op.class),
			CreatedAt: op.createdAt,
			UpdatedAt: op.updatedAt,
			Inputs:    op.inputs,
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

			if op.requestor.CallerIdentityID() != 0 {
				identityID := int64(op.requestor.CallerIdentityID())
				opInfo.RequestorIdentityID = &identityID
			}
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

// NewDurableOperation is a constructor of the Operation object based on its database representation.
func NewDurableOperation(ctx context.Context, tx *sql.Tx, s *state.State, dbOp *cluster.Operation, projectName string) (*Operation, error) {
	if dbOp.Class != int64(OperationClassDurable) {
		return nil, fmt.Errorf("Operation %s is not of durable class", dbOp.Reference)
	}

	op := Operation{
		projectName: projectName,
		id:          dbOp.Reference,
		class:       OperationClass(dbOp.Class),
		createdAt:   dbOp.CreatedAt,
		updatedAt:   dbOp.CreatedAt,
		status:      api.StatusCode(dbOp.Status),
		url:         api.NewURL().Path(version.APIVersion, "operations", dbOp.Reference).String(),
		description: dbOp.Type.Description(),
		dbOpType:    dbOp.Type,
		inputs:      dbOp.Inputs,
		finished:    cancel.New(),
		running:     cancel.New(),
		state:       s,
	}

	if dbOp.Error != nil {
		op.err = errors.New(*dbOp.Error)
	}

	op.entityType, op.entitlement = dbOp.Type.Permission()
	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})

	if s != nil {
		op.SetEventServer(s.Events)
	}

	op.resources = nil
	op.metadata = nil

	runHook, ok := durableOperations[op.dbOpType]
	if !ok {
		return nil, fmt.Errorf("No durable operation handlers defined for operation type %d (%q)", op.dbOpType, op.dbOpType.Description())
	}

	op.onRun = runHook

	return &op, nil
}
