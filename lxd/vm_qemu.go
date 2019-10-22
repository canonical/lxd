package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/digitalocean/go-qemu/qmp"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	lxdClient "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/lxd/vsock"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/units"
)

var vmVsockTimeout time.Duration = time.Second

func vmQemuLoad(s *state.State, args db.InstanceArgs, profiles []api.Profile) (Instance, error) {
	// Create the container struct.
	vm := vmQemuInstantiate(s, args)

	// Expand config and devices.
	err := vm.expandConfig(profiles)
	if err != nil {
		return nil, err
	}

	err = vm.expandDevices(profiles)
	if err != nil {
		return nil, err
	}

	err = vm.initAgentClient()
	if err != nil {
		return nil, err
	}

	return vm, nil
}

// vmQemuInstantiate creates a vmQemu struct without initializing it.
func vmQemuInstantiate(s *state.State, args db.InstanceArgs) *vmQemu {
	vm := &vmQemu{
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

	return vm
}

// vmQemuCreate creates a new storage volume record and returns an initialised Instance.
func vmQemuCreate(s *state.State, args db.InstanceArgs) (Instance, error) {
	// Create the instance struct.
	vm := &vmQemu{
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

	// Validate expanded config
	err = containerValidConfig(s.OS, vm.expandedConfig, false, true)
	if err != nil {
		logger.Error("Failed creating instance", ctxMap)
		return nil, err
	}

	err = containerValidDevices(s, s.Cluster, vm.Name(), vm.expandedDevices, true)
	if err != nil {
		logger.Error("Failed creating instance", ctxMap)
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Retrieve the instance's storage pool
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

// The QEMU virtual machine driver.
type vmQemu struct {
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

	agentClient *http.Client
}

func (vm *vmQemu) initAgentClient() error {
	agentCertFile, _, err := vm.generateAgentCert()
	if err != nil {
		return err
	}

	agentCert, err := ioutil.ReadFile(agentCertFile)
	if err != nil {
		return err
	}

	// The connection uses mutual authentication, so use the LXD server's key & cert for client.
	clientCertFile := shared.VarPath("server.crt")
	clientKeyFile := shared.VarPath("server.key")

	clientCert, err := ioutil.ReadFile(clientCertFile)
	if err != nil {
		return err
	}

	clientKey, err := ioutil.ReadFile(clientKeyFile)
	if err != nil {
		return err
	}

	vm.agentClient, err = vsock.HTTPClient(vm.vsockID(), string(clientCert), string(clientKey), string(agentCert))
	if err != nil {
		return err
	}

	return nil
}

// generateAgentCert creates the necessary server key and certificate if needed.
func (vm *vmQemu) generateAgentCert() (agentCertFile string, agentKeyFile string, err error) {
	agentCertFile = filepath.Join(vm.Path(), "agent.crt")
	agentKeyFile = filepath.Join(vm.Path(), "agent.key")

	// Create server certificate.
	err = shared.FindOrGenCert(agentCertFile, agentKeyFile, true)
	if err != nil {
		return
	}

	return
}

func (vm *vmQemu) Freeze() error {
	return nil
}

func (vm *vmQemu) Shutdown(timeout time.Duration) error {
	if !vm.IsRunning() {
		return fmt.Errorf("The instance is already stopped")
	}

	// Connect to the monitor.
	monitor, err := qmp.NewSocketMonitor("unix", vm.getMonitorPath(), vmVsockTimeout)
	if err != nil {
		return err
	}

	err = monitor.Connect()
	if err != nil {
		return err
	}
	defer monitor.Disconnect()

	// Send the system_powerdown command.
	_, err = monitor.Run([]byte("{'execute': 'system_powerdown'}"))
	if err != nil {
		return err
	}
	monitor.Disconnect()

	// Deal with the timeout.
	chShutdown := make(chan struct{}, 1)
	go func() {
		for {
			// Connect to socket, check if still running, then disconnect so we don't
			// block the qemu monitor socket for other users (such as lxc list).
			if !vm.IsRunning() {
				close(chShutdown)
				return
			}

			time.Sleep(500 * time.Millisecond) // Don't consume too many resources.
		}
	}()

	// If timeout provided, block until the VM is not running or the timeout has elapsed.
	if timeout > 0 {
		select {
		case <-chShutdown:
			return nil
		case <-time.After(timeout):
			return fmt.Errorf("Instance was not shutdown after timeout")
		}
	} else {
		<-chShutdown // Block until VM is not running if no timeout provided.
	}

	vm.cleanupDevices()
	os.Remove(vm.pidFilePath())
	os.Remove(vm.getMonitorPath())

	return nil
}

func (vm *vmQemu) Start(stateful bool) error {
	// Ensure the correct vhost_vsock kernel module is loaded before establishing the vsock.
	err := util.LoadModule("vhost_vsock")
	if err != nil {
		return err
	}

	if vm.IsRunning() {
		return fmt.Errorf("The instance is already running")
	}

	pidFile := vm.DevicesPath() + "/qemu.pid"
	configISOPath, err := vm.generateConfigDrive()
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

	// Generate an empty nvram file.
	nvramFile, err := os.OpenFile(vm.getNvramPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	err = nvramFile.Truncate(131072)
	if err != nil {
		return err
	}
	nvramFile.Close()

	tapDev := map[string]string{}

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

		if len(runConf.NetworkInterface) > 0 {
			for _, nicItem := range runConf.NetworkInterface {
				if nicItem.Key == "link" {
					tapDev["tap"] = nicItem.Value
					tapDev["hwaddr"] = vm.localConfig[fmt.Sprintf("volatile.%s.hwaddr", dev.Name)]
				}
			}

		}
	}

	confFile, err := vm.generateQemuConfigFile(configISOPath, tapDev)
	if err != nil {
		return err
	}

	// Check qemu is installed.
	_, err = exec.LookPath("qemu-system-x86_64")
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("qemu-system-x86_64", "-name", vm.Name(), "-uuid", vmUUID, "-daemonize", "-cpu", "host", "-nographic", "-serial", "chardev:console", "-nodefaults", "-readconfig", confFile, "-pidfile", pidFile)
	if err != nil {
		return err
	}

	return nil
}

// deviceVolatileGetFunc returns a function that retrieves a named device's volatile config and
// removes its device prefix from the keys.
func (vm *vmQemu) deviceVolatileGetFunc(devName string) func() map[string]string {
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
func (vm *vmQemu) deviceVolatileSetFunc(devName string) func(save map[string]string) error {
	return func(save map[string]string) error {
		volatileSave := make(map[string]string)
		for k, v := range save {
			volatileSave[fmt.Sprintf("volatile.%s.%s", devName, k)] = v
		}

		return vm.VolatileSet(volatileSave)
	}
}

// deviceLoad instantiates and validates a new device and returns it along with enriched config.
func (vm *vmQemu) deviceLoad(deviceName string, rawConfig deviceConfig.Device) (device.Device, deviceConfig.Device, error) {
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
func (vm *vmQemu) deviceStart(deviceName string, rawConfig deviceConfig.Device, isRunning bool) (*device.RunConfig, error) {
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
func (vm *vmQemu) deviceStop(deviceName string, rawConfig deviceConfig.Device) error {
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
func (vm *vmQemu) runHooks(hooks []func() error) error {
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

func (vm *vmQemu) getMonitorPath() string {
	return vm.DevicesPath() + "/qemu.monitor"
}

func (vm *vmQemu) getNvramPath() string {
	return vm.DevicesPath() + "/qemu.nvram"
}

func (vm *vmQemu) generateConfigDrive() (string, error) {
	configDrivePath := vm.Path() + "/config"

	// Create config drive dir.
	err := os.MkdirAll(configDrivePath, 0100)
	if err != nil {
		return "", err
	}

	// Add config drive mount instructions to cloud init.
	vendorData := `#cloud-config
runcmd:
 - "mkdir /media/lxd_config"
 - "mount -o ro -t iso9660 /dev/disk/by-label/cidata /media/lxd_config"
 - "cp /media/lxd_config/media-lxd_config.mount /etc/systemd/system/"
 - "systemctl enable media-lxd_config.mount"`

	path, err := exec.LookPath("lxd-agent")
	if err != nil {
		logger.Warnf("lxd-agent not found, skipping its inclusion in the VM config drive: %v", err)
	} else {
		// Install agent into config drive dir if found.
		_, err = shared.RunCommand("cp", path, configDrivePath+"/lxd-agent")
		if err != nil {
			return "", err
		}

		os.Chmod(configDrivePath+"/lxd-agent", 0700)

		// Add agent install and startup config to cloud init.
		vendorData += `
 - "cp /media/lxd_config/lxd-agent.service /etc/systemd/system/"
 - "systemctl enable lxd-agent.service"
 - "systemctl start lxd-agent"`
	}

	err = ioutil.WriteFile(configDrivePath+"/vendor-data", []byte(vendorData), 0400)
	if err != nil {
		return "", err
	}

	userData := vm.expandedConfig["user.user-data"]

	// Use an empty user-data file if no custom user-data supplied.
	if userData == "" {
		userData = "#cloud-config"
	}

	err = ioutil.WriteFile(configDrivePath+"/user-data", []byte(userData), 0400)
	if err != nil {
		return "", err
	}

	metaData := fmt.Sprintf(`instance-id: %s
local-hostname: %s
`, vm.Name(), vm.Name())

	err = ioutil.WriteFile(configDrivePath+"/meta-data", []byte(metaData), 0400)
	if err != nil {
		return "", err
	}

	lxdAgentServiceUnit := `[Unit]
Description=LXD - agent
After=media-lxd_config.mount
[Service]
Type=simple
ExecStart=/media/lxd_config/lxd-agent
[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(configDrivePath+"/lxd-agent.service", []byte(lxdAgentServiceUnit), 0400)
	if err != nil {
		return "", err
	}

	lxdConfigDriveMountUnit := `[Unit]
Description = LXD - config drive
Before=local-fs.target
[Mount]
Where=/media/lxd_config
What=/dev/disk/by-label/cidata
Type=iso9660
[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(configDrivePath+"/media-lxd_config.mount", []byte(lxdConfigDriveMountUnit), 0400)
	if err != nil {
		return "", err
	}

	// Copy the LXD's server.crt file for use with the lxd-agent.
	serverCrt, err := ioutil.ReadFile(shared.VarPath("server.crt"))
	if err != nil {
		return "", err
	}
	err = ioutil.WriteFile(configDrivePath+"/server.crt", serverCrt, 0400)
	if err != nil {
		return "", err
	}

	// Check if we have both agent.crt and agent.key and if not generate a new set.
	agentCertFile, agentKeyFile, err := vm.generateAgentCert()
	if err != nil {
		return "", err
	}

	// Copy the VM's LXD agent.crt file for use with the lxd-agent.
	agentCrt, err := ioutil.ReadFile(agentCertFile)
	if err != nil {
		return "", err
	}
	err = ioutil.WriteFile(configDrivePath+"/agent.crt", agentCrt, 0400)
	if err != nil {
		return "", err
	}

	// Copy the VM's LXD agent.crt file for use with the lxd-agent.
	agentKey, err := ioutil.ReadFile(agentKeyFile)
	if err != nil {
		return "", err
	}
	err = ioutil.WriteFile(configDrivePath+"/agent.key", agentKey, 0400)
	if err != nil {
		return "", err
	}

	// Finally convert the config drive dir into an ISO file. The cidata label is important
	// as this is what cloud-init uses to detect, mount the drive and run the cloud-init
	// templates on first boot. The vendor-data template then modifies the system so that the
	// config drive is mounted and the agent is started on subsequent boots.
	isoPath := vm.Path() + "/config.iso"
	_, err = shared.RunCommand("mkisofs", "-R", "-V", "cidata", "-o", isoPath, configDrivePath)
	if err != nil {
		return "", err
	}

	return isoPath, nil
}

// generateQemuConfigFile writes the qemu config file and returns its location.
func (vm *vmQemu) generateQemuConfigFile(configISOPath string, tapDev map[string]string) (string, error) {
	var sb *strings.Builder = &strings.Builder{}

	// Base config. This is common for all VMs and has no variables in it.
	sb.WriteString(`
# Machine
[machine]
graphics = "off"
type = "q35"
accel = "kvm"
usb = "off"
graphics = "off"
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"
[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "1"
[boot-opts]
strict = "on"
# SCSI root
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
`)

	// Now add the dynamic parts of the config.
	err := vm.addMemoryConfig(sb)
	if err != nil {
		return "", err
	}

	err = vm.addRootDriveConfig(sb)
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
	vm.addConfDriveConfig(sb, configISOPath)
	vm.addNetConfig(sb, tapDev)

	// Write the config file to disk.
	configPath := filepath.Join(vm.LogPath(), "qemu.conf")
	return configPath, ioutil.WriteFile(configPath, []byte(sb.String()), 0640)
}

func (vm *vmQemu) addMemoryConfig(sb *strings.Builder) error {
	// Configure memory limit.
	memSize := vm.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = "1GB" // Default to 1GB if no memory limit specified.
	}

	memSizeBytes, err := units.ParseByteSizeString(memSize)
	if err != nil {
		return fmt.Errorf("limits.memory invalid: %v", err)
	}

	memKB := memSizeBytes / 1000

	sb.WriteString(fmt.Sprintf(`
# Memory
[memory]
size = "%dK"
`, memKB))

	return nil
}

func (vm *vmQemu) addVsockConfig(sb *strings.Builder) {
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

func (vm *vmQemu) addCPUConfig(sb *strings.Builder) error {
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

func (vm *vmQemu) addMonitorConfig(sb *strings.Builder) {
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

func (vm *vmQemu) addFirmwareConfig(sb *strings.Builder) {
	nvramPath := vm.getNvramPath()

	sb.WriteString(fmt.Sprintf(`
# Firmware
[drive]
file = "/usr/share/OVMF/OVMF_CODE.fd"
if = "pflash"
format = "raw"
unit = "0"
readonly = "on"
[drive]
file = "%s"
if = "pflash"
format = "raw"
unit = "1"
`, nvramPath))

	return
}

func (vm *vmQemu) addRootDriveConfig(sb *strings.Builder) error {
	pool, err := storagePools.GetPoolByInstance(vm.state, vm)
	if err != nil {
		return err
	}

	rootDrivePath, rootDriveType, err := pool.GetInstanceDisk(vm)
	if err != nil {
		return err
	}

	sb.WriteString(fmt.Sprintf(`
# Root drive ("root" device)
[drive "lxd_root"]
file = "%s"
format = "%s"
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
`, rootDrivePath, rootDriveType))

	return nil
}

func (vm *vmQemu) addConfDriveConfig(sb *strings.Builder, configISOPath string) {
	sb.WriteString(fmt.Sprintf(`
# Config drive (set to last lun)
[drive "qemu_config"]
file = "%s"
format = "raw"
if = "none"
cache = "none"
aio = "native"
readonly = "on"
[device "dev-qemu_config"]
driver = "scsi-hd"
bus = "qemu_scsi.0"
channel = "0"
scsi-id = "1"
lun = "1"
drive = "qemu_config"
`, configISOPath))

	return
}

func (vm *vmQemu) addNetConfig(sb *strings.Builder, tapDev map[string]string) {
	sb.WriteString(fmt.Sprintf(`
# Network card ("eth0" device)
[netdev "lxd_eth0"]
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
`, tapDev["tap"], tapDev["hwaddr"]))

	return
}

func (vm *vmQemu) pidFilePath() string {
	return vm.DevicesPath() + "/qemu.pid"
}

func (vm *vmQemu) pid() (int, error) {
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

func (vm *vmQemu) Stop(stateful bool) error {
	if stateful {
		return fmt.Errorf("Stateful stop isn't supported for VMs at this time")
	}

	if !vm.IsRunning() {
		return fmt.Errorf("Instance is not running")
	}

	// Connect to the monitor.
	monitor, err := qmp.NewSocketMonitor("unix", vm.getMonitorPath(), vmVsockTimeout)
	if err != nil {
		return err
	}

	err = monitor.Connect()
	if err != nil {
		return err
	}
	defer monitor.Disconnect()

	// Send the quit command.
	_, err = monitor.Run([]byte("{'execute': 'quit'}"))
	if err != nil {
		return err
	}
	monitor.Disconnect()

	pid, err := vm.pid()
	if err != nil {
		return err
	}

	// No PID found, qemu not running.
	if pid < 0 {
		return nil
	}

	// Check if qemu process still running, if so wait.
	for {
		procPath := fmt.Sprintf("/proc/%d", pid)
		if shared.PathExists(procPath) {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		break
	}

	vm.cleanupDevices()
	os.Remove(vm.pidFilePath())
	os.Remove(vm.getMonitorPath())

	return nil
}

func (vm *vmQemu) Unfreeze() error {
	return fmt.Errorf("Unfreeze Not implemented")
}

func (vm *vmQemu) IsPrivileged() bool {
	return shared.IsTrue(vm.expandedConfig["security.privileged"])
}

func (vm *vmQemu) Restore(source Instance, stateful bool) error {
	return fmt.Errorf("Restore Not implemented")
}

func (vm *vmQemu) Snapshots() ([]Instance, error) {
	return []Instance{}, nil
}

func (vm *vmQemu) Backups() ([]backup.Backup, error) {
	return []backup.Backup{}, nil
}

func (vm *vmQemu) Rename(newName string) error {
	return fmt.Errorf("Rename Not implemented")
}

func (vm *vmQemu) Update(args db.InstanceArgs, userRequested bool) error {
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
	err := containerValidConfig(vm.state.OS, args.Config, false, false)
	if err != nil {
		return errors.Wrap(err, "Invalid config")
	}

	// Validate the new devices without using expanded devices validation (expensive checks disabled).
	err = containerValidDevices(vm.state, vm.state.Cluster, vm.Name(), args.Devices, false)
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
	err = containerValidConfig(vm.state.OS, vm.expandedConfig, false, true)
	if err != nil {
		return errors.Wrap(err, "Invalid expanded config")
	}

	// Do full expanded validation of the devices diff.
	err = containerValidDevices(vm.state, vm.state.Cluster, vm.Name(), vm.expandedDevices, true)
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

	// Finally, apply the changes to the database
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
				return errors.Wrap(err, "Container update")
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

	err = writeBackupFile(vm)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "Failed to write backup file")
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	var endpoint string

	if vm.IsSnapshot() {
		parentName, snapName, _ := shared.ContainerGetParentAndSnapshotName(vm.name)
		endpoint = fmt.Sprintf("/1.0/virtual-machines/%s/snapshots/%s", parentName, snapName)
	} else {
		endpoint = fmt.Sprintf("/1.0/virtual-machines/%s", vm.name)
	}

	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-updated", endpoint, nil)
	return nil
}

func (vm *vmQemu) updateDevices(removeDevices deviceConfig.Devices, addDevices deviceConfig.Devices, updateDevices deviceConfig.Devices, oldExpandedDevices deviceConfig.Devices) error {
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
func (vm *vmQemu) deviceUpdate(deviceName string, rawConfig deviceConfig.Device, oldDevices deviceConfig.Devices, isRunning bool) error {
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
func (vm *vmQemu) deviceResetVolatile(devName string, oldConfig, newConfig deviceConfig.Device) error {
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

func (vm *vmQemu) removeUnixDevices() error {
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

func (vm *vmQemu) removeDiskDevices() error {
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

func (vm *vmQemu) cleanup() {
	// Unmount any leftovers
	vm.removeUnixDevices()
	vm.removeDiskDevices()

	// Remove the devices path
	os.Remove(vm.DevicesPath())

	// Remove the shmounts path
	os.RemoveAll(vm.ShmountsPath())
}

// cleanupDevices performs any needed device cleanup steps when instance is stopped.
func (vm *vmQemu) cleanupDevices() {
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

func (vm *vmQemu) init() error {
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

func (vm *vmQemu) Delete() error {
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

	// Remove all backups if not snapshot.
	if !vm.IsSnapshot() {
		backups, err := vm.Backups()
		if err != nil {
			logger.Error("Failed to load backups", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
		}

		for _, backup := range backups {
			err = backup.Delete()
			if err != nil {
				logger.Error("Failed to delete backup", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "backup": backup.Name, "err": err})
			}
		}
	}

	// Attempt to initialize storage interface for the instance.
	pool, err := storagePools.GetPoolByInstance(vm.state, vm)
	if err != nil {
		logger.Error("Failed to init storage pool", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
	}

	if pool != nil {
		if vm.IsSnapshot() {
			if !isImport {
				// Remove snapshot volume and database record.
				err = pool.DeleteInstanceSnapshot(vm, nil)
				if err != nil {
					logger.Error("Failed to delete instance snapshot volume", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
				}
			}
		} else {
			// Remove all snapshots by initialising each snapshot as an Instance and
			// calling its Delete function.
			err := instanceDeleteSnapshots(vm.state, vm.Project(), vm.Name())
			if err != nil {
				logger.Error("Failed to delete instance snapshot volumes", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
			}

			if !isImport {
				// Remove the storage volume, snapshot volumes and database records.
				err = pool.DeleteInstance(vm, nil)
				if err != nil {
					logger.Error("Failed to delete instance volume", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
				}
			}
		}
	}

	// Perform other cleanup steps if not snapshot.
	if !vm.IsSnapshot() {
		// Clean things up.
		vm.cleanup()

		// Delete the MAAS entry.
		err = vm.maasDelete()
		if err != nil {
			logger.Error("Failed deleting instance MAAS record", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
		}

		// Run device removal function for each device.
		for k, m := range vm.expandedDevices {
			err = vm.deviceRemove(k, m)
			if err != nil && err != device.ErrUnsupportedDevType {
				logger.Error("Failed to remove device", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "device": k, "err": err})
			}
		}
	}

	// Remove the database record of the instance or snapshot instance.
	if err := vm.state.Cluster.InstanceRemove(vm.Project(), vm.Name()); err != nil {
		logger.Error("Failed deleting instance entry", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
		return err // This is the only step we should return prematurely at.
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

func (vm *vmQemu) deviceAdd(deviceName string, rawConfig deviceConfig.Device) error {
	return nil
}

func (vm *vmQemu) deviceRemove(deviceName string, rawConfig deviceConfig.Device) error {
	return nil
}

func (vm *vmQemu) Export(w io.Writer, properties map[string]string) error {
	return fmt.Errorf("Export Not implemented")
}

func (vm *vmQemu) CGroupGet(key string) (string, error) {
	return "", fmt.Errorf("CGroupGet Not implemented")
}

func (vm *vmQemu) CGroupSet(key string, value string) error {
	return fmt.Errorf("CGroupSet Not implemented")
}

func (vm *vmQemu) VolatileSet(changes map[string]string) error {
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

func (vm *vmQemu) FileExists(path string) error {
	return fmt.Errorf("FileExists Not implemented")
}

func (vm *vmQemu) FilePull(srcpath string, dstpath string) (int64, int64, os.FileMode, string, []string, error) {
	return 0, 0, 0, "", nil, fmt.Errorf("FilePull Not implemented")
}

func (vm *vmQemu) FilePush(fileType string, srcPath string, dstPath string, uid int64, gid int64, mode int, write string) error {
	agent, err := lxdClient.ConnectLXDHTTP(nil, vm.agentClient)
	if err != nil {
		return err
	}

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

func (vm *vmQemu) FileRemove(path string) error {
	return fmt.Errorf("FileRemove Not implemented")
}

func (vm *vmQemu) Console() (*os.File, error) {
	// Connect to the monitor.
	monitor, err := qmp.NewSocketMonitor("unix", vm.getMonitorPath(), vmVsockTimeout)
	if err != nil {
		return nil, err // The VM isn't running as no monitor socket available.
	}

	err = monitor.Connect()
	if err != nil {
		return nil, err // The capabilities handshake failed.
	}
	defer monitor.Disconnect()

	// Send the status command.
	respRaw, err := monitor.Run([]byte("{'execute': 'query-chardev'}"))
	if err != nil {
		return nil, err // Status command failed.
	}

	var respDecoded struct {
		Return []struct {
			Label    string `json:"label"`
			Filename string `json:"filename"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return nil, err // JSON decode failed.
	}

	var ptsPath string

	for _, v := range respDecoded.Return {
		if v.Label == "console" {
			ptsPath = strings.TrimPrefix(v.Filename, "pty:")
		}
	}

	if ptsPath == "" {
		return nil, fmt.Errorf("No PTS path found")
	}

	console, err := os.OpenFile(ptsPath, os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	return console, nil
}

func (vm *vmQemu) Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File, wait bool, cwd string, uid uint32, gid uint32) (*exec.Cmd, int, int, error) {
	return nil, 0, 0, fmt.Errorf("Exec Not implemented")

}

func (vm *vmQemu) Render() (interface{}, interface{}, error) {
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

func (vm *vmQemu) RenderFull() (*api.InstanceFull, interface{}, error) {
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

func (vm *vmQemu) RenderState() (*api.InstanceState, error) {
	statusCode := vm.statusCode()
	pid, _ := vm.pid()

	status, err := vm.agentGetState()
	if err != nil {
		logger.Warn("Could not get VM state from agent", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
	} else {
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
func (vm *vmQemu) agentGetState() (*api.InstanceState, error) {
	// Ensure the correct vhost_vsock kernel module is loaded before establishing the vsock.
	err := util.LoadModule("vhost_vsock")
	if err != nil {
		return nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, vm.agentClient)
	if err != nil {
		return nil, err
	}

	status, _, err := agent.GetInstanceState("")
	if err != nil {
		return nil, err
	}

	return status, nil
}

func (vm *vmQemu) IsRunning() bool {
	state := vm.State()
	return state != "BROKEN" && state != "STOPPED"
}

func (vm *vmQemu) IsFrozen() bool {
	return vm.State() == "FROZEN"
}

func (vm *vmQemu) IsEphemeral() bool {
	return vm.ephemeral
}

func (vm *vmQemu) IsSnapshot() bool {
	return vm.snapshot
}

func (vm *vmQemu) IsStateful() bool {
	return vm.stateful
}

func (vm *vmQemu) DeviceEventHandler(runConf *device.RunConfig) error {
	return fmt.Errorf("DeviceEventHandler Not implemented")
}

func (vm *vmQemu) ID() int {
	return vm.id
}

// vsockID returns the vsock context ID, 3 being the first ID that can be used.
func (vm *vmQemu) vsockID() int {
	return vm.id + 3
}

func (vm *vmQemu) Location() string {
	return vm.node
}

func (vm *vmQemu) Project() string {
	return vm.project
}

func (vm *vmQemu) Name() string {
	return vm.name
}

func (vm *vmQemu) Type() instancetype.Type {
	return vm.dbType
}

func (vm *vmQemu) Description() string {
	return vm.description
}

func (vm *vmQemu) Architecture() int {
	return vm.architecture
}

func (vm *vmQemu) CreationDate() time.Time {
	return vm.creationDate
}
func (vm *vmQemu) LastUsedDate() time.Time {
	return vm.lastUsedDate
}

func (vm *vmQemu) expandConfig(profiles []api.Profile) error {
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

func (vm *vmQemu) expandDevices(profiles []api.Profile) error {
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

func (vm *vmQemu) ExpandedConfig() map[string]string {
	return vm.expandedConfig
}

func (vm *vmQemu) ExpandedDevices() deviceConfig.Devices {
	return vm.expandedDevices
}

func (vm *vmQemu) LocalConfig() map[string]string {
	return vm.localConfig
}

func (vm *vmQemu) LocalDevices() deviceConfig.Devices {
	return vm.localDevices
}

func (vm *vmQemu) Profiles() []string {
	return vm.profiles
}

func (vm *vmQemu) InitPID() int {
	pid, _ := vm.pid()
	return pid
}

func (vm *vmQemu) statusCode() api.StatusCode {
	// Connect to the monitor.
	monitor, err := qmp.NewSocketMonitor("unix", vm.getMonitorPath(), vmVsockTimeout)
	if err != nil {
		return api.Stopped // The VM isn't running as no monitor socket available.
	}

	err = monitor.Connect()
	if err != nil {
		return api.Error // The capabilities handshake failed.
	}
	defer monitor.Disconnect()

	// Send the status command.
	respRaw, err := monitor.Run([]byte("{'execute': 'query-status'}"))
	if err != nil {
		return api.Error // Status command failed.
	}

	var respDecoded struct {
		ID     string `json:"id"`
		Return struct {
			Running    bool   `json:"running"`
			Singlestep bool   `json:"singlestep"`
			Status     string `json:"status"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return api.Error // JSON decode failed.
	}

	if respDecoded.Return.Status == "running" {
		return api.Running
	}

	return api.Stopped
}

func (vm *vmQemu) State() string {
	return strings.ToUpper(vm.statusCode().String())
}

func (vm *vmQemu) ExpiryDate() time.Time {
	if vm.IsSnapshot() {
		return vm.expiryDate
	}

	// Return zero time if the container is not a snapshot.
	return time.Time{}
}

func (vm *vmQemu) Path() string {
	return storagePools.InstancePath(vm.Type(), vm.Project(), vm.Name(), vm.IsSnapshot())
}

func (vm *vmQemu) DevicesPath() string {
	name := project.Prefix(vm.Project(), vm.Name())
	return shared.VarPath("devices", name)
}

func (vm *vmQemu) ShmountsPath() string {
	name := project.Prefix(vm.Project(), vm.Name())
	return shared.VarPath("shmounts", name)
}

func (vm *vmQemu) LogPath() string {
	name := project.Prefix(vm.Project(), vm.Name())
	return shared.LogPath(name)
}

func (vm *vmQemu) LogFilePath() string {
	return filepath.Join(vm.LogPath(), "lxvm.log")
}

func (vm *vmQemu) ConsoleBufferLogPath() string {
	return filepath.Join(vm.LogPath(), "console.log")
}

func (vm *vmQemu) RootfsPath() string {
	return filepath.Join(vm.Path(), "rootfs")
}

func (vm *vmQemu) TemplatesPath() string {
	return filepath.Join(vm.Path(), "templates")
}

func (vm *vmQemu) StatePath() string {
	return filepath.Join(vm.Path(), "state")
}

func (vm *vmQemu) StoragePool() (string, error) {
	poolName, err := vm.state.Cluster.InstancePool(vm.Project(), vm.Name())
	if err != nil {
		return "", err
	}

	return poolName, nil
}

func (vm *vmQemu) SetOperation(op *operations.Operation) {
	vm.op = op
}

// StorageStart deprecated.
func (vm *vmQemu) StorageStart() (bool, error) {
	return false, storagePools.ErrNotImplemented
}

// StorageStop deprecated.
func (vm *vmQemu) StorageStop() (bool, error) {
	return false, storagePools.ErrNotImplemented
}

// Storage is deprecated will always return nil.
// It is here just to satisfy the Instance interface (which will be updated in the future).
func (vm *vmQemu) Storage() storage {
	return nil
}

func (vm *vmQemu) DeferTemplateApply(trigger string) error {
	return nil
}

func (vm *vmQemu) DaemonState() *state.State {
	// FIXME: This function should go away, since the abstract container
	//        interface should not be coupled with internal state details.
	//        However this is not currently possible, because many
	//        higher-level APIs use container variables as "implicit
	//        handles" to database/OS state and then need a way to get a
	//        reference to it.
	return vm.state
}

// fillNetworkDevice takes a nic or infiniband device type and enriches it with automatically
// generated name and hwaddr properties if these are missing from the device.
func (vm *vmQemu) fillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
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
			volatileHwaddr, err := deviceNextInterfaceHWAddr()
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
func (vm *vmQemu) maasInterfaces(devices map[string]map[string]string) ([]maas.ContainerInterface, error) {
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

func (vm *vmQemu) maasDelete() error {
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

func (vm *vmQemu) maasUpdate(oldDevices map[string]map[string]string) error {
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
