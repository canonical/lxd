package connectors

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// devicePathFilterFunc is a function that accepts device path and returns true
// if the path matches the required criteria.
type devicePathFilterFunc func(devPath string) bool

// GetDiskDevicePath checks whether the disk device with a given prefix and suffix
// exists in /dev/disk/by-id directory. A device path is returned if the device is
// found, otherwise an error is returned.
func GetDiskDevicePath(diskNamePrefix string, diskPathFilter devicePathFilterFunc) (string, error) {
	devPath, err := findDiskDevicePath(diskNamePrefix, diskPathFilter)
	if err != nil {
		return "", err
	}

	if devPath == "" {
		return "", fmt.Errorf("Device not found")
	}

	return devPath, nil
}

// WaitDiskDevicePath waits for the disk device to appear in /dev/disk/by-id.
// It periodically checks for the device to appear and returns the device path
// once it is found. If the device does not appear within the timeout, an error
// is returned.
func WaitDiskDevicePath(ctx context.Context, diskNamePrefix string, diskPathFilter devicePathFilterFunc) (string, error) {
	var err error
	var diskPath string

	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 30 seconds for the context
		// if no deadline is already configured.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	for {
		// Check if the device is already present.
		diskPath, err = findDiskDevicePath(diskNamePrefix, diskPathFilter)
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return "", err
		}

		// If the device is found, return the device path.
		if diskPath != "" {
			break
		}

		// Check if context is cancelled.
		err := ctx.Err()
		if err != nil {
			return "", err
		}

		time.Sleep(500 * time.Millisecond)
	}

	return diskPath, nil
}

// findDiskDevivePath iterates over device names in /dev/disk/by-id directory and
// returns the path to the disk device that matches the given prefix and suffix.
// Disk partitions are skipped, and an error is returned if the device is not found.
func findDiskDevicePath(diskNamePrefix string, diskPathFilter devicePathFilterFunc) (string, error) {
	var diskPaths []string

	// If there are no other disks on the system by id, the directory might not
	// even be there. Returns ENOENT in case the by-id/ directory does not exist.
	diskPaths, err := resources.GetDisksByID(diskNamePrefix)
	if err != nil {
		return "", err
	}

	for _, diskPath := range diskPaths {
		// Skip the disk if it is only a partition of the actual volume.
		if strings.Contains(diskPath, "-part") {
			continue
		}

		// Use custom disk path filter, if one is provided.
		if diskPathFilter != nil && !diskPathFilter(diskPath) {
			continue
		}

		// The actual device might not already be created.
		// Returns ENOENT in case the device does not exist.
		devPath, err := filepath.EvalSymlinks(diskPath)
		if err != nil {
			return "", err
		}

		return devPath, nil
	}

	return "", nil
}

// WaitDiskDeviceGone waits for the disk device to disappear from /dev/disk/by-id.
// It periodically checks for the device to disappear and returns once the device
// is gone. If the device does not disappear within the timeout, an error is returned.
func WaitDiskDeviceGone(ctx context.Context, diskPath string) bool {
	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 30 seconds for the context
		// if no deadline is already configured.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	for {
		if !shared.PathExists(diskPath) {
			return true
		}

		if ctx.Err() != nil {
			return false
		}

		time.Sleep(500 * time.Millisecond)
	}
}

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
	// Acquire a lock to prevent concurrent connection attempts to the same
	// target.
	//
	// The unlock is not deferred here because it must remain held until all
	// connection attempts are complete. Releasing the lock prematurely after
	// the first successful connection (when this function exits) could lead
	// to race conditions if other connection attempts are still ongoing.
	// For the same reason, relying on a higher-level lock from the caller
	// (e.g., the storage driver) is insufficient.
	unlock, err := locking.Lock(ctx, targetQN)
	if err != nil {
		return nil, err
	}

	// Once the lock is obtained, search for an existing session.
	session, err := c.findSession(targetQN)
	if err != nil {
		return nil, err
	}

	// Context cancellation is not deferred to allow connection attempts to
	// continue after the first successful connection (which causes the function
	// to exit). The context is manually cancelled once all attempts complete.
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
