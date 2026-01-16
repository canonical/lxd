package operations

import (
	"context"
	"sync"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/logger"
)

var durableOperationRunHooksMu sync.RWMutex
var durableOperationRunHooks = make(map[operationtype.Type]RunHook)

// RegisterDurableOperationRunHook registers a [RunHook] for the [operationtype.Type].
// Run hooks for the [operationtype.OperationClassDurable] operation class must be statically defined and registered in
// advance. They must also be idempotent. A durable operation can be restarted on another cluster member if the cluster
// member that is running the operation is deemed offline (no heartbeat response for more than the cluster.offline_threshold).
func RegisterDurableOperationRunHook(operationType operationtype.Type, hook RunHook) {
	durableOperationRunHooksMu.Lock()
	durableOperationRunHooks[operationType] = hook
	durableOperationRunHooksMu.Unlock()
}

// getDurableOperationRunHook returns a [RunHook] for the given [operationtype.Type]. A boolean is also returned,
// indicating if the RunHook was present.
func getDurableOperationRunHook(operationType operationtype.Type) (RunHook, bool) {
	durableOperationRunHooksMu.RLock()
	hook, ok := durableOperationRunHooks[operationType]
	durableOperationRunHooksMu.RUnlock()

	return hook, ok
}

// SyncDurableOperations synchronizes the in-memory operations map with the database.
// This is because it is possible for heartbeat replies to be lost before reaching the leader node.
// In such case the node will continue running the durable operations (because it's receiving the heartbeats),
// but the leader will also restart those operations (because it's not receiving the heartbeats).
// To avoid this, we have a periodic task on each node checking that the node is doing what the database says.
// In other words, we cancel local tasks if these are not written in the database, and we start tasks which
// are in the database but are not running locally.
func SyncDurableOperations(ctx context.Context, s *state.State) {
	var err error
	var reconstructedLocalOps []*Operation
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbOps, err := dbCluster.GetOperationsByNodeIDAndClass(ctx, tx.Tx(), s.DB.Cluster.GetNodeID(), operationtype.OperationClassDurable)
		if err != nil {
			return err
		}

		reconstructedLocalOps, err = ConstructOperationsFromDB(ctx, tx.Tx(), s, dbOps)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warnf("Failed loading durable operations on node: %v", err)
		return
	}

	// Get the list of local durable operations.
	localOps := Clone()
	// Convert it to a map for easier lookup.
	localOpsMap := make(map[string]*Operation)
	for _, op := range localOps {
		if op.Class() != operationtype.OperationClassDurable {
			continue
		}

		localOpsMap[op.ID()] = op
	}

	// Ensure that all durable operations in the database are running locally.
	for _, op := range reconstructedLocalOps {
		if !op.IsRunning() {
			// Operation is in a final state, no need to create it.
			continue
		}

		// If the operation is already running locally, everything is great.
		_, ok := localOpsMap[op.ID()]
		if ok {
			continue
		}

		// Operation is not running locally, we need to restart it.
		logger.Warnf("Restarting durable operation %q", op.ID())
		restartOperation(op)
	}

	// Now we put the database operations in a map, and ensure that all local
	// durable operations are present in the database.
	reconstructedOpsMap := make(map[string]*Operation)
	for _, reconstructedLocalOp := range reconstructedLocalOps {
		reconstructedOpsMap[reconstructedLocalOp.ID()] = reconstructedLocalOp
		for _, child := range reconstructedLocalOp.Children() {
			reconstructedOpsMap[child.ID()] = child
		}
	}

	for _, op := range localOpsMap {
		if !op.IsRunning() {
			// Operation is in a final state, no need to cancel it.
			continue
		}

		// If the local operation is written in the DB, everything is great.
		_, ok := reconstructedOpsMap[op.ID()]
		if ok {
			continue
		}

		// Operation is not in the DB, we need to cancel it.
		cancelInternal(op)
	}
}
