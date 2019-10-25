package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	lxc "gopkg.in/lxc/go-lxc.v2"
	cron "gopkg.in/robfig/cron.v2"

	"github.com/flosch/pongo2"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/units"
)

func init() {
	// Expose instanceLoadNodeAll to the device package converting the response to a slice of Instances.
	// This is because container types are defined in the main package and are not importable.
	device.InstanceLoadNodeAll = func(s *state.State) ([]device.Instance, error) {
		containers, err := instanceLoadNodeAll(s)
		if err != nil {
			return nil, err
		}

		identifiers := []device.Instance{}
		for _, v := range containers {
			identifiers = append(identifiers, device.Instance(v))
		}

		return identifiers, nil
	}

	// Expose instanceLoadByProjectAndName to the device package converting the response to an Instance.
	// This is because container types are defined in the main package and are not importable.
	device.InstanceLoadByProjectAndName = func(s *state.State, project, name string) (device.Instance, error) {
		container, err := instanceLoadByProjectAndName(s, project, name)
		if err != nil {
			return nil, err
		}

		return device.Instance(container), nil
	}

	// Expose instanceLoadById to the backup package converting the response to an Instance.
	// This is because container types are defined in the main package and are not importable.
	backup.InstanceLoadByID = func(s *state.State, id int) (backup.Instance, error) {
		instance, err := instanceLoadById(s, id)
		if err != nil {
			return nil, err
		}

		return backup.Instance(instance), nil
	}
}

// Helper functions

func containerValidName(name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		return fmt.Errorf(
			"The character '%s' is reserved for snapshots.",
			shared.SnapshotDelimiter)
	}

	if !shared.ValidHostname(name) {
		return fmt.Errorf("Container name isn't a valid hostname")
	}

	return nil
}

func containerValidConfigKey(os *sys.OS, key string, value string) error {
	f, err := shared.ConfigKeyChecker(key)
	if err != nil {
		return err
	}
	if err = f(value); err != nil {
		return err
	}
	if key == "raw.lxc" {
		return lxcValidConfig(value)
	}
	if key == "security.syscalls.blacklist_compat" {
		for _, arch := range os.Architectures {
			if arch == osarch.ARCH_64BIT_INTEL_X86 ||
				arch == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN ||
				arch == osarch.ARCH_64BIT_POWERPC_BIG_ENDIAN {
				return nil
			}
		}
		return fmt.Errorf("security.syscalls.blacklist_compat isn't supported on this architecture")
	}
	return nil
}

func allowedUnprivilegedOnlyMap(rawIdmap string) error {
	rawMaps, err := parseRawIdmap(rawIdmap)
	if err != nil {
		return err
	}

	for _, ent := range rawMaps {
		if ent.Hostid == 0 {
			return fmt.Errorf("Cannot map root user into container as LXD was configured to only allow unprivileged containers")
		}
	}

	return nil
}

func containerValidConfig(sysOS *sys.OS, config map[string]string, profile bool, expanded bool) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if profile && strings.HasPrefix(k, "volatile.") {
			return fmt.Errorf("Volatile keys can only be set on containers")
		}

		if profile && strings.HasPrefix(k, "image.") {
			return fmt.Errorf("Image keys can only be set on containers")
		}

		err := containerValidConfigKey(sysOS, k, v)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, whitelist := config["security.syscalls.whitelist"]
	_, blacklist := config["security.syscalls.blacklist"]
	blacklistDefault := shared.IsTrue(config["security.syscalls.blacklist_default"])
	blacklistCompat := shared.IsTrue(config["security.syscalls.blacklist_compat"])

	if rawSeccomp && (whitelist || blacklist || blacklistDefault || blacklistCompat) {
		return fmt.Errorf("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if whitelist && (blacklist || blacklistDefault || blacklistCompat) {
		return fmt.Errorf("security.syscalls.whitelist is mutually exclusive with security.syscalls.blacklist*")
	}

	if expanded && (config["security.privileged"] == "" || !shared.IsTrue(config["security.privileged"])) && sysOS.IdmapSet == nil {
		return fmt.Errorf("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported")
	}

	unprivOnly := os.Getenv("LXD_UNPRIVILEGED_ONLY")
	if shared.IsTrue(unprivOnly) {
		if config["raw.idmap"] != "" {
			err := allowedUnprivilegedOnlyMap(config["raw.idmap"])
			if err != nil {
				return err
			}
		}

		if shared.IsTrue(config["security.privileged"]) {
			return fmt.Errorf("LXD was configured to only allow unprivileged containers")
		}
	}

	return nil
}

// containerValidDevices validate container device configs.
func containerValidDevices(state *state.State, cluster *db.Cluster, instanceName string, devices deviceConfig.Devices, expanded bool) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	// Create a temporary containerLXC struct to use as an Instance in device validation.
	// Populate it's name, localDevices and expandedDevices properties based on the mode of
	// validation occurring. In non-expanded validation expensive checks should be avoided.
	instance := &containerLXC{
		name:         instanceName,
		localDevices: devices.Clone(), // Prevent devices from modifying their config.
	}

	if expanded {
		instance.expandedDevices = instance.localDevices // Avoid another clone.
	}

	// Check each device individually using the device package.
	for name, config := range devices {
		_, err := device.New(instance, state, name, config, nil, nil)
		if err != nil {
			return err
		}

	}

	// Check we have a root disk if in expanded validation mode.
	if expanded {
		_, _, err := shared.GetRootDiskDevice(devices.CloneNative())
		if err != nil {
			return errors.Wrap(err, "Detect root disk device")
		}
	}

	return nil
}

// The container interface
type container interface {
	Instance

	/* actionScript here is a script called action.sh in the stateDir, to
	 * be passed to CRIU as --action-script
	 */
	Migrate(args *CriuMigrationArgs) error

	ConsoleLog(opts lxc.ConsoleLogOptions) (string, error)

	// Status
	IsNesting() bool

	// Hooks
	OnStart() error
	OnStopNS(target string, netns string) error
	OnStop(target string) error

	InsertSeccompUnixDevice(prefix string, m deviceConfig.Device, pid int) error

	CurrentIdmap() (*idmap.IdmapSet, error)
	DiskIdmap() (*idmap.IdmapSet, error)
	NextIdmap() (*idmap.IdmapSet, error)
}

// Loader functions
func containerCreateAsEmpty(d *Daemon, args db.InstanceArgs) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d.State(), args)
	if err != nil {
		return nil, err
	}

	// Now create the empty storage
	err = c.Storage().ContainerCreate(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	// Apply any post-storage configuration
	err = containerConfigureInternal(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateFromBackup(s *state.State, info backup.Info, data *os.File,
	customPool bool) (storage, error) {
	var pool storage
	var fixBackupFile = false

	// Get storage pool from index.yaml
	pool, storageErr := storagePoolInit(s, info.Pool)
	if storageErr != nil && errors.Cause(storageErr) != db.ErrNoSuchObject {
		// Unexpected error
		return nil, storageErr
	}

	if errors.Cause(storageErr) == db.ErrNoSuchObject {
		// The pool doesn't exist, and the backup is in binary format so we
		// cannot alter the backup.yaml.
		if info.HasBinaryFormat {
			return nil, storageErr
		}

		// Get the default profile
		_, profile, err := s.Cluster.ProfileGet(info.Project, "default")
		if err != nil {
			return nil, errors.Wrap(err, "Failed to get default profile")
		}

		_, v, err := shared.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to get root disk device")
		}

		// Use the default-profile's root pool
		pool, err = storagePoolInit(s, v["pool"])
		if err != nil {
			return nil, errors.Wrap(err, "Failed to initialize storage pool")
		}

		fixBackupFile = true
	}

	// Find the compression algorithm
	tarArgs, ext, decompressionArgs, err := shared.DetectCompressionFile(data)
	if err != nil {
		return nil, err
	}
	data.Seek(0, 0)

	if ext == ".squashfs" {
		data, err = shared.DecompressInPlace(data, decompressionArgs)
		if err != nil {
			return nil, err
		}
	}

	// Unpack tarball
	err = pool.ContainerBackupLoad(info, data, tarArgs)
	if err != nil {
		return nil, err
	}

	if fixBackupFile || customPool {
		// Update the pool
		err = backupFixStoragePool(s.Cluster, info, !customPool)
		if err != nil {
			return nil, err
		}
	}

	return pool, nil
}

func containerCreateEmptySnapshot(s *state.State, args db.InstanceArgs) (container, error) {
	// Create the snapshot
	c, err := containerCreateInternal(s, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty snapshot
	err = c.Storage().ContainerSnapshotCreateEmpty(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateFromImage(d *Daemon, args db.InstanceArgs, hash string, tracker *ioprogress.ProgressTracker) (container, error) {
	s := d.State()

	// Get the image properties
	_, img, err := s.Cluster.ImageGet(args.Project, hash, false, false)
	if err != nil {
		return nil, errors.Wrapf(err, "Fetch image %s from database", hash)
	}

	// Validate the type of the image matches the type of the instance.
	imgType, err := instancetype.New(img.Type)
	if err != nil {
		return nil, err
	}

	if imgType != args.Type {
		return nil, fmt.Errorf("Requested image doesn't match instance type")
	}

	// Check if the image is available locally or it's on another node.
	nodeAddress, err := s.Cluster.ImageLocate(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "Locate image %s in the cluster", hash)
	}
	if nodeAddress != "" {
		// The image is available from another node, let's try to
		// import it.
		logger.Debugf("Transferring image %s from node %s", hash, nodeAddress)
		client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), false)
		if err != nil {
			return nil, err
		}

		client = client.UseProject(args.Project)

		err = imageImportFromNode(filepath.Join(d.os.VarDir, "images"), client, hash)
		if err != nil {
			return nil, err
		}

		err = d.cluster.ImageAssociateNode(args.Project, hash)
		if err != nil {
			return nil, err
		}
	}

	// Set the "image.*" keys
	if img.Properties != nil {
		for k, v := range img.Properties {
			args.Config[fmt.Sprintf("image.%s", k)] = v
		}
	}

	// Set the BaseImage field (regardless of previous value)
	args.BaseImage = hash

	// Create the container
	c, err := containerCreateInternal(s, args)
	if err != nil {
		return nil, errors.Wrap(err, "Create container")
	}

	err = s.Cluster.ImageLastAccessUpdate(hash, time.Now().UTC())
	if err != nil {
		c.Delete()
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Now create the storage from an image
	err = c.Storage().ContainerCreateFromImage(c, hash, tracker)
	if err != nil {
		c.Delete()
		return nil, errors.Wrap(err, "Create container from image")
	}

	// Apply any post-storage configuration
	err = containerConfigureInternal(c)
	if err != nil {
		c.Delete()
		return nil, errors.Wrap(err, "Configure container")
	}

	return c, nil
}

func containerCreateAsCopy(s *state.State, args db.InstanceArgs, sourceContainer Instance, containerOnly bool, refresh bool) (Instance, error) {
	var ct Instance
	var err error

	if refresh {
		// Load the target container
		ct, err = instanceLoadByProjectAndName(s, args.Project, args.Name)
		if err != nil {
			refresh = false
		}
	}

	if !refresh {
		// Create the container.
		ct, err = containerCreateInternal(s, args)
		if err != nil {
			return nil, err
		}
	}

	if refresh && ct.IsRunning() {
		return nil, fmt.Errorf("Cannot refresh a running container")
	}

	// At this point we have already figured out the parent
	// container's root disk device so we can simply
	// retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := ct.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices.CloneNative())
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	csList := []*container{}
	var snapshots []Instance

	if !containerOnly {
		if refresh {
			// Compare snapshots
			syncSnapshots, deleteSnapshots, err := containerCompareSnapshots(sourceContainer, ct)
			if err != nil {
				return nil, err
			}

			// Delete extra snapshots
			for _, snap := range deleteSnapshots {
				err := snap.Delete()
				if err != nil {
					return nil, err
				}
			}

			// Only care about the snapshots that need updating
			snapshots = syncSnapshots
		} else {
			// Get snapshots of source container
			snapshots, err = sourceContainer.Snapshots()
			if err != nil {
				ct.Delete()

				return nil, err
			}
		}

		for _, snap := range snapshots {
			fields := strings.SplitN(snap.Name(), shared.SnapshotDelimiter, 2)

			// Ensure that snapshot and parent container have the
			// same storage pool in their local root disk device.
			// If the root disk device for the snapshot comes from a
			// profile on the new instance as well we don't need to
			// do anything.
			snapDevices := snap.LocalDevices()
			if snapDevices != nil {
				snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapDevices.CloneNative())
				if snapLocalRootDiskDeviceKey != "" {
					snapDevices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
				} else {
					snapDevices["root"] = map[string]string{
						"type": "disk",
						"path": "/",
						"pool": parentStoragePool,
					}
				}
			}

			newSnapName := fmt.Sprintf("%s/%s", ct.Name(), fields[1])
			csArgs := db.InstanceArgs{
				Architecture: snap.Architecture(),
				Config:       snap.LocalConfig(),
				Type:         sourceContainer.Type(),
				Snapshot:     true,
				Devices:      snapDevices,
				Description:  snap.Description(),
				Ephemeral:    snap.IsEphemeral(),
				Name:         newSnapName,
				Profiles:     snap.Profiles(),
				Project:      args.Project,
			}

			// Create the snapshots.
			cs, err := containerCreateInternal(s, csArgs)
			if err != nil {
				if !refresh {
					ct.Delete()
				}

				return nil, err
			}

			// Restore snapshot creation date
			err = s.Cluster.ContainerCreationUpdate(cs.ID(), snap.CreationDate())
			if err != nil {
				if !refresh {
					ct.Delete()
				}

				return nil, err
			}

			csList = append(csList, &cs)
		}
	}

	// Now clone or refresh the storage
	if refresh {
		err = ct.Storage().ContainerRefresh(ct, sourceContainer, snapshots)
		if err != nil {
			return nil, err
		}
	} else {
		err = ct.Storage().ContainerCopy(ct, sourceContainer, containerOnly)
		if err != nil {
			if !refresh {
				ct.Delete()
			}

			return nil, err
		}
	}

	// Apply any post-storage configuration.
	err = containerConfigureInternal(ct)
	if err != nil {
		if !refresh {
			ct.Delete()
		}

		return nil, err
	}

	if !containerOnly {
		for _, cs := range csList {
			// Apply any post-storage configuration.
			err = containerConfigureInternal(*cs)
			if err != nil {
				if !refresh {
					ct.Delete()
				}

				return nil, err
			}
		}
	}

	return ct, nil
}

func containerCreateAsSnapshot(s *state.State, args db.InstanceArgs, sourceInstance Instance) (Instance, error) {
	if sourceInstance.Type() != instancetype.Container {
		return nil, fmt.Errorf("Instance not container type")
	}

	// Deal with state
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

		criuMigrationArgs := CriuMigrationArgs{
			cmd:          lxc.MIGRATE_DUMP,
			stateDir:     stateDir,
			function:     "snapshot",
			stop:         false,
			actionScript: false,
			dumpDir:      "",
			preDumpDir:   "",
		}

		c := sourceInstance.(container)
		err = c.Migrate(&criuMigrationArgs)
		if err != nil {
			os.RemoveAll(sourceInstance.StatePath())
			return nil, err
		}
	}

	// Create the snapshot
	c, err := containerCreateInternal(s, args)
	if err != nil {
		return nil, err
	}

	// Clone the container
	err = sourceInstance.Storage().ContainerSnapshotCreate(c, sourceInstance)
	if err != nil {
		c.Delete()
		return nil, err
	}

	// Attempt to update backup.yaml on container
	ourStart, err := sourceInstance.StorageStart()
	if err != nil {
		c.Delete()
		return nil, err
	}
	if ourStart {
		defer sourceInstance.StorageStop()
	}

	err = writeBackupFile(sourceInstance)
	if err != nil {
		c.Delete()
		return nil, err
	}

	// Once we're done, remove the state directory
	if args.Stateful {
		os.RemoveAll(sourceInstance.StatePath())
	}

	s.Events.SendLifecycle(sourceInstance.Project(), "container-snapshot-created",
		fmt.Sprintf("/1.0/containers/%s", sourceInstance.Name()),
		map[string]interface{}{
			"snapshot_name": args.Name,
		})

	return c, nil
}

func containerCreateInternal(s *state.State, args db.InstanceArgs) (container, error) {
	// Set default values
	if args.Project == "" {
		args.Project = "default"
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

	// Validate container name if not snapshot (as snapshots use disallowed / char in names).
	if !args.Snapshot {
		err := containerValidName(args.Name)
		if err != nil {
			return nil, err
		}

		// Unset expiry date since containers don't expire
		args.ExpiryDate = time.Time{}
	}

	// Validate container config
	err := containerValidConfig(s.OS, args.Config, false, false)
	if err != nil {
		return nil, err
	}

	// Validate container devices with the supplied container name and devices.
	err = containerValidDevices(s, s.Cluster, args.Name, args.Devices, false)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Validate architecture
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	if !shared.IntInSlice(args.Architecture, s.OS.Architectures) {
		return nil, fmt.Errorf("Requested architecture isn't supported by this host")
	}

	// Validate profiles
	profiles, err := s.Cluster.Profiles(args.Project)
	if err != nil {
		return nil, err
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile '%s' doesn't exist", profile)
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

	var container db.Instance
	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		node, err := tx.NodeName()
		if err != nil {
			return err
		}

		// TODO: this check should probably be performed by the db
		// package itself.
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
			instance, err := tx.InstanceGet(args.Project, instanceName)
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
			_, err = tx.InstanceSnapshotCreate(snapshot)
			if err != nil {
				return errors.Wrap(err, "Add snapshot info to the database")
			}

			// Read back the snapshot, to get ID and creation time.
			s, err := tx.InstanceSnapshotGet(args.Project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrap(err, "Fetch created snapshot from the database")
			}

			container = db.InstanceSnapshotToInstance(instance, s)

			return nil
		}

		// Create the container entry
		container = db.Instance{
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

		_, err = tx.InstanceCreate(container)
		if err != nil {
			return errors.Wrap(err, "Add container info to the database")
		}

		// Read back the container, to get ID and creation time.
		c, err := tx.InstanceGet(args.Project, args.Name)
		if err != nil {
			return errors.Wrap(err, "Fetch created container from the database")
		}

		container = *c

		if container.ID < 1 {
			return errors.Wrapf(err, "Unexpected container database ID %d", container.ID)
		}

		return nil
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			thing := "Container"
			if shared.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}
			return nil, fmt.Errorf("%s '%s' already exists", thing, args.Name)
		}
		return nil, err
	}

	// Wipe any existing log for this container name
	os.RemoveAll(shared.LogPath(args.Name))

	args = db.ContainerToArgs(&container)

	// Setup the container struct and finish creation (storage and idmap)
	c, err := containerLXCCreate(s, args)
	if err != nil {
		s.Cluster.ContainerRemove(args.Project, args.Name)
		return nil, errors.Wrap(err, "Create LXC container")
	}

	return c, nil
}

func containerConfigureInternal(c Instance) error {
	// Find the root device
	_, rootDiskDevice, err := shared.GetRootDiskDevice(c.ExpandedDevices().CloneNative())
	if err != nil {
		return err
	}

	ourStart, err := c.StorageStart()
	if err != nil {
		return err
	}

	// handle quota: at this point, storage is guaranteed to be ready
	storage := c.Storage()
	if rootDiskDevice["size"] != "" {
		storageTypeName := storage.GetStorageTypeName()
		if (storageTypeName == "lvm" || storageTypeName == "ceph") && c.IsRunning() {
			err = c.VolatileSet(map[string]string{"volatile.apply_quota": rootDiskDevice["size"]})
			if err != nil {
				return err
			}
		} else {
			size, err := units.ParseByteSizeString(rootDiskDevice["size"])
			if err != nil {
				return err
			}

			err = storage.StorageEntitySetQuota(storagePoolVolumeTypeContainer, size, c)
			if err != nil {
				return err
			}
		}
	}

	if ourStart {
		defer c.StorageStop()
	}

	err = writeBackupFile(c)
	if err != nil {
		return err
	}

	return nil
}

func instanceLoadById(s *state.State, id int) (Instance, error) {
	// Get the DB record
	project, name, err := s.Cluster.ContainerProjectAndName(id)
	if err != nil {
		return nil, err
	}

	return instanceLoadByProjectAndName(s, project, name)
}

func instanceLoadByProjectAndName(s *state.State, project, name string) (Instance, error) {
	// Get the DB record
	var container *db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		if strings.Contains(name, shared.SnapshotDelimiter) {
			parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]

			instance, err := tx.InstanceGet(project, instanceName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
			}

			snapshot, err := tx.InstanceSnapshotGet(project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch snapshot %q of instance %q in project %q", snapshotName, instanceName, project)
			}

			c := db.InstanceSnapshotToInstance(instance, snapshot)
			container = &c
		} else {
			container, err = tx.InstanceGet(project, name)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch container %q in project %q", name, project)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	args := db.ContainerToArgs(container)
	inst, err := instanceLoad(s, args, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load container")
	}

	return inst, nil
}

func instanceLoadByProject(s *state.State, project string) ([]Instance, error) {
	// Get all the containers
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.InstanceFilter{
			Project: project,
			Type:    instancetype.Container,
		}
		var err error
		cts, err = tx.InstanceList(filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

// Load all instances across all projects.
func instanceLoadFromAllProjects(s *state.State) ([]Instance, error) {
	var projects []string

	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		projects, err = tx.ProjectNames()
		return err
	})
	if err != nil {
		return nil, err
	}

	instances := []Instance{}
	for _, project := range projects {
		projectInstances, err := instanceLoadByProject(s, project)
		if err != nil {
			return nil, errors.Wrapf(nil, "Load instances in project %s", project)
		}
		instances = append(instances, projectInstances...)
	}

	return instances, nil
}

// Legacy interface.
func instanceLoadAll(s *state.State) ([]Instance, error) {
	return instanceLoadByProject(s, "default")
}

// Load all instances of this nodes.
func instanceLoadNodeAll(s *state.State) ([]Instance, error) {
	// Get all the container arguments
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.ContainerNodeList()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

// Load all instances of this nodes under the given project.
func instanceLoadNodeProjectAll(s *state.State, project string, instanceType instancetype.Type) ([]Instance, error) {
	// Get all the container arguments
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.ContainerNodeProjectList(project, instanceType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

func instanceLoadAllInternal(dbInstances []db.Instance, s *state.State) ([]Instance, error) {
	// Figure out what profiles are in use
	profiles := map[string]map[string]api.Profile{}
	for _, instArgs := range dbInstances {
		projectProfiles, ok := profiles[instArgs.Project]
		if !ok {
			projectProfiles = map[string]api.Profile{}
			profiles[instArgs.Project] = projectProfiles
		}
		for _, profile := range instArgs.Profiles {
			_, ok := projectProfiles[profile]
			if !ok {
				projectProfiles[profile] = api.Profile{}
			}
		}
	}

	// Get the profile data
	for project, projectProfiles := range profiles {
		for name := range projectProfiles {
			_, profile, err := s.Cluster.ProfileGet(project, name)
			if err != nil {
				return nil, err
			}

			projectProfiles[name] = *profile
		}
	}

	// Load the instances structs
	instances := []Instance{}
	for _, dbInstance := range dbInstances {
		// Figure out the instances's profiles
		cProfiles := []api.Profile{}
		for _, name := range dbInstance.Profiles {
			cProfiles = append(cProfiles, profiles[dbInstance.Project][name])
		}

		args := db.ContainerToArgs(&dbInstance)
		inst, err := instanceLoad(s, args, cProfiles)
		if err != nil {
			return nil, err
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

// instanceLoad creates the underlying instance type struct and returns it as an Instance.
func instanceLoad(s *state.State, args db.InstanceArgs, cProfiles []api.Profile) (Instance, error) {
	var inst Instance
	var err error

	if args.Type == instancetype.Container {
		inst, err = containerLXCLoad(s, args, cProfiles)
	} else {
		return nil, fmt.Errorf("Invalid instance type for instance %s", args.Name)
	}

	if err != nil {
		return nil, err
	}

	return inst, nil
}

func containerCompareSnapshots(source Instance, target Instance) ([]Instance, []Instance, error) {
	// Get the source snapshots
	sourceSnapshots, err := source.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Get the target snapshots
	targetSnapshots, err := target.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Compare source and target
	sourceSnapshotsTime := map[string]time.Time{}
	targetSnapshotsTime := map[string]time.Time{}

	toDelete := []Instance{}
	toSync := []Instance{}

	for _, snap := range sourceSnapshots {
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())

		sourceSnapshotsTime[snapName] = snap.CreationDate()
	}

	for _, snap := range targetSnapshots {
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())

		targetSnapshotsTime[snapName] = snap.CreationDate()
		existDate, exists := sourceSnapshotsTime[snapName]
		if !exists {
			toDelete = append(toDelete, snap)
		} else if existDate != snap.CreationDate() {
			toDelete = append(toDelete, snap)
		}
	}

	for _, snap := range sourceSnapshots {
		_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())

		existDate, exists := targetSnapshotsTime[snapName]
		if !exists || existDate != snap.CreationDate() {
			toSync = append(toSync, snap)
		}
	}

	return toSync, toDelete, nil
}

func autoCreateContainerSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Load all local instances
		allContainers, err := instanceLoadNodeAll(d.State())
		if err != nil {
			logger.Error("Failed to load containers for scheduled snapshots", log.Ctx{"err": err})
			return
		}

		// Figure out which need snapshotting (if any)
		instances := []Instance{}
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

func autoCreateContainerSnapshots(ctx context.Context, d *Daemon, instances []Instance) error {
	// Make the snapshots
	for _, c := range instances {
		ch := make(chan error)
		go func() {
			snapshotName, err := containerDetermineNextSnapshotName(d, c, "snap%d")
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

			_, err = containerCreateAsSnapshot(d.State(), args, c)
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
		allInstances, err := instanceLoadNodeAll(d.State())
		if err != nil {
			logger.Error("Failed to load instances for snapshot expiry", log.Ctx{"err": err})
			return
		}

		// Figure out which need snapshotting (if any)
		expiredSnapshots := []Instance{}
		for _, c := range allInstances {
			snapshots, err := c.Snapshots()
			if err != nil {
				logger.Error("Failed to list snapshots", log.Ctx{"err": err, "container": c.Name(), "project": c.Project()})
				continue
			}

			for _, snapshot := range snapshots {
				if snapshot.ExpiryDate().IsZero() {
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
			logger.Error("Failed to start expired snapshots operation", log.Ctx{"err": err})
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

func pruneExpiredContainerSnapshots(ctx context.Context, d *Daemon, snapshots []Instance) error {
	// Find snapshots to delete
	for _, snapshot := range snapshots {
		err := snapshot.Delete()
		if err != nil {
			return errors.Wrapf(err, "Failed to delete expired snapshot '%s' in project '%s'", snapshot.Name(), snapshot.Project())
		}
	}

	return nil
}

func containerDetermineNextSnapshotName(d *Daemon, c Instance, defaultPattern string) (string, error) {
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
		i := d.cluster.ContainerNextSnapshot(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := c.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = fmt.Sprintf("%s-%%d", pattern)
		i := d.cluster.ContainerNextSnapshot(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}
