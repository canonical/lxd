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
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

func registerOperation(ctx context.Context, tx *db.ClusterTx, op *Operation, conflictReference string, parentOpID *int64) (int64, error) {
	// If a conflict reference is provided, check if any conflicting operation is already running before creating the new operation record.
	if op.dbOpType.ConflictAction() == operationtype.ConflictActionFail && conflictReference != "" {
		conflict, err := conflictingOperationExists(ctx, tx, conflictReference)
		if err != nil {
			return 0, err
		}

		if conflict {
			return 0, fmt.Errorf("Conflicting operation with conflict reference %q already exists", conflictReference)
		}
	}

	opInfo := cluster.Operation{
		UUID:      op.id,
		Type:      op.dbOpType,
		NodeID:    tx.GetNodeID(),
		Class:     (int64)(op.class),
		CreatedAt: op.createdAt,
		UpdatedAt: op.updatedAt,
		Status:    int64(op.Status()),
		Parent:    parentOpID,
	}

	if op.projectName != "" {
		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
		if err != nil {
			return 0, fmt.Errorf("Fetch project ID: %w", err)
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
		return 0, fmt.Errorf("Failed marshalling operation inputs: %w", err)
	}

	opInfo.Inputs = string(inputsJSON)

	metadataJSON, err := json.Marshal(op.metadata)
	if err != nil {
		return 0, fmt.Errorf("Failed marshalling operation metadata: %w", err)
	}

	opInfo.Metadata = string(metadataJSON)

	dbOpID, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
	if err != nil {
		return 0, err
	}

	if op.Class() == OperationClassDurable {
		err = cluster.CreateOperationResources(ctx, tx.Tx(), dbOpID, op.resources)
		if err != nil {
			return 0, err
		}
	}

	return dbOpID, nil
}

func registerDBOperation(op *Operation, conflictReference string) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create parent operation record.
		parentOpID, err := registerOperation(ctx, tx, op, conflictReference, nil)
		if err != nil {
			return err
		}

		for _, childOp := range op.children {
			_, err := registerOperation(ctx, tx, childOp, conflictReference, &parentOpID)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return nil
}

func updateDBOperation(ctx context.Context, op *Operation) error {
	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, op.updatedAt, op.status, string(metadataJSON), op.err)
	})
	if err != nil {
		return fmt.Errorf("Failed updating operation %q record: %w", op.id, err)
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

func conflictingOperationExists(ctx context.Context, tx *db.ClusterTx, conflictReference string) (bool, error) {
	var ops []cluster.Operation
	filter := cluster.OperationFilter{ConflictReference: &conflictReference}
	var err error
	ops, err = cluster.GetOperations(ctx, tx.Tx(), filter)
	if err != nil {
		return false, fmt.Errorf("Failed fetching operations with conflict reference %q: %w", conflictReference, err)
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

// NewDurableOperation is a constructor of a single Operation object based on its database representation.
// It is used when restarting durable operations on a different node, or when loading durable operations to display them in the API or CLI.
// NewDurableOperation doesn't populate the parent field, as that would require loading all other operations from the DB. Instead,
// the caller is expected to set the parent field on the returned Operation object based on other loaded operations, if needed.
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

// LoadDurableOperationsFromNode returns all durable operations from the db that exist on given node.
func LoadDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) ([]*Operation, error) {
	var dbOps []cluster.Operation
	opsMap := map[int64]struct {
		op   *Operation
		dbOp *cluster.Operation
	}{}

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjectIDsToNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading project IDs to names: %w", err)
		}

		// See if there are any durable operations running on this node which need to be restarted.
		durableClass := (int64)(OperationClassDurable)
		filter := cluster.OperationFilter{NodeID: &nodeID, Class: &durableClass}
		dbOps, err = cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading durable operations for node ID %d: %w", nodeID, err)
		}

		// We'll put both the operations.Operations and the DB operations in a map keyed by their DB ID.
		// This is needed to set the parent-child relationships between operations.
		for _, dbOp := range dbOps {
			// Don't restart operations which are already in a final state.
			if api.StatusCode(dbOp.Status).IsFinal() {
				continue
			}

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

			// Update the operation nodeID to this node if needed.
			if dbOp.NodeID != tx.GetNodeID() {
				dbOp.NodeID = tx.GetNodeID()
				dbOp.UpdatedAt = time.Now()
				err = cluster.UpdateOperationNodeID(ctx, tx.Tx(), dbOp.UUID, dbOp.NodeID, dbOp.UpdatedAt)
				if err != nil {
					return fmt.Errorf("Failed updating existing operation %q node ID: %w", dbOp.UUID, err)
				}
			}

			// Create an Operation object for each DB entry.
			op, err := NewDurableOperation(ctx, tx.Tx(), s, &dbOp, projectName)
			if err != nil {
				return fmt.Errorf("Failed creating durable operation from DB entry: %w", err)
			}

			opsMap[dbOp.ID] = struct {
				op   *Operation
				dbOp *cluster.Operation
			}{op: op, dbOp: &dbOp}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Now that we have all operations created, we can set the parent-child relationships.
	var result []*Operation
	for _, opPair := range opsMap {
		// If the operation has no parent, we'll return it as a top-level operation.
		if opPair.dbOp.Parent == nil {
			result = append(result, opPair.op)
			continue
		}

		// Otherwise find the parent operation.
		parentOpPair, ok := opsMap[*opPair.dbOp.Parent]
		if !ok {
			logger.Warn("Parent operation not found in the map of operations when setting parent-child relationships", logger.Ctx{"operationID": opPair.dbOp.UUID, "parentID": *opPair.dbOp.Parent})
			continue
		}

		// And set the parent-child relationship.
		opPair.op.parent = parentOpPair.op
		if parentOpPair.op.children == nil {
			parentOpPair.op.children = []*Operation{}
		}

		parentOpPair.op.children = append(parentOpPair.op.children, opPair.op)
	}

	// Return values of the operations map as a slice.
	return result, nil
}

// RestartDurableOperation creates and starts a new durable operation based on the provided Operation object.
// This is used to restart operations that were running on a node which failed to respond to heartbeats on other node.
func RestartDurableOperation(s *state.State, op *Operation) {
	// Don't restart operations which are already in a final state.
	if op.Status().IsFinal() {
		return
	}

	operationsLock.Lock()
	operations[op.id] = op
	for _, childOp := range op.children {
		operations[childOp.id] = childOp
	}

	operationsLock.Unlock()

	err := op.Start()
	if err != nil {
		logger.Warn("Failed starting durable operation", logger.Ctx{"err": err})
		return
	}

	op.logger.Debug("Durable operation restarted", logger.Ctx{"id": op.id})
}

// RestartDurableOperationsFromNode restarts all durable operations that were running on the node
// which failed to respond to heartbeats.
func RestartDurableOperationsFromNode(ctx context.Context, s *state.State, nodeID int64) error {
	operations, err := LoadDurableOperationsFromNode(ctx, s, nodeID)
	if err != nil {
		return err
	}

	for _, op := range operations {
		RestartDurableOperation(s, op)
	}

	return nil
}
