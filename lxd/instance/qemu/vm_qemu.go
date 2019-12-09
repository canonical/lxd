package qemu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	lxdClient "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cgroup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/qemu/qmp"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/lxd/vsock"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/termios"
	"github.com/lxc/lxd/shared/units"
)

var errQemuAgentOffline = fmt.Errorf("LXD VM agent isn't currently running")

var vmConsole = map[int]bool{}
var vmConsoleLock sync.Mutex

// Load creates a Qemu instance from the supplied InstanceArgs.
func Load(s *state.State, args db.InstanceArgs, profiles []api.Profile) (instance.Instance, error) {
	// Create the instance struct.
	vm := Instantiate(s, args, nil)

	// Expand config and devices.
	err := vm.expandConfig(profiles)
	if err != nil {
		return nil, err
	}

	err = vm.expandDevices(profiles)
	if err != nil {
		return nil, err
	}

	return vm, nil
}

// Instantiate creates a Qemu struct without expanding config. The expandedDevices argument is
// used during device config validation when the devices have already been expanded and we do not
// have access to the profiles used to do it. This can be safely passed as nil if not required.
func Instantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices) *Qemu {
	vm := &Qemu{
		state:        s,
		id:           args.ID,
		project:      args.Project,
		name:         args.Name,
		description:  args.Description,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		dbType:       args.Type,
		snapshot:     args.Snapshot,
		creationDate: args.CreationDate,
		lastUsedDate: args.LastUsedDate,
		profiles:     args.Profiles,
		localConfig:  args.Config,
		localDevices: args.Devices,
		stateful:     args.Stateful,
		node:         args.Node,
		expiryDate:   args.ExpiryDate,
	}

	// Cleanup the zero values.
	if vm.expiryDate.IsZero() {
		vm.expiryDate = time.Time{}
	}

	if vm.creationDate.IsZero() {
		vm.creationDate = time.Time{}
	}

	if vm.lastUsedDate.IsZero() {
		vm.lastUsedDate = time.Time{}
	}

	// This is passed during expanded config validation.
	if expandedDevices != nil {
		vm.expandedDevices = expandedDevices
	}

	return vm
}

// Create creates a new storage volume record and returns an initialised Instance.
func Create(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	// Create the instance struct.
	vm := &Qemu{
		state:        s,
		id:           args.ID,
		project:      args.Project,
		name:         args.Name,
		node:         args.Node,
		description:  args.Description,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		dbType:       args.Type,
		snapshot:     args.Snapshot,
		stateful:     args.Stateful,
		creationDate: args.CreationDate,
		lastUsedDate: args.LastUsedDate,
		profiles:     args.Profiles,
		localConfig:  args.Config,
		localDevices: args.Devices,
		expiryDate:   args.ExpiryDate,
	}

	// Cleanup the zero values.
	if vm.expiryDate.IsZero() {
		vm.expiryDate = time.Time{}
	}

	if vm.creationDate.IsZero() {
		vm.creationDate = time.Time{}
	}

	if vm.lastUsedDate.IsZero() {
		vm.lastUsedDate = time.Time{}
	}

	ctxMap := log.Ctx{
		"project":   args.Project,
		"name":      vm.name,
		"ephemeral": vm.ephemeral,
	}

	logger.Info("Creating instance", ctxMap)

	revert := true
	defer func() {
		if !revert {
			return
		}

		vm.Delete()
	}()

	// Load the config.
	err := vm.init()
	if err != nil {
		logger.Error("Failed creating instance", ctxMap)
		return nil, err
	}

	// Validate expanded config.
	err = instance.ValidConfig(s.OS, vm.expandedConfig, false, true)
	if err != nil {
		logger.Error("Failed creating instance", ctxMap)
		return nil, err
	}

	err = instance.ValidDevices(s, s.Cluster, vm.Type(), vm.Name(), vm.expandedDevices, true)
	if err != nil {
		logger.Error("Failed creating instance", ctxMap)
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Retrieve the instance's storage pool.
	_, rootDiskDevice, err := shared.GetRootDiskDevice(vm.expandedDevices.CloneNative())
	if err != nil {
		return nil, err
	}

	if rootDiskDevice["pool"] == "" {
		return nil, fmt.Errorf("The instances's root device is missing the pool property")
	}

	storagePool := rootDiskDevice["pool"]

	// Get the storage pool ID for the instance.
	poolID, pool, err := s.Cluster.StoragePoolGet(storagePool)
	if err != nil {
		return nil, err
	}

	// Fill in any default volume config.
	volumeConfig := map[string]string{}
	err = storagePools.VolumeFillDefault(storagePool, volumeConfig, pool)
	if err != nil {
		return nil, err
	}

	// Create a new database entry for the instance's storage volume.
	_, err = s.Cluster.StoragePoolVolumeCreate(args.Project, args.Name, "", db.StoragePoolVolumeTypeVM, false, poolID, volumeConfig)
	if err != nil {
		return nil, err
	}

	if !vm.IsSnapshot() {
		// Update MAAS.
		err = vm.maasUpdate(nil)
		if err != nil {
			logger.Error("Failed creating instance", ctxMap)
			return nil, err
		}

		// Add devices to instance.
		for k, m := range vm.expandedDevices {
			err = vm.deviceAdd(k, m)
			if err != nil && err != device.ErrUnsupportedDevType {
				return nil, errors.Wrapf(err, "Failed to add device '%s'", k)
			}
		}
	}

	logger.Info("Created instance", ctxMap)
	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-created",
		fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)

	revert = false
	return vm, nil
}

// Qemu is the QEMU virtual machine driver.
type Qemu struct {
	// Properties.
	architecture int
	dbType       instancetype.Type
	snapshot     bool
	creationDate time.Time
	lastUsedDate time.Time
	ephemeral    bool
	id           int
	project      string
	name         string
	description  string
	stateful     bool

	// Config.
	expandedConfig  map[string]string
	expandedDevices deviceConfig.Devices
	localConfig     map[string]string
	localDevices    deviceConfig.Devices
	profiles        []string

	state *state.State

	// Clustering.
	node string

	// Progress tracking.
	op *operations.Operation

	expiryDate time.Time

	// Cached handles.
	// Do not use these variables directly, instead use their associated get functions so they
	// will be initialised on demand.
	agentClient *http.Client
	storagePool storagePools.Pool
}

// getAgentClient returns the current agent client handle. To avoid TLS setup each time this
// function is called, the handle is cached internally in the Qemu struct.
func (vm *Qemu) getAgentClient() (*http.Client, error) {
	if vm.agentClient != nil {
		return vm.agentClient, nil
	}

	// The connection uses mutual authentication, so use the LXD server's key & cert for client.
	agentCert, _, clientCert, clientKey, err := vm.generateAgentCert()
	if err != nil {
		return nil, err
	}

	agent, err := vsock.HTTPClient(vm.vsockID(), clientCert, clientKey, agentCert)
	if err != nil {
		return nil, err
	}

	return agent, nil
}

// getStoragePool returns the current storage pool handle. To avoid a DB lookup each time this
// function is called, the handle is cached internally in the Qemu struct.
func (vm *Qemu) getStoragePool() (storagePools.Pool, error) {
	if vm.storagePool != nil {
		return vm.storagePool, nil
	}

	pool, err := storagePools.GetPoolByInstance(vm.state, vm)
	if err != nil {
		return nil, err
	}
	vm.storagePool = pool

	return vm.storagePool, nil
}

func (vm *Qemu) getMonitorEventHandler() func(event string, data map[string]interface{}) {
	id := vm.id
	state := vm.state

	return func(event string, data map[string]interface{}) {
		if !shared.StringInSlice(event, []string{"SHUTDOWN"}) {
			return
		}

		inst, err := instance.LoadByID(state, id)
		if err != nil {
			logger.Errorf("Failed to load instance with id=%d", id)
			return
		}

		if event == "SHUTDOWN" {
			target := "stop"
			entry, ok := data["reason"]
			if ok && entry == "guest-reset" {
				target = "reboot"
			}

			err = inst.(*Qemu).OnStop(target)
			if err != nil {
				logger.Errorf("Failed to cleanly stop instance '%s': %v", project.Prefix(inst.Project(), inst.Name()), err)
				return
			}
		}
	}
}

// mount the instance's config volume if needed.
func (vm *Qemu) mount() (bool, error) {
	var pool storagePools.Pool
	pool, err := vm.getStoragePool()
	if err != nil {
		return false, err
	}

	ourMount, err := pool.MountInstance(vm, nil)
	if err != nil {
		return false, err
	}

	return ourMount, nil
}

// unmount the instance's config volume if needed.
func (vm *Qemu) unmount() (bool, error) {
	var pool storagePools.Pool
	pool, err := vm.getStoragePool()
	if err != nil {
		return false, err
	}

	unmounted, err := pool.UnmountInstance(vm, nil)
	if err != nil {
		return false, err
	}

	return unmounted, nil
}

// generateAgentCert creates the necessary server key and certificate if needed.
func (vm *Qemu) generateAgentCert() (string, string, string, string, error) {
	// Mount the instance's config volume if needed.
	ourMount, err := vm.mount()
	if err != nil {
		return "", "", "", "", err
	}

	if ourMount {
		defer vm.unmount()
	}

	agentCertFile := filepath.Join(vm.Path(), "agent.crt")
	agentKeyFile := filepath.Join(vm.Path(), "agent.key")
	clientCertFile := filepath.Join(vm.Path(), "agent-client.crt")
	clientKeyFile := filepath.Join(vm.Path(), "agent-client.key")

	// Create server certificate.
	err = shared.FindOrGenCert(agentCertFile, agentKeyFile, false, false)
	if err != nil {
		return "", "", "", "", err
	}

	// Create client certificate.
	err = shared.FindOrGenCert(clientCertFile, clientKeyFile, true, false)
	if err != nil {
		return "", "", "", "", err
	}

	// Read all the files
	agentCert, err := ioutil.ReadFile(agentCertFile)
	if err != nil {
		return "", "", "", "", err
	}

	agentKey, err := ioutil.ReadFile(agentKeyFile)
	if err != nil {
		return "", "", "", "", err
	}

	clientCert, err := ioutil.ReadFile(clientCertFile)
	if err != nil {
		return "", "", "", "", err
	}

	clientKey, err := ioutil.ReadFile(clientKeyFile)
	if err != nil {
		return "", "", "", "", err
	}

	return string(agentCert), string(agentKey), string(clientCert), string(clientKey), nil
}

// Freeze freezes the instance.
func (vm *Qemu) Freeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// Send the stop command.
	err = monitor.Pause()
	if err != nil {
		return err
	}

	return nil
}

// OnStop is run when the instance stops.
func (vm *Qemu) OnStop(target string) error {
	vm.cleanupDevices()
	os.Remove(vm.pidFilePath())
	os.Remove(vm.getMonitorPath())
	vm.unmount()

	// Record power state
	err := vm.state.Cluster.ContainerSetState(vm.id, "STOPPED")
	if err != nil {
		return err
	}

	if target == "reboot" {
		return vm.Start(false)
	}

	return nil
}

// Shutdown shuts the instance down.
func (vm *Qemu) Shutdown(timeout time.Duration) error {
	if !vm.IsRunning() {
		return fmt.Errorf("The instance is already stopped")
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// Get the wait channel.
	chDisconnect, err := monitor.Wait()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			return nil
		}

		return err
	}

	// Send the system_powerdown command.
	err = monitor.Powerdown()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			return nil
		}

		return err
	}

	// If timeout provided, block until the VM is not running or the timeout has elapsed.
	if timeout > 0 {
		select {
		case <-chDisconnect:
			return nil
		case <-time.After(timeout):
			return fmt.Errorf("Instance was not shutdown after timeout")
		}
	} else {
		<-chDisconnect // Block until VM is not running if no timeout provided.
	}

	return nil
}

func (vm *Qemu) ovmfPath() string {
	if os.Getenv("LXD_OVMF_PATH") != "" {
		return os.Getenv("LXD_OVMF_PATH")
	}

	return "/usr/share/OVMF"
}

// Start starts the instance.
func (vm *Qemu) Start(stateful bool) error {
	// Ensure the correct vhost_vsock kernel module is loaded before establishing the vsock.
	err := util.LoadModule("vhost_vsock")
	if err != nil {
		return err
	}

	if vm.IsRunning() {
		return fmt.Errorf("The instance is already running")
	}

	// Mount the instance's config volume.
	_, err = vm.mount()
	if err != nil {
		return err
	}

	err = vm.generateConfigShare()
	if err != nil {
		return err
	}

	err = os.MkdirAll(vm.LogPath(), 0700)
	if err != nil {
		return err
	}

	err = os.MkdirAll(vm.DevicesPath(), 0711)
	if err != nil {
		return err
	}

	err = os.MkdirAll(vm.ShmountsPath(), 0711)
	if err != nil {
		return err
	}

	// Get a UUID for Qemu.
	vmUUID := vm.localConfig["volatile.vm.uuid"]
	if vmUUID == "" {
		vmUUID = uuid.New()
		vm.VolatileSet(map[string]string{"volatile.vm.uuid": vmUUID})
	}

	// Copy OVMF settings firmware to nvram file.
	// This firmware file can be modified by the VM so it must be copied from the defaults.
	if !shared.PathExists(vm.getNvramPath()) {
		err = vm.setupNvram()
		if err != nil {
			return err
		}
	}

	devConfs := make([]*deviceConfig.RunConfig, 0, len(vm.expandedDevices))

	// Setup devices in sorted order, this ensures that device mounts are added in path order.
	for _, dev := range vm.expandedDevices.Sorted() {
		// Start the device.
		runConf, err := vm.deviceStart(dev.Name, dev.Config, false)
		if err != nil {
			return errors.Wrapf(err, "Failed to start device '%s'", dev.Name)
		}

		if runConf == nil {
			continue
		}

		devConfs = append(devConfs, runConf)
	}

	// Get qemu configuration
	qemuBinary, qemuType, qemuConfig, err := vm.qemuArchConfig()
	if err != nil {
		return err
	}

	confFile, err := vm.generateQemuConfigFile(qemuType, qemuConfig, devConfs)
	if err != nil {
		return err
	}

	// Check qemu is installed.
	_, err = exec.LookPath(qemuBinary)
	if err != nil {
		return err
	}

	args := []string{
		"-S",
		"-name", vm.Name(),
		"-uuid", vmUUID,
		"-daemonize",
		"-cpu", "host",
		"-nographic",
		"-serial", "chardev:console",
		"-nodefaults",
		"-no-reboot",
		"-readconfig", confFile,
		"-pidfile", vm.pidFilePath(),
	}
	if shared.IsTrue(vm.expandedConfig["limits.memory.hugepages"]) {
		args = append(args, "-mem-path", "/dev/hugepages/", "-mem-prealloc")
	}

	if vm.expandedConfig["raw.qemu"] != "" {
		fields := strings.Split(vm.expandedConfig["raw.qemu"], " ")
		args = append(args, fields...)
	}

	_, err = shared.RunCommand(qemuBinary, args...)
	if err != nil {
		return err
	}

	// Start QMP monitoring.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// Start the VM.
	err = monitor.Start()
	if err != nil {
		return err
	}

	// Database updates
	err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Record current state
		err = tx.ContainerSetState(vm.id, "RUNNING")
		if err != nil {
			return errors.Wrap(err, "Error updating instance state")
		}

		// Update time instance last started time
		err = tx.ContainerLastUsedUpdate(vm.id, time.Now().UTC())
		if err != nil {
			return errors.Wrap(err, "Error updating instance last used")
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (vm *Qemu) setupNvram() error {
	srcOvmfFile := filepath.Join(vm.ovmfPath(), "OVMF_VARS.fd")
	if vm.expandedConfig["security.secureboot"] == "" || shared.IsTrue(vm.expandedConfig["security.secureboot"]) {
		srcOvmfFile = filepath.Join(vm.ovmfPath(), "OVMF_VARS.ms.fd")
	}

	if !shared.PathExists(srcOvmfFile) {
		return fmt.Errorf("Required EFI firmware settings file missing: %s", srcOvmfFile)
	}

	os.Remove(vm.getNvramPath())
	err := shared.FileCopy(srcOvmfFile, vm.getNvramPath())
	if err != nil {
		return err
	}

	return nil
}

func (vm *Qemu) qemuArchConfig() (string, string, string, error) {
	if vm.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		conf := `
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"

[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "1"
`
		return "qemu-system-x86_64", "q35", conf, nil
	} else if vm.architecture == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN {
		return "qemu-system-aarch64", "virt", "", nil
	}

	return "", "", "", fmt.Errorf("Architecture isn't supported for virtual machines")
}

// deviceVolatileGetFunc returns a function that retrieves a named device's volatile config and
// removes its device prefix from the keys.
func (vm *Qemu) deviceVolatileGetFunc(devName string) func() map[string]string {
	return func() map[string]string {
		volatile := make(map[string]string)
		prefix := fmt.Sprintf("volatile.%s.", devName)
		for k, v := range vm.localConfig {
			if strings.HasPrefix(k, prefix) {
				volatile[strings.TrimPrefix(k, prefix)] = v
			}
		}
		return volatile
	}
}

// deviceVolatileSetFunc returns a function that can be called to save a named device's volatile
// config using keys that do not have the device's name prefixed.
func (vm *Qemu) deviceVolatileSetFunc(devName string) func(save map[string]string) error {
	return func(save map[string]string) error {
		volatileSave := make(map[string]string)
		for k, v := range save {
			volatileSave[fmt.Sprintf("volatile.%s.%s", devName, k)] = v
		}

		return vm.VolatileSet(volatileSave)
	}
}

// deviceLoad instantiates and validates a new device and returns it along with enriched config.
func (vm *Qemu) deviceLoad(deviceName string, rawConfig deviceConfig.Device) (device.Device, deviceConfig.Device, error) {
	var configCopy deviceConfig.Device
	var err error

	// Create copy of config and load some fields from volatile if device is nic or infiniband.
	if shared.StringInSlice(rawConfig["type"], []string{"nic", "infiniband"}) {
		configCopy, err = vm.fillNetworkDevice(deviceName, rawConfig)
		if err != nil {
			return nil, nil, err
		}
	} else {
		// Othewise copy the config so it cannot be modified by device.
		configCopy = rawConfig.Clone()
	}

	d, err := device.New(vm, vm.state, deviceName, configCopy, vm.deviceVolatileGetFunc(deviceName), vm.deviceVolatileSetFunc(deviceName))

	// Return device and config copy even if error occurs as caller may still use device.
	return d, configCopy, err
}

// deviceStart loads a new device and calls its Start() function. After processing the runtime
// config returned from Start(), it also runs the device's Register() function irrespective of
// whether the instance is running or not.
func (vm *Qemu) deviceStart(deviceName string, rawConfig deviceConfig.Device, isRunning bool) (*deviceConfig.RunConfig, error) {
	d, _, err := vm.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return nil, err
	}

	if canHotPlug, _ := d.CanHotPlug(); isRunning && !canHotPlug {
		return nil, fmt.Errorf("Device cannot be started when instance is running")
	}

	runConf, err := d.Start()
	if err != nil {
		return nil, err
	}

	return runConf, nil
}

// deviceStop loads a new device and calls its Stop() function.
func (vm *Qemu) deviceStop(deviceName string, rawConfig deviceConfig.Device) error {
	d, _, err := vm.deviceLoad(deviceName, rawConfig)

	// If deviceLoad fails with unsupported device type then return.
	if err == device.ErrUnsupportedDevType {
		return err
	}

	// If deviceLoad fails for any other reason then just log the error and proceed, as in the
	// scenario that a new version of LXD has additional validation restrictions than older
	// versions we still need to allow previously valid devices to be stopped.
	if err != nil {
		// If there is no device returned, then we cannot proceed, so return as error.
		if d == nil {
			return fmt.Errorf("Device stop validation failed for '%s': %v", deviceName, err)

		}

		logger.Errorf("Device stop validation failed for '%s': %v", deviceName, err)
	}

	canHotPlug, _ := d.CanHotPlug()

	// An empty netns path means we haven't been called from the LXC stop hook, so are running.
	if vm.IsRunning() && !canHotPlug {
		return fmt.Errorf("Device cannot be stopped when instance is running")
	}

	runConf, err := d.Stop()
	if err != nil {
		return err
	}

	if runConf != nil {
		// Run post stop hooks irrespective of run state of instance.
		err = vm.runHooks(runConf.PostHooks)
		if err != nil {
			return err
		}
	}

	return nil
}

// runHooks executes the callback functions returned from a function.
func (vm *Qemu) runHooks(hooks []func() error) error {
	// Run any post start hooks.
	if len(hooks) > 0 {
		for _, hook := range hooks {
			err := hook()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (vm *Qemu) getMonitorPath() string {
	return filepath.Join(vm.LogPath(), "qemu.monitor")
}

func (vm *Qemu) getNvramPath() string {
	return filepath.Join(vm.Path(), "qemu.nvram")
}

// generateConfigShare generates the config share directory that will be exported to the VM via
// a 9P share. Due to the unknown size of templates inside the images this directory is created
// inside the VM's config volume so that it can be restricted by quota.
func (vm *Qemu) generateConfigShare() error {
	// Mount the instance's config volume if needed.
	ourMount, err := vm.mount()
	if err != nil {
		return err
	}

	if ourMount {
		defer vm.unmount()
	}

	configDrivePath := filepath.Join(vm.Path(), "config")

	// Create config drive dir.
	os.RemoveAll(configDrivePath)
	err = os.MkdirAll(configDrivePath, 0500)
	if err != nil {
		return err
	}

	// Generate the cloud-init config.
	err = os.MkdirAll(filepath.Join(configDrivePath, "cloud-init"), 0500)
	if err != nil {
		return err
	}

	if vm.ExpandedConfig()["user.user-data"] != "" {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "user-data"), []byte(vm.ExpandedConfig()["user.user-data"]), 0400)
		if err != nil {
			return err
		}
	} else {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "user-data"), []byte("#cloud-config\n"), 0400)
		if err != nil {
			return err
		}
	}

	if vm.ExpandedConfig()["user.vendor-data"] != "" {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "vendor-data"), []byte(vm.ExpandedConfig()["user.vendor-data"]), 0400)
		if err != nil {
			return err
		}
	} else {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "vendor-data"), []byte("#cloud-config\n"), 0400)
		if err != nil {
			return err
		}
	}

	if vm.ExpandedConfig()["user.network-config"] != "" {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "network-config"), []byte(vm.ExpandedConfig()["user.network-config"]), 0400)
		if err != nil {
			return err
		}
	} else {
		os.Remove(filepath.Join(configDrivePath, "cloud-init", "network-config"))
	}

	// Append any user.meta-data to our predefined meta-data config.
	err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "meta-data"), []byte(fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n%s\n", vm.Name(), vm.Name(), vm.ExpandedConfig()["user.meta-data"])), 0400)
	if err != nil {
		return err
	}

	// Add the VM agent.
	path, err := exec.LookPath("lxd-agent")
	if err != nil {
		logger.Warnf("lxd-agent not found, skipping its inclusion in the VM config drive: %v", err)
	} else {
		// Install agent into config drive dir if found.
		err = shared.FileCopy(path, filepath.Join(configDrivePath, "lxd-agent"))
		if err != nil {
			return err
		}

		err = os.Chmod(filepath.Join(configDrivePath, "lxd-agent"), 0500)
		if err != nil {
			return err
		}

		err = os.Chown(filepath.Join(configDrivePath, "lxd-agent"), 0, 0)
		if err != nil {
			return err
		}
	}

	agentCert, agentKey, clientCert, _, err := vm.generateAgentCert()
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "server.crt"), []byte(clientCert), 0400)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "agent.crt"), []byte(agentCert), 0400)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "agent.key"), []byte(agentKey), 0400)
	if err != nil {
		return err
	}

	// Systemd units.
	err = os.MkdirAll(filepath.Join(configDrivePath, "systemd"), 0500)
	if err != nil {
		return err
	}

	lxdAgentServiceUnit := `[Unit]
Description=LXD - agent
ConditionPathExists=/dev/virtio-ports/org.linuxcontainers.lxd
Requires=lxd-agent-9p.service
After=lxd-agent-9p.service
Before=cloud-init.target

[Service]
Type=simple
WorkingDirectory=/run/lxd_config
ExecStart=/run/lxd_config/lxd-agent

[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "systemd", "lxd-agent.service"), []byte(lxdAgentServiceUnit), 0400)
	if err != nil {
		return err
	}

	lxdConfigShareMountUnit := `[Unit]
Description=LXD - agent - 9p mount
ConditionPathExists=/dev/virtio-ports/org.linuxcontainers.lxd

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=-/sbin/modprobe 9pnet_virtio
ExecStartPre=/bin/mkdir -p /run/lxd_config
ExecStart=/bin/mount -t 9p config /run/lxd_config

[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "systemd", "lxd-agent-9p.service"), []byte(lxdConfigShareMountUnit), 0400)
	if err != nil {
		return err
	}

	// Install script for manual installs.
	lxdConfigShareInstall := `#!/bin/sh
if [ ! -e "systemd" ] || [ ! -e "lxd-agent" ]; then
    echo "This script must be run from within the 9p mount"
    exit 1
fi

if [ ! -e "/lib/systemd/system" ]; then
    echo "This script only works on systemd systems"
    exit 1
fi

cp systemd/lxd-agent.service /lib/systemd/system/
cp systemd/lxd-agent-9p.service /lib/systemd/system/
systemctl daemon-reload
systemctl enable lxd-agent.service lxd-agent-9p.service

echo ""
echo "LXD agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: systemctl start lxd-agent-9p lxd-agent"
`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "install.sh"), []byte(lxdConfigShareInstall), 0700)
	if err != nil {
		return err
	}

	return nil
}

// generateQemuConfigFile writes the qemu config file and returns its location.
// It writes the config file inside the VM's log path.
func (vm *Qemu) generateQemuConfigFile(qemuType string, qemuConf string, devConfs []*deviceConfig.RunConfig) (string, error) {
	var sb *strings.Builder = &strings.Builder{}

	// Base config. This is common for all VMs and has no variables in it.
	sb.WriteString(fmt.Sprintf(`
# Machine
[machine]
graphics = "off"
type = "%s"
accel = "kvm"
usb = "off"
graphics = "off"
%s
[boot-opts]
strict = "on"

# LXD serial identifier
[device]
driver = "virtio-serial"

[device]
driver = "virtserialport"
name = "org.linuxcontainers.lxd"
chardev = "vserial"

[chardev "vserial"]
backend = "ringbuf"
size = "%dB"

# PCIe root
[device "qemu_pcie1"]
driver = "pcie-root-port"
port = "0x10"
chassis = "1"
bus = "pcie.0"
multifunction = "on"
addr = "0x2"

[device "qemu_scsi"]
driver = "virtio-scsi-pci"
bus = "qemu_pcie1"
addr = "0x0"

# Balloon driver
[device "qemu_pcie2"]
driver = "pcie-root-port"
port = "0x12"
chassis = "2"
bus = "pcie.0"
addr = "0x2.0x1"

[device "qemu_ballon"]
driver = "virtio-balloon-pci"
bus = "qemu_pcie2"
addr = "0x0"

# Random number generator
[object "qemu_rng"]
qom-type = "rng-random"
filename = "/dev/urandom"

[device "qemu_pcie3"]
driver = "pcie-root-port"
port = "0x13"
chassis = "3"
bus = "pcie.0"
addr = "0x2.0x2"

[device "dev-qemu_rng"]
driver = "virtio-rng-pci"
rng = "qemu_rng"
bus = "qemu_pcie3"
addr = "0x0"

# Console
[chardev "console"]
backend = "pty"
`, qemuType, qemuConf, qmp.RingbufSize))

	// Now add the dynamic parts of the config.
	err := vm.addMemoryConfig(sb)
	if err != nil {
		return "", err
	}

	err = vm.addCPUConfig(sb)
	if err != nil {
		return "", err
	}

	vm.addFirmwareConfig(sb)
	vm.addVsockConfig(sb)
	vm.addMonitorConfig(sb)
	vm.addConfDriveConfig(sb)

	for _, runConf := range devConfs {
		// Add root drive device.
		if runConf.RootFS.Path != "" {
			err = vm.addRootDriveConfig(sb)
			if err != nil {
				return "", err
			}
		}

		// Add drive devices.
		if len(runConf.Mounts) > 0 {
			driveIndex := 0
			for _, drive := range runConf.Mounts {
				// Increment so index starts at 1, as root drive uses index 0.
				driveIndex++

				vm.addDriveConfig(sb, driveIndex, drive)
			}
		}

		// Add network device.
		if len(runConf.NetworkInterface) > 0 {
			vm.addNetDevConfig(sb, runConf.NetworkInterface)
		}
	}

	// Write the config file to disk.
	configPath := filepath.Join(vm.LogPath(), "qemu.conf")
	return configPath, ioutil.WriteFile(configPath, []byte(sb.String()), 0640)
}

// addMemoryConfig adds the qemu config required for setting the size of the VM's memory.
func (vm *Qemu) addMemoryConfig(sb *strings.Builder) error {
	// Configure memory limit.
	memSize := vm.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = "1GiB" // Default to 1GiB if no memory limit specified.
	}

	memSizeBytes, err := units.ParseByteSizeString(memSize)
	if err != nil {
		return fmt.Errorf("limits.memory invalid: %v", err)
	}

	sb.WriteString(fmt.Sprintf(`
# Memory
[memory]
size = "%dB"
`, memSizeBytes))

	return nil
}

// addVsockConfig adds the qemu config required for setting up the host->VM vsock socket.
func (vm *Qemu) addVsockConfig(sb *strings.Builder) {
	vsockID := vm.vsockID()

	sb.WriteString(fmt.Sprintf(`
# Vsock
[device "qemu_pcie4"]
driver = "pcie-root-port"
port = "0x13"
chassis = "4"
bus = "pcie.0"
addr = "0x2.0x3"

[device]
driver = "vhost-vsock-pci"
guest-cid = "%d"
bus = "qemu_pcie4"
addr = "0x0"
`, vsockID))

	return
}

// addCPUConfig adds the qemu config required for setting the number of virtualised CPUs.
func (vm *Qemu) addCPUConfig(sb *strings.Builder) error {
	// Configure CPU limit. TODO add control of sockets, cores and threads.
	cpus := vm.expandedConfig["limits.cpu"]
	if cpus == "" {
		cpus = "1"
	}

	cpuCount, err := strconv.Atoi(cpus)
	if err != nil {
		return fmt.Errorf("limits.cpu invalid: %v", err)
	}

	sb.WriteString(fmt.Sprintf(`
# CPU
[smp-opts]
cpus = "%d"
#sockets = "1"
#cores = "1"
#threads = "1"
`, cpuCount))

	return nil
}

// addMonitorConfig adds the qemu config required for setting up the host side VM monitor device.
func (vm *Qemu) addMonitorConfig(sb *strings.Builder) {
	monitorPath := vm.getMonitorPath()

	sb.WriteString(fmt.Sprintf(`
# Qemu control
[chardev "monitor"]
backend = "socket"
path = "%s"
server = "on"
wait = "off"

[mon]
chardev = "monitor"
mode = "control"
`, monitorPath))

	return
}

// addFirmwareConfig adds the qemu config required for adding a secure boot compatible EFI firmware.
func (vm *Qemu) addFirmwareConfig(sb *strings.Builder) {
	nvramPath := vm.getNvramPath()

	sb.WriteString(fmt.Sprintf(`
# Firmware (read only)
[drive]
file = "%s"
if = "pflash"
format = "raw"
unit = "0"
readonly = "on"

# Firmware settings (writable)
[drive]
file = "%s"
if = "pflash"
format = "raw"
unit = "1"
`, filepath.Join(vm.ovmfPath(), "OVMF_CODE.fd"), nvramPath))

	return
}

// addConfDriveConfig adds the qemu config required for adding the config drive.
func (vm *Qemu) addConfDriveConfig(sb *strings.Builder) {
	// Devices use "qemu_" prefix indicating that this is a internally named device.
	sb.WriteString(fmt.Sprintf(`
# Config drive
[fsdev "qemu_config"]
fsdriver = "local"
security_model = "none"
readonly = "on"
path = "%s"

[device "dev-qemu_config"]
driver = "virtio-9p-pci"
fsdev = "qemu_config"
mount_tag = "config"
`, filepath.Join(vm.Path(), "config")))

	return
}

// addRootDriveConfig adds the qemu config required for adding the root drive.
func (vm *Qemu) addRootDriveConfig(sb *strings.Builder) error {
	pool, err := vm.getStoragePool()
	if err != nil {
		return err
	}

	rootDrivePath, err := pool.GetInstanceDisk(vm)
	if err != nil {
		return err
	}

	// Devices use "lxd_" prefix indicating that this is a user named device.
	sb.WriteString(fmt.Sprintf(`
# Root drive ("root" device)
[drive "lxd_root"]
file = "%s"
format = "raw"
if = "none"
cache = "none"
aio = "native"

[device "dev-lxd_root"]
driver = "scsi-hd"
bus = "qemu_scsi.0"
channel = "0"
scsi-id = "0"
lun = "1"
drive = "lxd_root"
bootindex = "1"
`, rootDrivePath))

	return nil
}

// addDriveConfig adds the qemu config required for adding a supplementary drive.
func (vm *Qemu) addDriveConfig(sb *strings.Builder, driveIndex int, driveConf deviceConfig.MountEntryItem) {
	driveName := fmt.Sprintf(driveConf.TargetPath)

	// Devices use "lxd_" prefix indicating that this is a user named device.
	sb.WriteString(fmt.Sprintf(`
# %s drive
[drive "lxd_%s"]
file = "%s"
format = "raw"
if = "none"
cache = "none"
aio = "native"

[device "dev-lxd_%s"]
driver = "scsi-hd"
bus = "qemu_scsi.0"
channel = "0"
scsi-id = "%d"
lun = "1"
drive = "lxd_%s"
`, driveName, driveName, driveConf.DevPath, driveName, driveIndex, driveName))

	return
}

// addNetDevConfig adds the qemu config required for adding a network device.
func (vm *Qemu) addNetDevConfig(sb *strings.Builder, nicConfig []deviceConfig.RunConfigItem) {
	var devName, devTap, devHwaddr string
	for _, nicItem := range nicConfig {
		if nicItem.Key == "name" {
			devName = nicItem.Value
		} else if nicItem.Key == "link" {
			devTap = nicItem.Value
		} else if nicItem.Key == "hwaddr" {
			devHwaddr = nicItem.Value
		}
	}

	// Devices use "lxd_" prefix indicating that this is a user named device.
	sb.WriteString(fmt.Sprintf(`
# Network card ("%s" device)
[netdev "lxd_%s"]
type = "tap"
ifname = "%s"
script = "no"
downscript = "no"

[device "qemu_pcie5"]
driver = "pcie-root-port"
port = "0x11"
chassis = "5"
bus = "pcie.0"
addr = "0x2.0x4"

[device "dev-lxd_eth0"]
driver = "virtio-net-pci"
netdev = "lxd_eth0"
mac = "%s"
bus = "qemu_pcie5"
addr = "0x0"
bootindex = "2""
`, devName, devName, devTap, devHwaddr))

	return
}

// pidFilePath returns the path where the qemu process should write its PID.
func (vm *Qemu) pidFilePath() string {
	return filepath.Join(vm.LogPath(), "qemu.pid")
}

// pid gets the PID of the running qemu process.
func (vm *Qemu) pid() (int, error) {
	pidStr, err := ioutil.ReadFile(vm.pidFilePath())
	if os.IsNotExist(err) {
		return 0, nil
	}

	if err != nil {
		return -1, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidStr)))
	if err != nil {
		return -1, err
	}

	return pid, nil
}

// Stop stops the VM.
func (vm *Qemu) Stop(stateful bool) error {
	if stateful {
		return fmt.Errorf("Stateful stop isn't supported for VMs at this time")
	}

	if !vm.IsRunning() {
		return fmt.Errorf("Instance is not running")
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		// If we fail to connect, it's most likely because the VM is already off.
		return nil
	}

	// Get the wait channel.
	chDisconnect, err := monitor.Wait()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			return nil
		}

		return err
	}

	// Send the quit command.
	err = monitor.Quit()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			return nil
		}

		return err
	}

	// Wait for QEMU to exit (can take a while if pending I/O).
	<-chDisconnect

	return nil
}

// Unfreeze restores the instance to running.
func (vm *Qemu) Unfreeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// Send the cont command.
	err = monitor.Start()
	if err != nil {
		return err
	}

	return nil
}

// IsPrivileged does not apply to virtual machines. Always returns false.
func (vm *Qemu) IsPrivileged() bool {
	return false
}

// Restore restores an instance snapshot.
func (vm *Qemu) Restore(source instance.Instance, stateful bool) error {
	return fmt.Errorf("Restore Not implemented")
}

// Snapshots returns a list of snapshots.
func (vm *Qemu) Snapshots() ([]instance.Instance, error) {
	return []instance.Instance{}, nil
}

// Backups returns a list of backups.
func (vm *Qemu) Backups() ([]backup.Backup, error) {
	return []backup.Backup{}, nil
}

// Rename the instance.
func (vm *Qemu) Rename(newName string) error {
	return fmt.Errorf("Rename Not implemented")
}

// Update the instance config.
func (vm *Qemu) Update(args db.InstanceArgs, userRequested bool) error {
	if vm.IsRunning() {
		return fmt.Errorf("Update whilst running not supported")
	}

	// Set sane defaults for unset keys.
	if args.Project == "" {
		args.Project = "default"
	}

	if args.Architecture == 0 {
		args.Architecture = vm.architecture
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Profiles == nil {
		args.Profiles = []string{}
	}

	// Validate the new config.
	err := instance.ValidConfig(vm.state.OS, args.Config, false, false)
	if err != nil {
		return errors.Wrap(err, "Invalid config")
	}

	// Validate the new devices without using expanded devices validation (expensive checks disabled).
	err = instance.ValidDevices(vm.state, vm.state.Cluster, vm.Type(), vm.Name(), args.Devices, false)
	if err != nil {
		return errors.Wrap(err, "Invalid devices")
	}

	// Validate the new profiles.
	profiles, err := vm.state.Cluster.Profiles(args.Project)
	if err != nil {
		return errors.Wrap(err, "Failed to get profiles")
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return fmt.Errorf("Requested profile '%s' doesn't exist", profile)
		}

		if shared.StringInSlice(profile, checkedProfiles) {
			return fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile)
	}

	// Validate the new architecture.
	if args.Architecture != 0 {
		_, err = osarch.ArchitectureName(args.Architecture)
		if err != nil {
			return fmt.Errorf("Invalid architecture ID: %s", err)
		}
	}

	// Check that volatile and image keys weren't modified.
	if userRequested {
		for k, v := range args.Config {
			if strings.HasPrefix(k, "volatile.") && vm.localConfig[k] != v {
				return fmt.Errorf("Volatile keys are read-only")
			}

			if strings.HasPrefix(k, "image.") && vm.localConfig[k] != v {
				return fmt.Errorf("Image keys are read-only")
			}
		}

		for k, v := range vm.localConfig {
			if strings.HasPrefix(k, "volatile.") && args.Config[k] != v {
				return fmt.Errorf("Volatile keys are read-only")
			}

			if strings.HasPrefix(k, "image.") && args.Config[k] != v {
				return fmt.Errorf("Image keys are read-only")
			}
		}
	}

	// Get a copy of the old configuration.
	oldDescription := vm.Description()
	oldArchitecture := 0
	err = shared.DeepCopy(&vm.architecture, &oldArchitecture)
	if err != nil {
		return err
	}

	oldEphemeral := false
	err = shared.DeepCopy(&vm.ephemeral, &oldEphemeral)
	if err != nil {
		return err
	}

	oldExpandedDevices := deviceConfig.Devices{}
	err = shared.DeepCopy(&vm.expandedDevices, &oldExpandedDevices)
	if err != nil {
		return err
	}

	oldExpandedConfig := map[string]string{}
	err = shared.DeepCopy(&vm.expandedConfig, &oldExpandedConfig)
	if err != nil {
		return err
	}

	oldLocalDevices := deviceConfig.Devices{}
	err = shared.DeepCopy(&vm.localDevices, &oldLocalDevices)
	if err != nil {
		return err
	}

	oldLocalConfig := map[string]string{}
	err = shared.DeepCopy(&vm.localConfig, &oldLocalConfig)
	if err != nil {
		return err
	}

	oldProfiles := []string{}
	err = shared.DeepCopy(&vm.profiles, &oldProfiles)
	if err != nil {
		return err
	}

	oldExpiryDate := vm.expiryDate

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path.  Track whether or not we want to undo the changes
	// using a closure.
	undoChanges := true
	defer func() {
		if undoChanges {
			vm.description = oldDescription
			vm.architecture = oldArchitecture
			vm.ephemeral = oldEphemeral
			vm.expandedConfig = oldExpandedConfig
			vm.expandedDevices = oldExpandedDevices
			vm.localConfig = oldLocalConfig
			vm.localDevices = oldLocalDevices
			vm.profiles = oldProfiles
			vm.expiryDate = oldExpiryDate
		}
	}()

	// Apply the various changes.
	vm.description = args.Description
	vm.architecture = args.Architecture
	vm.ephemeral = args.Ephemeral
	vm.localConfig = args.Config
	vm.localDevices = args.Devices
	vm.profiles = args.Profiles
	vm.expiryDate = args.ExpiryDate

	// Expand the config and refresh the LXC config.
	err = vm.expandConfig(nil)
	if err != nil {
		return errors.Wrap(err, "Expand config")
	}

	err = vm.expandDevices(nil)
	if err != nil {
		return errors.Wrap(err, "Expand devices")
	}

	// Diff the configurations.
	changedConfig := []string{}
	for key := range oldExpandedConfig {
		if oldExpandedConfig[key] != vm.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range vm.expandedConfig {
		if oldExpandedConfig[key] != vm.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices.
	removeDevices, addDevices, updateDevices, updateDiff := oldExpandedDevices.Update(vm.expandedDevices, func(oldDevice deviceConfig.Device, newDevice deviceConfig.Device) []string {
		// This function needs to return a list of fields that are excluded from differences
		// between oldDevice and newDevice. The result of this is that as long as the
		// devices are otherwise identical except for the fields returned here, then the
		// device is considered to be being "updated" rather than "added & removed".
		if oldDevice["type"] != newDevice["type"] || oldDevice["nictype"] != newDevice["nictype"] {
			return []string{} // Device types aren't the same, so this cannot be an update.
		}

		d, err := device.New(vm, vm.state, "", newDevice, nil, nil)
		if err != nil {
			return []string{} // Couldn't create Device, so this cannot be an update.
		}

		_, updateFields := d.CanHotPlug()
		return updateFields
	})

	// Do some validation of the config diff.
	err = instance.ValidConfig(vm.state.OS, vm.expandedConfig, false, true)
	if err != nil {
		return errors.Wrap(err, "Invalid expanded config")
	}

	// Do full expanded validation of the devices diff.
	err = instance.ValidDevices(vm.state, vm.state.Cluster, vm.Type(), vm.Name(), vm.expandedDevices, true)
	if err != nil {
		return errors.Wrap(err, "Invalid expanded devices")
	}

	// Use the device interface to apply update changes.
	err = vm.updateDevices(removeDevices, addDevices, updateDevices, oldExpandedDevices)
	if err != nil {
		return err
	}

	// Update MAAS (must run after the MAC addresses have been generated).
	updateMAAS := false
	for _, key := range []string{"maas.subnet.ipv4", "maas.subnet.ipv6", "ipv4.address", "ipv6.address"} {
		if shared.StringInSlice(key, updateDiff) {
			updateMAAS = true
			break
		}
	}

	if !vm.IsSnapshot() && updateMAAS {
		err = vm.maasUpdate(oldExpandedDevices.CloneNative())
		if err != nil {
			return err
		}
	}

	if shared.StringInSlice("security.secureboot", changedConfig) {
		// Re-generate the NVRAM.
		err = vm.setupNvram()
		if err != nil {
			return err
		}
	}

	// Finally, apply the changes to the database.
	err = query.Retry(func() error {
		tx, err := vm.state.Cluster.Begin()
		if err != nil {
			return err
		}

		// Snapshots should update only their descriptions and expiry date.
		if vm.IsSnapshot() {
			err = db.InstanceSnapshotUpdate(tx, vm.id, vm.description, vm.expiryDate)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "Snapshot update")
			}
		} else {
			err = db.ContainerConfigClear(tx, vm.id)
			if err != nil {
				tx.Rollback()
				return err

			}
			err = db.ContainerConfigInsert(tx, vm.id, vm.localConfig)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "Config insert")
			}

			err = db.ContainerProfilesInsert(tx, vm.id, vm.project, vm.profiles)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "Profiles insert")
			}

			err = db.DevicesAdd(tx, "instance", int64(vm.id), vm.localDevices)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "Device add")
			}

			err = db.ContainerUpdate(tx, vm.id, vm.description, vm.architecture, vm.ephemeral, vm.expiryDate)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "Instance update")
			}

		}

		if err := db.TxCommit(tx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "Failed to update database")
	}

	err = instance.WriteBackupFile(vm.state, vm)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "Failed to write backup file")
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	var endpoint string

	if vm.IsSnapshot() {
		parentName, snapName, _ := shared.InstanceGetParentAndSnapshotName(vm.name)
		endpoint = fmt.Sprintf("/1.0/virtual-machines/%s/snapshots/%s", parentName, snapName)
	} else {
		endpoint = fmt.Sprintf("/1.0/virtual-machines/%s", vm.name)
	}

	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-updated", endpoint, nil)
	return nil
}

func (vm *Qemu) updateDevices(removeDevices deviceConfig.Devices, addDevices deviceConfig.Devices, updateDevices deviceConfig.Devices, oldExpandedDevices deviceConfig.Devices) error {
	isRunning := vm.IsRunning()

	// Remove devices in reverse order to how they were added.
	for _, dev := range removeDevices.Reversed() {
		if isRunning {
			err := vm.deviceStop(dev.Name, dev.Config)
			if err == device.ErrUnsupportedDevType {
				continue // No point in trying to remove device below.
			} else if err != nil {
				return errors.Wrapf(err, "Failed to stop device '%s'", dev.Name)
			}
		}

		err := vm.deviceRemove(dev.Name, dev.Config)
		if err != nil && err != device.ErrUnsupportedDevType {
			return errors.Wrapf(err, "Failed to remove device '%s'", dev.Name)
		}

		// Check whether we are about to add the same device back with updated config and
		// if not, or if the device type has changed, then remove all volatile keys for
		// this device (as its an actual removal or a device type change).
		err = vm.deviceResetVolatile(dev.Name, dev.Config, addDevices[dev.Name])
		if err != nil {
			return errors.Wrapf(err, "Failed to reset volatile data for device '%s'", dev.Name)
		}
	}

	// Add devices in sorted order, this ensures that device mounts are added in path order.
	for _, dev := range addDevices.Sorted() {
		err := vm.deviceAdd(dev.Name, dev.Config)
		if err == device.ErrUnsupportedDevType {
			continue // No point in trying to start device below.
		} else if err != nil {
			return errors.Wrapf(err, "Failed to add device '%s'", dev.Name)
		}

		if isRunning {
			_, err := vm.deviceStart(dev.Name, dev.Config, isRunning)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to start device '%s'", dev.Name)
			}
		}
	}

	for _, dev := range updateDevices.Sorted() {
		err := vm.deviceUpdate(dev.Name, dev.Config, oldExpandedDevices, isRunning)
		if err != nil && err != device.ErrUnsupportedDevType {
			return errors.Wrapf(err, "Failed to update device '%s'", dev.Name)
		}
	}

	return nil
}

// deviceUpdate loads a new device and calls its Update() function.
func (vm *Qemu) deviceUpdate(deviceName string, rawConfig deviceConfig.Device, oldDevices deviceConfig.Devices, isRunning bool) error {
	d, _, err := vm.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return err
	}

	err = d.Update(oldDevices, isRunning)
	if err != nil {
		return err
	}

	return nil
}

// deviceResetVolatile resets a device's volatile data when its removed or updated in such a way
// that it is removed then added immediately afterwards.
func (vm *Qemu) deviceResetVolatile(devName string, oldConfig, newConfig deviceConfig.Device) error {
	volatileClear := make(map[string]string)
	devicePrefix := fmt.Sprintf("volatile.%s.", devName)

	// If the device type has changed, remove all old volatile keys.
	// This will occur if the newConfig is empty (i.e the device is actually being removed) or
	// if the device type is being changed but keeping the same name.
	if newConfig["type"] != oldConfig["type"] || newConfig["nictype"] != oldConfig["nictype"] {
		for k := range vm.localConfig {
			if !strings.HasPrefix(k, devicePrefix) {
				continue
			}

			volatileClear[k] = ""
		}

		return vm.VolatileSet(volatileClear)
	}

	// If the device type remains the same, then just remove any volatile keys that have
	// the same key name present in the new config (i.e the new config is replacing the
	// old volatile key).
	for k := range vm.localConfig {
		if !strings.HasPrefix(k, devicePrefix) {
			continue
		}

		devKey := strings.TrimPrefix(k, devicePrefix)
		if _, found := newConfig[devKey]; found {
			volatileClear[k] = ""
		}
	}

	return vm.VolatileSet(volatileClear)
}

func (vm *Qemu) removeUnixDevices() error {
	// Check that we indeed have devices to remove.
	if !shared.PathExists(vm.DevicesPath()) {
		return nil
	}

	// Load the directory listing.
	dents, err := ioutil.ReadDir(vm.DevicesPath())
	if err != nil {
		return err
	}

	// Go through all the unix devices.
	for _, f := range dents {
		// Skip non-Unix devices.
		if !strings.HasPrefix(f.Name(), "forkmknod.unix.") && !strings.HasPrefix(f.Name(), "unix.") && !strings.HasPrefix(f.Name(), "infiniband.unix.") {
			continue
		}

		// Remove the entry
		devicePath := filepath.Join(vm.DevicesPath(), f.Name())
		err := os.Remove(devicePath)
		if err != nil {
			logger.Error("Failed removing unix device", log.Ctx{"err": err, "path": devicePath})
		}
	}

	return nil
}

func (vm *Qemu) removeDiskDevices() error {
	// Check that we indeed have devices to remove.vm
	if !shared.PathExists(vm.DevicesPath()) {
		return nil
	}

	// Load the directory listing.
	dents, err := ioutil.ReadDir(vm.DevicesPath())
	if err != nil {
		return err
	}

	// Go through all the unix devices
	for _, f := range dents {
		// Skip non-disk devices
		if !strings.HasPrefix(f.Name(), "disk.") {
			continue
		}

		// Always try to unmount the host side
		_ = unix.Unmount(filepath.Join(vm.DevicesPath(), f.Name()), unix.MNT_DETACH)

		// Remove the entry
		diskPath := filepath.Join(vm.DevicesPath(), f.Name())
		err := os.Remove(diskPath)
		if err != nil {
			logger.Error("Failed to remove disk device path", log.Ctx{"err": err, "path": diskPath})
		}
	}

	return nil
}

func (vm *Qemu) cleanup() {
	// Unmount any leftovers
	vm.removeUnixDevices()
	vm.removeDiskDevices()

	// Remove the devices path
	os.Remove(vm.DevicesPath())

	// Remove the shmounts path
	os.RemoveAll(vm.ShmountsPath())
}

// cleanupDevices performs any needed device cleanup steps when instance is stopped.
func (vm *Qemu) cleanupDevices() {
	for _, dev := range vm.expandedDevices.Sorted() {
		// Use the device interface if device supports it.
		err := vm.deviceStop(dev.Name, dev.Config)
		if err == device.ErrUnsupportedDevType {
			continue
		} else if err != nil {
			logger.Errorf("Failed to stop device '%s': %v", dev.Name, err)
		}
	}
}

func (vm *Qemu) init() error {
	// Compute the expanded config and device list.
	err := vm.expandConfig(nil)
	if err != nil {
		return err
	}

	err = vm.expandDevices(nil)
	if err != nil {
		return err
	}

	return nil
}

// Delete the instance.
func (vm *Qemu) Delete() error {
	ctxMap := log.Ctx{
		"project":   vm.project,
		"name":      vm.name,
		"created":   vm.creationDate,
		"ephemeral": vm.ephemeral,
		"used":      vm.lastUsedDate}

	logger.Info("Deleting instance", ctxMap)

	// Check if instance is delete protected.
	if shared.IsTrue(vm.expandedConfig["security.protection.delete"]) && !vm.IsSnapshot() {
		return fmt.Errorf("Instance is protected")
	}

	// Check if we're dealing with "lxd import".
	// TODO consider lxd import detection for VMs.
	isImport := false

	// Attempt to initialize storage interface for the instance.
	pool, err := vm.getStoragePool()
	if err != nil && err != db.ErrNoSuchObject {
		// Because of the way QemuCreate creates the storage volume record before loading
		// the storage pool driver, Delete() may be called as part of a revertion if the
		// pool being used to create the VM on doesn't support VMs. This deletion will then
		// fail too, so we need to detect this scenario and just remove the storage volume
		// DB record.
		// TODO: This can be removed once all pool drivers are ported to new storage layer.
		if err == storageDrivers.ErrUnknownDriver || err == storageDrivers.ErrNotImplemented {
			logger.Warn("Unsupported storage pool type, removing DB volume record", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
			// Remove the volume record from the database. This deletion would
			// normally be handled by DeleteInstance() call below but since the storage
			// driver (new storage) is not implemented, we need to do it here manually.
			poolName, err := vm.StoragePool()
			if err != nil {
				return err
			}

			poolID, err := vm.state.Cluster.StoragePoolGetID(poolName)
			if err != nil {
				return err
			}

			err = vm.state.Cluster.StoragePoolVolumeDelete(vm.Project(), vm.Name(), db.StoragePoolVolumeTypeVM, poolID)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	if pool != nil {
		if vm.IsSnapshot() {
			if !isImport {
				// Remove snapshot volume and database record.
				err = pool.DeleteInstanceSnapshot(vm, nil)
				if err != nil {
					return err
				}
			}
		} else {
			// Remove all snapshots by initialising each snapshot as an Instance and
			// calling its Delete function.
			err := instance.DeleteSnapshots(vm.state, vm.Project(), vm.Name())
			if err != nil {
				return err
			}

			if !isImport {
				// Remove the storage volume, snapshot volumes and database records.
				err = pool.DeleteInstance(vm, nil)
				if err != nil {
					return err
				}
			}
		}
	}

	// Perform other cleanup steps if not snapshot.
	if !vm.IsSnapshot() {
		// Remove all backups.
		backups, err := vm.Backups()
		if err != nil {
			return err
		}

		for _, backup := range backups {
			err = backup.Delete()
			if err != nil {
				return err
			}
		}

		// Delete the MAAS entry.
		err = vm.maasDelete()
		if err != nil {
			logger.Error("Failed deleting instance MAAS record", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
			return err
		}

		// Run device removal function for each device.
		for k, m := range vm.expandedDevices {
			err = vm.deviceRemove(k, m)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to remove device '%s'", k)
			}
		}

		// Clean things up.
		vm.cleanup()
	}

	// Remove the database record of the instance or snapshot instance.
	if err := vm.state.Cluster.InstanceRemove(vm.Project(), vm.Name()); err != nil {
		logger.Error("Failed deleting instance entry", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
		return err
	}

	logger.Info("Deleted instance", ctxMap)

	if vm.IsSnapshot() {
		vm.state.Events.SendLifecycle(vm.project, "virtual-machine-snapshot-deleted",
			fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), map[string]interface{}{
				"snapshot_name": vm.name,
			})
	} else {
		vm.state.Events.SendLifecycle(vm.project, "virtual-machine-deleted",
			fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)
	}

	return nil
}

func (vm *Qemu) deviceAdd(deviceName string, rawConfig deviceConfig.Device) error {
	return nil
}

func (vm *Qemu) deviceRemove(deviceName string, rawConfig deviceConfig.Device) error {
	return nil
}

// Export publishes the instance.
func (vm *Qemu) Export(w io.Writer, properties map[string]string) error {
	return fmt.Errorf("Export Not implemented")
}

// CGroupGetV1 is not implemented for VMs.
func (vm *Qemu) CGroupGet(property cgroup.Property) (string, error) {
	return "", fmt.Errorf("CGroupGet Not implemented")
}

// CGroupGetV1 is not implemented for VMs.
func (vm *Qemu) CGroupGetV1(key string) (string, error) {
	return "", fmt.Errorf("CGroupGetV1 Not implemented")
}

// CGroupSet is not implemented for VMs.
func (vm *Qemu) CGroupSet(property cgroup.Property, value string) error {
	return fmt.Errorf("CGroupSet Not implemented")
}

// CGroupSetV1 is not implemented for VMs.
func (vm *Qemu) CGroupSetV1(key string, value string) error {
	return fmt.Errorf("CGroupSetV1 Not implemented")
}

// VolatileSet sets one or more volatile config keys.
func (vm *Qemu) VolatileSet(changes map[string]string) error {
	// Sanity check.
	for key := range changes {
		if !strings.HasPrefix(key, "volatile.") {
			return fmt.Errorf("Only volatile keys can be modified with VolatileSet")
		}
	}

	// Update the database.
	var err error
	if vm.IsSnapshot() {
		err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.InstanceSnapshotConfigUpdate(vm.id, changes)
		})
	} else {
		err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.ContainerConfigUpdate(vm.id, changes)
		})
	}
	if err != nil {
		return errors.Wrap(err, "Failed to volatile config")
	}

	// Apply the change locally.
	for key, value := range changes {
		if value == "" {
			delete(vm.expandedConfig, key)
			delete(vm.localConfig, key)
			continue
		}

		vm.expandedConfig[key] = value
		vm.localConfig[key] = value
	}

	return nil
}

// FileExists is not implemented for VMs.
func (vm *Qemu) FileExists(path string) error {
	return fmt.Errorf("FileExists Not implemented")
}

// FilePull retrieves a file from the instance.
func (vm *Qemu) FilePull(srcPath string, dstPath string) (int64, int64, os.FileMode, string, []string, error) {
	client, err := vm.getAgentClient()
	if err != nil {
		return 0, 0, 0, "", nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", vm.Name(), err)
		return 0, 0, 0, "", nil, fmt.Errorf("Failed to connect to lxd-agent")
	}
	defer agent.Disconnect()

	content, resp, err := agent.GetInstanceFile("", srcPath)
	if err != nil {
		return 0, 0, 0, "", nil, err
	}

	switch resp.Type {
	case "file", "symlink":
		data, err := ioutil.ReadAll(content)
		if err != nil {
			return 0, 0, 0, "", nil, err
		}

		err = ioutil.WriteFile(dstPath, data, os.FileMode(resp.Mode))
		if err != nil {
			return 0, 0, 0, "", nil, err
		}

		err = os.Lchown(dstPath, int(resp.UID), int(resp.GID))
		if err != nil {
			return 0, 0, 0, "", nil, err
		}

		return resp.UID, resp.GID, os.FileMode(resp.Mode), resp.Type, nil, nil
	case "directory":
		return resp.UID, resp.GID, os.FileMode(resp.Mode), resp.Type, resp.Entries, nil
	}

	return 0, 0, 0, "", nil, fmt.Errorf("bad file type %s", resp.Type)
}

// FilePush pushes a file into the instance.
func (vm *Qemu) FilePush(fileType string, srcPath string, dstPath string, uid int64, gid int64, mode int, write string) error {
	client, err := vm.getAgentClient()
	if err != nil {
		return err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", vm.Name(), err)
		return fmt.Errorf("Failed to connect to lxd-agent")
	}
	defer agent.Disconnect()

	args := lxdClient.InstanceFileArgs{
		GID:       gid,
		Mode:      mode,
		Type:      fileType,
		UID:       uid,
		WriteMode: write,
	}

	if fileType == "file" {
		f, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer f.Close()

		args.Content = f
	} else if fileType == "symlink" {
		symlinkTarget, err := os.Readlink(dstPath)
		if err != nil {
			return err
		}

		args.Content = bytes.NewReader([]byte(symlinkTarget))
	}

	err = agent.CreateInstanceFile("", dstPath, args)
	if err != nil {
		return err
	}

	return nil
}

// FileRemove removes a file from the instance.
func (vm *Qemu) FileRemove(path string) error {
	// Connect to the agent.
	client, err := vm.getAgentClient()
	if err != nil {
		return err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		return fmt.Errorf("Failed to connect to lxd-agent")
	}
	defer agent.Disconnect()

	// Delete instance file.
	err = agent.DeleteInstanceFile("", path)
	if err != nil {
		return err
	}

	return nil
}

// Console gets access to the instance's console.
func (vm *Qemu) Console() (*os.File, chan error, error) {
	chDisconnect := make(chan error, 1)

	// Avoid duplicate connects.
	vmConsoleLock.Lock()
	if vmConsole[vm.id] {
		vmConsoleLock.Unlock()
		return nil, nil, fmt.Errorf("There is already an active console for this instance")
	}
	vmConsoleLock.Unlock()

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		return nil, nil, err // The VM isn't running as no monitor socket available.
	}

	// Get the console.
	console, err := monitor.Console("console")
	if err != nil {
		return nil, nil, err
	}

	// Record the console is in use.
	vmConsoleLock.Lock()
	vmConsole[vm.id] = true
	vmConsoleLock.Unlock()

	// Handle console disconnection.
	go func() {
		<-chDisconnect

		vmConsoleLock.Lock()
		vmConsole[vm.id] = false
		vmConsoleLock.Unlock()
	}()

	return console, chDisconnect, nil
}

func (vm *Qemu) forwardSignal(control *websocket.Conn, sig unix.Signal) error {
	logger.Debugf("Forwarding signal to lxd-agent: %s", sig)

	w, err := control.NextWriter(websocket.TextMessage)
	if err != nil {
		return err
	}

	msg := api.InstanceExecControl{}
	msg.Command = "signal"
	msg.Signal = int(sig)

	buf, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)

	w.Close()
	return err
}

// Exec a command inside the instance.
func (vm *Qemu) Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File, cwd string, uid uint32, gid uint32) (instance.Cmd, error) {
	var instCmd *Cmd

	// Because this function will exit before the remote command has finished, we create a
	// cleanup function that will be passed to the instance function if successfully started to
	// perform any cleanup needed when finished.
	cleanupFuncs := []func(){}
	cleanupFunc := func() {
		for _, f := range cleanupFuncs {
			f()
		}
	}

	defer func() {
		// If no instance command has been been created it means something went wrong
		// starting the remote command, so we should cleanup as this function ends.
		// If the instance command is non-nil then we let the instance command itself run
		// the cleanup functions when it is done.
		if instCmd == nil {
			cleanupFunc()
		}
	}()

	client, err := vm.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", vm.Name(), err)
		return nil, fmt.Errorf("Failed to connect to lxd-agent")
	}
	cleanupFuncs = append(cleanupFuncs, agent.Disconnect)

	post := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: stdin == stdout,
		Environment: env,
		User:        uid,
		Group:       gid,
		Cwd:         cwd,
	}

	if post.Interactive {
		// Set console to raw.
		oldttystate, err := termios.MakeRaw(int(stdin.Fd()))
		if err != nil {
			return nil, err
		}
		cleanupFuncs = append(cleanupFuncs, func() {
			termios.Restore(int(stdin.Fd()), oldttystate)
		})
	}

	dataDone := make(chan bool)
	signalSendCh := make(chan unix.Signal)
	signalResCh := make(chan error)

	// This is the signal control handler, it receives signals from lxc CLI and forwards them
	// to the VM agent.
	controlHander := func(control *websocket.Conn) {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		defer control.WriteMessage(websocket.CloseMessage, closeMsg)

		for {
			select {
			case signal := <-signalSendCh:
				err := vm.forwardSignal(control, signal)
				signalResCh <- err
			case <-dataDone:
				return
			}
		}
	}

	args := lxdClient.InstanceExecArgs{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   stderr,
		DataDone: dataDone,
		Control:  controlHander,
	}

	op, err := agent.ExecInstance("", post, &args)
	if err != nil {
		return nil, err
	}

	instCmd = &Cmd{
		cmd:              op,
		attachedChildPid: -1, // Process is not running on LXD host.
		dataDone:         args.DataDone,
		cleanupFunc:      cleanupFunc,
		signalSendCh:     signalSendCh,
		signalResCh:      signalResCh,
	}

	return instCmd, nil
}

// Render returns info about the instance.
func (vm *Qemu) Render() (interface{}, interface{}, error) {
	// Ignore err as the arch string on error is correct (unknown)
	architectureName, _ := osarch.ArchitectureName(vm.architecture)

	if vm.IsSnapshot() {
		// Prepare the ETag
		etag := []interface{}{vm.expiryDate}

		vmSnap := api.InstanceSnapshot{
			CreatedAt:       vm.creationDate,
			ExpandedConfig:  vm.expandedConfig,
			ExpandedDevices: vm.expandedDevices.CloneNative(),
			LastUsedAt:      vm.lastUsedDate,
			Name:            strings.SplitN(vm.name, "/", 2)[1],
			Stateful:        vm.stateful,
		}
		vmSnap.Architecture = architectureName
		vmSnap.Config = vm.localConfig
		vmSnap.Devices = vm.localDevices.CloneNative()
		vmSnap.Ephemeral = vm.ephemeral
		vmSnap.Profiles = vm.profiles
		vmSnap.ExpiresAt = vm.expiryDate

		return &vmSnap, etag, nil
	}

	// Prepare the ETag
	etag := []interface{}{vm.architecture, vm.localConfig, vm.localDevices, vm.ephemeral, vm.profiles}

	vmState := api.Instance{
		ExpandedConfig:  vm.expandedConfig,
		ExpandedDevices: vm.expandedDevices.CloneNative(),
		Name:            vm.name,
		Status:          vm.statusCode().String(),
		StatusCode:      vm.statusCode(),
		Location:        vm.node,
		Type:            vm.Type().String(),
	}

	vmState.Description = vm.description
	vmState.Architecture = architectureName
	vmState.Config = vm.localConfig
	vmState.CreatedAt = vm.creationDate
	vmState.Devices = vm.localDevices.CloneNative()
	vmState.Ephemeral = vm.ephemeral
	vmState.LastUsedAt = vm.lastUsedDate
	vmState.Profiles = vm.profiles
	vmState.Stateful = vm.stateful

	return &vmState, etag, nil
}

// RenderFull returns all info about the instance.
func (vm *Qemu) RenderFull() (*api.InstanceFull, interface{}, error) {
	if vm.IsSnapshot() {
		return nil, nil, fmt.Errorf("RenderFull doesn't work with snapshots")
	}

	// Get the Instance struct.
	base, etag, err := vm.Render()
	if err != nil {
		return nil, nil, err
	}

	// Convert to InstanceFull.
	vmState := api.InstanceFull{Instance: *base.(*api.Instance)}

	// Add the InstanceState.
	vmState.State, err = vm.RenderState()
	if err != nil {
		return nil, nil, err
	}

	// Add the InstanceSnapshots.
	snaps, err := vm.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	for _, snap := range snaps {
		render, _, err := snap.Render()
		if err != nil {
			return nil, nil, err
		}

		if vmState.Snapshots == nil {
			vmState.Snapshots = []api.InstanceSnapshot{}
		}

		vmState.Snapshots = append(vmState.Snapshots, *render.(*api.InstanceSnapshot))
	}

	// Add the InstanceBackups.
	backups, err := vm.Backups()
	if err != nil {
		return nil, nil, err
	}

	for _, backup := range backups {
		render := backup.Render()

		if vmState.Backups == nil {
			vmState.Backups = []api.InstanceBackup{}
		}

		vmState.Backups = append(vmState.Backups, *render)
	}

	return &vmState, etag, nil
}

// RenderState returns just state info about the instance.
func (vm *Qemu) RenderState() (*api.InstanceState, error) {
	statusCode := vm.statusCode()
	pid, _ := vm.pid()

	if statusCode == api.Running {
		status, err := vm.agentGetState()
		if err != nil {
			if err != errQemuAgentOffline {
				logger.Warn("Could not get VM state from agent", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
			}

			// Fallback data.
			status = &api.InstanceState{}
			status.Processes = -1
			networks := map[string]api.InstanceStateNetwork{}
			for k, m := range vm.ExpandedDevices() {
				// We only care about nics.
				if m["type"] != "nic" || m["nictype"] != "bridged" {
					continue
				}

				// Fill the MAC address.
				m, err := vm.fillNetworkDevice(k, m)
				if err != nil {
					return nil, err
				}

				// Parse the lease file.
				addresses, err := instance.NetworkGetLeaseAddresses(vm.state, m["parent"], m["hwaddr"])
				if err != nil {
					return nil, err
				}

				if len(addresses) == 0 {
					continue
				}

				// Get MTU.
				iface, err := net.InterfaceByName(m["parent"])
				if err != nil {
					return nil, err
				}

				if m["host_name"] == "" {
					m["host_name"] = vm.localConfig[fmt.Sprintf("volatile.%s.host_name", k)]
				}

				// Retrieve the host counters, as we report the values
				// from the instance's point of view, those counters need to be reversed below.
				hostCounters := shared.NetworkGetCounters(m["host_name"])

				networks[k] = api.InstanceStateNetwork{
					Addresses: addresses,
					Counters: api.InstanceStateNetworkCounters{
						BytesReceived:   hostCounters.BytesSent,
						BytesSent:       hostCounters.BytesReceived,
						PacketsReceived: hostCounters.PacketsSent,
						PacketsSent:     hostCounters.PacketsReceived,
					},
					Hwaddr:   m["hwaddr"],
					HostName: m["host_name"],
					Mtu:      iface.MTU,
					State:    "up",
					Type:     "broadcast",
				}
			}

			status.Network = networks
		}

		status.Pid = int64(pid)
		status.Status = statusCode.String()
		status.StatusCode = statusCode

		return status, nil
	}

	// At least return the Status and StatusCode if we couldn't get any
	// information for the VM agent.
	return &api.InstanceState{
		Pid:        int64(pid),
		Status:     statusCode.String(),
		StatusCode: statusCode,
	}, nil
}

// agentGetState connects to the agent inside of the VM and does
// an API call to get the current state.
func (vm *Qemu) agentGetState() (*api.InstanceState, error) {
	// Check if the agent is running.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		return nil, err
	}

	if !monitor.AgentReady() {
		return nil, errQemuAgentOffline
	}

	client, err := vm.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		return nil, err
	}
	defer agent.Disconnect()

	status, _, err := agent.GetInstanceState("")
	if err != nil {
		return nil, err
	}

	return status, nil
}

// IsRunning returns whether or not the instance is running.
func (vm *Qemu) IsRunning() bool {
	state := vm.State()
	return state != "BROKEN" && state != "STOPPED"
}

// IsFrozen returns whether the instance frozen or not.
func (vm *Qemu) IsFrozen() bool {
	return vm.State() == "FROZEN"
}

// IsEphemeral returns whether the instanc is ephemeral or not.
func (vm *Qemu) IsEphemeral() bool {
	return vm.ephemeral
}

// IsSnapshot returns whether instance is snapshot or not.
func (vm *Qemu) IsSnapshot() bool {
	return vm.snapshot
}

// IsStateful retuens whether instance is stateful or not.
func (vm *Qemu) IsStateful() bool {
	return vm.stateful
}

// DeviceEventHandler handles events occurring on the instance's devices.
func (vm *Qemu) DeviceEventHandler(runConf *deviceConfig.RunConfig) error {
	return fmt.Errorf("DeviceEventHandler Not implemented")
}

// ID returns the instance's ID.
func (vm *Qemu) ID() int {
	return vm.id
}

// vsockID returns the vsock context ID, 3 being the first ID that can be used.
func (vm *Qemu) vsockID() int {
	return vm.id + 3
}

// Location returns instance's location.
func (vm *Qemu) Location() string {
	return vm.node
}

// Project returns instance's project.
func (vm *Qemu) Project() string {
	return vm.project
}

// Name returns the instance's name.
func (vm *Qemu) Name() string {
	return vm.name
}

// Type returns the instance's type.
func (vm *Qemu) Type() instancetype.Type {
	return vm.dbType
}

// Description returns the instance's description.
func (vm *Qemu) Description() string {
	return vm.description
}

// Architecture returns the instance's architecture.
func (vm *Qemu) Architecture() int {
	return vm.architecture
}

// CreationDate returns the instance's creation date.
func (vm *Qemu) CreationDate() time.Time {
	return vm.creationDate
}

// LastUsedDate returns the instance's last used date.
func (vm *Qemu) LastUsedDate() time.Time {
	return vm.lastUsedDate
}

func (vm *Qemu) expandConfig(profiles []api.Profile) error {
	if profiles == nil && len(vm.profiles) > 0 {
		var err error
		profiles, err = vm.state.Cluster.ProfilesGet(vm.project, vm.profiles)
		if err != nil {
			return err
		}
	}

	vm.expandedConfig = db.ProfilesExpandConfig(vm.localConfig, profiles)

	return nil
}

func (vm *Qemu) expandDevices(profiles []api.Profile) error {
	if profiles == nil && len(vm.profiles) > 0 {
		var err error
		profiles, err = vm.state.Cluster.ProfilesGet(vm.project, vm.profiles)
		if err != nil {
			return err
		}
	}

	vm.expandedDevices = db.ProfilesExpandDevices(vm.localDevices, profiles)

	return nil
}

// ExpandedConfig returns instance's expanded config.
func (vm *Qemu) ExpandedConfig() map[string]string {
	return vm.expandedConfig
}

// ExpandedDevices returns instance's expanded device config.
func (vm *Qemu) ExpandedDevices() deviceConfig.Devices {
	return vm.expandedDevices
}

// LocalConfig returns the instance's local config.
func (vm *Qemu) LocalConfig() map[string]string {
	return vm.localConfig
}

// LocalDevices returns the instance's local device config.
func (vm *Qemu) LocalDevices() deviceConfig.Devices {
	return vm.localDevices
}

// Profiles returns the instance's profiles.
func (vm *Qemu) Profiles() []string {
	return vm.profiles
}

// InitPID returns the instance's current process ID.
func (vm *Qemu) InitPID() int {
	pid, _ := vm.pid()
	return pid
}

func (vm *Qemu) statusCode() api.StatusCode {
	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.getMonitorPath(), vm.getMonitorEventHandler())
	if err != nil {
		// If we fail to connect, chances are the VM isn't running.
		return api.Stopped
	}

	status, err := monitor.Status()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			return api.Stopped
		}

		return api.Error
	}

	if status == "running" {
		return api.Running
	} else if status == "paused" {
		return api.Frozen
	}

	return api.Stopped
}

// State returns the instance's state code.
func (vm *Qemu) State() string {
	return strings.ToUpper(vm.statusCode().String())
}

// ExpiryDate returns when this snapshot expires.
func (vm *Qemu) ExpiryDate() time.Time {
	if vm.IsSnapshot() {
		return vm.expiryDate
	}

	// Return zero time if the instance is not a snapshot.
	return time.Time{}
}

// Path returns the instance's path.
func (vm *Qemu) Path() string {
	return storagePools.InstancePath(vm.Type(), vm.Project(), vm.Name(), vm.IsSnapshot())
}

// DevicesPath returns the instance's devices path.
func (vm *Qemu) DevicesPath() string {
	name := project.Prefix(vm.Project(), vm.Name())
	return shared.VarPath("devices", name)
}

// ShmountsPath returns the instance's shared mounts path.
func (vm *Qemu) ShmountsPath() string {
	name := project.Prefix(vm.Project(), vm.Name())
	return shared.VarPath("shmounts", name)
}

// LogPath returns the instance's log path.
func (vm *Qemu) LogPath() string {
	name := project.Prefix(vm.Project(), vm.Name())
	return shared.LogPath(name)
}

// LogFilePath returns the instance's log path.
func (vm *Qemu) LogFilePath() string {
	return filepath.Join(vm.LogPath(), "lxvm.log") // tomp TODO is this correct?
}

// ConsoleBufferLogPath returns the instance's console buffer log path.
func (vm *Qemu) ConsoleBufferLogPath() string {
	return filepath.Join(vm.LogPath(), "console.log")
}

// RootfsPath returns the instance's rootfs path.
func (vm *Qemu) RootfsPath() string {
	return filepath.Join(vm.Path(), "rootfs")
}

// TemplatesPath returns the instance's templates path.
func (vm *Qemu) TemplatesPath() string {
	return filepath.Join(vm.Path(), "templates")
}

// StatePath returns the instance's state path.
func (vm *Qemu) StatePath() string {
	return filepath.Join(vm.Path(), "state")
}

// StoragePool returns the name of the instance's storage pool.
func (vm *Qemu) StoragePool() (string, error) {
	poolName, err := vm.state.Cluster.InstancePool(vm.Project(), vm.Name())
	if err != nil {
		return "", err
	}

	return poolName, nil
}

// SetOperation sets the current operation.
func (vm *Qemu) SetOperation(op *operations.Operation) {
	vm.op = op
}

// StorageStart deprecated.
func (vm *Qemu) StorageStart() (bool, error) {
	return false, storagePools.ErrNotImplemented
}

// StorageStop deprecated.
func (vm *Qemu) StorageStop() (bool, error) {
	return false, storagePools.ErrNotImplemented
}

// DeferTemplateApply not used currently.
func (vm *Qemu) DeferTemplateApply(trigger string) error {
	return nil
}

// DaemonState returns the state of the daemon. Deprecated.
func (vm *Qemu) DaemonState() *state.State {
	// FIXME: This function should go away, since the abstract instance
	//        interface should not be coupled with internal state details.
	//        However this is not currently possible, because many
	//        higher-level APIs use instance variables as "implicit
	//        handles" to database/OS state and then need a way to get a
	//        reference to it.
	return vm.state
}

// fillNetworkDevice takes a nic or infiniband device type and enriches it with automatically
// generated name and hwaddr properties if these are missing from the device.
func (vm *Qemu) fillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
	var err error

	newDevice := m.Clone()
	updateKey := func(key string, value string) error {
		tx, err := vm.state.Cluster.Begin()
		if err != nil {
			return err
		}

		err = db.ContainerConfigInsert(tx, vm.id, map[string]string{key: value})
		if err != nil {
			tx.Rollback()
			return err
		}

		err = db.TxCommit(tx)
		if err != nil {
			return err
		}

		return nil
	}

	// Fill in the MAC address
	if !shared.StringInSlice(m["nictype"], []string{"physical", "ipvlan", "sriov"}) && m["hwaddr"] == "" {
		configKey := fmt.Sprintf("volatile.%s.hwaddr", name)
		volatileHwaddr := vm.localConfig[configKey]
		if volatileHwaddr == "" {
			// Generate a new MAC address
			volatileHwaddr, err = instance.DeviceNextInterfaceHWAddr()
			if err != nil {
				return nil, err
			}

			// Update the database
			err = query.Retry(func() error {
				err := updateKey(configKey, volatileHwaddr)
				if err != nil {
					// Check if something else filled it in behind our back
					value, err1 := vm.state.Cluster.ContainerConfigGet(vm.id, configKey)
					if err1 != nil || value == "" {
						return err
					}

					vm.localConfig[configKey] = value
					vm.expandedConfig[configKey] = value
					return nil
				}

				vm.localConfig[configKey] = volatileHwaddr
				vm.expandedConfig[configKey] = volatileHwaddr
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		newDevice["hwaddr"] = volatileHwaddr
	}

	return newDevice, nil
}

// Internal MAAS handling.
func (vm *Qemu) maasInterfaces(devices map[string]map[string]string) ([]maas.ContainerInterface, error) {
	interfaces := []maas.ContainerInterface{}
	for k, m := range devices {
		if m["type"] != "nic" {
			continue
		}

		if m["maas.subnet.ipv4"] == "" && m["maas.subnet.ipv6"] == "" {
			continue
		}

		m, err := vm.fillNetworkDevice(k, m)
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

func (vm *Qemu) maasDelete() error {
	maasURL, err := cluster.ConfigGetString(vm.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

	if maasURL == "" {
		return nil
	}

	interfaces, err := vm.maasInterfaces(vm.expandedDevices.CloneNative())
	if err != nil {
		return err
	}

	if len(interfaces) == 0 {
		return nil
	}

	if vm.state.MAAS == nil {
		return fmt.Errorf("Can't perform the operation because MAAS is currently unavailable")
	}

	exists, err := vm.state.MAAS.DefinedContainer(project.Prefix(vm.project, vm.name))
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	return vm.state.MAAS.DeleteContainer(project.Prefix(vm.project, vm.name))
}

func (vm *Qemu) maasUpdate(oldDevices map[string]map[string]string) error {
	// Check if MAAS is configured
	maasURL, err := cluster.ConfigGetString(vm.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

	if maasURL == "" {
		return nil
	}

	// Check if there's something that uses MAAS
	interfaces, err := vm.maasInterfaces(vm.expandedDevices.CloneNative())
	if err != nil {
		return err
	}

	var oldInterfaces []maas.ContainerInterface
	if oldDevices != nil {
		oldInterfaces, err = vm.maasInterfaces(oldDevices)
		if err != nil {
			return err
		}
	}

	if len(interfaces) == 0 && len(oldInterfaces) == 0 {
		return nil
	}

	// See if we're connected to MAAS
	if vm.state.MAAS == nil {
		return fmt.Errorf("Can't perform the operation because MAAS is currently unavailable")
	}

	exists, err := vm.state.MAAS.DefinedContainer(project.Prefix(vm.project, vm.name))
	if err != nil {
		return err
	}

	if exists {
		if len(interfaces) == 0 && len(oldInterfaces) > 0 {
			return vm.state.MAAS.DeleteContainer(project.Prefix(vm.project, vm.name))
		}

		return vm.state.MAAS.UpdateContainer(project.Prefix(vm.project, vm.name), interfaces)
	}

	return vm.state.MAAS.CreateContainer(project.Prefix(vm.project, vm.name), interfaces)
}
