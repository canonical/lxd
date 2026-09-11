package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// runScheduledReplicatorsTask returns a background task that checks replicator schedules every minute
// and triggers replication for any replicator whose cron expression matches the current time.
func runScheduledReplicatorsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		err := runScheduledReplicators(ctx, stateFunc())
		if err != nil {
			logger.Error("Failed running scheduled replicator task", logger.Ctx{"err": err})
		}
	}

	first := true
	schedule := func() (time.Duration, error) {
		// Skip the first run to avoid triggering replicators immediately at daemon
		// startup if the start time happens to coincide with a scheduled minute.
		if first {
			first = false
			return time.Minute, task.ErrSkip
		}

		return time.Minute, nil
	}

	return f, schedule
}

const (
	// Parent operation inputs, shared across all child operations.
	durableOperationInputKeyReplicatorClusterLinkName   operations.InputKey = "replicator_cluster_link_name"
	durableOperationInputKeyReplicatorTargetProjectName operations.InputKey = "replicator_target_project_name"

	// Child operation inputs. ID is used for forward replication. Name is used for restore (because the instance may not exist).
	durableOperationInputKeyReplicatorInstanceID   operations.InputKey = "replicator_instance_id"
	durableOperationInputKeyReplicatorInstanceName operations.InputKey = "replicator_instance_name"

	// Finalization operation input. This updates the [cluster.ReplicatorsStatusRow] with the run status.
	durableOperationInputKeyReplicatorRunID operations.InputKey = "replicator_run_id"

	// Finalisation operation input. This is used to create a warning for the replicator if it failed, or to resolve warnings if it succeeded.
	durableOperationInputKeyReplicatorID operations.InputKey = "replicator_id"
)

const (
	// replicatorMetadataSnapshotStarted is an operation metadata key used to get/set the time at which the instance snapshot was started.
	replicatorMetadataSnapshotStarted = "snapshot_started"

	// replicatorMetadataSnapshotFinished is an operation metadata key used to get/set the time at which the instance snapshot completed.
	replicatorMetadataSnapshotFinished = "snapshot_finished"
)

func init() {
	operations.RegisterDurableOperationRunHook(operationtype.ReplicatorFinalize, replicatorFinalizeDurableOperationRunHook)
	operations.RegisterDurableOperationRunHook(operationtype.ReplicatorRunInstanceForward, replicatorRunInstanceForwardDurableOperationHook)
	operations.RegisterDurableOperationRunHook(operationtype.ReplicatorRunInstanceRestore, replicatorRunInstanceRestoreDurableOperationHook)
	operations.RegisterDurableOperationRunHook(operationtype.ReplicatorSnapshotInstance, replicatorRunInstanceForwardSnapshotDurableOperationHook)
}

// loadSharedReplicatorDetails loads the target project, cluster link, target cluster certificate, and a map of cluster member name to address.
// These are common details shared by all children, so the project and cluster link name inputs are on the parent operation.
func loadSharedReplicatorDetails(ctx context.Context, op *operations.Operation) (string, *api.ClusterLink, *x509.Certificate, map[string]string, error) {
	parentOp := op
	if parentOp.Parent() != nil {
		parentOp = parentOp.Parent()
	}

	clusterLinkName, err := operations.GetOperationInputValue[string](parentOp, durableOperationInputKeyReplicatorClusterLinkName)
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("Failed loading cluster link name from operation inputs: %w", err)
	}

	projectName, err := operations.GetOperationInputValue[string](parentOp, durableOperationInputKeyReplicatorTargetProjectName)
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("Failed loading project name from operation inputs: %w", err)
	}

	s := parentOp.State()

	// Load all DB state in a single transaction before any network I/O.
	var clusterLink *api.ClusterLink
	var targetCert *x509.Certificate
	var nodeAddressByName map[string]string
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		_, clusterLink, targetCert, err = cluster.LoadClusterLinkAndCert(ctx, tx.Tx(), clusterLinkName)
		if err != nil {
			return err
		}

		// Pre-load node addresses for forwarding to other cluster members.
		nodes, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed listing cluster members: %w", err)
		}

		nodeAddressByName = make(map[string]string, len(nodes))
		for _, node := range nodes {
			nodeAddressByName[node.Name] = node.Address
		}

		return nil
	})
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("Failed loading replicator details: %w", err)
	}

	return projectName, clusterLink, targetCert, nodeAddressByName, nil
}

func replicatorFinalizeDurableOperationRunHook(opCtx context.Context, op *operations.Operation) error {
	// Get some details for the log context. Can't fail to parse the entity URL here because this is guaranteed by the operations package.
	_, projectName, _, args, err := entity.ParseURL(op.EntityURL().URL)
	if err != nil || len(args) != 1 {
		if err == nil {
			err = fmt.Errorf("Replicator URL should contain 1 path argument but contains %d", len(args))
		}

		return fmt.Errorf("Failed parsing operation entity URL: %w", err)
	}

	replicatorName := args[0]

	runID, err := operations.GetOperationInputValue[int64](op, durableOperationInputKeyReplicatorRunID)
	if err != nil {
		return fmt.Errorf("Failed getting replicator run ID from operation inputs: %w", err)
	}

	replicatorID, err := operations.GetOperationInputValue[int64](op, durableOperationInputKeyReplicatorID)
	if err != nil {
		return fmt.Errorf("Failed getting replicator ID from operation inputs: %w", err)
	}

	// Use a fresh context so the status write always completes, even if the operation context was cancelled.
	ctx := context.Background()

	// Get the current status so that we can compare start/finish times for metrics.
	var status *dbCluster.ReplicatorsStatusRow
	s := op.State()
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		status, err = query.SelectOne[dbCluster.ReplicatorsStatusRow](ctx, tx.Tx(), "WHERE id = ?", runID)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting replicator run status: %w", err)
	}

	// Iterate over all operations for the bulk replicator run.
	// If any operations (that are not this one) have failed, then the replicator run has failed overall.
	var inspectionErrs []error
	runStatus := api.ReplicatorStatusCompleted
	var earliestSnapshotStarted, latestSnapshotFinished *time.Time
	var instancesTotal, instancesFailed uint
	var oldestSnapshotInstanceName string
	for _, child := range op.Parent().Children() {
		if child.ID() != op.ID() && child.Status() != api.Success && runStatus == api.ReplicatorStatusCompleted {
			runStatus = api.ReplicatorStatusFailed
		}

		if child.Type() == operationtype.ReplicatorRunInstanceForward || child.Type() == operationtype.ReplicatorRunInstanceRestore {
			instancesTotal++
			if child.Status() != api.Success {
				instancesFailed++

				instanceName, err := operations.GetOperationInputValue[string](child, durableOperationInputKeyReplicatorInstanceName)
				if err != nil {
					logger.Warn("Instance name not found in forward or restore replication input arguments", logger.Ctx{"parent_operation": op.Parent().ID(), "child_operation": child.ID(), "err": err})
					instanceName = "UNKNOWN"
				}

				// Log each failure individually so that a partial failure can be
				// investigated without having to correlate the child operations by hand.
				logger.Warn("Replicator failed replicating instance", logger.Ctx{
					"replicator": replicatorName,
					"project":    projectName,
					"instance":   instanceName,
					"status":     child.Status(),
					"err":        child.Err(),
				})
			}
		}

		if child.Type() == operationtype.ReplicatorSnapshotInstance && child.Status() == api.Success {
			snapshotStarted, snapshotFinished, err := extractSnapshotTimestamps(child)
			if err != nil {
				logger.Warn("Failed getting replicator snapshot operation telemetry", logger.Ctx{"parent_operation": op.Parent().ID(), "child_operation": child.ID(), "err": err})
				inspectionErrs = append(inspectionErrs, err)
				continue
			}

			if earliestSnapshotStarted == nil || earliestSnapshotStarted.After(*snapshotStarted) {
				oldestSnapshotInstanceName, err = operations.GetOperationInputValue[string](child, durableOperationInputKeyReplicatorInstanceName)
				if err != nil {
					logger.Warn("Failed getting instance name for oldest snapshot", logger.Ctx{"parent_operation": op.Parent().ID(), "child_operation": child.ID(), "err": err})
					inspectionErrs = append(inspectionErrs, err)
				}

				earliestSnapshotStarted = snapshotStarted
			}

			if latestSnapshotFinished == nil || latestSnapshotFinished.Before(*snapshotFinished) {
				latestSnapshotFinished = snapshotFinished
			}
		}
	}

	// Set both values back to nil if there were any inspection errors. Prefer to not set values that might be wrong.
	if len(inspectionErrs) > 0 {
		latestSnapshotFinished, earliestSnapshotStarted = nil, nil

		// Also set the run status to failed. We can't measure it.
		runStatus = api.ReplicatorStatusFailed
	}

	// Calculate run duration.
	completedAt := time.Now()
	durationSeconds := completedAt.Sub(status.StartedDate).Seconds()

	var eventCtx = map[string]any{
		"status":           runStatus,
		"instances_total":  instancesTotal,
		"instances_failed": instancesFailed,
		"duration_seconds": durationSeconds,
	}

	if earliestSnapshotStarted != nil {
		effectiveRPO := completedAt.Sub(*earliestSnapshotStarted).Seconds()

		// Surface the instance that determines the project's recovery point, since
		// that is the one to look at when the RPO is worse than expected.
		logger.Info("Replicator run recovery point", logger.Ctx{
			"replicator":            replicatorName,
			"project":               projectName,
			"instance":              oldestSnapshotInstanceName,
			"oldest_snapshot":       *earliestSnapshotStarted,
			"effective_rpo_seconds": effectiveRPO,
		})

		eventCtx["effective_rpo_seconds"] = effectiveRPO
	}

	logCtx := logger.Ctx{
		"replicator":       replicatorName,
		"project":          projectName,
		"instances_total":  instancesTotal,
		"instances_failed": instancesFailed,
		"duration_seconds": durationSeconds,
	}

	// The warning is recorded against the cluster as a whole rather than the member
	// that happened to run the replicator, so that a later run on a different member
	// can resolve it.
	if runStatus == api.ReplicatorStatusCompleted {
		logger.Info("Replicator run completed", logCtx)

		err = warnings.ResolveWarningsByNodeAndProjectAndTypeAndEntity(s.DB.Cluster, "", projectName, warningtype.ReplicatorRunFailure, entity.TypeReplicator, int(replicatorID))
		if err != nil {
			logger.Warn("Failed resolving replicator run failure warning", logger.Ctx{"replicator": replicatorName, "project": projectName, "err": err})
		}
	} else {
		logger.Error("Replicator run failed", logCtx)

		replicatorRaiseRunFailureWarning(s, projectName, replicatorName, replicatorID, fmt.Sprintf("Replication of %d out of %d instances failed", instancesFailed, instancesTotal))
	}

	// Use the operation context here for getting requestor details. It doesn't matter that the context may already have
	// been cancelled, as it is not used for the call to SendLifecycle (it's only used for creating the event data).
	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorRun.Event(opCtx, replicatorName, projectName, eventCtx))

	// Use a fresh context so the status write always completes, even if the operation context was cancelled.
	return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.FinalizeReplicatorStatus(ctx, tx.Tx(), runID, runStatus, completedAt, earliestSnapshotStarted, latestSnapshotFinished)
	})
}

// replicatorRaiseRunFailureWarning records a warning for a failed replicator run.
//
// The warning is recorded against the cluster as a whole rather than the member that happened
// to run the replicator, because a replicator is a cluster-wide entity and a later run may well
// be picked up by a different member. A member scoped warning would never be resolved by that
// run's success.
func replicatorRaiseRunFailureWarning(s *state.State, projectName string, name string, replicatorID int64, message string) {
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpsertWarning(ctx, "", projectName, entity.TypeReplicator, int(replicatorID), warningtype.ReplicatorRunFailure, message)
	})
	if err != nil {
		logger.Warn("Failed creating replicator run failure warning", logger.Ctx{"replicator": name, "project": projectName, "err": err})
	}
}

func extractSnapshotTimestamps(op *operations.Operation) (snapshotStarted *time.Time, snapshotFinished *time.Time, err error) {
	opMeta := op.Metadata()
	snapshotStartedAny, ok := opMeta[replicatorMetadataSnapshotStarted]
	if !ok {
		return nil, nil, errors.New("Replicator snapshot operation did not contain a snapshot start timestamp")
	}

	snapshotFinishedAny, ok := opMeta[replicatorMetadataSnapshotFinished]
	if !ok {
		return nil, nil, errors.New("Replicator snapshot operation did not contain a snapshot finished timestamp")
	}

	// Time can be stored in metadata either as a time.Time or as a string. If the durable operation is restarted on another cluster member, then
	// the time value is unmarshalled into a string. If the whole operation runs on one member, it is still in memory and therefore a time.Time.
	getTime := func(tAny any) (*time.Time, error) {
		t, ok := tAny.(time.Time)
		if !ok {
			tStr, ok := tAny.(string)
			if !ok {
				return nil, fmt.Errorf("Timestamp is of type %T (expected %T or %T)", snapshotStartedAny, time.Time{}, "")
			}

			t, err = time.Parse(time.RFC3339Nano, tStr)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing timestamp: %w", err)
			}
		}

		return &t, nil
	}

	started, err := getTime(snapshotStartedAny)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting snapshot start timestamp: %w", err)
	}

	finished, err := getTime(snapshotFinishedAny)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting snapshot finished timestamp: %w", err)
	}

	return started, finished, nil
}

func replicatorRunInstanceForwardSnapshotDurableOperationHook(ctx context.Context, op *operations.Operation) error {
	instanceID, err := operations.GetOperationInputValue[int64](op, durableOperationInputKeyReplicatorInstanceID)
	if err != nil {
		return fmt.Errorf("Failed getting instance ID from operation inputs: %w", err)
	}

	_, _, _, memberAddresses, err := loadSharedReplicatorDetails(ctx, op)
	if err != nil {
		return fmt.Errorf("Failed loading replicator details: %w", err)
	}

	return snapshotInstance(ctx, op, instanceID, memberAddresses)
}

func replicatorRunInstanceForwardDurableOperationHook(ctx context.Context, op *operations.Operation) error {
	instanceID, err := operations.GetOperationInputValue[int64](op, durableOperationInputKeyReplicatorInstanceID)
	if err != nil {
		return fmt.Errorf("Failed getting instance ID from operation inputs: %w", err)
	}

	err = checkSnapshotStageSucceededForInstance(instanceID, op)
	if err != nil {
		return err
	}

	targetProject, clusterLink, targetCert, memberAddresses, err := loadSharedReplicatorDetails(ctx, op)
	if err != nil {
		return fmt.Errorf("Failed loading replicator details: %w", err)
	}

	s := op.State()
	clusterCert := s.Endpoints.NetworkCert()
	dstClient, err := cluster.ConnectCluster(ctx, *clusterLink, cluster.GetClusterLinkConnectionArgs(clusterCert, targetCert))
	if err != nil {
		return fmt.Errorf("Failed connecting to target cluster: %w", err)
	}

	dstClient = dstClient.UseProject(targetProject)

	return replicateInstance(ctx, s, op, instanceID, dstClient, targetCert, memberAddresses)
}

func checkSnapshotStageSucceededForInstance(instanceID int64, op *operations.Operation) error {
	// Check that the snapshot stage for this instance has succeeded.
	for _, child := range op.Parent().Children() {
		if child.Type() != operationtype.ReplicatorSnapshotInstance {
			continue
		}

		snapInstID, err := operations.GetOperationInputValue[int64](child, durableOperationInputKeyReplicatorInstanceID)
		if err != nil {
			return fmt.Errorf("Failed getting instance ID from snapshot operation inputs: %w", err)
		}

		if snapInstID != instanceID {
			continue
		}

		if child.Status() != api.Success {
			return errors.New("Skipping instance replication due to failed or cancelled snapshot")
		}
	}

	return nil
}

func replicatorRunInstanceRestoreDurableOperationHook(ctx context.Context, op *operations.Operation) error {
	targetProject, clusterLink, targetCert, memberAddresses, err := loadSharedReplicatorDetails(ctx, op)
	if err != nil {
		return fmt.Errorf("Failed loading replicator details: %w", err)
	}

	instName, err := operations.GetOperationInputValue[string](op, durableOperationInputKeyReplicatorInstanceName)
	if err != nil {
		return fmt.Errorf("Failed getting instance name from operation inputs: %w", err)
	}

	s := op.State()

	clusterCert := s.Endpoints.NetworkCert()
	dstClient, err := cluster.ConnectCluster(ctx, *clusterLink, cluster.GetClusterLinkConnectionArgs(clusterCert, targetCert))
	if err != nil {
		return fmt.Errorf("Failed connecting to target cluster: %w", err)
	}

	dstClient = dstClient.UseProject(targetProject)

	// In restore mode the local copy is stale; fetch current metadata from
	// the current leader cluster so the restore uses up-to-date config/state.
	freshInst, _, err := dstClient.GetInstance(instName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Instance was deleted on the current leader cluster after failover; skip it rather
			// than failing the whole run, since the deletion is intentional.
			logger.Warn("Skipping restore of instance deleted on current leader cluster", logger.Ctx{"instance": instName})
			return nil
		}

		return fmt.Errorf("Failed getting instance %q from current leader cluster: %w", instName, err)
	}

	// Use our cluster certificate so the leader can verify TLS when
	// pushing data back to us.
	localCertPEM := string(clusterCert.PublicKey())

	// The promoted cluster decides what travels, so the mode comes from its view of the project.
	remoteProject, _, err := dstClient.GetProject(targetProject)
	if err != nil {
		return fmt.Errorf("Failed getting project %q from current leader cluster: %w", targetProject, err)
	}

	diskVolumesMode := replicationDiskVolumesMode(remoteProject.Config)

	var instanceLocation string
	inst, err := instance.LoadByProjectAndName(s, targetProject, instName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed checking if instance exists: %w", err)
	}

	if inst != nil {
		instanceLocation = inst.Location()
	}

	// If the instance lives on a remote cluster member, forward the restore
	// migration to that member so the refresh runs where the storage volume is.
	// Instances on the local member (including unclustered servers) are handled
	// directly below. This mirrors the logic in replicateInstance.
	if instanceLocation != "" && instanceLocation != s.ServerName {
		memberAddress, ok := memberAddresses[instanceLocation]
		if !ok {
			return fmt.Errorf("Failed resolving cluster member address for instance %q", instName)
		}

		memberClient, err := cluster.Connect(ctx, memberAddress, clusterCert, s.ServerCert(), true)
		if err != nil {
			return fmt.Errorf("Failed connecting to hosting cluster member for instance %q: %w", instName, err)
		}

		memberClient = memberClient.UseProject(targetProject)

		// Set up a push-mode migration sink on the hosting cluster member.
		restoreOp, err := memberClient.CreateInstance(api.InstancesPost{
			Name:        instName,
			InstancePut: freshInst.Writable(),
			Type:        api.InstanceType(freshInst.Type),
			Source: api.InstanceSource{
				Type:            api.SourceTypeMigration,
				Mode:            "push",
				Refresh:         true,
				DiskVolumesMode: diskVolumesMode,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting restore on hosting cluster member for instance %q: %w", instName, err)
		}

		restoreOpCancelled := false
		defer func() {
			if !restoreOpCancelled {
				_ = restoreOp.Cancel()
			}
		}()

		restoreOpAPI := restoreOp.Get()
		restoreSecrets, err := restoreOpAPI.WebsocketSecrets()
		if err != nil {
			return fmt.Errorf("Failed getting websocket secrets from hosting cluster member for instance %q: %w", instName, err)
		}

		// Tell the current leader cluster to push-migrate the instance to the hosting cluster member's sink.
		remoteMigrateOp, err := dstClient.MigrateInstance(instName, api.InstancePost{
			Migration:       true,
			DiskVolumesMode: diskVolumesMode,
			Target: &api.InstancePostTarget{
				Operation:   restoreOp.URL().String(),
				Websockets:  restoreSecrets,
				Certificate: localCertPEM,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed starting push migration on current leader cluster for instance %q: %w", instName, err)
		}

		restoreOpCancelled = true

		err = remoteMigrateOp.Wait()
		if err != nil {
			return fmt.Errorf("Restore of instance %q failed on current leader cluster: %w", instName, err)
		}

		return restoreOp.Wait()
	}

	// Load profiles for the instance to pass to the migration sink.
	var profiles []api.Profile
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		profiles, err = instanceProfilesFromNames(ctx, tx, targetProject, freshInst.Profiles)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed loading profiles for instance %q: %w", instName, err)
	}

	// Set up a push-mode migration sink locally so the leader pushes data to us.
	migrateReq := &api.InstancesPost{
		InstancePut: api.InstancePut{
			Architecture: freshInst.Architecture,
			Config:       freshInst.Config,
			Devices:      freshInst.Devices,
			Description:  freshInst.Description,
			Ephemeral:    freshInst.Ephemeral,
			Profiles:     freshInst.Profiles,
			Stateful:     freshInst.Stateful,
		},
		Name: instName,
		Type: api.InstanceType(freshInst.Type),
		Source: api.InstanceSource{
			Type:            api.SourceTypeMigration,
			Mode:            "push",
			Refresh:         true,
			DiskVolumesMode: diskVolumesMode,
		},
	}

	result, err := prepareInstanceMigrationSink(ctx, s, targetProject, profiles, migrateReq, "")
	if err != nil {
		return fmt.Errorf("Failed preparing migration sink for instance %q: %w", instName, err)
	}

	defer result.revert.Fail()

	// Schedule the sink operation so it gets an ID and can accept websocket connections.
	sinkOpArgs := operations.OperationArgs{
		ProjectName: targetProject,
		EntityURL:   api.NewURL().Path(version.APIVersion, "projects", targetProject),
		Type:        operationtype.InstanceCreate,
		Class:       operationtype.OperationClassWebsocket,
		Metadata:    result.sink.Metadata(),
		ConnectHook: result.sink.Connect,
		RunHook:     result.run,
	}

	var sinkOp *operations.Operation
	if op.Requestor() != nil {
		sinkOp, err = operations.ScheduleUserOperationFromOperation(s, op, sinkOpArgs)
	} else {
		sinkOp, err = operations.ScheduleServerOperation(s, sinkOpArgs)
	}

	if err != nil {
		return fmt.Errorf("Failed scheduling migration sink operation for instance %q: %w", instName, err)
	}

	_, sinkOpAPI := sinkOp.Render()
	sinkSecrets, err := sinkOpAPI.WebsocketSecrets()
	if err != nil {
		return fmt.Errorf("Failed getting websocket secrets from local sink for instance %q: %w", instName, err)
	}

	// Build the operation URL using a reachable address for this server.
	// For clustered members the address from the nodes table is already a
	// concrete, registered address. For unclustered servers the nodes table
	// stores the sentinel "0.0.0.0", so fall back to the configured HTTPS
	// address. Return an error if we still cannot determine a concrete address,
	// because the leader would not be able to reach us.
	localAddress := memberAddresses[s.ServerName]
	if util.IsWildCardAddress(localAddress) {
		localAddress = s.LocalConfig.ClusterAddress()
		if localAddress == "" {
			localAddress = s.LocalConfig.HTTPSAddress()
		}

		if util.IsWildCardAddress(localAddress) || localAddress == "" {
			_ = sinkOp.Cancel()
			return errors.New("Cannot restore to this server: configure a concrete address using cluster.https_address or core.https_address")
		}
	}

	sinkOpURL := "https://" + localAddress + sinkOp.URL()

	// Tell the current leader cluster to push-migrate the instance to our local sink.
	remoteMigrateOp, err := dstClient.MigrateInstance(instName, api.InstancePost{
		Migration:       true,
		DiskVolumesMode: diskVolumesMode,
		Target: &api.InstancePostTarget{
			Operation:   sinkOpURL,
			Websockets:  sinkSecrets,
			Certificate: localCertPEM,
		},
	})
	if err != nil {
		_ = sinkOp.Cancel()
		return fmt.Errorf("Failed starting push migration on current leader cluster for instance %q: %w", instName, err)
	}

	remoteErr := remoteMigrateOp.Wait()
	if remoteErr != nil {
		_ = sinkOp.Cancel()
		return fmt.Errorf("Restore of instance %q failed on current leader cluster: %w", instName, remoteErr)
	}

	sinkErr := sinkOp.Wait(context.Background())
	if sinkErr != nil {
		return fmt.Errorf("Restore of instance %q failed: %w", instName, sinkErr)
	}

	result.revert.Success()
	return nil
}

// prepareReplicatorRunOperationArgs builds the operation used to run a replicator.
func prepareReplicatorRunOperationArgs(ctx context.Context, s *state.State, projectName string, name string, clusterLinkName string, restore bool, replicatorID, runID int64) (*operations.OperationArgs, error) {
	// Load all DB state in a single transaction before any network I/O.
	var clusterLink *api.ClusterLink
	var targetCert *x509.Certificate
	var sourceProject *api.Project
	var allInsts []instance.Instance
	var nodeAddressByName map[string]string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		_, clusterLink, targetCert, err = cluster.LoadClusterLinkAndCert(ctx, tx.Tx(), clusterLinkName)
		if err != nil {
			return err
		}

		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		sourceProject, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Load all instances in the project across all cluster members as
		// instance.Instance. instance.Load() only performs in-memory config
		// expansion and is safe for non-local instances.
		err = tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := instance.Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q: %w", dbInst.Name, err)
			}

			allInsts = append(allInsts, inst)
			return nil
		}, dbCluster.InstanceFilter{Project: &projectName})
		if err != nil {
			return fmt.Errorf("Failed listing project instances: %w", err)
		}

		// Pre-load node addresses for forwarding to other cluster members.
		nodes, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed listing cluster members: %w", err)
		}

		nodeAddressByName = make(map[string]string, len(nodes))
		for _, node := range nodes {
			nodeAddressByName[node.Name] = node.Address
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed loading replicator state: %w", err)
	}

	clusterCert := s.Endpoints.NetworkCert()

	// Mutual TLS is safe to assume here: replicatorValidateConfig rejects public cluster links, which
	// are the only type that connects without presenting a client certificate.
	connArgs := cluster.GetClusterLinkConnectionArgs(clusterCert, targetCert)
	targetClient, err := cluster.ConnectCluster(ctx, *clusterLink, connArgs)
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to target cluster: %w", err)
	}

	targetClient = targetClient.UseProject(projectName)

	targetProject, _, err := targetClient.GetProject(projectName)
	if err != nil {
		return nil, fmt.Errorf("Failed getting target project: %w", err)
	}

	err = validateReplicatorModes(sourceProject.ReplicaMode, targetProject.ReplicaMode, restore)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "%s", err)
	}

	// In restore mode, all project instances across all cluster members must be stopped
	// before proceeding. The restore operation refreshes each existing instance from the
	// current leader cluster and creates any that only exist on the leader; a running instance
	// cannot be refreshed. Fail fast here to avoid a partial restore where some instances
	// are updated and others are not.
	if restore {
		err = replicatorCheckInstancesStopped(allInsts)
		if err != nil {
			return nil, err
		}
	}

	// In restore mode the current leader cluster is the source of truth: use its instance list so
	// that instances created on the leader after failover are included. Restore is additive
	// only: local instances that do not exist on the leader are left in place and not deleted.
	var iterNames []string
	if restore {
		remoteInsts, err := targetClient.GetInstances(lxd.GetInstancesArgs{InstanceType: api.InstanceTypeAny})
		if err != nil {
			return nil, fmt.Errorf("Failed listing instances on target: %w", err)
		}

		iterNames = make([]string, 0, len(remoteInsts))
		for _, ri := range remoteInsts {
			iterNames = append(iterNames, ri.Name)
		}
	}

	replicatorURL := entity.ReplicatorURL(projectName, name)
	parentArgs := operations.OperationArgs{
		ProjectName:       projectName,
		EntityURL:         replicatorURL,
		Type:              operationtype.ReplicatorRun,
		Class:             operationtype.OperationClassDurable,
		ConflictReference: replicatorURL.String(), // Prevents concurrent runs; paired with ConflictActionFail on the operation type to enforce cluster-wide exclusivity.
	}

	err = parentArgs.SetInputValues(map[operations.InputKey]any{
		durableOperationInputKeyReplicatorTargetProjectName: projectName,
		durableOperationInputKeyReplicatorClusterLinkName:   clusterLinkName,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed preparing replicator run operation: %w", err)
	}

	builder := operations.NewBulkArgBuilder(parentArgs)

	// Forward replication: iterate over all loaded instances directly.
	if !restore {
		for _, inst := range allInsts {
			err = builder.AddChildArgs(operations.OperationArgs{
				ProjectName: projectName,
				EntityURL:   entity.InstanceURL(projectName, inst.Name()),
				Type:        operationtype.ReplicatorSnapshotInstance,
				Class:       operationtype.OperationClassDurable,
			}, map[operations.InputKey]any{
				durableOperationInputKeyReplicatorInstanceID: inst.ID(),
				// The instance name is not used for the snapshot stage. It is used by the finalization stage so that it
				// can associate the oldest snapshot with a particular instance, for telemetry purposes.
				durableOperationInputKeyReplicatorInstanceName: inst.Name(),
			})
			if err != nil {
				return nil, fmt.Errorf("Failed preparing instance forward replication snapshot operation: %w", err)
			}
		}

		builder.IncrementStage()
		for _, inst := range allInsts {
			err = builder.AddChildArgs(operations.OperationArgs{
				ProjectName: projectName,
				EntityURL:   entity.InstanceURL(projectName, inst.Name()),
				Type:        operationtype.ReplicatorRunInstanceForward,
				Class:       operationtype.OperationClassDurable,
			}, map[operations.InputKey]any{
				durableOperationInputKeyReplicatorInstanceID: inst.ID(),
				// The instance name is not used for the replication stage. It is used by the finalization stage so that it
				// can log the instance name and an error message, for telemetry purposes.
				durableOperationInputKeyReplicatorInstanceName: inst.Name(),
			})
			if err != nil {
				return nil, fmt.Errorf("Failed preparing instance forward replication operation: %w", err)
			}
		}

		return finalizeReplicatorRunOperationArgs(builder, projectName, replicatorURL, replicatorID, runID)
	}

	// For restore operations the project URL is used as the primary entity URL because the instance may exist on the
	// current leader cluster, but not on the standby. In this case the restore effectively becomes an instance create
	// operation, for which we use the parent entity as the primary entity URL.
	projectURL := entity.ProjectURL(projectName)
	for _, instName := range iterNames {
		err = builder.AddChildArgs(operations.OperationArgs{
			ProjectName: projectName,
			EntityURL:   projectURL,
			Type:        operationtype.ReplicatorRunInstanceRestore,
			Class:       operationtype.OperationClassDurable,
			// Include the instance name in the operation metadata to offer a link after the instance is created.
			Metadata: map[string]any{
				api.MetadataEntityURL: entity.InstanceURL(projectName, instName).String(),
			},
		}, map[operations.InputKey]any{
			durableOperationInputKeyReplicatorInstanceName: instName,
		})
		if err != nil {
			return nil, err
		}
	}

	return finalizeReplicatorRunOperationArgs(builder, projectName, replicatorURL, replicatorID, runID)
}

func finalizeReplicatorRunOperationArgs(builder *operations.BulkArgBuilder, projectName string, replicatorURL *api.URL, replicatorID int64, runID int64) (*operations.OperationArgs, error) {
	builder.IncrementStage()
	err := builder.AddChildArgs(operations.OperationArgs{
		ProjectName: projectName,
		Type:        operationtype.ReplicatorFinalize,
		Class:       operationtype.OperationClassDurable,
		EntityURL:   replicatorURL,
	}, map[operations.InputKey]any{
		durableOperationInputKeyReplicatorRunID: runID,
		durableOperationInputKeyReplicatorID:    replicatorID,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed preparing replicator finalization operation: %w", err)
	}

	args := builder.Args()
	return &args, nil
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

// replicationDiskVolumesMode returns the disk volumes mode for a replication push. Projects that inherit their
// volumes own none of them and the migration API rejects all-exclusive for them, so those push the root disk alone.
func replicationDiskVolumesMode(projectConfig map[string]string) string {
	// The same rule the storage layer applies, so a project whose key is unset is treated as inheriting
	// rather than owning volumes it has none of.
	if shared.IsFalseOrEmpty(projectConfig["features.storage.volumes"]) {
		return api.DiskVolumesModeRoot
	}

	return api.DiskVolumesModeAllExclusive
}

func snapshotInstance(ctx context.Context, op *operations.Operation, instanceID int64, memberAddresses map[string]string) error {
	s := op.State()
	inst, err := instance.LoadByID(s, int(instanceID))
	if err != nil {
		return err
	}

	instName := inst.Name()
	projectName := inst.Project().Name

	// All-exclusive mode captures the root disk and the instance's exclusively attached custom
	// volumes at the same moment, so the replicated set is crash consistent. Projects that inherit
	// volumes from the default project have no project-local volumes to capture, so those fall back
	// to the root disk alone. Migration leaves their volumes alone too, so nothing is replicated
	// without a snapshot behind it.
	diskVolumesMode := api.DiskVolumesModeAllExclusive
	if shared.IsFalseOrEmpty(inst.Project().Config["features.storage.volumes"]) {
		diskVolumesMode = api.DiskVolumesModeRoot
	}

	// Snapshotting is unconditional; the only exception is when the instance already has a
	// snapshot schedule defined, since scheduled snapshots provide point-in-time history so
	// an extra one here would be redundant.
	createSnapshot := inst.ExpandedConfig()["snapshots.schedule"] == ""

	// Scheduled snapshots only capture the root disk, so an instance whose custom volumes travel
	// with it still needs one here to put the whole replicated set at the same point in time.
	if !createSnapshot && diskVolumesMode == api.DiskVolumesModeAllExclusive {
		createSnapshot = len(inst.ExpandedDevices().Filter(filters.IsCustomVolumeDisk)) > 0
	}

	// If not creating a snapshot, get the most recent snapshot creation time so that we can record the RPO.
	if !createSnapshot {
		var snapshotCreationDate *time.Time
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			snapshotCreationDate, err = dbCluster.GetMostRecentSnapshotCreationDate(ctx, tx.Tx(), instanceID)
			return err
		})
		if err != nil {
			return fmt.Errorf("Failed determining snapshot creation time of instance with snapshot schedule: %w", err)
		}

		// If we found the latest snapshot creation date then a snapshot exists that we can use, and we can set the creation
		// date as the snapshot creation time for this operation. Otherwise, the instance has a snapshot schedule but no
		// snapshot has been created yet, so we need to create one.
		if snapshotCreationDate != nil {
			// Use the creation date for both the started and finished times, because we don't know when the snapshot
			// finished or how long it took to complete.
			return setInstanceSnapshotStartAndFinishTimestamps(op, *snapshotCreationDate, *snapshotCreationDate)
		}

		// If we didn't find the latest snapshot creation date then the instance has a schedule but no snapshot has been created yet.
		// Continue to create one.
	}

	instanceLocation := inst.Location()
	if instanceLocation != s.ServerName {
		memberAddress, ok := memberAddresses[instanceLocation]
		if !ok {
			return fmt.Errorf("Failed resolving cluster member address for instance %q", instName)
		}

		// Connect to the hosting cluster member.
		memberClient, err := cluster.Connect(ctx, memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return fmt.Errorf("Failed connecting to hosting cluster member for instance %q: %w", instName, err)
		}

		memberClient = memberClient.UseProject(projectName)

		// Create a snapshot on the hosting cluster member if needed.
		snapStartedAt := time.Now()
		snapOp, err := memberClient.CreateInstanceSnapshot(instName, api.InstanceSnapshotsPost{DiskVolumesMode: diskVolumesMode})
		if err != nil {
			return fmt.Errorf("Failed creating snapshot of instance %q on hosting cluster member: %w", instName, err)
		}

		err = snapOp.Wait()
		if err != nil {
			return fmt.Errorf("Failed waiting for snapshot of instance %q on hosting cluster member: %w", instName, err)
		}

		snapFinishedAt := time.Now()
		return setInstanceSnapshotStartAndFinishTimestamps(op, snapStartedAt, snapFinishedAt)
	}

	snapName, err := instance.NextSnapshotName(s, inst, "snap%d")
	if err != nil {
		return fmt.Errorf("Failed generating snapshot name for instance %q: %w", instName, err)
	}

	snapStartedAt := time.Now()
	err = inst.Snapshot(ctx, snapName, nil, false, diskVolumesMode, nil)
	if err != nil {
		return fmt.Errorf("Failed creating snapshot of instance %q: %w", instName, err)
	}

	snapFinishedAt := time.Now()
	return setInstanceSnapshotStartAndFinishTimestamps(op, snapStartedAt, snapFinishedAt)
}

func setInstanceSnapshotStartAndFinishTimestamps(op *operations.Operation, startedAt time.Time, finishedAt time.Time) error {
	err := op.ExtendMetadata(map[string]any{
		replicatorMetadataSnapshotStarted:  startedAt,
		replicatorMetadataSnapshotFinished: finishedAt,
	})
	if err != nil {
		return fmt.Errorf("Failed set snapshot timestamps in operation metadata: %w", err)
	}

	// We need to persist the metadata because this is a durable operation and the information is relied upon in a later stage.
	err = op.Persist()
	if err != nil {
		return fmt.Errorf("Failed persisting snapshot timestamps in operation metadata: %w", err)
	}

	return nil
}

// replicateInstance handles forward replication of a single instance to the
// destination cluster. It handles both instances on the local cluster member
// and instances on other cluster members.
func replicateInstance(ctx context.Context, s *state.State, op *operations.Operation, instanceID int64, dstClient lxd.InstanceServer, targetCert *x509.Certificate, memberAddresses map[string]string) error {
	inst, err := instance.LoadByID(s, int(instanceID))
	if err != nil {
		return err
	}

	instName := inst.Name()
	projectName := inst.Project().Name

	// The source enters the custom volume section only in all-exclusive mode, and the sink defers the
	// devices of missing exclusive volumes only when asked the same way.
	diskVolumesMode := replicationDiskVolumesMode(inst.Project().Config)

	targetCertPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

	// Instance on another cluster member: connect to the hosting cluster member and
	// drive the snapshot (if needed) and push migration through its API so the
	// migration source has direct access to the instance's storage.
	instanceLocation := inst.Location()
	if instanceLocation != s.ServerName {
		memberAddress, ok := memberAddresses[instanceLocation]
		if !ok {
			return fmt.Errorf("Failed resolving cluster member address for instance %q", instName)
		}

		// Connect to the hosting cluster member.
		memberClient, err := cluster.Connect(ctx, memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), false)
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
				Type:            api.SourceTypeMigration,
				Mode:            "push",
				Refresh:         true,
				DiskVolumesMode: diskVolumesMode,
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
			Migration:       true,
			DiskVolumesMode: diskVolumesMode,
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
			Type:            api.SourceTypeMigration,
			Mode:            "push",
			Refresh:         true,
			DiskVolumesMode: diskVolumesMode,
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

	srcMigration, err := newMigrationSource(inst, false, false, false, diskVolumesMode, "", pushTarget)
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

		statuses, err := dbCluster.GetReplicatorStatuses(ctx, tx.Tx(), nil)
		if err != nil {
			return fmt.Errorf("Failed loading last replicator statuses: %w", err)
		}

		apiReplicatorsTx := make([]*api.Replicator, 0, len(replicators))
		for _, replicator := range replicators {
			apiReplicatorsTx = append(apiReplicatorsTx, replicator.ToAPI(allConfigs, statuses))
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
	// Split on ", " (comma+space) to match validate.IsCron, preserving intra-field commas like "0,30 * * * *".
	for _, s := range shared.SplitNTrimSpace(spec, ", ", -1, true) {
		isActive, err := shared.CronSpecIsActiveThisMinute(s, now)
		if err != nil {
			logger.Warn("Failed parsing replicator schedule expression", logger.Ctx{"spec": s, "err": err})
			continue
		}

		if isActive {
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

	// Create a ReplicatorsStatusRow for the replicator to update when it finishes
	var runID int64
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		runID, err = dbCluster.CreateNewReplicatorStatus(ctx, tx.Tx(), row.Row.ID, time.Now(), api.ReplicatorStatusRunning, dbCluster.ReplicatorRunModeScheduled)
		return err
	})
	if err != nil {
		logger.Warn("Failed creating replicator run status data", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project, "err": err})
		return fmt.Errorf("Failed creating replicator run status data: %w", err)
	}

	opArgs, err := prepareReplicatorRunOperationArgs(ctx, s, replicator.Project, replicator.Name, clusterLinkName, false, row.Row.ID, runID)
	if err != nil {
		handleReplicatorSchedulingError(ctx, s, replicator.Project, replicator.Name, row.Row.ID, runID, false, err)
		return err
	}

	op, err := operations.ScheduleServerOperation(s, *opArgs)
	if err != nil {
		handleReplicatorSchedulingError(ctx, s, replicator.Project, replicator.Name, row.Row.ID, runID, false, err)
		if api.StatusErrorCheck(err, http.StatusConflict) {
			logger.Warn("Skipping scheduled replicator, a run is already in progress", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project})
			return nil
		}

		return fmt.Errorf("Failed scheduling replicator operation: %w", err)
	}

	return op.Wait(ctx)
}

// handleReplicatorSchedulingError finalizes or deletes the replicators_status row for the replicator run, depending on the error.
// For conflict errors, a replicator is already running, so we don't want to pollute the API last_run_status of the replicator with a failure
// while waiting for the running operation to finish. For all other errors, the row is set to a failure status and the time is recorded.
func handleReplicatorSchedulingError(inCtx context.Context, s *state.State, projectName string, replicatorName string, replicatorID int64, runID int64, restore bool, scheduleErr error) {
	// Use a background context for most cases. We need to handle this error even if the request context was cancelled.
	ctx := context.Background()

	// If there is a conflict, the replicator is already running. In this case we don't want the last run status to be
	// "failed", because it only failed due to an already running replication job. So we delete the replicators_status_row associated with the run.
	if api.StatusErrorCheck(scheduleErr, http.StatusConflict) {
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return query.DeleteOne[dbCluster.ReplicatorsStatusRow](ctx, tx.Tx(), "WHERE id = ?", runID)
		})
		if err != nil {
			logger.Warn("Failed deleting replicator status data when the operation failed to schedule due to conflict", logger.Ctx{"replicator": replicatorName, "project": projectName})
		}

		return
	}

	// Otherwise, we mark the run as failed, so that we know an attempt was made.
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.FinalizeReplicatorStatus(ctx, tx.Tx(), runID, api.ReplicatorStatusFailed, time.Now(), nil, nil)
	})
	if err != nil {
		logger.Warn("Failed finalizing replicator status data when the operation failed to schedule", logger.Ctx{"replicator": replicatorName, "project": projectName})
	}

	// A restore is a failback rather than a replication run, so it only records its status.
	if restore {
		return
	}

	logger.Error("Replicator run failed to start", logger.Ctx{"replicator": replicatorName, "project": projectName, "err": scheduleErr})

	replicatorRaiseRunFailureWarning(s, projectName, replicatorName, replicatorID, "Replicator run failed to start: "+scheduleErr.Error())

	// Use the input context when creating the lifecycle event data so that the requestor is extracted (if present).
	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorRun.Event(inCtx, replicatorName, projectName, map[string]any{
		"status":           api.ReplicatorStatusFailed,
		"instances_total":  0,
		"instances_failed": 0,
		"err":              scheduleErr.Error(),
	}))
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
