//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

func updateDBNodeID(op *Operation) error {
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load the database entry and make sure it's the same operation.
		existingOpFilter := cluster.OperationFilter{Reference: &op.id}
		existingOps, err := cluster.GetOperations(ctx, tx.Tx(), existingOpFilter)
		if err != nil {
			return fmt.Errorf("Failed checking for existing operation with reference %q: %w", op.id, err)
		}

		switch len(existingOps) {
		case 0:
			return fmt.Errorf("Operation %q not found in the DB", op.id)
		case 1:
			// Existing operation found.
			existingOp := existingOps[0]

			if api.StatusCode(existingOp.Status).IsFinal() {
				return fmt.Errorf("Cannot restart finalized operation with reference %q", op.id)
			}

			// If it's a running durable operation, we'll just update its node ID.
			if existingOp.Class == int64(OperationClassDurable) &&
				existingOp.Class == int64(op.class) &&
				existingOp.Type == op.dbOpType {
				err = cluster.UpdateOperationNodeID(ctx, tx.Tx(), op.id, tx.GetNodeID(), time.Now())
				if err != nil {
					return fmt.Errorf("Failed updating existing operation %q node ID: %w", op.id, err)
				}

				return nil
			}

			// Else it's an existing non-durable operation, we can't proceed.
			return api.StatusErrorf(http.StatusConflict, "Another operation with reference %q already exists", op.id)
		default:
			return fmt.Errorf("Multiple existing operations found with reference %q", op.id)
		}
	})
	if err != nil {
		return fmt.Errorf("Failed updating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return err
}

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
					permissionEntityType, _ := op.dbOpType.Permission()
					if entityReference.EntityType != cluster.EntityType(permissionEntityType) {
						return fmt.Errorf("Mismatched entity type %q for resource URL %q, expected %q", entityReference.EntityType, entityURLs[0].String(), permissionEntityType)
					}

					opInfo.EntityID = &entityReference.EntityID
				}
			}
		}

		opID, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
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

func updateDBOperationMetadata(op *Operation) error {
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.OperationFilter{Reference: &op.id}
		dbOps, err := cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed querying operation %s: %w", op.id, err)
		}

		if len(dbOps) != 1 {
			return fmt.Errorf("Operation %s not found in database", op.id)
		}

		err = cluster.CreateOrInsertDurableOperationMetadata(ctx, tx.Tx(), dbOps[0].ID, op.metadata)
		if err != nil {
			return fmt.Errorf("Failed adding operation %s metadata to database: %w", op.id, err)
		}

		op.updatedAt = time.Now()
		return cluster.UpdateOperationUpdatedAt(ctx, tx.Tx(), op.id, op.updatedAt)
	})
	if err != nil {
		return fmt.Errorf("Failed adding operation %s metadata to database: %w", op.id, err)
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

	// Load the requestor identity if provided.
	if dbOp.RequestorIdentityID != nil {
		identityFilter := cluster.IdentityFilter{ID: dbOp.RequestorIdentityID}
		clusterIdentities, err := cluster.GetIdentitys(ctx, tx, identityFilter)
		if err != nil {
			return nil, fmt.Errorf("Failed loading identity for operation %d: %w", dbOp.ID, err)
		}

		if len(clusterIdentities) != 1 {
			return nil, fmt.Errorf("Unexpected number of identities (%d) found for id %d", len(clusterIdentities), dbOp.ID)
		}

		// We need to construct the requestor object to hold the identity information.
		// The standard way is to construct it through the http request, but in this case we have no real http request.
		// We'll just use an empty one temporarily.
		r := &http.Request{}
		args := request.RequestorArgs{
			Trusted:  true,
			Username: clusterIdentities[0].Identifier,
			Protocol: dbOp.RequestorProtocol,
		}

		err = request.SetRequestor(r, requestorHook, args)
		if err != nil {
			return nil, fmt.Errorf("Failed setting requestor for operation %d: %w", dbOp.ID, err)
		}

		op.requestor, err = request.GetRequestor(r.Context())
		if err != nil {
			return nil, fmt.Errorf("Failed constructing requestor for operation %d: %w", dbOp.ID, err)
		}
	}

	runHook, ok := durableOperations[op.dbOpType]
	if !ok {
		return nil, fmt.Errorf("No durable operation handlers defined for operation type %d (%q)", op.dbOpType, op.dbOpType.Description())
	}

	op.onRun = runHook

	return &op, nil
}

// loadDurableOperationFromDB reloads a durable operation from the database based on its Reference.
// This is only used to ease debugging to ensure that the reloaded operation is identical to the one originally created.
func loadDurableOperationFromDB(op *Operation) (*Operation, error) {
	var result *Operation
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.OperationFilter{Reference: &op.id}
		dbOps, err := cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading operation %q from database: %w", op.id, err)
		}

		if len(dbOps) != 1 {
			return fmt.Errorf("Operation %q not found in database", op.id)
		}

		dbOp := dbOps[0]
		projectName := ""
		if dbOp.ProjectID != nil {
			projectID := (int)(*dbOp.ProjectID)
			filter := cluster.ProjectFilter{ID: &projectID}
			projects, err := cluster.GetProjects(ctx, tx.Tx(), filter)
			if err != nil {
				return fmt.Errorf("Failed loading project for operation %q from database: %w", op.id, err)
			}

			if len(projects) != 1 {
				return fmt.Errorf("Project ID %d for operation %q not found in database", *dbOp.ProjectID, op.id)
			}

			projectName = projects[0].Name
		}

		op, err := NewDurableOperation(ctx, tx.Tx(), op.state, &dbOp, projectName)
		if err != nil {
			return fmt.Errorf("Failed constructing operation %q from database: %w", op.id, err)
		}

		result = op
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// PruneExpiredDurableOperations deletes database entries of all durable operations which finished more than 24 hours ago.
func PruneExpiredDurableOperations(ctx context.Context, s *state.State) error {
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		durableClass := (int64)(OperationClassDurable)
		filter := cluster.OperationFilter{Class: &durableClass}
		dbOps, err := cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading durable operations: %w", err)
		}

		for _, dbOp := range dbOps {
			if !api.StatusCode(dbOp.Status).IsFinal() {
				continue
			}

			if dbOp.UpdatedAt.Add(24 * time.Hour).After(time.Now()) {
				continue
			}

			err = cluster.DeleteOperation(ctx, tx.Tx(), dbOp.Reference)
			if err != nil {
				return fmt.Errorf("Failed deleting expired durable operation: %w", err)
			}

			logger.Info("Pruned expired durable operation", logger.Ctx{"operation": dbOp.Reference})
		}

		return nil
	})
	if err != nil {
		return err
	}

	logger.Debug("Done pruning expired durable operations")

	return nil
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

// RestartDurableOperation creates and starts a new durable operation based on the provided Operation object.
// This is used to restart operations that were running on a node which failed to respond to heartbeats on other node.
func RestartDurableOperation(s *state.State, op *Operation) {
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
		Inputs:      op.inputs,
	}

	createdOp, err := initOperation(s, op.requestor, args)
	if err != nil {
		logger.Warn("Failed creating durable operation", logger.Ctx{"err": err})
		return
	}

	// Fix the operation ID to match the original one.
	createdOp.id = op.id

	// Update the DB record to point to this node.
	err = updateDBNodeID(createdOp)
	if err != nil {
		logger.Warn("Failed creating durable operation", logger.Ctx{"err": err})
		return
	}

	operationsLock.Lock()
	operations[createdOp.id] = createdOp
	operationsLock.Unlock()

	err = createdOp.Start()
	if err != nil {
		logger.Warn("Failed starting durable operation", logger.Ctx{"err": err})
		return
	}

	op.logger.Debug("Durable operation restarted", logger.Ctx{"id": op.id})
}

// RestartDurableOperationsFromNode restarts all durable operations that were running on the node
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	operations, err := GetDurableOperationsOnNode(ctx, s, nodeID)
	if err != nil {
		return err
	}

	for _, op := range operations {
		RestartDurableOperation(s, op)
	}

	return nil
}
