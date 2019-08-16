package storage

import (
	"fmt"
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

// The following functions are used to construct simple operation codes that are
// unique.
func getPoolMountLockID(poolName string) string {
	return fmt.Sprintf("mount/pool/%s", poolName)
}

func getPoolUmountLockID(poolName string) string {
	return fmt.Sprintf("umount/pool/%s", poolName)
}

func getContainerMountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("mount/container/%s/%s", poolName, containerName)
}

func getContainerUmountLockID(poolName string, containerName string) string {
	return fmt.Sprintf("umount/container/%s/%s", poolName, containerName)
}

func getCustomMountLockID(poolName string, volumeName string) string {
	return fmt.Sprintf("mount/custom/%s/%s", poolName, volumeName)
}

func getCustomUmountLockID(poolName string, volumeName string) string {
	return fmt.Sprintf("umount/custom/%s/%s", poolName, volumeName)
}

func getImageCreateLockID(poolName string, fingerprint string) string {
	return fmt.Sprintf("create/image/%s/%s", poolName, fingerprint)
}

// LockPoolMount creates a lock on the given pool for a mount operation,
// and returns an unlock function which is to be called by the caller.
func LockPoolMount(poolName string) func() {
	return lock(getPoolMountLockID(poolName))
}

// LockPoolUmount creates a lock on the given pool for a umount operation,
// and returns an unlock function which is to be called by the caller.
func LockPoolUmount(poolName string) func() {
	return lock(getPoolUmountLockID(poolName))
}

// LockContainerMount creates a lock on the given container for a mount operation,
// and returns an unlock function which is to be called by the caller.
func LockContainerMount(poolName string, containerName string) func() {
	return lock(getContainerMountLockID(poolName, containerName))
}

// LockContainerUmount creates a lock on the given container for a umount operation,
// and returns an unlock function which is to be called by the caller.
func LockContainerUmount(poolName string, containerName string) func() {
	return lock(getContainerUmountLockID(poolName, containerName))
}

// LockCustomMount creates a lock on the given custom volume for a mount operation,
// and returns an unlock function which is to be called by the caller.
func LockCustomMount(poolName string, volumeName string) func() {
	return lock(getCustomMountLockID(poolName, volumeName))
}

// LockCustomUmount creates a lock on the given custom volume for a umount operation,
// and returns an unlock function which is to be called by the caller.
func LockCustomUmount(poolName string, volumeName string) func() {
	return lock(getCustomUmountLockID(poolName, volumeName))
}

// LockImageCreate creates a lock on the given image for a create operation,
// and returns an unlock function which is to be called by the caller.
func LockImageCreate(poolName string, fingerprint string) func() {
	return lock(getImageCreateLockID(poolName, fingerprint))
}

func lock(lockID string) func() {
	lxdStorageMapLock.Lock()

	if waitChannel, ok := lxdStorageOngoingOperationMap[lockID]; ok {
		lxdStorageMapLock.Unlock()

		_, ok := <-waitChannel
		if ok {
			logger.Warnf("Received value over semaphore, this should not have happened")
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
