package locking

import (
	"fmt"
	"sync"
)

// ongoingOperationMap is a hashmap that allows functions to check whether the
// operation they are about to perform is already in progress. If it is the
// channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the
// hashmap.
// Note that any access to this map must be done while holding a lock.
var ongoingOperationMap = map[string]chan struct{}{}

// ongoingOperationMapLock is used to access ongoingOperationMap.
var ongoingOperationMapLock sync.Mutex

// Lock creates a lock for a specific storage volume to allow activities that
// require exclusive access to take place. Will block until the lock is
// established. On success, it returns an unlock function which needs to be
// called to unlock the lock.
func Lock(poolName string, volType string, volName string) func() {
	lockID := fmt.Sprintf("%s/%s/%s", poolName, volType, volName)

	for {
		// Get exclusive access to the map and see if there is already an operation ongoing.
		ongoingOperationMapLock.Lock()
		waitCh, ok := ongoingOperationMap[lockID]

		if !ok {
			// No ongoing operation, create a new channel to indicate our new operation.
			waitCh = make(chan struct{})
			ongoingOperationMap[lockID] = waitCh
			ongoingOperationMapLock.Unlock()

			// Return a function that will complete the operation.
			return func() {
				// Get exclusive access to the map.
				ongoingOperationMapLock.Lock()
				doneCh, ok := ongoingOperationMap[lockID]

				// Load our existing operation.
				if ok {
					// Close the channel to indicate to other waiting users
					// they can now try again to create a new operation.
					close(doneCh)

					// Remove our existing operation entry from the map.
					delete(ongoingOperationMap, lockID)
				}

				// Release the lock now that the done channel is closed and the
				// map entry has been deleted, this will allow any waiting users
				// to try and get access to the map to create a new operation.
				ongoingOperationMapLock.Unlock()
			}
		}

		// An existing operation is ongoing, lets wait for that to finish and then try
		// to get exlusive access to create a new operation again.
		ongoingOperationMapLock.Unlock()
		<-waitCh
	}
}
