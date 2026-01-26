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
