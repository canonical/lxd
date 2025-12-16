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
		opID, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return api.StatusErrorf(http.StatusConflict, "Another operation with reference %q already exists", op.id)
			}

			return err
		}

		// For durable operations we need to register metadata and resources in the database.
		if op.class == OperationClassDurable {
			err = cluster.CreateOrInsertDurableOperationMetadata(ctx, tx.Tx(), opID, op.metadata)
			if err != nil {
				return fmt.Errorf("Failed adding operation %s metadata to database: %w", op.id, err)
			}
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

	// Load the metadata of the durable operation.
	metadata, err := cluster.GetDurableOperationMetadata(ctx, tx, dbOp.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed loading durable operation metadata for operation %d: %w", dbOp.ID, err)
	}

	// We have to convert metadata from map[string]string to map[string]any.
	op.metadata = make(map[string]any)
	for k, v := range metadata {
		op.metadata[k] = v
	}

	runHook, ok := durableOperations[op.dbOpType]
	if !ok {
		return nil, fmt.Errorf("No durable operation handlers defined for operation type %d (%q)", op.dbOpType, op.dbOpType.Description())
	}

	op.onRun = runHook

	return &op, nil
}

// GetDurableOperationsOnNode returns all durable operations from the db that exist on given node.
func GetDurableOperationsOnNode(ctx context.Context, s *state.State, nodeID int64) ([]*Operation, error) {
	var result []*Operation

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjectIDsToNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading project IDs to names: %w", err)
		}

		// See if there are any durable operations running on this node which need to be restarted.
		durableClass := (int64)(OperationClassDurable)
		filter := cluster.OperationFilter{NodeID: &nodeID, Class: &durableClass}
		dbOps, err := cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading durable operations for node ID %d: %w", nodeID, err)
		}

		result = make([]*Operation, 0, len(dbOps))

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

			op, err := NewDurableOperation(ctx, tx.Tx(), s, &dbOp, projectName)
			if err != nil {
				return fmt.Errorf("Failed creating durable operation from DB entry: %w", err)
			}

			result = append(result, op)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// CreateDurableOperation creates and starts a new durable operation based on the provided Operation object.
// This is used to restart operations that were running on a node which failed to respond to heartbeats on other node.
func CreateDurableOperation(s *state.State, op *Operation) {
	// Don't restart operations which are already in a final state.
	if op.Status().IsFinal() {
		return
	}

	args := OperationArgs{
		ProjectName: op.projectName,
		Type:        op.dbOpType,
		Class:       op.class,
		Resources:   op.resources,
		Metadata:    op.metadata,
		Reference:   op.id,
		Inputs:      op.inputs,
	}

	var createdOp *Operation
	var err error
	if op.requestor != nil {
		createdOp, err = CreateUserOperation(s, op.requestor, args)
	} else {
		createdOp, err = CreateServerOperation(s, args)
	}

	if err != nil {
		logger.Warn("Failed creating durable operation", logger.Ctx{"err": err})
		return
	}

	err = createdOp.Start()
	if err != nil {
		logger.Warn("Failed starting durable operation", logger.Ctx{"err": err})
	}
}

// RestartDurableOperationsFromNode restarts all durable operations that were running on the node
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	operations, err := GetDurableOperationsOnNode(ctx, s, nodeID)
	if err != nil {
		return err
	}

	for _, op := range operations {
		CreateDurableOperation(s, op)
	}

	return nil
}
