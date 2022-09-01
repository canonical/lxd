package drivers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/device/nictype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// ErrInstanceIsStopped indicates that the instance is stopped.
var ErrInstanceIsStopped error = fmt.Errorf("The instance is already stopped")

// deviceManager is an interface that allows managing device lifecycle.
type deviceManager interface {
	deviceAdd(dev device.Device, instanceRunning bool) error
	deviceRemove(dev device.Device, instanceRunning bool) error
	deviceStart(dev device.Device, instanceRunning bool) (*deviceConfig.RunConfig, error)
	deviceStop(dev device.Device, instanceRunning bool, stopHookNetnsPath string) error
}

// common provides structure common to all instance types.
type common struct {
	op    *operations.Operation
	state *state.State

	architecture    int
	creationDate    time.Time
	dbType          instancetype.Type
	description     string
	ephemeral       bool
	expandedConfig  map[string]string
	expandedDevices deviceConfig.Devices
	expiryDate      time.Time
	id              int
	lastUsedDate    time.Time
	localConfig     map[string]string
	localDevices    deviceConfig.Devices
	logger          logger.Logger
	name            string
	node            string
	profiles        []api.Profile
	project         string
	snapshot        bool
	stateful        bool

	// Cached handles.
	// Do not use these variables directly, instead use their associated get functions so they
	// will be initialised on demand.
	storagePool storagePools.Pool
}

//
// SECTION: property getters
//

// Architecture returns the instance's architecture.
func (d *common) Architecture() int {
	return d.architecture
}

// CreationDate returns the instance's creation date.
func (d *common) CreationDate() time.Time {
	return d.creationDate
}

// Type returns the instance's type.
func (d *common) Type() instancetype.Type {
	return d.dbType
}

// Description returns the instance's description.
func (d *common) Description() string {
	return d.description
}

// IsEphemeral returns whether the instanc is ephemeral or not.
func (d *common) IsEphemeral() bool {
	return d.ephemeral
}

// ExpandedConfig returns instance's expanded config.
func (d *common) ExpandedConfig() map[string]string {
	return d.expandedConfig
}

// ExpandedDevices returns instance's expanded device config.
func (d *common) ExpandedDevices() deviceConfig.Devices {
	return d.expandedDevices
}

// ExpiryDate returns when this snapshot expires.
func (d *common) ExpiryDate() time.Time {
	if d.snapshot {
		return d.expiryDate
	}

	// Return zero time if the instance is not a snapshot.
	return time.Time{}
}

// ID gets instances's ID.
func (d *common) ID() int {
	return d.id
}

// LastUsedDate returns the instance's last used date.
func (d *common) LastUsedDate() time.Time {
	return d.lastUsedDate
}

// LocalConfig returns the instance's local config.
func (d *common) LocalConfig() map[string]string {
	return d.localConfig
}

// LocalDevices returns the instance's local device config.
func (d *common) LocalDevices() deviceConfig.Devices {
	return d.localDevices
}

// Name returns the instance's name.
func (d *common) Name() string {
	return d.name
}

// CloudInitID returns the cloud-init instance-id.
func (d *common) CloudInitID() string {
	id := d.LocalConfig()["volatile.cloud-init.instance-id"]
	if id != "" {
		return id
	}

	return d.name
}

// Location returns instance's location.
func (d *common) Location() string {
	return d.node
}

// Profiles returns the instance's profiles.
func (d *common) Profiles() []api.Profile {
	return d.profiles
}

// Project returns instance's project.
func (d *common) Project() string {
	return d.project
}

// IsSnapshot returns whether instance is snapshot or not.
func (d *common) IsSnapshot() bool {
	return d.snapshot
}

// IsStateful returns whether instance is stateful or not.
func (d *common) IsStateful() bool {
	return d.stateful
}

// Operation returns the instance's current operation.
func (d *common) Operation() *operations.Operation {
	return d.op
}

//
// SECTION: general functions
//

// Backups returns a list of backups.
func (d *common) Backups() ([]backup.InstanceBackup, error) {
	// Get all the backups
	backupNames, err := d.state.DB.Cluster.GetInstanceBackups(d.project, d.name)
	if err != nil {
		return nil, err
	}

	// Build the backup list
	backups := []backup.InstanceBackup{}
	for _, backupName := range backupNames {
		backup, err := instance.BackupLoadByName(d.state, d.project, backupName)
		if err != nil {
			return nil, err
		}

		backups = append(backups, *backup)
	}

	return backups, nil
}

// DeferTemplateApply records a template trigger to apply on next instance start.
func (d *common) DeferTemplateApply(trigger instance.TemplateTrigger) error {
	// Avoid over-writing triggers that have already been set.
	if d.localConfig["volatile.apply_template"] != "" {
		return nil
	}

	err := d.VolatileSet(map[string]string{"volatile.apply_template": string(trigger)})
	if err != nil {
		return fmt.Errorf("Failed to set apply_template volatile key: %w", err)
	}

	return nil
}

// SetOperation sets the current operation.
func (d *common) SetOperation(op *operations.Operation) {
	d.op = op
}

// Snapshots returns a list of snapshots.
func (d *common) Snapshots() ([]instance.Instance, error) {
	if d.snapshot {
		return []instance.Instance{}, nil
	}

	var snapshotArgs map[int]db.InstanceArgs

	// Get all the snapshots for instance.
	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.InstanceSnapshotFilter{
			Project:  &d.project,
			Instance: &d.name,
		}

		dbSnapshots, err := dbCluster.GetInstanceSnapshots(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		dbInstances := make([]dbCluster.Instance, len(dbSnapshots))
		for i, s := range dbSnapshots {
			dbInstances[i] = s.ToInstance(d.name, d.node, d.dbType, d.architecture)
		}

		snapshotArgs, err = tx.InstancesToInstanceArgs(ctx, false, dbInstances...)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	snapshots := make([]instance.Instance, 0, len(snapshotArgs))
	for _, snapshotArg := range snapshotArgs {
		// Populate profile info that was already loaded.
		snapshotArg.Profiles = d.profiles

		snapInst, err := instance.Load(d.state, snapshotArg, nil)
		if err != nil {
			return nil, err
		}

		snapshots = append(snapshots, instance.Instance(snapInst))
	}

	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshots[i].CreationDate().Before(snapshots[j].CreationDate())
	})

	return snapshots, nil
}

// VolatileSet sets one or more volatile config keys.
func (d *common) VolatileSet(changes map[string]string) error {
	// Quick check.
	for key := range changes {
		if !strings.HasPrefix(key, shared.ConfigVolatilePrefix) {
			return fmt.Errorf("Only volatile keys can be modified with VolatileSet")
		}
	}

	// Update the database.
	var err error
	if d.snapshot {
		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceSnapshotConfig(d.id, changes)
		})
	} else {
		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceConfig(d.id, changes)
		})
	}

	if err != nil {
		return fmt.Errorf("Failed to set volatile config: %w", err)
	}

	// Apply the change locally.
	for key, value := range changes {
		if value == "" {
			delete(d.expandedConfig, key)
			delete(d.localConfig, key)
			continue
		}

		d.expandedConfig[key] = value
		d.localConfig[key] = value
	}

	return nil
}

//
// SECTION: path getters
//

// ConsoleBufferLogPath returns the instance's console buffer log path.
func (d *common) ConsoleBufferLogPath() string {
	return filepath.Join(d.LogPath(), "console.log")
}

// DevicesPath returns the instance's devices path.
func (d *common) DevicesPath() string {
	name := project.Instance(d.project, d.name)
	return shared.VarPath("devices", name)
}

// LogPath returns the instance's log path.
func (d *common) LogPath() string {
	name := project.Instance(d.project, d.name)
	return shared.LogPath(name)
}

// Path returns the instance's path.
func (d *common) Path() string {
	return storagePools.InstancePath(d.dbType, d.project, d.name, d.snapshot)
}

// RootfsPath returns the instance's rootfs path.
func (d *common) RootfsPath() string {
	return filepath.Join(d.Path(), "rootfs")
}

// ShmountsPath returns the instance's shared mounts path.
func (d *common) ShmountsPath() string {
	name := project.Instance(d.project, d.name)
	return shared.VarPath("shmounts", name)
}

// StatePath returns the instance's state path.
func (d *common) StatePath() string {
	return filepath.Join(d.Path(), "state")
}

// TemplatesPath returns the instance's templates path.
func (d *common) TemplatesPath() string {
	return filepath.Join(d.Path(), "templates")
}

// StoragePool returns the storage pool name.
func (d *common) StoragePool() (string, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return "", err
	}

	return pool.Name(), nil
}

//
// SECTION: internal functions
//

// deviceVolatileReset resets a device's volatile data when its removed or updated in such a way
// that it is removed then added immediately afterwards.
func (d *common) deviceVolatileReset(devName string, oldConfig, newConfig deviceConfig.Device) error {
	volatileClear := make(map[string]string)
	devicePrefix := fmt.Sprintf("volatile.%s.", devName)

	newNICType, err := nictype.NICType(d.state, d.project, newConfig)
	if err != nil {
		return err
	}

	oldNICType, err := nictype.NICType(d.state, d.project, oldConfig)
	if err != nil {
		return err
	}

	// If the device type has changed, remove all old volatile keys.
	// This will occur if the newConfig is empty (i.e the device is actually being removed) or
	// if the device type is being changed but keeping the same name.
	if newConfig["type"] != oldConfig["type"] || newNICType != oldNICType {
		for k := range d.localConfig {
			if !strings.HasPrefix(k, devicePrefix) {
				continue
			}

			volatileClear[k] = ""
		}

		return d.VolatileSet(volatileClear)
	}

	// If the device type remains the same, then just remove any volatile keys that have
	// the same key name present in the new config (i.e the new config is replacing the
	// old volatile key).
	for k := range d.localConfig {
		if !strings.HasPrefix(k, devicePrefix) {
			continue
		}

		devKey := strings.TrimPrefix(k, devicePrefix)
		_, found := newConfig[devKey]
		if found {
			volatileClear[k] = ""
		}
	}

	return d.VolatileSet(volatileClear)
}

// deviceVolatileGetFunc returns a function that retrieves a named device's volatile config and
// removes its device prefix from the keys.
func (d *common) deviceVolatileGetFunc(devName string) func() map[string]string {
	return func() map[string]string {
		volatile := make(map[string]string)
		prefix := fmt.Sprintf("volatile.%s.", devName)
		for k, v := range d.localConfig {
			if strings.HasPrefix(k, prefix) {
				volatile[strings.TrimPrefix(k, prefix)] = v
			}
		}
		return volatile
	}
}

// deviceVolatileSetFunc returns a function that can be called to save a named device's volatile
// config using keys that do not have the device's name prefixed.
func (d *common) deviceVolatileSetFunc(devName string) func(save map[string]string) error {
	return func(save map[string]string) error {
		volatileSave := make(map[string]string)
		for k, v := range save {
			volatileSave[fmt.Sprintf("volatile.%s.%s", devName, k)] = v
		}

		return d.VolatileSet(volatileSave)
	}
}

// expandConfig applies the config of each profile in order, followed by the local config.
func (d *common) expandConfig() error {
	d.expandedConfig = db.ExpandInstanceConfig(d.localConfig, d.profiles)
	d.expandedDevices = db.ExpandInstanceDevices(d.localDevices, d.profiles)

	return nil
}

// restartCommon handles the common part of instance restarts.
func (d *common) restartCommon(inst instance.Instance, timeout time.Duration) error {
	// Setup a new operation for the stop/shutdown phase.
	op, err := operationlock.Create(d.Project(), d.Name(), operationlock.ActionRestart, true, true)
	if err != nil {
		return fmt.Errorf("Create restart operation: %w", err)
	}

	// Handle ephemeral instances.
	ephemeral := inst.IsEphemeral()
	if ephemeral {
		// Unset ephemeral flag
		args := db.InstanceArgs{
			Architecture: inst.Architecture(),
			Config:       inst.LocalConfig(),
			Description:  inst.Description(),
			Devices:      inst.LocalDevices(),
			Ephemeral:    false,
			Profiles:     inst.Profiles(),
			Project:      inst.Project(),
			Type:         inst.Type(),
			Snapshot:     inst.IsSnapshot(),
		}

		err := inst.Update(args, false)
		if err != nil {
			return err
		}

		// On function return, set the flag back on
		defer func() {
			args.Ephemeral = ephemeral
			_ = inst.Update(args, false)
		}()
	}

	if timeout == 0 {
		err := inst.Stop(false)
		if err != nil {
			op.Done(err)
			return err
		}
	} else {
		if inst.IsFrozen() {
			err = fmt.Errorf("Instance is not running")
			op.Done(err)
			return err
		}

		err := inst.Shutdown(timeout * time.Second)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Setup a new operation for the start phase.
	op, err = operationlock.Create(d.Project(), d.Name(), operationlock.ActionRestart, true, true)
	if err != nil {
		return fmt.Errorf("Create restart (for start) operation: %w", err)
	}

	err = inst.Start(false)
	if err != nil {
		op.Done(err)
		return err
	}

	return nil
}

// runHooks executes the callback functions returned from a function.
func (d *common) runHooks(hooks []func() error) error {
	// Run any post start hooks.
	for _, hook := range hooks {
		err := hook()
		if err != nil {
			return err
		}
	}

	return nil
}

// snapshot handles the common part of the snapshoting process.
func (d *common) snapshotCommon(inst instance.Instance, name string, expiry time.Time, stateful bool) error {
	revert := revert.New()
	defer revert.Fail()

	// Setup the arguments.
	args := db.InstanceArgs{
		Project:      inst.Project(),
		Architecture: inst.Architecture(),
		Config:       inst.LocalConfig(),
		Type:         inst.Type(),
		Snapshot:     true,
		Devices:      inst.LocalDevices(),
		Ephemeral:    inst.IsEphemeral(),
		Name:         inst.Name() + shared.SnapshotDelimiter + name,
		Profiles:     inst.Profiles(),
		Stateful:     stateful,
		ExpiryDate:   expiry,
	}

	// Create the snapshot.
	snap, snapInstOp, cleanup, err := instance.CreateInternal(d.state, args, true)
	if err != nil {
		return fmt.Errorf("Failed creating instance snapshot record %q: %w", name, err)
	}

	revert.Add(cleanup)
	defer snapInstOp.Done(err)

	pool, err := storagePools.LoadByInstance(d.state, snap)
	if err != nil {
		return err
	}

	err = pool.CreateInstanceSnapshot(snap, inst, d.op)
	if err != nil {
		return fmt.Errorf("Create instance snapshot: %w", err)
	}

	revert.Add(func() { _ = snap.Delete(true) })

	// Mount volume for backup.yaml writing.
	_, err = pool.MountInstance(inst, d.op)
	if err != nil {
		return fmt.Errorf("Create instance snapshot (mount source): %w", err)
	}

	defer func() { _ = pool.UnmountInstance(inst, d.op) }()

	// Attempt to update backup.yaml for instance.
	err = inst.UpdateBackupFile()
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// updateProgress updates the operation metadata with a new progress string.
func (d *common) updateProgress(progress string) {
	if d.op == nil {
		return
	}

	meta := d.op.Metadata()
	if meta == nil {
		meta = make(map[string]any)
	}

	if meta["container_progress"] != progress {
		meta["container_progress"] = progress
		_ = d.op.UpdateMetadata(meta)
	}
}

// insertConfigkey function attempts to insert the instance config key into the database. If the insert fails
// then the database is queried to check whether another query inserted the same key. If the key is still
// unpopulated then the insert querty is retried until it succeeds or a retry limit is reached.
// If the insert succeeds or the key is found to have been populated then the value of the key is returned.
func (d *common) insertConfigkey(key string, value string) (string, error) {
	err := query.Retry(func() error {
		err := query.Transaction(context.TODO(), d.state.DB.Cluster.DB(), func(ctx context.Context, tx *sql.Tx) error {
			return db.CreateInstanceConfig(tx, d.id, map[string]string{key: value})
		})
		if err != nil {
			// Check if something else filled it in behind our back.
			existingValue, errCheckExists := d.state.DB.Cluster.GetInstanceConfig(d.id, key)
			if errCheckExists != nil {
				return err
			}

			value = existingValue
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return value, nil
}

// isRunningStatusCode returns if instance is running from status code.
func (d *common) isRunningStatusCode(statusCode api.StatusCode) bool {
	return statusCode != api.Error && statusCode != api.Stopped
}

// isStartableStatusCode returns an error if the status code means the instance cannot be started currently.
func (d *common) isStartableStatusCode(statusCode api.StatusCode) error {
	if d.isRunningStatusCode(statusCode) {
		return fmt.Errorf("The instance is already running")
	}

	// If the instance process exists but is crashed, don't allow starting until its been cleaned up, as it
	// would likely fail to start anyway or leave the old process untracked.
	if statusCode == api.Error {
		return fmt.Errorf("The instance cannot be started as in %s status", statusCode)
	}

	return nil
}

// startupSnapshot triggers a snapshot if configured.
func (d *common) startupSnapshot(inst instance.Instance) error {
	schedule := strings.ToLower(d.expandedConfig["snapshots.schedule"])
	if schedule == "" {
		return nil
	}

	triggers := strings.Split(schedule, ", ")
	if !shared.StringInSlice("@startup", triggers) {
		return nil
	}

	expiry, err := shared.GetSnapshotExpiry(time.Now(), d.expandedConfig["snapshots.expiry"])
	if err != nil {
		return err
	}

	name, err := instance.NextSnapshotName(d.state, inst, "snap%d")
	if err != nil {
		return err
	}

	return inst.Snapshot(name, expiry, false)
}

// Internal MAAS handling.
func (d *common) maasUpdate(inst instance.Instance, oldDevices map[string]map[string]string) error {
	// Check if MAAS is configured
	maasURL, _ := d.state.GlobalConfig.MAASController()

	if maasURL == "" {
		return nil
	}

	// Check if there's something that uses MAAS
	interfaces, err := d.maasInterfaces(inst, d.expandedDevices.CloneNative())
	if err != nil {
		return err
	}

	var oldInterfaces []maas.ContainerInterface
	if oldDevices != nil {
		oldInterfaces, err = d.maasInterfaces(inst, oldDevices)
		if err != nil {
			return err
		}
	}

	if len(interfaces) == 0 && len(oldInterfaces) == 0 {
		return nil
	}

	// See if we're connected to MAAS
	if d.state.MAAS == nil {
		return fmt.Errorf("Can't perform the operation because MAAS is currently unavailable")
	}

	exists, err := d.state.MAAS.DefinedContainer(d)
	if err != nil {
		return err
	}

	if exists {
		if len(interfaces) == 0 && len(oldInterfaces) > 0 {
			return d.state.MAAS.DeleteContainer(d)
		}

		return d.state.MAAS.UpdateContainer(d, interfaces)
	}

	return d.state.MAAS.CreateContainer(d, interfaces)
}

func (d *common) maasInterfaces(inst instance.Instance, devices map[string]map[string]string) ([]maas.ContainerInterface, error) {
	interfaces := []maas.ContainerInterface{}
	for k, m := range devices {
		if m["type"] != "nic" {
			continue
		}

		if m["maas.subnet.ipv4"] == "" && m["maas.subnet.ipv6"] == "" {
			continue
		}

		m, err := inst.FillNetworkDevice(k, m)
		if err != nil {
			return nil, err
		}

		subnets := []maas.ContainerInterfaceSubnet{}

		// IPv4
		if m["maas.subnet.ipv4"] != "" {
			subnet := maas.ContainerInterfaceSubnet{
				Name:    m["maas.subnet.ipv4"],
				Address: m["ipv4.address"],
			}

			subnets = append(subnets, subnet)
		}

		// IPv6
		if m["maas.subnet.ipv6"] != "" {
			subnet := maas.ContainerInterfaceSubnet{
				Name:    m["maas.subnet.ipv6"],
				Address: m["ipv6.address"],
			}

			subnets = append(subnets, subnet)
		}

		iface := maas.ContainerInterface{
			Name:       m["name"],
			MACAddress: m["hwaddr"],
			Subnets:    subnets,
		}

		interfaces = append(interfaces, iface)
	}

	return interfaces, nil
}

func (d *common) maasRename(inst instance.Instance, newName string) error {
	maasURL, _ := d.state.GlobalConfig.MAASController()

	if maasURL == "" {
		return nil
	}

	interfaces, err := d.maasInterfaces(inst, d.expandedDevices.CloneNative())
	if err != nil {
		return err
	}

	if len(interfaces) == 0 {
		return nil
	}

	if d.state.MAAS == nil {
		return fmt.Errorf("Can't perform the operation because MAAS is currently unavailable")
	}

	exists, err := d.state.MAAS.DefinedContainer(d)
	if err != nil {
		return err
	}

	if !exists {
		return d.maasUpdate(inst, nil)
	}

	return d.state.MAAS.RenameContainer(d, newName)
}

func (d *common) maasDelete(inst instance.Instance) error {
	maasURL, _ := d.state.GlobalConfig.MAASController()

	if maasURL == "" {
		return nil
	}

	interfaces, err := d.maasInterfaces(inst, d.expandedDevices.CloneNative())
	if err != nil {
		return err
	}

	if len(interfaces) == 0 {
		return nil
	}

	if d.state.MAAS == nil {
		return fmt.Errorf("Can't perform the operation because MAAS is currently unavailable")
	}

	exists, err := d.state.MAAS.DefinedContainer(d)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	return d.state.MAAS.DeleteContainer(d)
}

// onStopOperationSetup creates or picks up the relevant operation. This is used in the stopns and stop hooks to
// ensure that a lock on their activities is held before the instance process is stopped. This prevents a start
// request run at the same time from overlapping with the stop process.
// Returns the operation along with a boolean indicating if the operation was created or not.
func (d *common) onStopOperationSetup(target string) (*operationlock.InstanceOperation, bool, error) {
	var err error

	// Pick up the existing stop operation lock created in Stop() function.
	// If there is another ongoing operation (such as start), wait until that has finished before proceeding
	// to run the hook (this should be quick as it will fail showing instance is already running).
	op := operationlock.Get(d.Project(), d.Name())
	if op != nil && !op.ActionMatch(operationlock.ActionStop, operationlock.ActionRestart, operationlock.ActionRestore) {
		d.logger.Debug("Waiting for existing operation lock to finish before running hook", logger.Ctx{"action": op.Action()})
		_ = op.Wait()
		op = nil
	}

	instanceInitiated := false

	if op == nil {
		d.logger.Debug("Instance initiated stop", logger.Ctx{"action": target})
		instanceInitiated = true

		action := operationlock.ActionStop
		if target == "reboot" {
			action = operationlock.ActionRestart
		}

		op, err = operationlock.Create(d.Project(), d.Name(), action, false, false)
		if err != nil {
			return nil, false, fmt.Errorf("Failed creating %q operation: %w", action, err)
		}
	}

	return op, instanceInitiated, nil
}

// warningsDelete deletes any persistent warnings for the instance.
func (d *common) warningsDelete() error {
	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteWarnings(ctx, tx.Tx(), dbCluster.TypeInstance, d.ID())
	})
	if err != nil {
		return fmt.Errorf("Failed deleting persistent warnings: %w", err)
	}

	return nil
}

// canMigrate returns whether the instance can be migrated.
func (d *common) canMigrate(inst instance.Instance) (bool, bool) {
	// Check policy for the instance.
	config := d.ExpandedConfig()
	val, ok := config["cluster.evacuate"]
	if !ok {
		val = "auto"
	}

	if val == "migrate" {
		return true, false
	}

	if val == "live-migrate" {
		return true, true
	}

	if val == "stop" {
		return false, false
	}

	// Look at attached devices.
	volatileGet := func() map[string]string { return map[string]string{} }
	volatileSet := func(_ map[string]string) error { return nil }
	for deviceName, rawConfig := range d.ExpandedDevices() {
		dev, err := device.New(inst, d.state, deviceName, rawConfig, volatileGet, volatileSet)
		if err != nil {
			return false, false
		}

		if !dev.CanMigrate() {
			return false, false
		}
	}

	// Check if set up for live migration.
	// Limit automatic live-migration to virtual machines for now.
	live := false
	if inst.Type() == instancetype.VM {
		live = shared.IsTrue(config["migration.stateful"])
	}

	return true, live
}

// recordLastState records last power and used time into local config and database config.
func (d *common) recordLastState() error {
	var err error

	// Record power state.
	d.localConfig["volatile.last_state.power"] = "RUNNING"
	d.expandedConfig["volatile.last_state.power"] = "RUNNING"

	// Database updates
	return d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Record power state.
		err = tx.UpdateInstancePowerState(d.id, "RUNNING")
		if err != nil {
			err = fmt.Errorf("Error updating instance power state: %w", err)
			return err
		}

		// Update time instance last started time.
		err = tx.UpdateInstanceLastUsedDate(d.id, time.Now().UTC())
		if err != nil {
			err = fmt.Errorf("Error updating instance last used: %w", err)
			return err
		}

		return nil
	})
}

func (d *common) setCoreSched(pids []int) error {
	if !d.state.OS.CoreScheduling {
		return nil
	}

	args := []string{
		"forkcoresched",
		"0",
	}

	for _, pid := range pids {
		args = append(args, strconv.Itoa(pid))
	}

	_, err := shared.RunCommand(d.state.OS.ExecPath, args...)
	return err
}

// getRootDiskDevice gets the name and configuration of the root disk device of an instance.
func (d *common) getRootDiskDevice() (string, map[string]string, error) {
	devices := d.ExpandedDevices()
	if d.IsSnapshot() {
		parentName, _, _ := api.GetParentAndSnapshotName(d.name)

		// Load the parent.
		storageInstance, err := instance.LoadByProjectAndName(d.state, d.project, parentName)
		if err != nil {
			return "", nil, err
		}

		devices = storageInstance.ExpandedDevices()
	}

	// Retrieve the instance's storage pool.
	name, configuration, err := shared.GetRootDiskDevice(devices.CloneNative())
	if err != nil {
		return "", nil, err
	}

	return name, configuration, nil
}

// resetInstanceID generates a new UUID and puts it in volatile.
func (d *common) resetInstanceID() error {
	err := d.VolatileSet(map[string]string{"volatile.cloud-init.instance-id": uuid.New()})
	if err != nil {
		return fmt.Errorf("Failed to set volatile.cloud-init.instance-id: %w", err)
	}

	return nil
}

// needsNewInstanceID checks the changed data in an Update call to determine if a new instance-id is necessary.
func (d *common) needsNewInstanceID(changedConfig []string, oldExpandedDevices deviceConfig.Devices) bool {
	// Look for cloud-init related config changes.
	for _, key := range []string{
		"cloud-init.vendor-data",
		"cloud-init.user-data",
		"cloud-init.network-config",
		"user.vendor-data",
		"user.user-data",
		"user.network-config",
	} {
		if shared.StringInSlice(key, changedConfig) {
			return true
		}
	}

	// Look for changes in network interface names.
	getNICNames := func(devs deviceConfig.Devices) []string {
		names := make([]string, 0, len(devs))
		for devName, dev := range devs {
			if dev["type"] != "nic" {
				continue
			}

			if dev["name"] != "" {
				names = append(names, dev["name"])
				continue
			}

			configKey := fmt.Sprintf("volatile.%s.name", devName)
			volatileName := d.localConfig[configKey]
			if volatileName != "" {
				names = append(names, dev["name"])
				continue
			}

			names = append(names, devName)
		}

		return names
	}

	oldNames := getNICNames(oldExpandedDevices)
	newNames := getNICNames(d.expandedDevices)

	for _, entry := range oldNames {
		if !shared.StringInSlice(entry, newNames) {
			return true
		}
	}

	for _, entry := range newNames {
		if !shared.StringInSlice(entry, oldNames) {
			return true
		}
	}

	return false
}

// getStoragePool returns the current storage pool handle. To avoid a DB lookup each time this
// function is called, the handle is cached internally in the struct.
func (d *common) getStoragePool() (storagePools.Pool, error) {
	if d.storagePool != nil {
		return d.storagePool, nil
	}

	poolName, err := d.state.DB.Cluster.GetInstancePool(d.Project(), d.Name())
	if err != nil {
		return nil, err
	}

	pool, err := storagePools.LoadByName(d.state, poolName)
	if err != nil {
		return nil, err
	}

	d.storagePool = pool

	return d.storagePool, nil
}

// deviceLoad instantiates and validates a new device and returns it along with enriched config.
func (d *common) deviceLoad(inst instance.Instance, deviceName string, rawConfig deviceConfig.Device) (device.Device, error) {
	var configCopy deviceConfig.Device
	var err error

	// Create copy of config and load some fields from volatile if device is nic or infiniband.
	if shared.StringInSlice(rawConfig["type"], []string{"nic", "infiniband"}) {
		configCopy, err = inst.FillNetworkDevice(deviceName, rawConfig)
		if err != nil {
			return nil, err
		}
	} else {
		// Othewise copy the config so it cannot be modified by device.
		configCopy = rawConfig.Clone()
	}

	dev, err := device.New(inst, d.state, deviceName, configCopy, d.deviceVolatileGetFunc(deviceName), d.deviceVolatileSetFunc(deviceName))

	// If validation fails with unsupported device type then don't return the device for use.
	if errors.Is(err, device.ErrUnsupportedDevType) {
		return nil, err
	}

	// Return device even if error occurs as caller may still attempt to use device for stop and remove.
	return dev, err
}

// deviceAdd loads a new device and calls its Add() function.
func (d *common) deviceAdd(dev device.Device, instanceRunning bool) error {
	l := d.logger.AddContext(logger.Ctx{"device": dev.Name(), "type": dev.Config()["type"]})
	l.Debug("Adding device")

	if instanceRunning && !dev.CanHotPlug() {
		return fmt.Errorf("Device cannot be added when instance is running")
	}

	return dev.Add()
}

// deviceRemove loads a new device and calls its Remove() function.
func (d *common) deviceRemove(dev device.Device, instanceRunning bool) error {
	l := d.logger.AddContext(logger.Ctx{"device": dev.Name(), "type": dev.Config()["type"]})
	l.Debug("Removing device")

	if instanceRunning && !dev.CanHotPlug() {
		return fmt.Errorf("Device cannot be removed when instance is running")
	}

	return dev.Remove()
}

// devicesAdd adds devices to instance and registers with MAAS.
func (d *common) devicesAdd(inst instance.Instance, instanceRunning bool) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	for _, entry := range d.expandedDevices.Sorted() {
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config)
		if err != nil {
			// If device conflicts with another device then do not call the deviceAdd function below
			// as this could cause the original device to be disrupted (such as allowing conflicting
			// static NIC DHCP leases to be created). Instead just log an error.
			// This will allow instances to be created with conflicting devices (such as when copying
			// or restoring a backup) and allows the user to manually fix the conflicts in order to
			// allow the the instance to start.
			if api.StatusErrorCheck(err, http.StatusConflict) {
				d.logger.Error("Failed add validation for device, skipping add action", logger.Ctx{"device": entry.Name, "err": err})

				continue
			}

			return nil, fmt.Errorf("Failed add validation for device %q: %w", entry.Name, err)
		}

		err = d.deviceAdd(dev, instanceRunning)
		if err != nil {
			return nil, fmt.Errorf("Failed to add device %q: %w", dev.Name(), err)
		}

		revert.Add(func() { _ = d.deviceRemove(dev, instanceRunning) })
	}

	// Update MAAS (must run after the MAC addresses have been generated).
	err := d.maasUpdate(inst, nil)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = d.maasDelete(inst) })

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// devicesRegister calls the Register() function on all of the instance's devices.
func (d *common) devicesRegister(inst instance.Instance) {
	for _, entry := range d.ExpandedDevices().Sorted() {
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config)
		if err != nil {
			d.logger.Error("Failed register validation for device", logger.Ctx{"err": err, "device": entry.Name})
			continue
		}

		// Check whether device wants to register for any events.
		err = dev.Register()
		if err != nil {
			d.logger.Error("Failed to register device", logger.Ctx{"err": err, "device": entry.Name})
			continue
		}
	}
}

// devicesUpdate applies device changes to an instance.
func (d *common) devicesUpdate(inst instance.Instance, removeDevices deviceConfig.Devices, addDevices deviceConfig.Devices, updateDevices deviceConfig.Devices, oldExpandedDevices deviceConfig.Devices, instanceRunning bool, userRequested bool) error {
	revert := revert.New()
	defer revert.Fail()

	dm, ok := inst.(deviceManager)
	if !ok {
		return fmt.Errorf("Instance is not compatible with deviceManager interface")
	}

	// Remove devices in reverse order to how they were added.
	for _, entry := range removeDevices.Reversed() {
		l := d.logger.AddContext(logger.Ctx{"device": entry.Name, "userRequested": userRequested})
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config)
		if err != nil {
			// Just log an error, but still allow the device to be removed if usable device returned.
			l.Error("Failed remove validation for device", logger.Ctx{"err": err})
		}

		// If a device was returned from deviceLoad even if validation fails, then try to stop and remove.
		if dev != nil {
			if instanceRunning {
				err = dm.deviceStop(dev, instanceRunning, "")
				if err != nil {
					return fmt.Errorf("Failed to stop device %q: %w", dev.Name(), err)
				}
			}

			err = d.deviceRemove(dev, instanceRunning)
			if err != nil && err != device.ErrUnsupportedDevType {
				return fmt.Errorf("Failed to remove device %q: %w", dev.Name(), err)
			}
		}

		// Check whether we are about to add the same device back with updated config and
		// if not, or if the device type has changed, then remove all volatile keys for
		// this device (as its an actual removal or a device type change).
		err = d.deviceVolatileReset(entry.Name, entry.Config, addDevices[entry.Name])
		if err != nil {
			return fmt.Errorf("Failed to reset volatile data for device %q: %w", entry.Name, err)
		}
	}

	// Add devices in sorted order, this ensures that device mounts are added in path order.
	for _, entry := range addDevices.Sorted() {
		l := d.logger.AddContext(logger.Ctx{"device": entry.Name, "userRequested": userRequested})
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config)
		if err != nil {
			if userRequested {
				return fmt.Errorf("Failed add validation for device %q: %w", entry.Name, err)
			}

			// If update is non-user requested (i.e from a snapshot restore), there's nothing we can
			// do to fix the config and we don't want to prevent the snapshot restore so log and allow.
			l.Error("Failed add validation for device, skipping as non-user requested", logger.Ctx{"err": err})

			continue
		}

		err = d.deviceAdd(dev, instanceRunning)
		if err != nil {
			if userRequested {
				return fmt.Errorf("Failed to add device %q: %w", dev.Name(), err)
			}

			// If update is non-user requested (i.e from a snapshot restore), there's nothing we can
			// do to fix the config and we don't want to prevent the snapshot restore so log and allow.
			l.Error("Failed to add device, skipping as non-user requested", logger.Ctx{"err": err})
		}

		revert.Add(func() { _ = d.deviceRemove(dev, instanceRunning) })

		if instanceRunning {
			err = dev.PreStartCheck()
			if err != nil {
				return fmt.Errorf("Failed pre-start check for device %q: %w", dev.Name(), err)
			}

			_, err := dm.deviceStart(dev, instanceRunning)
			if err != nil && err != device.ErrUnsupportedDevType {
				return fmt.Errorf("Failed to start device %q: %w", dev.Name(), err)
			}

			revert.Add(func() { _ = dm.deviceStop(dev, instanceRunning, "") })
		}
	}

	for _, entry := range updateDevices.Sorted() {
		l := d.logger.AddContext(logger.Ctx{"device": entry.Name, "userRequested": userRequested})
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config)
		if err != nil {
			if userRequested {
				return fmt.Errorf("Failed update validation for device %q: %w", entry.Name, err)
			}

			// If update is non-user requested (i.e from a snapshot restore), there's nothing we can
			// do to fix the config and we don't want to prevent the snapshot restore so log and allow.
			// By not calling dev.Update on validation error we avoid potentially disrupting another
			// existing device if this device conflicts with it (such as allowing conflicting static
			// NIC DHCP leases to be created).
			l.Error("Failed update validation for device, removing device", logger.Ctx{"err": err})

			// If a device was returned from deviceLoad when validation fails, then try to stop and
			// remove it. This is to prevent devices being left in a state that is different to the
			// invalid non-user requested config that has been applied to DB. The safest thing to do
			// is to cleanup the device and wait for the config to be corrected.
			if dev != nil {
				if instanceRunning {
					err = dm.deviceStop(dev, instanceRunning, "")
					if err != nil {
						l.Error("Failed to stop device after update validation failed", logger.Ctx{"err": err})
					}
				}

				err = d.deviceRemove(dev, instanceRunning)
				if err != nil && err != device.ErrUnsupportedDevType {
					l.Error("Failed to remove device after update validation failed", logger.Ctx{"err": err})
				}
			}

			continue
		}

		err = dev.Update(oldExpandedDevices, instanceRunning)
		if err != nil {
			return fmt.Errorf("Failed to update device %q: %w", dev.Name(), err)
		}
	}

	revert.Success()
	return nil
}

// devicesRemove runs device removal function for each device.
func (d *common) devicesRemove(inst instance.Instance) {
	for _, entry := range d.expandedDevices.Reversed() {
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config)
		if err != nil {
			// Just log an error, but still allow the device to be removed if usable device returned.
			d.logger.Error("Failed remove validation for device", logger.Ctx{"device": entry.Name, "err": err})
		}

		// If a usable device was returned from deviceLoad try to remove anyway, even if validation fails.
		// This allows for the scenario where a new version of LXD has additional validation restrictions
		// than older versions and we still need to allow previously valid devices to be stopped even if
		// they are no longer considered valid.
		if dev != nil {
			err = d.deviceRemove(dev, false)
			if err != nil {
				d.logger.Error("Failed to remove device", logger.Ctx{"device": dev.Name(), "err": err})
			}
		}
	}
}
