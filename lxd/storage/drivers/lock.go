package drivers

import (
	"sync"

	"github.com/lxc/lxd/shared/logger"
)

// lxdStorageLockMap is a hashmap that allows functions to check whether the
// operation they are about to perform is already in progress. If it is the
// channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the
// hashmap.
// Note that any access to this map must be done while holding a lock.
var lxdStorageOngoingOperationMap = map[string]chan bool{}

// lxdStorageMapLock is used to access lxdStorageOngoingOperationMap.
var lxdStorageMapLock sync.Mutex

func lock(lockID string) func() {
	lxdStorageMapLock.Lock()

	if waitChannel, ok := lxdStorageOngoingOperationMap[lockID]; ok {
		lxdStorageMapLock.Unlock()

		_, ok := <-waitChannel
		if ok {
			logger.Warnf("Received value over semaphore, this should ot have happened")
		}

		// Give the benefit of the doubt and assume that the other
		// thread actually succeeded in mounting the storage pool.
		return nil
	}

	lxdStorageOngoingOperationMap[lockID] = make(chan bool)
	lxdStorageMapLock.Unlock()

	return func() {
		lxdStorageMapLock.Lock()

		waitChannel, ok := lxdStorageOngoingOperationMap[lockID]
		if ok {
			close(waitChannel)
			delete(lxdStorageOngoingOperationMap, lockID)
		}

		lxdStorageMapLock.Unlock()
	}
}
