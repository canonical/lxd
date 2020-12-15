package operationlock

import (
	"fmt"
	"sync"
	"time"

	"github.com/lxc/lxd/shared"
)

var instanceOperationsLock sync.Mutex
var instanceOperations = make(map[int]*InstanceOperation)

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
			// Reset operation timeout without releasing lock or deadlocking using Reset() function.
			op.chanReset <- true
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

	instanceOperations[instanceID] = op

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

	return op, nil
}

// CreateWaitGet is a weird function which does what we happen to want most of the time.
//
// If the instance has an operation of the same type and it's not reusable
// or the caller doesn't want to reuse it, the function will wait and
// indicate that it did so.
//
// If the instance has an operation of one of the alternate types, then
// the operation is returned to the user.
//
// If the instance doesn't have an operation, has an operation of a different
// type that is not in the alternate list or has the right type and is
// being reused, then this behaves as a Create call.
func CreateWaitGet(instanceID int, action string, altActions []string, reusable bool, reuse bool) (bool, *InstanceOperation, error) {
	op := Get(instanceID)

	// No existing operation, call create.
	if op == nil {
		op, err := Create(instanceID, action, reusable, reuse)
		return false, op, err
	}

	// Operation matches and not reusable or asked to reuse, wait.
	if op.action == action && (!reuse || !op.reusable) {
		err := op.Wait()
		return true, nil, err
	}

	// Operation matches one the alternate actions, return the operation.
	if shared.StringInSlice(op.action, altActions) {
		return false, op, nil
	}

	// Send the rest to Create
	op, err := Create(instanceID, action, reusable, reuse)
	return false, op, err
}

// Get retrieves an existing lock or returns nil if no lock exists.
func Get(instanceID int) *InstanceOperation {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	return instanceOperations[instanceID]
}

// Reset resets the operation timeout.
func (op *InstanceOperation) Reset() error {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	// This function can be called on a nil struct.
	if op == nil {
		return nil
	}

	// Check if already done
	runningOp, ok := instanceOperations[op.id]
	if !ok || runningOp != op {
		return fmt.Errorf("Operation is already done or expired")
	}

	op.chanReset <- true
	return nil
}

// Wait waits for an operation to finish.
func (op *InstanceOperation) Wait() error {
	// This function can be called on a nil struct.
	if op == nil {
		return nil
	}

	<-op.chanDone

	return op.err
}

// Done indicates the operation has finished.
func (op *InstanceOperation) Done(err error) {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	// This function can be called on a nil struct.
	if op == nil {
		return
	}

	// Check if already done
	runningOp, ok := instanceOperations[op.id]
	if !ok || runningOp != op {
		return
	}

	op.err = err
	close(op.chanDone)

	delete(instanceOperations, op.id)
}
