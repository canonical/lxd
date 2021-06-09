package drivers

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
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

// common provides structure common to all instance types.
type common struct {
	op    *operations.Operation
	state *state.State

	architecture    int
	creationDate    time.Time
	dbType          instancetype.Type
	description     string
	devPaths        []string
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
	profiles        []string
	project         string
	snapshot        bool
	stateful        bool
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

// DevPaths() returns a list of /dev devices which the instance requires access to.
// This is function is only safe to call from within the security
// packages as called during instance startup, the rest of the time this
// will likely return nil.
func (d *common) DevPaths() []string {
	return d.devPaths
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

// Location returns instance's location.
func (d *common) Location() string {
	return d.node
}

// Profiles returns the instance's profiles.
func (d *common) Profiles() []string {
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

// IsStateful retuens whether instance is stateful or not.
func (d *common) IsStateful() bool {
	return d.stateful
}

//
// SECTION: general functions
//

// Backups returns a list of backups.
func (d *common) Backups() ([]backup.InstanceBackup, error) {
	// Get all the backups
	backupNames, err := d.state.Cluster.GetInstanceBackups(d.project, d.name)
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
		return errors.Wrap(err, "Failed to set apply_template volatile key")
	}

	return nil
}

// SetOperation sets the current operation.
func (d *common) SetOperation(op *operations.Operation) {
	d.op = op
}

// Snapshots returns a list of snapshots.
func (d *common) Snapshots() ([]instance.Instance, error) {
	var snaps []db.Instance

	if d.snapshot {
		return []instance.Instance{}, nil
	}

	// Get all the snapshots
	err := d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		snaps, err = tx.GetInstanceSnapshotsWithName(d.project, d.name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Build the snapshot list
	snapshots, err := instance.LoadAllInternal(d.state, snaps)
	if err != nil {
		return nil, err
	}

	instances := make([]instance.Instance, len(snapshots))
	for k, v := range snapshots {
		instances[k] = instance.Instance(v)
	}

	return instances, nil
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
		err = d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.UpdateInstanceSnapshotConfig(d.id, changes)
		})
	} else {
		err = d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.UpdateInstanceConfig(d.id, changes)
		})
	}
	if err != nil {
		return errors.Wrap(err, "Failed to volatile config")
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
		if _, found := newConfig[devKey]; found {
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
func (d *common) expandConfig(profiles []api.Profile) error {
	if profiles == nil && len(d.profiles) > 0 {
		var err error
		profiles, err = d.state.Cluster.GetProfiles(d.project, d.profiles)
		if err != nil {
			return err
		}
	}

	d.expandedConfig = db.ExpandInstanceConfig(d.localConfig, profiles)

	return nil
}

// expandDevices applies the devices of each profile in order, followed by the local devices.
func (d *common) expandDevices(profiles []api.Profile) error {
	if profiles == nil && len(d.profiles) > 0 {
		var err error
		profiles, err = d.state.Cluster.GetProfiles(d.project, d.profiles)
		if err != nil {
			return err
		}
	}

	d.expandedDevices = db.ExpandInstanceDevices(d.localDevices, profiles)

	return nil
}

// restartCommon handles the common part of instance restarts.
func (d *common) restartCommon(inst instance.Instance, timeout time.Duration) error {
	// Setup a new operation for the stop/shutdown phase.
	op, err := operationlock.Create(d.id, "restart", true, true)
	if err != nil {
		return errors.Wrap(err, "Create restart operation")
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
			inst.Update(args, false)
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
	op, err = operationlock.Create(d.id, "restart", true, true)
	if err != nil {
		return errors.Wrap(err, "Create restart operation")
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
	snap, err := instance.CreateInternal(d.state, args, revert)
	if err != nil {
		return errors.Wrapf(err, "Failed creating instance snapshot record %q", name)
	}

	pool, err := storagePools.GetPoolByInstance(d.state, snap)
	if err != nil {
		return err
	}

	err = pool.CreateInstanceSnapshot(snap, inst, d.op)
	if err != nil {
		return errors.Wrap(err, "Create instance snapshot")
	}

	revert.Add(func() { snap.Delete(true) })

	// Mount volume for backup.yaml writing.
	_, err = pool.MountInstance(inst, d.op)
	if err != nil {
		return errors.Wrap(err, "Create instance snapshot (mount source)")
	}
	defer pool.UnmountInstance(inst, d.op)

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
		meta = make(map[string]interface{})
	}

	if meta["container_progress"] != progress {
		meta["container_progress"] = progress
		d.op.UpdateMetadata(meta)
	}
}

// lifecycle is used to send a lifecycle event with some instance context.
func (d *common) lifecycle(action string, ctx map[string]interface{}) error {
	prefix := "instance"
	u := fmt.Sprintf("/1.0/instances/%s", url.PathEscape(d.name))

	if d.snapshot {
		name, snapName, _ := shared.InstanceGetParentAndSnapshotName(d.name)
		u = fmt.Sprintf("/1.0/instances/%s/snapshots/%s", url.PathEscape(name), url.PathEscape(snapName))
		prefix = "instance-snapshot"
	}

	if d.project != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(d.project))
	}

	var requestor *api.EventLifecycleRequestor
	if d.op != nil {
		requestor = d.op.Requestor()
	}

	return d.state.Events.SendLifecycle(d.project, fmt.Sprintf("%s-%s", prefix, action), u, ctx, requestor)
}

// insertConfigkey function attempts to insert the instance config key into the database. If the insert fails
// then the database is queried to check whether another query inserted the same key. If the key is still
// unpopulated then the insert querty is retried until it succeeds or a retry limit is reached.
// If the insert succeeds or the key is found to have been populated then the value of the key is returned.
func (d *common) insertConfigkey(key string, value string) (string, error) {
	err := query.Retry(func() error {
		err := query.Transaction(d.state.Cluster.DB(), func(tx *sql.Tx) error {
			return db.CreateInstanceConfig(tx, d.id, map[string]string{key: value})
		})
		if err != nil {
			// Check if something else filled it in behind our back.
			existingValue, errCheckExists := d.state.Cluster.GetInstanceConfig(d.id, key)
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
	maasURL, err := cluster.ConfigGetString(d.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

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
	maasURL, err := cluster.ConfigGetString(d.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

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
	maasURL, err := cluster.ConfigGetString(d.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

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
