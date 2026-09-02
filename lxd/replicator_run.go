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
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
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

	// Finalization operation input. This updates the replicator row with the run status.
	durableOperationInputKeyReplicatorID operations.InputKey = "replicator_id"
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
		return "", nil, nil, nil, fmt.Errorf("Failed loading cluster link name from operation inputs: %w", err)
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

func replicatorFinalizeDurableOperationRunHook(_ context.Context, op *operations.Operation) error {
	replicatorID, err := operations.GetOperationInputValue[int64](op, durableOperationInputKeyReplicatorID)
	if err != nil {
		return fmt.Errorf("Failed getting replicator ID from operation inputs: %w", err)
	}

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
	return op.State().DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.FinalizeReplicatorStatus(ctx, tx.Tx(), replicatorID, runStatus, time.Now())
	})
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

	return snapshotInstance(ctx, op.State(), instanceID, memberAddresses)
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

		if child.Status() == api.Failure {
			return errors.New("Skipping instance replication due to failed snapshot")
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
				Type:    api.SourceTypeMigration,
				Mode:    "push",
				Refresh: true,
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
			Migration: true,
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
			Type:    api.SourceTypeMigration,
			Mode:    "push",
			Refresh: true,
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
		Migration: true,
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
func prepareReplicatorRunOperationArgs(ctx context.Context, s *state.State, projectName string, name string, clusterLinkName string, restore bool, replicatorID int64) (*operations.OperationArgs, error) {
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
			})
			if err != nil {
				return nil, fmt.Errorf("Failed preparing instance forward replication operation: %w", err)
			}
		}

		return finalizeReplicatorRunOperationArgs(builder, projectName, replicatorURL, replicatorID)
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

	return finalizeReplicatorRunOperationArgs(builder, projectName, replicatorURL, replicatorID)
}

func finalizeReplicatorRunOperationArgs(builder *operations.BulkArgBuilder, projectName string, replicatorURL *api.URL, replicatorID int64) (*operations.OperationArgs, error) {
	builder.IncrementStage()
	err := builder.AddChildArgs(operations.OperationArgs{
		ProjectName: projectName,
		Type:        operationtype.ReplicatorFinalize,
		Class:       operationtype.OperationClassDurable,
		EntityURL:   replicatorURL,
	}, map[operations.InputKey]any{
		durableOperationInputKeyReplicatorID: replicatorID,
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

func snapshotInstance(ctx context.Context, s *state.State, instanceID int64, memberAddresses map[string]string) error {
	inst, err := instance.LoadByID(s, int(instanceID))
	if err != nil {
		return err
	}

	instName := inst.Name()
	projectName := inst.Project().Name
	// Snapshotting is unconditional; the only exception is when the instance already has a
	// snapshot schedule defined, since scheduled snapshots provide point-in-time history so
	// an extra one here would be redundant.
	createSnapshot := inst.ExpandedConfig()["snapshots.schedule"] == ""
	if !createSnapshot {
		return nil
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

		lastStatuses, err := dbCluster.GetLastReplicatorStatuses(ctx, tx.Tx(), nil)
		if err != nil {
			return fmt.Errorf("Failed loading last replicator statuses: %w", err)
		}

		apiReplicatorsTx := make([]*api.Replicator, 0, len(replicators))
		for _, replicator := range replicators {
			apiReplicatorsTx = append(apiReplicatorsTx, replicator.ToAPI(allConfigs, lastStatuses))
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

	opArgs, err := prepareReplicatorRunOperationArgs(ctx, s, replicator.Project, replicator.Name, clusterLinkName, false, row.Row.ID)
	if err != nil {
		return err
	}

	// Set status to Running before scheduling the operation. The operation's RunHook writes
	// the terminal status (Completed/Failed) when it finishes. If the project has no instances,
	// the RunHook can complete synchronously inside ScheduleServerOperation before it returns,
	// writing the terminal status first. By setting Running here, that terminal write always
	// comes after Running, so the status is never left stuck at Running.
	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.CreateNewReplicatorStatus(ctx, tx.Tx(), row.Row.ID, time.Now(), api.ReplicatorStatusRunning, dbCluster.ReplicatorRunModeScheduled)
	})
	if err != nil {
		logger.Warn("Failed updating replicator last run status to running", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project, "err": err})
	}

	op, err := operations.ScheduleServerOperation(s, *opArgs)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			logger.Warn("Skipping scheduled replicator, a run is already in progress", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project})
			// Don't revert Running: another operation is in progress and owns the status;
			// it will write its own terminal state when it completes.
			return nil
		}

		// Revert Running to Failed so the status doesn't get stuck.
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.FinalizeReplicatorStatus(ctx, tx.Tx(), row.Row.ID, api.ReplicatorStatusFailed, time.Now())
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
