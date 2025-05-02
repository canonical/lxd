package connectors

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// connectFunc is invoked by "connect" for each provided address.
// It receives a session and a target address. A non-nil session indicates
// an existing session for the target.
//
// The function is responsible for establishing new connections or handling
// necessary actions for already connected target addresses.
type connectFunc func(ctx context.Context, s *session, addr string) error

// connect attempts to establish connections to all provided addresses,
// succeeding if at least one connection is successful.
//
// If all connection attempts fail, an error is returned, and the function
// ensures the session is cleanup if one was created during this call.
//
// IMPORTANT:
// If at least one connection succeeds, no error is returned. In this case,
// the caller is responsible for disconnection by calling "connectors.Disconnect"
// when safe. The returned reverter will only cancel ongoing connection attempts
// but will **not** attempt disconnection.
func connect(ctx context.Context, c Connector, targetQN string, targetAddrs []string, connectFunc connectFunc) (revert.Hook, error) {
	reverter := revert.New()
	defer reverter.Fail()

	var cancel context.CancelFunc
	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 30 seconds for the context
		// if no deadline is already configured.
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	} else {
		// Otherwise, wrap the context to allow manual cancellation.
		ctx, cancel = context.WithCancel(ctx)
	}

	// Add the context cancellation to the reverter.
	// In case the caller never starts any connection attempts and returns early
	// the derived context has to be cancelled too to not leak the deadline's timer.
	reverter.Add(func() { cancel() })

	// Acquire a lock to prevent concurrent connection attempts to the same
	// target.
	//
	// The unlock is not deferred here because it must remain held until all
	// connection attempts are complete. Releasing the lock prematurely after
	// the first successful connection (when this function exits) could lead
	// to race conditions if other connection attempts are still ongoing.
	// For the same reason, relying on a higher-level lock from the caller
	// (e.g., the storage driver) is insufficient.
	var unlock locking.UnlockFunc
	for {
		unlock = locking.TryLock(targetQN)
		if unlock == nil {
			// The lock is already taken, which means some other routine is trying
			// to connect to the same target.
			// Therefore, search for an existing session and check if we are already connected to any address.
			session, err := c.findSession(targetQN)
			if err != nil {
				return nil, err
			}

			// Check if there already is a single established connection for the given targetQN.
			// In this case skip our attempt to connect and return early because the other routine is actively trying
			// to connect at the moment.
			// This prevents waiting until a connection has been established with all the target addresses.
			// A single connection is already sufficient and the behavior is identical to the other routine
			// which will return after the first connection attempt was successful (without releasing the lock yet).
			// If there isn't yet any established connection, we will continue to wait for the lock.
			if session != nil && len(session.addresses) > 0 {
				// There is at least one established connection, return early.
				// Return an empty cleanup hook instead of nil to not cause a panic if the caller decides to run the cleanup hook.
				// If a function returns a cleanup hook it's added to the callers reverter without checking for its actual value.
				return func() {}, nil
			}

			select {
			case <-ctx.Done():
				return nil, errors.New("Failed to find any session whilst trying to connect")
			default:
				// Sleep a while before trying to acquire the lock another time.
				time.Sleep(500 * time.Millisecond)

				continue
			}
		}

		// The lock was acquired, break the loop and continue to establish connections.
		break
	}

	// Once the lock is obtained, search for an existing session.
	session, err := c.findSession(targetQN)
	if err != nil {
		return nil, err
	}

	// Clean the reverter to remove the context's cancellation.
	// Context cancellation is not deferred by default to allow connection attempts to
	// continue after the first successful connection (which causes the connect function
	// to exit). The context is manually cancelled once all attempts complete.
	reverter.Success()

	var wg sync.WaitGroup
	resChan := make(chan bool, len(targetAddrs))

	var successLock sync.Mutex
	isSuccess := false

	go func() {
		// Connect to all target addresses.
		for _, addr := range targetAddrs {
			wg.Add(1)

			go func(addr string) {
				defer wg.Done()

				err := connectFunc(ctx, session, addr)
				if err != nil {
					// Log warning for each failed connection attempt.
					logger.Warn("Failed connecting to target", logger.Ctx{"target_qualified_name": targetQN, "target_address": addr, "err": err})
				} else {
					successLock.Lock()
					isSuccess = true
					successLock.Unlock()
				}

				resChan <- (err == nil)
			}(addr)
		}

		// Wait for all connection attempts to complete.
		wg.Wait()

		// Cleanup.
		close(resChan)
		cancel()

		// Ensure the session is removed if no successful connection was
		// established and no session existed before.
		//
		// If at least one connection succeeded, the caller is responsible
		// for handling disconnection to avoid inadvertently disconnecting
		// subsequent operations that may have reused the session after
		// this function releases the lock. The lock being released is also
		// the reason why disconnect is not returned in the outer reverter.
		//
		// Additionally, do not disconnect a session that existed once
		// this function has obtained a lock. Even if no connection was
		// successful, retaining the session allows other devices using
		// it to recover. For example, the remote storage may have become
		// inaccessible due to power loss. Removing the session would prevent
		// existing devices from reconnecting once the remote storage becomes
		// accessible again.
		if !isSuccess && session == nil {
			_ = c.Disconnect(targetQN)
		}

		unlock()
	}()

	// Wait until either a successful connection is established
	// or all connection attempts fail.
	for success := range resChan {
		if success {
			// At least one connection succeeded.
			//
			// Return a reverter that cancels any ongoing connection
			// attempts and waits for them to complete.
			outerReverter := revert.New()
			outerReverter.Add(func() {
				cancel()
				wg.Wait()
			})

			return outerReverter.Fail, nil
		}
	}

	// All connections attempts have failed.
	return nil, fmt.Errorf("Failed to connect to any address on target %q", targetQN)
}
