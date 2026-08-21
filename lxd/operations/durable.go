package operations

import (
	"sync"

	"github.com/canonical/lxd/lxd/db/operationtype"
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
