package connectors

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// // lockAll invokes lockFn for all of the provided values, if nay invocation
// // returns non nill error, function unlocks all already acquired values and
// // returning this error. Otherwise returns combined unlock function and nil
// // error.
// func lockAll[T any](ctx context.Context, lockFn func(context.Context, T) (locking.UnlockFunc, error), cmp func(T, T) int, values ...T) (locking.UnlockFunc, error) {
// 	// Sort provided values to avoid deadlocks.
// 	values = slices.Clone(values)
// 	slices.SortFunc(values, cmp)
// 	values = slices.CompactFunc(values, func(x, y T) bool { return cmp(x, y) == 0 })

// 	unlocks := make([]locking.UnlockFunc, 0, len(values))
// 	unlock := func() {
// 		for _, un := range unlocks {
// 			un()
// 		}
// 	}

// 	reverter := revert.New()
// 	defer reverter.Fail()
// 	reverter.Add(unlock)

// 	for _, v := range values {
// 		un, err := lockFn(ctx, v)
// 		if err != nil {
// 			return nil, err
// 		}

// 		unlocks = append(unlocks, un)
// 	}

// 	reverter.Success()
// 	return unlock, nil
// }

// lockQualifiedName locks the global lock associated with the provided qualified name.
func lockQualifiedName(ctx context.Context, qualifiedName string) (locking.UnlockFunc, error) {
	return locking.Lock(ctx, "connectors:"+qualifiedName)
}

// lockTarget locks the global lock associated with the provided target.
func lockTarget(ctx context.Context, target Target) (locking.UnlockFunc, error) {
	return locking.Lock(ctx, fmt.Sprintf("connectors:%s:%s", target.QualifiedName, target.Address))
}

// discoverOperationFunc represents operation on a discovery endpoint.
type discoverOperationFunc func(ctx context.Context, discoveryAddress string) ([]Target, error)

// discover attempts to discover available targets, succeeding if at least one
// discovery endpoint is successful and returns non empty discovery log.
//
// On success function cancels other concurrent discovery operations.
func discover(ctx context.Context, discoverOperation discoverOperationFunc, discoveryAddresses ...string) ([]Target, error) {
	result := make(chan []Target, len(discoveryAddresses))
	wrappedDiscoverOperation := func(ctx context.Context, discoveryAddress string) error {
		log, err := discoverOperation(ctx, discoveryAddress)
		if err != nil {
			return err
		}

		result <- log
		return nil
	}

	cleanup := func(done <-chan struct{}, cancel context.CancelFunc) {
		// Cancel all operations, if not already done.
		cancel()

		// Wait for all operations to complete.
		<-done
	}

	// Make sure the provided addresses are unique without modifying the original
	// slice passed by the caller.
	discoveryAddresses = shared.Unique(slices.Clone(discoveryAddresses))

	// Set a deadline for the overall discovery.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	done, errs := par(ctx, wrappedDiscoverOperation, discoveryAddresses...)
	defer cleanup(done, cancel)

	for {
		// Use double select to gave higher preference to retrieving the result
		// rather than handling errors.
		select {
		case log := <-result:
			if len(log) == 0 {
				continue
			}

			return log, nil
		default:
		}

		select {
		case err, ok := <-errs:
			if !ok {
				return nil, fmt.Errorf("Failed fetching a discovery log record from any of the discovery addresses %q", discoveryAddresses)
			}

			logger.Warn("Connector discovery failure", logger.Ctx{"err": err})

		case log := <-result:
			if len(log) == 0 {
				continue
			}

			return log, nil
		}
	}
}

// targetOperationFunc represents operation on an individual target.
type targetOperationFunc func(ctx context.Context, target Target) error

// connect attempts to establish connections to all provided targets,
// succeeding if at least one connection is successful.
//
// Before any connection attempt, function acquires an exclusive lock for
// a given target.
//
// IMPORTANT:
// If at least one connection succeeds, no error is returned. In this case,
// the caller is responsible for disconnection by calling "connectors.Disconnect"
// when safe. The returned reverter will only cancel ongoing connection attempts
// but will **not** attempt disconnection.
func connect(ctx context.Context, connectOperation targetOperationFunc, targets ...Target) (revert.Hook, error) {
	wrappedConnectOperation := func(ctx context.Context, target Target) error {
		unlock, err := lockTarget(ctx, target)
		if err != nil {
			logger.Warn("Failed connecting to target due to lock acquisition failure", logger.Ctx{"target_qualified_name": target.QualifiedName, "target_address": target.Address, "err": err})
			return err
		}

		defer unlock()

		err = connectOperation(ctx, target)
		if err != nil {
			logger.Warn("Failed connecting to target", logger.Ctx{"target_qualified_name": target.QualifiedName, "target_address": target.Address, "err": err})
		}

		return err
	}

	cleanupRoutine := func(done <-chan struct{}, cancel context.CancelFunc) {
		// Wait for all operations to complete.
		<-done

		// Clean properly the context, if not already done.
		cancel()
	}

	revertHook := func(done <-chan struct{}, cancel context.CancelFunc) {
		// Cancel all operations, if not already done.
		cancel()

		// Wait for all operations to complete.
		<-done
	}

	// Make sure the provided targets are unique without modifying the original
	// slice passed by the caller.
	targets = shared.Unique(slices.Clone(targets))

	// Set a default timeout of 30 seconds for the context if no timeout is already
	// configured.
	ctx, cancel := shared.WithDefaultTimeout(ctx, 30*time.Second)

	success, done, _ := parWithMode(ctx, parMode(1), wrappedConnectOperation, targets...)
	go cleanupRoutine(done, cancel)

	if !success {
		revertHook(done, cancel)
		return nil, fmt.Errorf("Failed connecting to any of targets %q", targetsAddresses(targets...))
	}

	return func() { revertHook(done, cancel) }, nil
}

// disconnect attempts to disconnect all provided targets, succeeding only if
// all operations are successful. However on failure function do not interrupt
// other disconnection attempts.
//
// Before any disconnection attempt, function acquires an exclusive lock for
// a given target.
func disconnect(ctx context.Context, disconnectOperation targetOperationFunc, targets ...Target) error {
	wrappedDisconnectOperation := func(ctx context.Context, target Target) error {
		unlock, err := lockTarget(ctx, target)
		if err != nil {
			return fmt.Errorf("Failed disconnecting from target %q [%s] due to the target lock acquisition failure: %w", target.QualifiedName, target.Address, err)
		}

		defer unlock()

		err = disconnectOperation(ctx, target)
		if err != nil {
			return err
		}

		return err
	}

	// Make sure the provided targets are unique without modifying the original
	// slice passed by the caller.
	targets = shared.Unique(slices.Clone(targets))

	_, errs := par(ctx, wrappedDisconnectOperation, targets...)
	return errors.Join(collectChan(errs)...)
}
