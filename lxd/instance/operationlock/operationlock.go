package operationlock

import (
	"fmt"
	"sync"
	"time"
)

var instanceOperationsLock sync.Mutex
var instanceOperations map[int]*InstanceOperation = make(map[int]*InstanceOperation)

// InstanceOperation operation locking.
type InstanceOperation struct {
	action    string
	chanDone  chan error
	chanReset chan bool
	err       error
	id        int
	reusable  bool
}

// Action returns operation's action.
func (op InstanceOperation) Action() string {
	return op.action
}

// Create creates a new operation lock for an Instance if one does not already exist and returns it.
// The lock will be released after 30s or when Done() is called, which ever occurs first.
// If reusable is set as true then future lock attempts can specify the reuse argument as true which
// will then trigger a reset of the 30s timeout on the existing lock and return it.
func Create(instanceID int, action string, reusable bool, reuse bool) (*InstanceOperation, error) {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	op := instanceOperations[instanceID]
	if op != nil {
		if op.reusable && reuse {
			op.Reset()
			return op, nil
		}

		return nil, fmt.Errorf("Instance is busy running a %s operation", op.action)
	}

	op = &InstanceOperation{}
	op.id = instanceID
	op.action = action
	op.reusable = reusable
	op.chanDone = make(chan error, 0)
	op.chanReset = make(chan bool, 0)

	go func(op *InstanceOperation) {
		for {
			select {
			case <-op.chanReset:
				continue
			case <-time.After(time.Second * 30):
				op.Done(fmt.Errorf("Instance %s operation timed out after 30 seconds", op.action))
				return
			}
		}
	}(op)

	instanceOperations[instanceID] = op

	return op, nil
}

// Get retrieves an existing lock or returns nil if no lock exists.
func Get(instanceID int) *InstanceOperation {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	return instanceOperations[instanceID]
}

// Reset resets an operation.
func (op *InstanceOperation) Reset() error {
	if !op.reusable {
		return fmt.Errorf("Can't reset a non-reusable operation")
	}

	op.chanReset <- true
	return nil
}

// Wait waits for an operation to finish.
func (op *InstanceOperation) Wait() error {
	<-op.chanDone

	return op.err
}

// Done indicates the operation has finished.
func (op *InstanceOperation) Done(err error) {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	// Check if already done
	runningOp, ok := instanceOperations[op.id]
	if !ok || runningOp != op {
		return
	}

	op.err = err
	close(op.chanDone)

	delete(instanceOperations, op.id)
}
