package locking

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// ClusterLock acquires the local lock, then attempts to create a ClusterLock operation.
// This operation will remain running until the UnlockFunc is called.
// If such an operation already exists, we will wait until the operation is finished and then retry.
func ClusterLock(ctx context.Context, s *state.State, lockName string) (UnlockFunc, error) {
	var localUnlocker UnlockFunc
	reverter := revert.New()
	defer reverter.Fail()

	// If the system isn't clustered, then just return a local lock.
	if !s.ServerClustered {
		reverter.Success()

		return Lock(ctx, lockName)
	}

	// clusterUnlockChan is used to notify the running operation that we have requested to release the lock, ending the operation.
	clusterUnlockChan := make(chan struct{})

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Failed to acquire %q cluster lock: %w", lockName, ctx.Err())
		default:
			// Acquire a local lock.
			if localUnlocker == nil {
				var err error
				localUnlocker, err = Lock(ctx, lockName)
				if err != nil {
					return nil, err
				}

				// Release the local lock if we run into trouble.
				reverter.Add(func() {
					localUnlocker()
					close(clusterUnlockChan)
				})
			}

			// The operation will run until the clusterUnlockChan is closed, at which point we can close the local lock as well.
			onRun := func(o *operations.Operation) error {
				logger.Debugf("Cluster lock %q acquired by system %q", lockName, s.ServerName)
				select {
				case <-ctx.Done():
					return fmt.Errorf("%q cluster lock operation failed to persist: %w", lockName, ctx.Err())
				case <-clusterUnlockChan:
					localUnlocker()
					return nil
				}
			}

			// Actually try creating the operation.
			opMetadata := operations.ClusterLockMetadata{Name: lockName}
			op, err := operations.OperationCreate(s, api.ProjectDefaultName, operations.OperationClassTask, operationtype.ClusterLock, nil, opMetadata, onRun, nil, nil, nil)
			if err == nil {
				// We successfully created the operation which means we acquired the lock, so initiate the operation.
				err = op.Start()
				if err != nil {
					return nil, fmt.Errorf("Failed to start %q cluster lock operation: %w", lockName, err)
				}

				// If we didn't run into an error, don't unlock the local lock until we call the UnlockFunc.
				reverter.Success()

				// Return an UnlockFunc which closes the channel that keeps the operation running.
				return func() {
					if locks[lockName] != nil {
						close(clusterUnlockChan)
					}
				}, nil
			}

			// A 409 (conflict) error means the lock has already been grabbed, so we have to wait for it. Any other error means we ran into a problem.
			if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
				return nil, fmt.Errorf("Failed to create %q cluster lock operation: %w", lockName, err)
			}

			d, err := lxd.ConnectLXDUnixWithContext(ctx, "", nil)
			if err != nil {
				return nil, err
			}

			// Block until whoever has the lock releases it.
			_, _, err = d.GetOperationWait(uuid.NewSHA1(uuid.Nil, []byte(lockName)).String(), 600)
			if err != nil {
				logger.Error("Failed to wait for cluster lock operation", logger.Ctx{"error": err, "name": lockName, "server": s.ServerName})
			}

			// Sleep a bit between checks.
			time.Sleep(1 * time.Second)
		}
	}
}
