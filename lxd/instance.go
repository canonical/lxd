package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// Helper functions

// instanceCreateAsEmpty creates an empty instance.
func instanceCreateAsEmpty(d *Daemon, args db.InstanceArgs) (instance.Instance, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the instance record.
	inst, instOp, cleanup, err := instance.CreateInternal(d.State(), args, true)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance record: %w", err)
	}

	revert.Add(cleanup)
	defer instOp.Done(err)

	pool, err := storagePools.LoadByInstance(d.State(), inst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	err = pool.CreateInstance(inst, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance: %w", err)
	}

	revert.Add(func() { _ = inst.Delete(true) })

	err = inst.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	revert.Success()
	return inst, nil
}

// instanceImageTransfer transfers an image from another cluster node.
func instanceImageTransfer(d *Daemon, r *http.Request, projectName string, hash string, nodeAddress string) error {
	logger.Debugf("Transferring image %q from node %q", hash, nodeAddress)
	client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), d.serverCert(), r, false)
	if err != nil {
		return err
	}

	client = client.UseProject(projectName)

	err = imageImportFromNode(filepath.Join(d.os.VarDir, "images"), client, hash)
	if err != nil {
		return err
	}

	return nil
}

// instanceCreateFromImage creates an instance from a rootfs image.
func instanceCreateFromImage(d *Daemon, r *http.Request, args db.InstanceArgs, hash string, op *operations.Operation) (instance.Instance, error) {
	revert := revert.New()
	defer revert.Fail()

	s := d.State()

	// Get the image properties.
	var img *api.Image
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		_, img, err = tx.GetImageByFingerprintPrefix(ctx, hash, dbCluster.ImageFilter{Project: &args.Project})
		if err != nil {
			return fmt.Errorf("Fetch image %s from database: %w", hash, err)
		}

		// Set the default profiles if necessary.
		if args.Profiles == nil {
			args.Profiles = make([]api.Profile, 0, len(img.Profiles))
			profiles, err := dbCluster.GetProfilesIfEnabled(ctx, tx.Tx(), args.Project, img.Profiles)
			if err != nil {
				return err
			}

			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				args.Profiles = append(args.Profiles, *apiProfile)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Validate the type of the image matches the type of the instance.
	imgType, err := instancetype.New(img.Type)
	if err != nil {
		return nil, err
	}

	if imgType != args.Type {
		return nil, fmt.Errorf("Requested image's type '%s' doesn't match instance type '%s'", imgType, args.Type)
	}

	// Check if the image is available locally or it's on another member.
	// Ensure we are the only ones operating on this image. Otherwise another instance created at the same
	// time may also arrive at the conclusion that the image doesn't exist on this cluster member and then
	// think it needs to download the image and store the record in the database as well, which will lead to
	// duplicate record errors.
	unlock := d.imageOperationLock(img.Fingerprint)

	nodeAddress, err := s.DB.Cluster.LocateImage(hash)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("Locate image %q in the cluster: %w", hash, err)
	}

	if nodeAddress != "" {
		// The image is available from another node, let's try to import it.
		err = instanceImageTransfer(d, r, args.Project, img.Fingerprint, nodeAddress)
		if err != nil {
			unlock()
			return nil, fmt.Errorf("Failed transferring image %q from %q: %w", img.Fingerprint, nodeAddress, err)
		}

		// As the image record already exists in the project, just add the node ID to the image.
		err = d.db.Cluster.AddImageToLocalNode(args.Project, img.Fingerprint)
		if err != nil {
			unlock()
			return nil, fmt.Errorf("Failed adding transferred image %q record to local cluster member: %w", img.Fingerprint, err)
		}
	}

	unlock() // Image is available locally.

	// Set the "image.*" keys.
	if img.Properties != nil {
		for k, v := range img.Properties {
			args.Config[fmt.Sprintf("image.%s", k)] = v
		}
	}

	// Set the BaseImage field (regardless of previous value).
	args.BaseImage = hash

	// Create the instance.
	inst, instOp, cleanup, err := instance.CreateInternal(s, args, true)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance record: %w", err)
	}

	revert.Add(cleanup)
	defer instOp.Done(nil)

	err = s.DB.Cluster.UpdateImageLastUseDate(args.Project, hash, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	pool, err := storagePools.LoadByInstance(d.State(), inst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	err = pool.CreateInstanceFromImage(inst, hash, op)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance from image: %w", err)
	}

	revert.Add(func() { _ = inst.Delete(true) })

	err = inst.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	revert.Success()
	return inst, nil
}

// instanceCreateAsCopyOpts options for copying an instance.
type instanceCreateAsCopyOpts struct {
	sourceInstance       instance.Instance // Source instance.
	targetInstance       db.InstanceArgs   // Configuration for new instance.
	instanceOnly         bool              // Only copy the instance and not it's snapshots.
	refresh              bool              // Refresh an existing target instance.
	applyTemplateTrigger bool              // Apply deferred TemplateTriggerCopy.
	allowInconsistent    bool              // Ignore some copy errors
}

// instanceCreateAsCopy create a new instance by copying from an existing instance.
func instanceCreateAsCopy(s *state.State, opts instanceCreateAsCopyOpts, op *operations.Operation) (instance.Instance, error) {
	var inst instance.Instance
	var instOp *operationlock.InstanceOperation
	var err error
	var cleanup revert.Hook

	revert := revert.New()
	defer revert.Fail()

	if opts.refresh {
		// Load the target instance.
		inst, err = instance.LoadByProjectAndName(s, opts.targetInstance.Project, opts.targetInstance.Name)
		if err != nil {
			opts.refresh = false // Instance doesn't exist, so switch to copy mode.
		}
	}

	// If we are not in refresh mode, then create a new instance as we are in copy mode.
	if !opts.refresh {
		// Create the instance.
		inst, instOp, cleanup, err = instance.CreateInternal(s, opts.targetInstance, true)
		if err != nil {
			return nil, fmt.Errorf("Failed creating instance record: %w", err)
		}

		revert.Add(cleanup)
	} else {
		instOp, err = inst.LockExclusive()
		if err != nil {
			return nil, fmt.Errorf("Failed getting exclusive access to instance: %w", err)
		}
	}

	defer instOp.Done(err)

	// At this point we have already figured out the instance's root disk device so we can simply retrieve it
	// from the expanded devices.
	instRootDiskDeviceKey, instRootDiskDevice, err := shared.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	var snapshots []instance.Instance

	if !opts.instanceOnly {
		if opts.refresh {
			// Compare snapshots.
			sourceSnaps, err := opts.sourceInstance.Snapshots()
			if err != nil {
				return nil, err
			}

			sourceSnapshotComparable := make([]storagePools.ComparableSnapshot, 0, len(sourceSnaps))
			for _, sourceSnap := range sourceSnaps {
				_, sourceSnapName, _ := api.GetParentAndSnapshotName(sourceSnap.Name())

				sourceSnapshotComparable = append(sourceSnapshotComparable, storagePools.ComparableSnapshot{
					Name:         sourceSnapName,
					CreationDate: sourceSnap.CreationDate(),
				})
			}

			targetSnaps, err := inst.Snapshots()
			if err != nil {
				return nil, err
			}

			targetSnapshotsComparable := make([]storagePools.ComparableSnapshot, 0, len(targetSnaps))
			for _, targetSnap := range targetSnaps {
				_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name())

				targetSnapshotsComparable = append(targetSnapshotsComparable, storagePools.ComparableSnapshot{
					Name:         targetSnapName,
					CreationDate: targetSnap.CreationDate(),
				})
			}

			syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := storagePools.CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

			// Delete extra snapshots first.
			for _, deleteTargetSnapIndex := range deleteTargetSnapshotIndexes {
				err := targetSnaps[deleteTargetSnapIndex].Delete(true)
				if err != nil {
					return nil, err
				}
			}

			// Only send the snapshots that need updating.
			snapshots = make([]instance.Instance, 0, len(syncSourceSnapshotIndexes))
			for _, syncSourceSnapIndex := range syncSourceSnapshotIndexes {
				snapshots = append(snapshots, sourceSnaps[syncSourceSnapIndex])
			}
		} else {
			// Get snapshots of source instance.
			snapshots, err = opts.sourceInstance.Snapshots()
			if err != nil {
				return nil, err
			}
		}

		for _, srcSnap := range snapshots {
			snapLocalDevices := srcSnap.LocalDevices().Clone()

			// Load snap root disk from expanded devices (in case it doesn't have its own root disk).
			snapExpandedRootDiskDevKey, snapExpandedRootDiskDev, err := shared.GetRootDiskDevice(srcSnap.ExpandedDevices().CloneNative())
			if err == nil {
				// If the expanded devices has a root disk, but its pool doesn't match our new
				// parent instance's pool, then either modify the device if it is local or add a
				// new one to local devices if its coming from the profiles.
				if snapExpandedRootDiskDev["pool"] != instRootDiskDevice["pool"] {
					localRootDiskDev, found := snapLocalDevices[snapExpandedRootDiskDevKey]
					if found {
						// Modify exist local device's pool.
						localRootDiskDev["pool"] = instRootDiskDevice["pool"]
						snapLocalDevices[snapExpandedRootDiskDevKey] = localRootDiskDev
					} else {
						// Add a new local device using parent instance's pool.
						snapLocalDevices[instRootDiskDeviceKey] = map[string]string{
							"type": "disk",
							"path": "/",
							"pool": instRootDiskDevice["pool"],
						}
					}
				}
			} else if errors.Is(err, shared.ErrNoRootDisk) {
				// If no root disk defined in either local devices or profiles, then add one to the
				// snapshot local devices using the same device name from the parent instance.
				snapLocalDevices[instRootDiskDeviceKey] = map[string]string{
					"type": "disk",
					"path": "/",
					"pool": instRootDiskDevice["pool"],
				}
			} else { //nolint:staticcheck // (keep the empty branch for the comment)
				// Snapshot has multiple root disk devices, we can't automatically fix this so
				// leave alone so we don't prevent copy.
			}

			fields := strings.SplitN(srcSnap.Name(), shared.SnapshotDelimiter, 2)
			newSnapName := fmt.Sprintf("%s/%s", inst.Name(), fields[1])
			snapInstArgs := db.InstanceArgs{
				Architecture: srcSnap.Architecture(),
				Config:       srcSnap.LocalConfig(),
				Type:         opts.sourceInstance.Type(),
				Snapshot:     true,
				Devices:      snapLocalDevices,
				Description:  srcSnap.Description(),
				Ephemeral:    srcSnap.IsEphemeral(),
				Name:         newSnapName,
				Profiles:     srcSnap.Profiles(),
				Project:      opts.targetInstance.Project,
				ExpiryDate:   srcSnap.ExpiryDate(),
				CreationDate: srcSnap.CreationDate(),
			}

			// Create the snapshots.
			_, snapInstOp, cleanup, err := instance.CreateInternal(s, snapInstArgs, true)
			if err != nil {
				return nil, fmt.Errorf("Failed creating instance snapshot record %q: %w", newSnapName, err)
			}

			revert.Add(cleanup)
			defer snapInstOp.Done(err)
		}
	}

	// Copy the storage volume.
	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	if opts.refresh {
		err = pool.RefreshInstance(inst, opts.sourceInstance, snapshots, opts.allowInconsistent, op)
		if err != nil {
			return nil, fmt.Errorf("Refresh instance: %w", err)
		}
	} else {
		err = pool.CreateInstanceFromCopy(inst, opts.sourceInstance, !opts.instanceOnly, opts.allowInconsistent, op)
		if err != nil {
			return nil, fmt.Errorf("Create instance from copy: %w", err)
		}

		revert.Add(func() { _ = inst.Delete(true) })

		if opts.applyTemplateTrigger {
			// Trigger the templates on next start.
			err = inst.DeferTemplateApply(instance.TemplateTriggerCopy)
			if err != nil {
				return nil, err
			}
		}
	}

	err = inst.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	revert.Success()
	return inst, nil
}

// Load all instances of this nodes under the given project.
func instanceLoadNodeProjectAll(s *state.State, project string, instanceType instancetype.Type) ([]instance.Instance, error) {
	var err error
	var instances []instance.Instance

	filter := dbCluster.InstanceFilter{Type: instanceType.Filter(), Project: &project}
	if s.ServerName != "" {
		filter.Node = &s.ServerName
	}

	err = s.DB.Cluster.InstanceList(func(dbInst db.InstanceArgs, p api.Project) error {
		inst, err := instance.Load(s, dbInst, p)
		if err != nil {
			return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
		}

		instances = append(instances, inst)

		return nil
	}, filter)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

func autoCreateInstanceSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		var instanceArgs map[int]db.InstanceArgs
		projects := make(map[string]*api.Project)

		// Get eligible instances.
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Get all projects.
			allProjects, err := dbCluster.GetProjects(context.Background(), tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			dbInstances := []dbCluster.Instance{}

			// Filter projects that aren't allowed to have snapshots.
			for _, dbProject := range allProjects {
				p, err := dbProject.ToAPI(context.Background(), tx.Tx())
				if err != nil {
					return err
				}

				err = project.AllowSnapshotCreation(p)
				if err != nil {
					continue
				}

				projects[p.Name] = p

				// Get instances.
				filter := dbCluster.InstanceFilter{Project: &p.Name}
				entries, err := tx.GetLocalInstancesInProject(ctx, filter)
				if err != nil {
					return err
				}

				dbInstances = append(dbInstances, entries...)
			}

			instanceArgs, err = tx.InstancesToInstanceArgs(ctx, true, dbInstances...)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return
		}

		// Figure out which need snapshotting (if any).
		instances := make([]instance.Instance, 0)
		for _, instArg := range instanceArgs {
			inst, err := instance.Load(s, instArg, *projects[instArg.Project])
			if err != nil {
				logger.Error("Failed loading instance for snapshot task", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
				continue
			}

			schedule, ok := inst.ExpandedConfig()["snapshots.schedule"]
			if !ok || schedule == "" {
				continue
			}

			// Check if snapshot is scheduled.
			if !snapshotIsScheduledNow(schedule, int64(inst.ID())) {
				continue
			}

			// Check if the instance is running.
			if shared.IsFalseOrEmpty(inst.ExpandedConfig()["snapshots.schedule.stopped"]) && !inst.IsRunning() {
				continue
			}

			instances = append(instances, inst)
		}

		if len(instances) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return autoCreateInstanceSnapshots(ctx, d, instances)
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.SnapshotCreate, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start create snapshot operation", logger.Ctx{"err": err})
			return
		}

		logger.Info("Creating scheduled instance snapshots")

		err = op.Start()
		if err != nil {
			logger.Error("Failed creating scheduled instance snapshots", logger.Ctx{"err": err})
		}

		_, _ = op.Wait(ctx)
		logger.Info("Done creating scheduled instance snapshots")
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func autoCreateInstanceSnapshots(ctx context.Context, d *Daemon, instances []instance.Instance) error {
	// Make the snapshots.
	for _, inst := range instances {
		ch := make(chan error)
		go func(inst instance.Instance) {
			l := logger.AddContext(logger.Log, logger.Ctx{"project": inst.Project(), "instance": inst.Name()})

			snapshotName, err := instance.NextSnapshotName(d.State(), inst, "snap%d")
			if err != nil {
				l.Error("Error retrieving next snapshot name", logger.Ctx{"err": err})
				ch <- nil
				return
			}

			expiry, err := shared.GetExpiry(time.Now(), inst.ExpandedConfig()["snapshots.expiry"])
			if err != nil {
				logger.Error("Error getting expiry date")
				ch <- nil
				return
			}

			err = inst.Snapshot(snapshotName, expiry, false)
			if err != nil {
				logger.Error("Error creating snapshots", logger.Ctx{"err": err})
			}

			ch <- nil
		}(inst)
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

func pruneExpiredInstanceSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		var expiredSnapshotInstances []instance.Instance

		// Load local expired snapshots.
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			snapshots, err := tx.GetLocalExpiredInstanceSnapshots(ctx)
			if err != nil {
				return err
			}

			if len(snapshots) == 0 {
				return nil
			}

			expiredSnapshots := make([]dbCluster.Instance, 0, len(snapshots))
			instances := make(map[string]*dbCluster.Instance, 0)

			for _, snapshot := range snapshots {
				instanceKey := snapshot.Project + "/" + snapshot.Instance
				instance, ok := instances[instanceKey]
				if !ok {
					instance, err = dbCluster.GetInstance(ctx, tx.Tx(), snapshot.Project, snapshot.Instance)
					if err != nil {
						return err
					}

					instances[instanceKey] = instance
				}

				expiredSnapshots = append(expiredSnapshots, snapshot.ToInstance(instance.Name, instance.Node, instance.Type, instance.Architecture))
			}

			snapshotArgs, err := tx.InstancesToInstanceArgs(ctx, true, expiredSnapshots...)
			if err != nil {
				return fmt.Errorf("Failed loading expired instance snapshots: %w", err)
			}

			projects := make(map[string]*api.Project)

			expiredSnapshotInstances = make([]instance.Instance, 0)
			for _, snapshotArg := range snapshotArgs {
				// Load project if not already loaded.
				p, found := projects[snapshotArg.Project]
				if !found {
					dbProject, err := dbCluster.GetProject(context.Background(), tx.Tx(), snapshotArg.Project)
					if err != nil {
						return err
					}

					p, err = dbProject.ToAPI(ctx, tx.Tx())
					if err != nil {
						return err
					}
				}

				inst, err := instance.Load(s, snapshotArg, *p)
				if err != nil {
					logger.Error("Failed loading instance for snapshot prune task", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
					continue
				}

				expiredSnapshotInstances = append(expiredSnapshotInstances, inst)
			}

			return nil
		})
		if err != nil {
			logger.Error("Failed getting expired instance snapshots", logger.Ctx{"err": err})
			return
		}

		// Skip if no expired snapshots.
		if len(expiredSnapshotInstances) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return pruneExpiredInstanceSnapshots(ctx, d, expiredSnapshotInstances)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, operationtype.SnapshotsExpire, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired instance snapshots operation", logger.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired instance snapshots")

		err = op.Start()
		if err != nil {
			logger.Error("Failed to remove expired instance snapshots", logger.Ctx{"err": err})
		}

		_, _ = op.Wait(ctx)
		logger.Info("Done pruning expired instance snapshots")
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

var instSnapshotsPruneRunning = sync.Map{}

func pruneExpiredInstanceSnapshots(ctx context.Context, d *Daemon, snapshots []instance.Instance) error {
	// Find snapshots to delete
	for _, snapshot := range snapshots {
		_, loaded := instSnapshotsPruneRunning.LoadOrStore(snapshot.ID(), struct{}{})
		if loaded {
			continue // Deletion of this snapshot is already running, skip.
		}

		err := snapshot.Delete(true)
		instSnapshotsPruneRunning.Delete(snapshot.ID())
		if err != nil {
			return fmt.Errorf("Failed to delete expired instance snapshot %q in project %q: %w", snapshot.Name(), snapshot.Project().Name, err)
		}

		logger.Debug("Deleted instance snapshot", logger.Ctx{"project": snapshot.Project(), "snapshot": snapshot.Name()})
	}

	return nil
}
