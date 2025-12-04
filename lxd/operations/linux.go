//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

		// Durable operations support only up to a single resource. If there is one, verify and register its entity_id.
		if op.class == OperationClassDurable && len(op.resources) > 0 {
			for _, entityURLs := range op.resources {
				if len(entityURLs) > 0 {
					entityReference, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), &entityURLs[0])
					if err != nil {
						return fmt.Errorf("Failed getting entity ID from resource URL %q: %w", entityURLs[0].String(), err)
					}

					// The EntityType of the resource must be the same as the EntityType of the required permission defined on the type of the operation.
					// We don't store EntityType of the resource in the DB, instead, we just use the entityType as defined by the required permissions.
					permissionEntityType, _ := opType.Permission()
					if entityReference.EntityType != cluster.EntityType(permissionEntityType) {
						return fmt.Errorf("Mismatched entity type %q for resource URL %q, expected %q", entityReference.EntityType, entityURLs[0].String(), permissionEntityType)
					}

					opInfo.EntityID = &entityReference.EntityID
				}
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

func updateDBOperationNodeID(op *Operation) error {
	op.updatedAt = time.Now()

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := cluster.UpdateOperationNodeID(ctx, tx.Tx(), op.id, tx.GetNodeID(), op.updatedAt)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed updating Operation %s node ID in database: %w", op.id, err)
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

	// Load the resource URL if entity ID is provided.
	if dbOp.EntityID != nil {
		entityType, _ := dbOp.Type.Permission()
		entityURL, err := cluster.GetEntityURL(ctx, tx, entityType, *dbOp.EntityID)
		if err != nil {
			return nil, fmt.Errorf("Failed getting entity URL for entity type %q and ID %d: %w", entityType.String(), *dbOp.EntityID, err)
		}

		op.resources = map[string][]api.URL{string(entityType): {*entityURL}}
	}

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
		return nil, fmt.Errorf("No durable operation handlers defined for operation type %q", op.dbOpType)
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
			return fmt.Errorf("Failed loading durable operations for the node %d: %w", nodeID, err)
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

// RestartDurableOperationsFromNode restarts all durable operations that were running on the node
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	operations, err := GetDurableOperationsOnNode(ctx, s, nodeID)
	if err != nil {
		return err
	}

	for _, op := range operations {
		args := OperationArgs{
			ProjectName: op.projectName,
			Type:        op.dbOpType,
			Class:       op.class,
			Resources:   op.resources,
			Metadata:    op.metadata,
			Reference:   op.id,
		}

		createdOp, err := CreateServerOperation(s, args)
		if err != nil {
			logger.Warn("Failed creating durable operation", logger.Ctx{"err": err})
			continue
		}

		err = createdOp.Start()
		if err != nil {
			logger.Warn("Failed starting durable operation", logger.Ctx{"err": err})
		}
	}

	return nil
}
