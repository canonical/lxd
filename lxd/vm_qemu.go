package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/digitalocean/go-qemu/qmp"
	"github.com/linuxkit/virtsock/pkg/vsock"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

var vmVsockTimeout time.Duration = time.Second

func vmQemuLoad(s *state.State, args db.ContainerArgs, profiles []api.Profile) (Instance, error) {
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

	return vm, nil
}

// vmQemuInstantiate creates a vmQemu struct without initializing it.
func vmQemuInstantiate(s *state.State, args db.ContainerArgs) *vmQemu {
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

func vmQemuCreate(s *state.State, args db.ContainerArgs) (Instance, error) {
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

	// Load the config.
	err := vm.init()
	if err != nil {
		vm.Delete()
		logger.Error("Failed creating instance", ctxMap)
		return nil, err
	}

	// Validate expanded config
	err = containerValidConfig(s.OS, vm.expandedConfig, false, true)
	if err != nil {
		vm.Delete()
		logger.Error("Failed creating instance", ctxMap)
		return nil, err
	}

	err = containerValidDevices(s, s.Cluster, vm.Name(), vm.expandedDevices, true)
	if err != nil {
		vm.Delete()
		logger.Error("Failed creating instance", ctxMap)
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Retrieve the instance's storage pool
	_, rootDiskDevice, err := shared.GetRootDiskDevice(vm.expandedDevices.CloneNative())
	if err != nil {
		vm.Delete()
		return nil, err
	}

	if rootDiskDevice["pool"] == "" {
		vm.Delete()
		return nil, fmt.Errorf("The instances's root device is missing the pool property")
	}

	storagePool := rootDiskDevice["pool"]

	// Get the storage pool ID for the instance.
	poolID, pool, err := s.Cluster.StoragePoolGet(storagePool)
	if err != nil {
		vm.Delete()
		return nil, err
	}

	// Fill in any default volume config.
	volumeConfig := map[string]string{}
	err = storageVolumeFillDefault(storagePool, volumeConfig, pool)
	if err != nil {
		vm.Delete()
		return nil, err
	}

	// Create a new database entry for the instance's storage volume.
	_, err = s.Cluster.StoragePoolVolumeCreate(args.Project, args.Name, "", storagePoolVolumeTypeContainer, false, poolID, volumeConfig)
	if err != nil {
		vm.Delete()
		return nil, err
	}

	// Initialize the instance storage.
	cStorage, err := storagePoolVolumeContainerCreateInit(s, args.Project, storagePool, args.Name)
	if err != nil {
		vm.Delete()
		s.Cluster.StoragePoolVolumeDelete(args.Project, args.Name, storagePoolVolumeTypeContainer, poolID)
		logger.Error("Failed to initialize instance storage", ctxMap)
		return nil, err
	}
	vm.storage = cStorage

	if !vm.IsSnapshot() {
		// Update MAAS.
		err = vm.maasUpdate(nil)
		if err != nil {
			vm.Delete()
			logger.Error("Failed creating container", ctxMap)
			return nil, err
		}

		// Add devices to instance.
		for k, m := range vm.expandedDevices {
			err = vm.deviceAdd(k, m)
			if err != nil && err != device.ErrUnsupportedDevType {
				vm.Delete()
				return nil, errors.Wrapf(err, "Failed to add device '%s'", k)
			}
		}
	}

	logger.Info("Created instance", ctxMap)
	eventSendLifecycle(vm.project, "container-created",
		fmt.Sprintf("/1.0/containers/%s", vm.name), nil)

	return vm, nil
}

// The QEMU virtual machine driver.
type vmQemu struct {
	// Properties.
	architecture int
	dbType       instance.Type
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

	// Storage.
	storage storage

	// Clustering.
	node string

	// Progress tracking.
	op *operation

	expiryDate time.Time
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

	os.Remove(vm.pidFilePath())
	os.Remove(vm.getMonitorPath())

	return nil
}

func (vm *vmQemu) Start(stateful bool) error {
	// Create any missing directories.
	err := os.MkdirAll(vm.Path(), 0100)
	if err != nil {
		return err
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

	_, err = vm.StorageStart()
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
		if dev.Config["nictype"] != "bridged" {
			continue
		}

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

	path, err := exec.LookPath("vm-agent")
	if err != nil {
		return "", err
	}

	// Install agent into config drive dir.
	_, err = shared.RunCommand("cp", path, configDrivePath+"/")
	if err != nil {
		return "", err
	}

	vendorData := `#cloud-config
runcmd:
 - "mkdir /media/lxd_config"
 - "mount -o ro -t iso9660 /dev/disk/by-label/cidata /media/lxd_config"
 - "cp /media/lxd_config/media-lxd_config.mount /etc/systemd/system/"
 - "cp /media/lxd_config/lxd-agent.service /etc/systemd/system/"
 - "systemctl enable media-lxd_config.mount"
 - "systemctl enable lxd-agent.service"
 - "systemctl start lxd-agent"
`

	err = ioutil.WriteFile(configDrivePath+"/vendor-data", []byte(vendorData), 0600)
	if err != nil {
		return "", err
	}

	if vm.expandedConfig["user.user-data"] != "" {
		err = ioutil.WriteFile(configDrivePath+"/user-data", []byte(vm.expandedConfig["user.user-data"]), 0600)
		if err != nil {
			return "", err
		}
	}

	metaData := fmt.Sprintf(`instance-id: %s
local-hostname: %s
`, vm.Name(), vm.Name())

	err = ioutil.WriteFile(configDrivePath+"/meta-data", []byte(metaData), 0600)
	if err != nil {
		return "", err
	}

	lxdAgentServiceUnit := `[Unit]
Description=LXD - agent
After=media-lxd_config.mount

[Service]
Type=simple
ExecStart=/media/lxd_config/vm-agent

[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(configDrivePath+"/lxd-agent.service", []byte(lxdAgentServiceUnit), 0600)
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

	err = ioutil.WriteFile(configDrivePath+"/media-lxd_config.mount", []byte(lxdConfigDriveMountUnit), 0600)
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

// generateQemuConfigFile writes the qemu config file.
func (vm *vmQemu) generateQemuConfigFile(configISOPath string, tapDev map[string]string) (string, error) {
	_, _, onDiskPoolName := vm.storage.GetContainerPoolInfo()
	volumeName := project.Prefix(vm.Project(), vm.Name())
	// TODO add function to the storage API to get block device path.
	rootDrive := fmt.Sprintf("/dev/zvol/%s/containers/%s", onDiskPoolName, volumeName)
	monitorPath := vm.getMonitorPath()
	nvramPath := vm.getNvramPath()
	vsockID := vm.vsockID()

	conf := fmt.Sprintf(`
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

# CPU
[smp-opts]
cpus = "4"
sockets = "1"
cores = "2"
threads = "2"

# Memory
[memory]
size = "2G"

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

# Console
[chardev "console"]
backend = "pty"

# Qemu control
[chardev "monitor"]
backend = "socket"
path = "%s"
server = "on"
wait = "off"

[mon]
chardev = "monitor"
mode = "control"

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
bootindex = "2"
`, nvramPath, monitorPath, vsockID, rootDrive, configISOPath, tapDev["tap"], tapDev["hwaddr"])
	configPath := filepath.Join(vm.LogPath(), "qemu.conf")
	return configPath, ioutil.WriteFile(configPath, []byte(conf), 0640)
}

func (vm *vmQemu) pidFilePath() string {
	return vm.DevicesPath() + "/qemu.pid"
}

func (vm *vmQemu) Stop(stateful bool) error {
	if stateful {
		return fmt.Errorf("Stateful stop isn't supported for VMs at this time")
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

	// Wait for qemu to stop.
	for {
		pid, err := ioutil.ReadFile(vm.pidFilePath())
		if os.IsNotExist(err) {
			return nil
		}

		if err != nil {
			return err
		}

		// Check if qemu process still running, if so wait.
		procPath := "/proc" + strings.TrimSpace(string(pid))
		if shared.PathExists(procPath) {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		break
	}

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

func (vm *vmQemu) Backups() ([]backup, error) {
	return []backup{}, nil
}

func (vm *vmQemu) Rename(newName string) error {
	return fmt.Errorf("Rename Not implemented")
}

func (vm *vmQemu) Update(args db.ContainerArgs, userRequested bool) error {
	return fmt.Errorf("Update Not implemented")
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

// Initialize storage interface for this instance.
func (vm *vmQemu) initStorage() error {
	if vm.storage != nil {
		return nil
	}

	s, err := storagePoolVolumeContainerLoadInit(vm.state, vm.Project(), vm.Name())
	if err != nil {
		return err
	}

	vm.storage = s

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

	if shared.IsTrue(vm.expandedConfig["security.protection.delete"]) && !vm.IsSnapshot() {
		err := fmt.Errorf("Instance is protected")
		logger.Warn("Failed to delete instance", log.Ctx{"name": vm.Name(), "err": err})
		return err
	}

	// Check if we're dealing with "lxd import".
	isImport := false
	if vm.storage != nil {
		_, poolName, _ := vm.storage.GetContainerPoolInfo()

		if vm.IsSnapshot() {
			vmName, _, _ := shared.ContainerGetParentAndSnapshotName(vm.name)
			if shared.PathExists(shared.VarPath("storage-pools", poolName, "containers", vmName, ".importing")) {
				isImport = true
			}
		} else {
			if shared.PathExists(shared.VarPath("storage-pools", poolName, "containers", vm.name, ".importing")) {
				isImport = true
			}
		}
	}

	// Attempt to initialize storage interface for the instance.
	err := vm.initStorage()
	if err != nil {
		logger.Warnf("Failed to init storage: %v", err)
	}

	if vm.IsSnapshot() {
		// Remove the snapshot.
		if vm.storage != nil && !isImport {
			err := vm.storage.ContainerSnapshotDelete(vm)
			if err != nil {
				logger.Warn("Failed to delete snapshot", log.Ctx{"name": vm.Name(), "err": err})
				return err
			}
		}
	} else {
		// Remove all snapshots.
		err := containerDeleteSnapshots(vm.state, vm.Project(), vm.Name())
		if err != nil {
			logger.Warn("Failed to delete snapshots", log.Ctx{"name": vm.Name(), "err": err})
			return err
		}

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

		// Clean things up.
		vm.cleanup()

		// Delete the container from disk.
		if vm.storage != nil && !isImport {
			err := vm.storage.ContainerDelete(vm)
			if err != nil {
				logger.Error("Failed deleting container storage", log.Ctx{"name": vm.Name(), "err": err})
				return err
			}
		}

		// Delete the MAAS entry.
		err = vm.maasDelete()
		if err != nil {
			logger.Error("Failed deleting container MAAS record", log.Ctx{"name": vm.Name(), "err": err})
			return err
		}

		// Remove devices from container.
		for k, m := range vm.expandedDevices {
			err = vm.deviceRemove(k, m)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to remove device '%s'", k)
			}
		}
	}

	// Remove the database record
	if err := vm.state.Cluster.ContainerRemove(vm.project, vm.Name()); err != nil {
		logger.Error("Failed deleting instance entry", log.Ctx{"name": vm.Name(), "err": err})
		return err
	}

	// Remove the database entry for the pool device
	if vm.storage != nil {
		// Get the name of the storage pool the container is attached to. This
		// reverse-engineering works because container names are globally
		// unique.
		poolID, _, _ := vm.storage.GetContainerPoolInfo()

		// Remove volume from storage pool.
		err := vm.state.Cluster.StoragePoolVolumeDelete(vm.Project(), vm.Name(), storagePoolVolumeTypeContainer, poolID)
		if err != nil {
			return err
		}
	}

	logger.Info("Deleted instance", ctxMap)

	if vm.IsSnapshot() {
		eventSendLifecycle(vm.project, "container-snapshot-deleted",
			fmt.Sprintf("/1.0/containers/%s", vm.name), map[string]interface{}{
				"snapshot_name": vm.name,
			})
	} else {
		eventSendLifecycle(vm.project, "container-deleted",
			fmt.Sprintf("/1.0/containers/%s", vm.name), nil)
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

func (vm *vmQemu) FilePush(type_ string, srcpath string, dstpath string, uid int64, gid int64, mode int, write string) error {
	return fmt.Errorf("FilePush Not implemented")
}

func (vm *vmQemu) FileRemove(path string) error {
	return fmt.Errorf("FileRemove Not implemented")
}

func (vm *vmQemu) Console(terminal *os.File) *exec.Cmd {
	// Connect to the monitor.
	monitor, err := qmp.NewSocketMonitor("unix", vm.getMonitorPath(), vmVsockTimeout)
	if err != nil {
		return nil // The VM isn't running as no monitor socket available.
	}

	err = monitor.Connect()
	if err != nil {
		return nil // The capabilities handshake failed.
	}
	defer monitor.Disconnect()

	// Send the status command.
	respRaw, err := monitor.Run([]byte("{'execute': 'query-chardev'}"))
	if err != nil {
		return nil // Status command failed.
	}

	var respDecoded struct {
		Return []struct {
			Label    string `json:"label"`
			Filename string `json:"filename"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return nil // JSON decode failed.
	}

	var ptsPath string

	for _, v := range respDecoded.Return {
		if v.Label == "console" {
			ptsPath = strings.TrimPrefix(v.Filename, "pty:")
		}
	}

	if ptsPath == "" {
		return nil
	}

	args := []string{
		"screen",
		ptsPath,
	}

	cmd := exec.Cmd{}
	cmd.Path = "/usr/bin/screen" // TODO dont rely on screen.
	cmd.Args = args
	cmd.Stdin = terminal
	cmd.Stdout = terminal
	cmd.Stderr = terminal
	return &cmd
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

	status, err := vm.agentGetState()
	if err == nil {
		status.Status = statusCode.String()
		status.StatusCode = statusCode

		return status, nil

	}

	// At least return the Status and StatusCode if we couldn't get any
	// information for the VM agent.
	return &api.InstanceState{
		Status:     statusCode.String(),
		StatusCode: statusCode,
	}, nil
}

// agentGetState connects to the agent inside of the VM and does
// an API call to get the current state.
func (vm *vmQemu) agentGetState() (*api.InstanceState, error) {
	var status api.InstanceState

	client := http.Client{
		Transport: &http.Transport{
			Dial: func(network, addr string) (net.Conn, error) {
				return vsock.Dial(uint32(vm.vsockID()), 8443)
			},
		},
	}

	resp, err := client.Get("http://vm.socket/state")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&status)
	if err != nil {
		return nil, err
	}

	return &status, nil
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

func (vm *vmQemu) Id() int {
	return vm.id
}

// vsockID returns the vsock context ID, 3 being the first ID that
// can be used.
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

func (vm *vmQemu) Type() instance.Type {
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
	return -1
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
	name := project.Prefix(vm.Project(), vm.Name())
	return driver.ContainerPath(name, vm.IsSnapshot())
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
	poolName, err := vm.state.Cluster.ContainerPool(vm.Project(), vm.Name())
	if err != nil {
		return "", err
	}

	return poolName, nil
}

func (vm *vmQemu) SetOperation(op *operation) {
	vm.op = op
}

func (vm *vmQemu) StorageStart() (bool, error) {
	// Initialize storage interface for the container.
	err := vm.initStorage()
	if err != nil {
		return false, err
	}

	return false, nil
}

func (vm *vmQemu) StorageStop() (bool, error) {
	return false, nil
}

func (vm *vmQemu) Storage() storage {
	if vm.storage == nil {
		vm.initStorage()
	}

	return vm.storage
}

func (vm *vmQemu) TemplateApply(trigger string) error {
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
