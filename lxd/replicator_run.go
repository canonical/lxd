package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/canonical/lxd/client"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// replicatorFinalizeOperationArgs returns the arguments for the operation that closes out a
// replicator run. It is scheduled as the last stage so that by the time it runs every other child
// operation has settled, and the run status it records reflects whether any of them failed.
func replicatorFinalizeOperationArgs(s *state.State, projectName string, replicatorURL *api.URL, replicatorID int64, stage uint16) *operations.OperationArgs {
	return &operations.OperationArgs{
		ProjectName: projectName,
		Type:        operationtype.ReplicatorFinalize,
		Class:       operationtype.OperationClassTask,
		EntityURL:   replicatorURL,
		RunHook: func(_ context.Context, op *operations.Operation) error {
			// Iterate over all operations for the bulk replicator run.
			// If any operations (that are not this one) have failed, then the replicator run has failed overall.
			runStatus := api.ReplicatorStatusCompleted
			for _, child := range op.Parent().Children() {
				if child.ID() != op.ID() && child.Status() != api.Success {
					runStatus = api.ReplicatorStatusFailed
					break
				}
			}

			// Use a fresh context so the status write always completes, even if the operation context was cancelled.
			// Only the status is updated here; last_run_date was already set when the operation started.
			return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return dbCluster.UpdateReplicatorLastRunStatus(ctx, tx.Tx(), replicatorID, runStatus)
			})
		},
		Stage: stage,
	}
}

// replicatorCheckInstancesStopped verifies that all project instances across all
// cluster members are stopped before a restore operation. It checks the
// volatile.last_state.power config key from the database for all instances.
func replicatorCheckInstancesStopped(allInsts []instance.Instance) error {
	for _, inst := range allInsts {
		if inst.LocalConfig()["volatile.last_state.power"] == instance.PowerStateRunning {
			return fmt.Errorf("Instance %q is running, stop all project instances before restoring", inst.Name())
		}
	}

	return nil
}

// replicateInstance handles forward replication of a single instance to the
// destination cluster. It handles both instances on the local cluster member
// and instances on other cluster members.
func replicateInstance(ctx context.Context, s *state.State, op *operations.Operation, inst instance.Instance, memberAddress string, dstClient lxd.InstanceServer, targetCertPEM string) error {
	instName := inst.Name()
	projectName := inst.Project().Name

	// Instance on another cluster member: connect to the hosting cluster member and drive the
	// push migration through its API so the migration source has direct access to the
	// instance's storage.
	if inst.Location() != s.ServerName {
		if memberAddress == "" {
			return fmt.Errorf("Failed resolving cluster member address for instance %q", instName)
		}

		// Connect to the hosting cluster member.
		memberClient, err := lxdCluster.Connect(ctx, memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return fmt.Errorf("Failed connecting to hosting cluster member for instance %q: %w", instName, err)
		}

		memberClient = memberClient.UseProject(projectName)

		// Get instance metadata from the hosting cluster member.
		srcInstInfo, _, err := memberClient.GetInstance(instName)
		if err != nil {
			return fmt.Errorf("Failed getting instance %q from hosting cluster member: %w", instName, err)
		}

		// Set up a push-mode migration sink on the destination.
		destOp, err := dstClient.CreateInstance(api.InstancesPost{
			Name:        instName,
			InstancePut: srcInstInfo.Writable(),
			Type:        api.InstanceType(srcInstInfo.Type),
			Source: api.InstanceSource{
				Type:    api.SourceTypeMigration,
				Mode:    "push",
				Refresh: true,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination for %q: %w", instName, err)
		}

		destOpCancelled := false
		defer func() {
			if !destOpCancelled {
				_ = destOp.Cancel()
			}
		}()

		destOpAPI := destOp.Get()
		destSecrets, err := destOpAPI.WebsocketSecrets()
		if err != nil {
			return fmt.Errorf("Failed getting websocket secrets from destination for instance %q: %w", instName, err)
		}

		// Tell the hosting cluster member to push-migrate the instance to the destination.
		srcMigrateOp, err := memberClient.MigrateInstance(instName, api.InstancePost{
			Migration: true,
			Target: &api.InstancePostTarget{
				Operation:   destOp.URL().String(),
				Websockets:  destSecrets,
				Certificate: targetCertPEM,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed starting push migration for instance %q: %w", instName, err)
		}

		err = srcMigrateOp.Wait()
		if err != nil {
			return fmt.Errorf("Replication of instance %q failed on hosting cluster member: %w", instName, err)
		}

		destOpCancelled = true

		return destOp.Wait()
	}

	// Local instance: handle replication directly.
	srcRenderRes, _, err := inst.Render()
	if err != nil {
		return fmt.Errorf("Failed rendering source instance %q: %w", instName, err)
	}

	srcInstInfo, ok := srcRenderRes.(*api.Instance)
	if !ok {
		return fmt.Errorf("Unexpected result from source instance render for %q", instName)
	}

	// Set up a push-mode migration sink on the destination. In push mode the
	// leader (source) connects outward to the destination, so the destination
	// does not need to reach back into the leader. This is required when the
	// destination project is restricted, which disallows pull-mode migrations.
	destOp, err := dstClient.CreateInstance(api.InstancesPost{
		Name:        instName,
		InstancePut: srcInstInfo.Writable(),
		Type:        api.InstanceType(srcInstInfo.Type),
		Source: api.InstanceSource{
			Type:    api.SourceTypeMigration,
			Mode:    "push",
			Refresh: true,
		},
	})
	if err != nil {
		return fmt.Errorf("Failed requesting instance create on destination: %w", err)
	}

	// Guard against leaving the destination sink operation running if we fail
	// before starting the source; disarmed once the source op is scheduled.
	destOpCancelled := false
	defer func() {
		if !destOpCancelled {
			_ = destOp.Cancel()
		}
	}()

	destOpAPI := destOp.Get()
	destSecrets, err := destOpAPI.WebsocketSecrets()
	if err != nil {
		return fmt.Errorf("Failed getting websocket secrets from destination for instance %q: %w", instName, err)
	}

	pushTarget := &api.InstancePostTarget{
		Operation:   destOp.URL().String(),
		Websockets:  destSecrets,
		Certificate: targetCertPEM,
	}

	srcMigration, err := newMigrationSource(inst, false, false, false, "", pushTarget)
	if err != nil {
		return fmt.Errorf("Failed setting up migration source for instance %q: %w", instName, err)
	}

	migrArgs := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   entity.InstanceURL(projectName, instName),
		Type:        operationtype.InstanceMigrate,
		Class:       operationtype.OperationClassTask,
		RunHook: func(ctx context.Context, innerOp *operations.Operation) error {
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-done:
				case <-ctx.Done():
					srcMigration.disconnect()
				}
			}()

			return srcMigration.Do(ctx, s, innerOp)
		},
	}

	var srcOp *operations.Operation
	if op.Requestor() != nil {
		srcOp, err = operations.ScheduleUserOperationFromOperation(s, op, migrArgs)
	} else {
		srcOp, err = operations.ScheduleServerOperation(s, migrArgs)
	}

	if err != nil {
		return err
	}

	destOpCancelled = true // source is now connected via websockets; cancel would interrupt an in-flight transfer

	err = srcOp.Wait(context.Background())
	if err != nil {
		return fmt.Errorf("Replication of instance %q failed on source: %w", instName, err)
	}

	return destOp.Wait()
}

// runScheduledReplicators loads all replicators, checks their schedule config key against the current
// time, and triggers replication for those that are due.
func runScheduledReplicators(ctx context.Context, s *state.State) error {
	// Load all replicators across all projects.
	var apiReplicators []*api.Replicator
	var replicatorRows []dbCluster.Replicator
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		replicators, _, err := dbCluster.GetReplicatorsAndURLs(ctx, tx.Tx(), nil, func(_ dbCluster.Replicator) bool { return true })
		if err != nil {
			return fmt.Errorf("Failed loading replicators: %w", err)
		}

		allConfigs, err := dbCluster.ReplicatorsConfigStore().GetAll(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading replicator configs: %w", err)
		}

		apiReplicatorsTx := make([]*api.Replicator, 0, len(replicators))
		for _, replicator := range replicators {
			apiReplicatorsTx = append(apiReplicatorsTx, replicator.ToAPI(allConfigs))
		}

		apiReplicators = apiReplicatorsTx
		replicatorRows = replicators
		return nil
	})
	if err != nil {
		return err
	}

	// Build a per-project replica mode map so the loop can skip standby projects
	// without an extra DB round-trip per replicator.
	projectModes := make(map[string]string, len(apiReplicators))
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, replicator := range apiReplicators {
			_, ok := projectModes[replicator.Project]
			if ok {
				continue
			}

			dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), replicator.Project)
			if err != nil {
				return fmt.Errorf("Failed loading project %q: %w", replicator.Project, err)
			}

			projectModes[replicator.Project] = string(dbProject.ReplicaMode)
		}

		return nil
	})
	if err != nil {
		return err
	}

	now := time.Now()
	for i, replicator := range apiReplicators {
		if projectModes[replicator.Project] != api.ReplicatorProjectModeLeader {
			continue
		}

		schedule, ok := replicator.Config["schedule"]
		if !ok || schedule == "" {
			continue
		}

		if !replicatorIsScheduledNow(schedule, now) {
			continue
		}

		row := &replicatorRows[i]
		logger.Debug("Running scheduled replicator", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project, "schedule": schedule})

		err := triggerScheduledReplicator(ctx, s, replicator, row)
		if err != nil {
			logger.Error("Failed running scheduled replicator", logger.Ctx{
				"replicator": replicator.Name,
				"project":    replicator.Project,
				"err":        err,
			})
		}
	}

	return nil
}

// replicatorIsScheduledNow returns true if any of the (comma-separated) cron expressions in spec matches the provided minute.
func replicatorIsScheduledNow(spec string, now time.Time) bool {
	t := now.Truncate(time.Minute)
	// Split on ", " (comma+space) to match validate.IsCron, preserving intra-field commas like "0,30 * * * *".
	for _, s := range shared.SplitNTrimSpace(spec, ", ", -1, true) {
		sched, err := cron.ParseStandard(s)
		if err != nil {
			logger.Warn("Failed parsing replicator schedule expression", logger.Ctx{"spec": s, "err": err})
			continue
		}

		// Next(t - 1s) returns the next scheduled time strictly after t-1s.
		// If t itself is a scheduled minute, that equals t.
		if sched.Next(t.Add(-time.Second)).Equal(t) {
			return true
		}
	}

	return false
}

// triggerScheduledReplicator runs replication for a single replicator as a background server operation.
// It blocks until the operation completes so that last_run_date is persisted before the next scheduler
// tick and operation results are visible to callers.
func triggerScheduledReplicator(ctx context.Context, s *state.State, replicator *api.Replicator, row *dbCluster.Replicator) error {
	clusterLinkName := replicator.Config["cluster"]
	if clusterLinkName == "" {
		return fmt.Errorf("Replicator %q has no cluster link configured", replicator.Name)
	}

	opArgs, err := prepareReplicatorRunOperation(ctx, s, replicator.Project, replicator.Name, clusterLinkName, false, row.Row.ID)
	if err != nil {
		return err
	}

	// Set status to Running before scheduling the operation. The operation's RunHook writes
	// the terminal status (Completed/Failed) when it finishes. If the project has no instances,
	// the RunHook can complete synchronously inside ScheduleServerOperation before it returns,
	// writing the terminal status first. By setting Running here, that terminal write always
	// comes after Running, so the status is never left stuck at Running.
	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.UpdateReplicatorLastRun(ctx, tx.Tx(), row.Row.ID, time.Now(), api.ReplicatorStatusRunning)
	})
	if err != nil {
		logger.Warn("Failed updating replicator last run status to running", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project, "err": err})
	}

	op, err := operations.ScheduleServerOperation(s, opArgs)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			logger.Warn("Skipping scheduled replicator, a run is already in progress", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project})
			// Don't revert Running: another operation is in progress and owns the status;
			// it will write its own terminal state when it completes.
			return nil
		}

		// Revert Running to Failed so the status doesn't get stuck.
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.UpdateReplicatorLastRunStatus(ctx, tx.Tx(), row.Row.ID, api.ReplicatorStatusFailed)
		})

		return fmt.Errorf("Failed scheduling replicator operation: %w", err)
	}

	return op.Wait(ctx)
}

// validateReplicatorModes checks the source and target replica modes for a run.
func validateReplicatorModes(sourceMode string, targetMode string, restore bool) error {
	if restore {
		if sourceMode != api.ReplicatorProjectModeStandby {
			return errors.New("Local project must be in standby mode to run replicator in restore mode")
		}

		if targetMode != api.ReplicatorProjectModeLeader {
			return errors.New("Project on the remote cluster must be in leader mode to run replicator in restore mode")
		}
	} else {
		if sourceMode != api.ReplicatorProjectModeLeader {
			return errors.New("Local project must be in leader mode to run replicator")
		}

		if targetMode != api.ReplicatorProjectModeStandby {
			return errors.New("Project on the remote cluster must be in standby mode to run replicator")
		}
	}

	return nil
}

// snapshotInstance takes a snapshot of an instance before replication. Instances with a snapshot
// schedule are skipped; their own scheduled snapshots serve as the replication basis.
func snapshotInstance(ctx context.Context, s *state.State, inst instance.Instance, memberAddress string) error {
	if inst.ExpandedConfig()["snapshots.schedule"] != "" {
		return nil
	}

	instName := inst.Name()

	// Instances hosted elsewhere are snapshotted through the hosting member's API, since only
	// that member has direct access to the instance's storage.
	if inst.Location() != s.ServerName {
		if memberAddress == "" {
			return fmt.Errorf("Failed resolving cluster member address for instance %q", instName)
		}

		memberClient, err := lxdCluster.Connect(ctx, memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return fmt.Errorf("Failed connecting to hosting cluster member for instance %q: %w", instName, err)
		}

		memberClient = memberClient.UseProject(inst.Project().Name)

		snapOp, err := memberClient.CreateInstanceSnapshot(instName, api.InstanceSnapshotsPost{})
		if err != nil {
			return fmt.Errorf("Failed creating snapshot of instance %q on hosting cluster member: %w", instName, err)
		}

		err = snapOp.Wait()
		if err != nil {
			return fmt.Errorf("Failed waiting for snapshot of instance %q on hosting cluster member: %w", instName, err)
		}

		return nil
	}

	snapName, err := instance.NextSnapshotName(s, inst, "snap%d")
	if err != nil {
		return fmt.Errorf("Failed generating snapshot name for instance %q: %w", instName, err)
	}

	err = inst.Snapshot(ctx, snapName, nil, false, api.DiskVolumesModeRoot, nil)
	if err != nil {
		return fmt.Errorf("Failed creating snapshot of instance %q: %w", instName, err)
	}

	return nil
}

// instancesByName indexes the given instances by name for lookup during child-op construction.
func instancesByName(insts []instance.Instance) map[string]instance.Instance {
	byName := make(map[string]instance.Instance, len(insts))
	for _, inst := range insts {
		byName[inst.Name()] = inst
	}

	return byName
}

// buildForwardChildOps builds the child operations for a forward replicator run: one per instance
// at stage 0, followed by the finalize operation at stage 1.
func buildForwardChildOps(s *state.State, projectName string, replicatorURL *api.URL, replicatorID int64, allInsts []instance.Instance, nodeAddressByName map[string]string, clusterLink *api.ClusterLink, clusterCert *shared.CertInfo, targetCert *x509.Certificate, targetCertPEM string) []*operations.OperationArgs {
	childArgs := make([]*operations.OperationArgs, 0, len(allInsts)+1)

	for _, inst := range allInsts {
		memberAddress := nodeAddressByName[inst.Location()]

		childArgs = append(childArgs, &operations.OperationArgs{
			ProjectName: projectName,
			EntityURL:   entity.InstanceURL(projectName, inst.Name()),
			Type:        operationtype.ReplicatorRunInstanceForward,
			Class:       operationtype.OperationClassTask,
			Stage:       0,
			RunHook: func(ctx context.Context, op *operations.Operation) error {
				err := snapshotInstance(ctx, s, inst, memberAddress)
				if err != nil {
					return err
				}

				// Connect from inside the hook so the connection is made when the operation
				// runs rather than when it is queued.
				dstClient, err := lxdCluster.ConnectCluster(ctx, *clusterLink, lxdCluster.GetClusterLinkConnectionArgs(clusterCert, targetCert))
				if err != nil {
					return fmt.Errorf("Failed connecting to target cluster: %w", err)
				}

				dstClient = dstClient.UseProject(projectName)

				return replicateInstance(ctx, s, op, inst, memberAddress, dstClient, targetCertPEM)
			},
		})
	}

	return append(childArgs, replicatorFinalizeOperationArgs(s, projectName, replicatorURL, replicatorID, 1))
}
