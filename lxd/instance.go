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

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
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
	"github.com/lxc/lxd/shared/logger"
)

// Helper functions

// instanceCreateAsEmpty creates an empty instance.
func instanceCreateAsEmpty(d *Daemon, args db.InstanceArgs) (instance.Instance, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the instance record.
	inst, instOp, err := instance.CreateInternal(d.State(), args, true, revert)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance record: %w", err)
	}
	defer instOp.Done(err)

	pool, err := storagePools.LoadByInstance(d.State(), inst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	err = pool.CreateInstance(inst, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance: %w", err)
	}

	revert.Add(func() { inst.Delete(true) })

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
	_, img, err := s.Cluster.GetImage(hash, db.ImageFilter{Project: &args.Project})
	if err != nil {
		return nil, fmt.Errorf("Fetch image %s from database: %w", hash, err)
	}

	// Set the default profiles if necessary.
	if args.Profiles == nil {
		args.Profiles = img.Profiles
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
	unlock := d.imageDownloadLock(img.Fingerprint)

	nodeAddress, err := s.Cluster.LocateImage(hash)
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
		err = d.cluster.AddImageToLocalNode(args.Project, img.Fingerprint)
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
	inst, instOp, err := instance.CreateInternal(s, args, true, revert)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance record: %w", err)
	}
	defer instOp.Done(nil)

	err = s.Cluster.UpdateImageLastUseDate(hash, time.Now().UTC())
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

	revert.Add(func() { inst.Delete(true) })

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

	revert := revert.New()
	defer revert.Fail()

	if opts.refresh {
		// Load the target instance.
		inst, err = instance.LoadByProjectAndName(s, opts.targetInstance.Project, opts.targetInstance.Name)
		if err != nil {
			opts.refresh = false // Instance doesn't exist, so switch to copy mode.
		}

		if inst.IsRunning() {
			return nil, fmt.Errorf("Cannot refresh a running instance")
		}
	}

	// If we are not in refresh mode, then create a new instance as we are in copy mode.
	if !opts.refresh {
		// Create the instance.
		inst, instOp, err = instance.CreateInternal(s, opts.targetInstance, true, revert)
		if err != nil {
			return nil, fmt.Errorf("Failed creating instance record: %w", err)
		}
		defer instOp.Done(err)
	}

	// At this point we have already figured out the instance's root disk device so we can simply retrieve it
	// from the expanded devices.
	instRootDiskDeviceKey, instRootDiskDevice, err := shared.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	snapList := []*instance.Instance{}
	var snapshots []instance.Instance

	if !opts.instanceOnly {
		if opts.refresh {
			// Compare snapshots.
			syncSnapshots, deleteSnapshots, err := instance.CompareSnapshots(opts.sourceInstance, inst)
			if err != nil {
				return nil, err
			}

			// Delete extra snapshots first.
			for _, snap := range deleteSnapshots {
				err := snap.Delete(true)
				if err != nil {
					return nil, err
				}
			}

			// Only care about the snapshots that need updating.
			snapshots = syncSnapshots
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
					if localRootDiskDev, found := snapLocalDevices[snapExpandedRootDiskDevKey]; found {
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
			} else {
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
			snapInst, snapInstOp, err := instance.CreateInternal(s, snapInstArgs, true, revert)
			if err != nil {
				return nil, fmt.Errorf("Failed creating instance snapshot record %q: %w", newSnapName, err)
			}
			defer snapInstOp.Done(err)

			snapList = append(snapList, &snapInst)
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

		revert.Add(func() { inst.Delete(true) })

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
	// Get all the container arguments
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		filter := db.InstanceTypeFilter(instanceType)
		filter.Project = &project
		cts, err = tx.GetLocalInstancesInProject(filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instance.LoadAllInternal(s, cts)
}

func autoCreateContainerSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Get projects.
		var projects []db.Project
		err := d.State().Cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			projects, err = tx.GetProjects(db.ProjectFilter{})
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			return err
		})
		if err != nil {
			return
		}

		// Load local instances by project
		allInstances := []instance.Instance{}
		for _, p := range projects {
			err = project.AllowSnapshotCreation(&p)
			if err != nil {
				continue
			}

			projectInstances, err := instanceLoadNodeProjectAll(d.State(), p.Name, instancetype.Any)
			if err != nil {
				continue
			}

			allInstances = append(allInstances, projectInstances...)
		}

		// Figure out which need snapshotting (if any)
		instances := []instance.Instance{}
		for _, c := range allInstances {
			schedule, ok := c.ExpandedConfig()["snapshots.schedule"]
			if !ok || schedule == "" {
				continue
			}

			// Check if snapshot is scheduled
			if !snapshotIsScheduledNow(schedule, int64(c.ID())) {
				continue
			}

			// Check if the instance is running
			if shared.IsFalseOrEmpty(c.ExpandedConfig()["snapshots.schedule.stopped"]) && !c.IsRunning() {
				continue
			}

			instances = append(instances, c)
		}

		if len(instances) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return autoCreateContainerSnapshots(ctx, d, instances)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationSnapshotCreate, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start create snapshot operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Creating scheduled container snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to create scheduled container snapshots", log.Ctx{"err": err})
		}

		logger.Info("Done creating scheduled container snapshots")
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

func autoCreateContainerSnapshots(ctx context.Context, d *Daemon, instances []instance.Instance) error {
	// Make the snapshots
	for _, c := range instances {
		ch := make(chan error)
		go func() {
			snapshotName, err := instance.NextSnapshotName(d.State(), c, "snap%d")
			if err != nil {
				logger.Error("Error retrieving next snapshot name", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			expiry, err := shared.GetSnapshotExpiry(time.Now(), c.ExpandedConfig()["snapshots.expiry"])
			if err != nil {
				logger.Error("Error getting expiry date", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			err = c.Snapshot(snapshotName, expiry, false)
			if err != nil {
				logger.Error("Error creating snapshots", log.Ctx{"err": err, "container": c})
			}

			ch <- nil
		}()
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
		// Load all local instances
		allInstances, err := instance.LoadNodeAll(d.State(), instancetype.Any)
		if err != nil {
			logger.Error("Failed to load instances for snapshot expiry", log.Ctx{"err": err})
			return
		}

		// Figure out which need snapshotting (if any)
		expiredSnapshots := []instance.Instance{}
		for _, c := range allInstances {
			snapshots, err := c.Snapshots()
			if err != nil {
				logger.Error("Failed to list instance snapshots", log.Ctx{"err": err, "instance": c.Name(), "project": c.Project()})
				continue
			}

			for _, snapshot := range snapshots {
				// Since zero time causes some issues due to timezones, we check the
				// unix timestamp instead of IsZero().
				if snapshot.ExpiryDate().Unix() <= 0 {
					// Snapshot doesn't expire
					continue
				}

				if time.Now().Unix()-snapshot.ExpiryDate().Unix() >= 0 {
					expiredSnapshots = append(expiredSnapshots, snapshot)
				}
			}
		}

		if len(expiredSnapshots) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return pruneExpiredInstanceSnapshots(ctx, d, expiredSnapshots)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationSnapshotsExpire, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired instance snapshots operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired instance snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to remove expired instance snapshots", log.Ctx{"err": err})
		}

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
		if _, loaded := instSnapshotsPruneRunning.LoadOrStore(snapshot.ID(), struct{}{}); loaded {
			continue // Deletion of this snapshot is already running, skip.
		}

		err := snapshot.Delete(true)
		instSnapshotsPruneRunning.Delete(snapshot.ID())
		if err != nil {
			return fmt.Errorf("Failed to delete expired instance snapshot %q in project %q: %w", snapshot.Name(), snapshot.Project(), err)
		}
	}

	return nil
}
