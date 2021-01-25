package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	liblxc "gopkg.in/lxc/go-lxc.v2"
	cron "gopkg.in/robfig/cron.v2"

	"github.com/flosch/pongo2"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/drivers"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

// Helper functions

// instanceCreateAsEmpty creates an empty instance.
func instanceCreateAsEmpty(d *Daemon, args db.InstanceArgs) (instance.Instance, error) {
	// Create the instance record.
	inst, err := instanceCreateInternal(d.State(), args)
	if err != nil {
		return nil, errors.Wrap(err, "Failed creating instance record")
	}

	revert := true
	defer func() {
		if !revert {
			return
		}

		inst.Delete(true)
	}()

	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
	if err != nil {
		return nil, errors.Wrap(err, "Load instance storage pool")
	}

	err = pool.CreateInstance(inst, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Create instance")
	}

	err = inst.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	revert = false
	return inst, nil
}

// instanceImageTransfer transfers an image from another cluster node.
func instanceImageTransfer(d *Daemon, projectName string, hash string, nodeAddress string) error {
	logger.Debugf("Transferring image %q from node %q", hash, nodeAddress)
	client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), false)
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
func instanceCreateFromImage(d *Daemon, args db.InstanceArgs, hash string, op *operations.Operation) (instance.Instance, error) {
	s := d.State()

	// Get the image properties.
	_, img, err := s.Cluster.GetImage(args.Project, hash, false)
	if err != nil {
		return nil, errors.Wrapf(err, "Fetch image %s from database", hash)
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

	// Check if the image is available locally or it's on another node.
	nodeAddress, err := s.Cluster.LocateImage(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "Locate image %q in the cluster", hash)
	}

	if nodeAddress != "" {
		// The image is available from another node, let's try to import it.
		err = instanceImageTransfer(d, args.Project, img.Fingerprint, nodeAddress)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed transferring image")
		}

		// As the image record already exists in the project, just add the node ID to the image.
		err = d.cluster.AddImageToLocalNode(args.Project, img.Fingerprint)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed adding image to local node")
		}
	}

	// Set the "image.*" keys.
	if img.Properties != nil {
		for k, v := range img.Properties {
			args.Config[fmt.Sprintf("image.%s", k)] = v
		}
	}

	// Set the BaseImage field (regardless of previous value).
	args.BaseImage = hash

	// Create the instance.
	inst, err := instanceCreateInternal(s, args)
	if err != nil {
		return nil, errors.Wrap(err, "Failed creating instance record")
	}

	revert := revert.New()
	defer revert.Fail()
	revert.Add(func() { inst.Delete(true) })

	err = s.Cluster.UpdateImageLastUseDate(hash, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
	if err != nil {
		return nil, errors.Wrap(err, "Load instance storage pool")
	}

	err = pool.CreateInstanceFromImage(inst, hash, op)
	if err != nil {
		return nil, errors.Wrap(err, "Create instance from image")
	}

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
}

// instanceCreateAsCopy create a new instance by copying from an existing instance.
func instanceCreateAsCopy(s *state.State, opts instanceCreateAsCopyOpts, op *operations.Operation) (instance.Instance, error) {
	var inst instance.Instance
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
		inst, err = instanceCreateInternal(s, opts.targetInstance)
		if err != nil {
			return nil, errors.Wrap(err, "Failed creating instance record")
		}

		revert.Add(func() { inst.Delete(true) })
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
			} else if errors.Cause(err) == shared.ErrNoRootDisk {
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
			}

			// Create the snapshots.
			snapInst, err := instanceCreateInternal(s, snapInstArgs)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed creating instance snapshot record %q", newSnapName)
			}

			// Set snapshot creation date to that of the source snapshot.
			err = s.Cluster.UpdateInstanceSnapshotCreationDate(snapInst.ID(), srcSnap.CreationDate())
			if err != nil {
				return nil, err
			}

			snapList = append(snapList, &snapInst)
		}
	}

	pool, err := storagePools.GetPoolByInstance(s, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Load instance storage pool")
	}

	if opts.refresh {
		err = pool.RefreshInstance(inst, opts.sourceInstance, snapshots, op)
		if err != nil {
			return nil, errors.Wrap(err, "Refresh instance")
		}
	} else {
		err = pool.CreateInstanceFromCopy(inst, opts.sourceInstance, !opts.instanceOnly, op)
		if err != nil {
			return nil, errors.Wrap(err, "Create instance from copy")
		}

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

func instanceCreateAsSnapshot(s *state.State, args db.InstanceArgs, sourceInstance instance.Instance, op *operations.Operation) (instance.Instance, error) {
	if sourceInstance.Type() != args.Type {
		return nil, fmt.Errorf("Source instance and snapshot instance types do not match")
	}

	// Deal with state.
	if args.Stateful {
		if !sourceInstance.IsRunning() {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. The instance isn't running")
		}

		_, err := exec.LookPath("criu")
		if err != nil {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. CRIU isn't installed")
		}

		stateDir := sourceInstance.StatePath()
		err = os.MkdirAll(stateDir, 0700)
		if err != nil {
			return nil, err
		}

		/* TODO: ideally we would freeze here and unfreeze below after
		 * we've copied the filesystem, to make sure there are no
		 * changes by the container while snapshotting. Unfortunately
		 * there is abug in CRIU where it doesn't leave the container
		 * in the same state it found it w.r.t. freezing, i.e. CRIU
		 * freezes too, and then /always/ thaws, even if the container
		 * was frozen. Until that's fixed, all calls to Unfreeze()
		 * after snapshotting will fail.
		 */

		criuMigrationArgs := instance.CriuMigrationArgs{
			Cmd:          liblxc.MIGRATE_DUMP,
			StateDir:     stateDir,
			Function:     "snapshot",
			Stop:         false,
			ActionScript: false,
			DumpDir:      "",
			PreDumpDir:   "",
		}

		err = sourceInstance.Migrate(&criuMigrationArgs)
		if err != nil {
			os.RemoveAll(sourceInstance.StatePath())
			return nil, err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// Create the snapshot.
	inst, err := instanceCreateInternal(s, args)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed creating instance snapshot record %q", args.Name)
	}
	revert.Add(func() { inst.Delete(true) })

	pool, err := storagePools.GetPoolByInstance(s, inst)
	if err != nil {
		return nil, err
	}

	err = pool.CreateInstanceSnapshot(inst, sourceInstance, op)
	if err != nil {
		return nil, errors.Wrap(err, "Create instance snapshot")
	}

	// Mount volume for backup.yaml writing.
	_, err = pool.MountInstance(sourceInstance, op)
	if err != nil {
		return nil, errors.Wrap(err, "Create instance snapshot (mount source)")
	}
	defer pool.UnmountInstance(sourceInstance, op)

	// Attempt to update backup.yaml for instance.
	err = sourceInstance.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	// Once we're done, remove the state directory.
	if args.Stateful {
		os.RemoveAll(sourceInstance.StatePath())
	}

	revert.Success()
	return inst, nil
}

// instanceCreateInternal creates an instance record and storage volume record in the database.
func instanceCreateInternal(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	// Set default values.
	if args.Project == "" {
		args.Project = project.Default
	}

	if args.Profiles == nil {
		args.Profiles = []string{"default"}
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
	}

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = s.OS.Architectures[0]
	}

	err := instance.ValidName(args.Name, args.Snapshot)
	if err != nil {
		return nil, err
	}

	if !args.Snapshot {
		// Unset expiry date since instances don't expire.
		args.ExpiryDate = time.Time{}
	}

	// Validate container config.
	err = instance.ValidConfig(s.OS, args.Config, false, false)
	if err != nil {
		return nil, err
	}

	// Validate container devices with the supplied container name and devices.
	err = instance.ValidDevices(s, s.Cluster, args.Project, args.Type, args.Devices, false)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Validate architecture.
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	if !shared.IntInSlice(args.Architecture, s.OS.Architectures) {
		return nil, fmt.Errorf("Requested architecture isn't supported by this host")
	}

	// Validate profiles.
	profiles, err := s.Cluster.GetProfileNames(args.Project)
	if err != nil {
		return nil, err
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile %q doesn't exist", profile)
		}

		if shared.StringInSlice(profile, checkedProfiles) {
			return nil, fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile)
	}

	if args.CreationDate.IsZero() {
		args.CreationDate = time.Now().UTC()
	}

	if args.LastUsedDate.IsZero() {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	var dbInst db.Instance

	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		node, err := tx.GetLocalNodeName()
		if err != nil {
			return err
		}

		// TODO: this check should probably be performed by the db package itself.
		exists, err := tx.ProjectExists(args.Project)
		if err != nil {
			return errors.Wrapf(err, "Check if project %q exists", args.Project)
		}
		if !exists {
			return fmt.Errorf("Project %q does not exist", args.Project)
		}

		if args.Snapshot {
			parts := strings.SplitN(args.Name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]
			instance, err := tx.GetInstance(args.Project, instanceName)
			if err != nil {
				return fmt.Errorf("Get instance %q in project %q", instanceName, args.Project)
			}
			snapshot := db.InstanceSnapshot{
				Project:      args.Project,
				Instance:     instanceName,
				Name:         snapshotName,
				CreationDate: args.CreationDate,
				Stateful:     args.Stateful,
				Description:  args.Description,
				Config:       args.Config,
				Devices:      args.Devices.CloneNative(),
				ExpiryDate:   args.ExpiryDate,
			}
			_, err = tx.CreateInstanceSnapshot(snapshot)
			if err != nil {
				return errors.Wrap(err, "Add snapshot info to the database")
			}

			// Read back the snapshot, to get ID and creation time.
			s, err := tx.GetInstanceSnapshot(args.Project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrap(err, "Fetch created snapshot from the database")
			}

			dbInst = db.InstanceSnapshotToInstance(instance, s)

			return nil
		}

		// Create the instance entry.
		dbInst = db.Instance{
			Project:      args.Project,
			Name:         args.Name,
			Node:         node,
			Type:         args.Type,
			Snapshot:     args.Snapshot,
			Architecture: args.Architecture,
			Ephemeral:    args.Ephemeral,
			CreationDate: args.CreationDate,
			Stateful:     args.Stateful,
			LastUseDate:  args.LastUsedDate,
			Description:  args.Description,
			Config:       args.Config,
			Devices:      args.Devices.CloneNative(),
			Profiles:     args.Profiles,
			ExpiryDate:   args.ExpiryDate,
		}

		_, err = tx.CreateInstance(dbInst)
		if err != nil {
			return errors.Wrap(err, "Add instance info to the database")
		}

		// Read back the instance, to get ID and creation time.
		dbRow, err := tx.GetInstance(args.Project, args.Name)
		if err != nil {
			return errors.Wrap(err, "Fetch created instance from the database")
		}

		dbInst = *dbRow

		if dbInst.ID < 1 {
			return errors.Wrapf(err, "Unexpected instance database ID %d", dbInst.ID)
		}

		return nil
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			thing := "Instance"
			if shared.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}
			return nil, fmt.Errorf("%s %q already exists", thing, args.Name)
		}
		return nil, err
	}

	revert := true
	defer func() {
		if !revert {
			return
		}

		s.Cluster.DeleteInstance(dbInst.Project, dbInst.Name)
	}()

	args = db.InstanceToArgs(&dbInst)
	inst, err := instance.Create(s, args)
	if err != nil {
		logger.Error("Failed initialising instance", log.Ctx{"project": args.Project, "instance": args.Name, "type": args.Type, "err": err})
		return nil, errors.Wrap(err, "Failed initialising instance")
	}

	// Wipe any existing log for this instance name.
	os.RemoveAll(inst.LogPath())

	revert = false
	return inst, nil
}

// Load all instances of this nodes under the given project.
func instanceLoadNodeProjectAll(s *state.State, project string, instanceType instancetype.Type) ([]instance.Instance, error) {
	// Get all the container arguments
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.GetLocalInstancesInProject(project, instanceType)
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
		// Load all local instances
		allContainers, err := instance.LoadNodeAll(d.State(), instancetype.Any)
		if err != nil {
			logger.Error("Failed to load containers for scheduled snapshots", log.Ctx{"err": err})
			return
		}

		// Figure out which need snapshotting (if any)
		instances := []instance.Instance{}
		for _, c := range allContainers {
			schedule := c.ExpandedConfig()["snapshots.schedule"]

			if schedule == "" {
				continue
			}

			// Extend our schedule to one that is accepted by the used cron parser
			sched, err := cron.Parse(fmt.Sprintf("* %s", schedule))
			if err != nil {
				continue
			}

			// Check if it's time to snapshot
			now := time.Now()

			// Truncate the time now back to the start of the minute, before passing to
			// the cron scheduler, as it will add 1s to the scheduled time and we don't
			// want the next scheduled time to roll over to the next minute and break
			// the time comparison below.
			now = now.Truncate(time.Minute)

			// Calculate the next scheduled time based on the snapshots.schedule
			// pattern and the time now.
			next := sched.Next(now)

			// Ignore everything that is more precise than minutes.
			next = next.Truncate(time.Minute)

			if !now.Equal(next) {
				continue
			}

			// Check if the container is running
			if !shared.IsTrue(c.ExpandedConfig()["snapshots.schedule.stopped"]) && !c.IsRunning() {
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

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationSnapshotCreate, nil, nil, opRun, nil, nil)
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
			snapshotName, err := instanceDetermineNextSnapshotName(d, c, "snap%d")
			if err != nil {
				logger.Error("Error retrieving next snapshot name", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			snapshotName = fmt.Sprintf("%s%s%s", c.Name(), shared.SnapshotDelimiter, snapshotName)

			expiry, err := shared.GetSnapshotExpiry(time.Now(), c.ExpandedConfig()["snapshots.expiry"])
			if err != nil {
				logger.Error("Error getting expiry date", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			args := db.InstanceArgs{
				Architecture: c.Architecture(),
				Config:       c.LocalConfig(),
				Type:         c.Type(),
				Snapshot:     true,
				Devices:      c.LocalDevices(),
				Ephemeral:    c.IsEphemeral(),
				Name:         snapshotName,
				Profiles:     c.Profiles(),
				Project:      c.Project(),
				Stateful:     false,
				ExpiryDate:   expiry,
			}

			_, err = instanceCreateAsSnapshot(d.State(), args, c, nil)
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

func pruneExpiredContainerSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
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
			return pruneExpiredContainerSnapshots(ctx, d, expiredSnapshots)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationSnapshotsExpire, nil, nil, opRun, nil, nil)
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

func pruneExpiredContainerSnapshots(ctx context.Context, d *Daemon, snapshots []instance.Instance) error {
	// Find snapshots to delete
	for _, snapshot := range snapshots {
		err := snapshot.Delete(true)
		if err != nil {
			return errors.Wrapf(err, "Failed to delete expired instance snapshot '%s' in project '%s'", snapshot.Name(), snapshot.Project())
		}
	}

	return nil
}

func instanceDetermineNextSnapshotName(d *Daemon, c instance.Instance, defaultPattern string) (string, error) {
	var err error

	pattern := c.ExpandedConfig()["snapshots.pattern"]
	if pattern == "" {
		pattern = defaultPattern
	}

	pattern, err = shared.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		i := d.cluster.GetNextInstanceSnapshotIndex(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := c.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = fmt.Sprintf("%s-%%d", pattern)
		i := d.cluster.GetNextInstanceSnapshotIndex(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}

var instanceDriversCacheVal atomic.Value
var instanceDriversCacheLock sync.Mutex

func readInstanceDriversCache() map[string]string {
	drivers := instanceDriversCacheVal.Load()
	if drivers == nil {
		createInstanceDriversCache()
		drivers = instanceDriversCacheVal.Load()
	}

	return drivers.(map[string]string)
}

func createInstanceDriversCache() {
	// Create the list of instance drivers in use on this LXD instance
	// namely LXC and QEMU. Given that LXC and QEMU cannot update while
	// the LXD instance is running, only one cache is ever needed.

	data := map[string]string{}

	info := drivers.SupportedInstanceDrivers()
	for _, entry := range info {
		if entry.Version != "" {
			data[entry.Name] = entry.Version
		}
	}

	// Store the value in the cache
	instanceDriversCacheLock.Lock()
	instanceDriversCacheVal.Store(data)
	instanceDriversCacheLock.Unlock()

	return
}
