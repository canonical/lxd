//go:build linux && cgo && !agent

package operations

import (
	"context"
	"database/sql"
	"encoding/json"
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
		existingOpFilter := cluster.OperationFilter{UUID: &op.id}
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
			UUID:      op.id,
			Type:      op.dbOpType,
			NodeID:    tx.GetNodeID(),
			Class:     (int64)(op.class),
			CreatedAt: op.createdAt,
			UpdatedAt: op.updatedAt,
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
			value := cluster.RequestorProtocol(op.requestor.CallerProtocol())
			opInfo.RequestorProtocol = &value

			requestorCallerIdentityID := op.requestor.CallerIdentityID()
			if requestorCallerIdentityID != 0 {
				identityID := int64(requestorCallerIdentityID)
				opInfo.RequestorIdentityID = &identityID
			}
		}

		inputsJSON, err := json.Marshal(op.inputs)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation inputs: %w", err)
		}

		opInfo.Inputs = string(inputsJSON)

		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		opInfo.Metadata = string(metadataJSON)

		dbOpID, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
		if err != nil {
			return err
		}

		if op.Class() == OperationClassDurable {
			opInfo.EntityID, err = cluster.CreateOperationResources(ctx, tx.Tx(), dbOpID, op.resources)
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return nil
}

func updateDBOperationStatus(op *Operation) error {
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		opErr := ""
		if op.err != nil {
			opErr = op.err.Error()
		}

		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		return cluster.UpdateOperationStatus(ctx, tx.Tx(), op.id, op.updatedAt, op.status, string(metadataJSON), opErr)
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

func conflictingOperationExists(op *Operation, conflictReference string) (bool, error) {
	var ops []cluster.Operation
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.OperationFilter{ConflictReference: &conflictReference}
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
		return nil, fmt.Errorf("Operation %s is not of durable class", dbOp.UUID)
	}

	op := Operation{
		projectName: projectName,
		id:          dbOp.UUID,
		class:       OperationClass(dbOp.Class),
		createdAt:   dbOp.CreatedAt,
		updatedAt:   dbOp.CreatedAt,
		status:      api.StatusCode(dbOp.Status),
		url:         api.NewURL().Path(version.APIVersion, "operations", dbOp.UUID).String(),
		description: dbOp.Type.Description(),
		dbOpType:    dbOp.Type,
		finished:    cancel.New(),
		running:     cancel.New(),
		state:       s,
	}

	if dbOp.Error != "" {
		op.err = errors.New(dbOp.Error)
	}

	op.entityType, op.entitlement = dbOp.Type.Permission()
	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})

	if s != nil {
		op.SetEventServer(s.Events)
	}

	// Load operation inputs.
	var inputs map[string]any
	var err error
	err = json.Unmarshal([]byte(dbOp.Inputs), &inputs)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation inputs for operation %d: %w", dbOp.ID, err)
	}

	op.inputs = inputs

	// Load operation resources.
	op.resources, err = cluster.GetOperationResources(ctx, tx, dbOp.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed loading operation resources for operation %d: %w", dbOp.ID, err)
	}

	// Load operation metadata.
	var metadata map[string]any
	err = json.Unmarshal([]byte(dbOp.Metadata), &metadata)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling operation metadata for operation %d: %w", dbOp.ID, err)
	}

	op.metadata = metadata

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
		protocol := ""
		if dbOp.RequestorProtocol != nil {
			protocol = string(*dbOp.RequestorProtocol)
		}

		args := request.RequestorArgs{
			Trusted:  true,
			Username: clusterIdentities[0].Identifier,
			Protocol: protocol,
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

// loadDurableOperationFromDB reloads a durable operation from the database based on its UUID.
// This is only used to ease debugging to ensure that the reloaded operation is identical to the one originally created.
func loadDurableOperationFromDB(op *Operation) (*Operation, error) {
	var result *Operation
	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.OperationFilter{UUID: &op.id}
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
