package locking

import (
	"context"
	"fmt"
	"sync"
)

// locks is a map that allows functions to check whether the operation they are about to perform is already in progress.
// If it is the channel can be used to wait for the operation to finish.
// If it is not, the function that wants to perform the operation stores a new channel in the map.
// Note that any access to this map must be done while holding a lock.
var locks = map[string]chan struct{}{}

// friendlyLocks is a map that allows functions to check whether the operation they are about to perform
// is already in progress and can be skipped because the actual owner of the lock has given the permission.
// Note that any access to this map must be done while holding a lock.
var friendlyLocks = map[string]chan struct{}{}

// locksMutex is used to access locks safely.
var locksMutex sync.Mutex

// UnlockFunc unlocks the lock.
type UnlockFunc func()

// Lock creates a named lock to allow activities that require exclusive access to occur.
// Will block until the lock is established or the context is cancelled.
// On successfully acquiring the lock, it returns an unlock function which needs to be called to unlock the lock.
// If the context is canceled then nil will be returned.
func Lock(ctx context.Context, lockName string) (UnlockFunc, error) {
	for {
		// Get exclusive access to the map and see if there is already an operation ongoing.
		locksMutex.Lock()
		waitCh, ok := locks[lockName]

		if !ok {
			// No ongoing operation, create a new channel to indicate our new operation.
			waitCh = make(chan struct{})
			locks[lockName] = waitCh
			locksMutex.Unlock()

			// Return a function that will complete the operation.
			return func() {
				// Get exclusive access to the map.
				locksMutex.Lock()
				doneCh, ok := locks[lockName]

				// Load our existing operation.
				if ok {
					// Close the channel to indicate to other waiting users
					// they can now try again to create a new operation.
					close(doneCh)

					// Remove our existing operation entry from the map.
					delete(locks, lockName)
				}

				// Release the lock now that the done channel is closed and the
				// map entry has been deleted, this will allow any waiting users
				// to try and get access to the map to create a new operation.
				locksMutex.Unlock()
			}, nil
		}

		// An existing operation is ongoing, lets wait for that to finish and then try
		// to get exlusive access to create a new operation again.
		locksMutex.Unlock()

		select {
		case <-waitCh:
			continue
		case <-ctx.Done():
			return nil, fmt.Errorf("Failed to obtain lock %q: %w", lockName, ctx.Err())
		}
	}
}

// LockFriendly creates a named lock (and corresponding friendly lock) to allow activities that require exclusive access to occur.
// Unlike Lock it allows subsequent callers to be unblocked early ("friendly") if permitted by the current owner of the lock.
// In this case no unlock functions are returned as the lock's management is exclusively maintained by the current owner of the lock.
// In addition the returned bool is set to true.
// LockFriendly blocks until the lock is established, the friendly lock is released or the context is cancelled.
// On successfully acquiring the lock, it returns an unlock function which needs to be called to unlock the lock.
// Additionally an unlockFirendly function is returned which can be used to prematurely ("friendly") unblock subsequent callers.
// In such cases the returned bool is set to false.
// If the context is canceled then no unlock function is returned either.
func LockFriendly(ctx context.Context, lockName string) (friendly bool, unlock UnlockFunc, unlockFriendly UnlockFunc, err error) {
	for {
		// Get exclusive access to the map.
		locksMutex.Lock()

		// See if there already is a friendly lock channel.
		// Create one if it doesn't yet exist.
		friendlyWaitCh, ok := friendlyLocks[lockName]
		if !ok {
			friendlyLocks[lockName] = make(chan struct{})
		}

		// See if there already is an operation ongoing.
		waitCh, ok := locks[lockName]
		if !ok {
			// No ongoing operation, create a new channel to indicate our new operation.
			waitCh = make(chan struct{})
			locks[lockName] = waitCh
			locksMutex.Unlock()

			// Ensure the friendly lock's channel is never closed twice.
			// This might happen in case the friendly unlock is fired (which closes the channel)
			// and sometime after the actual lock is released which also performs cleanups.
			var closeOnce sync.Once

			// Function that will complete the operation.
			unlock := func() {
				// Get exclusive access to the map.
				locksMutex.Lock()

				friendlyDoneCh, ok := friendlyLocks[lockName]
				if ok {
					// Cleanup the friendly lock.
					// The friendly unlock might have only closed the channel.
					closeOnce.Do(func() {
						close(friendlyDoneCh)
					})

					delete(friendlyLocks, lockName)
				}

				doneCh, ok := locks[lockName]

				// Load our existing operation.
				if ok {
					// Close the channel to indicate to other waiting users
					// they can now try again to create a new operation.
					close(doneCh)

					// Remove our existing operation entry from the map.
					delete(locks, lockName)
				}

				// Release the lock now that the done channel is closed and the
				// map entry has been deleted, this will allow any waiting users
				// to try and get access to the map to create a new operation.
				locksMutex.Unlock()
			}

			// Function that will perform a friendly unlock.
			friendlyUnlock := func() {
				locksMutex.Lock()
				friendlyDoneCh, ok := friendlyLocks[lockName]

				if ok {
					// Close the channel to friendly indicate to other waiting users
					// they can proceed because the current owner of the lock has given permission.
					// Don't yet delete the channel to allow new callers to also make
					// use of the friendly lock.
					// The channel gets deleted when unlocking the non-friendly lock.
					closeOnce.Do(func() {
						close(friendlyDoneCh)
					})
				}

				locksMutex.Unlock()
			}

			return false, unlock, friendlyUnlock, nil
		}

		// An existing operation is ongoing, lets wait for that to finish and then try
		// to get exlusive access to create a new operation again.
		locksMutex.Unlock()

		select {
		case <-waitCh:
			continue
		case <-friendlyWaitCh:
			return true, nil, nil, nil
		case <-ctx.Done():
			return false, nil, nil, fmt.Errorf("Failed to obtain friendly lock %q: %w", lockName, ctx.Err())
		}
	}
}
