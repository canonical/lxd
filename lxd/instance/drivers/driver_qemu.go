package drivers

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
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/flosch/pongo2"
	"github.com/gorilla/websocket"
	"github.com/kballard/go-shellquote"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	lxdClient "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/apparmor"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cgroup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/device/nictype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/drivers/qmp"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	pongoTemplate "github.com/lxc/lxd/lxd/template"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/lxd/vsock"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/instancewriter"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/subprocess"
	"github.com/lxc/lxd/shared/termios"
	"github.com/lxc/lxd/shared/units"
)

// qemuAsyncIO is used to indicate disk should use unsafe cache I/O.
const qemuUnsafeIO = "unsafeio"

// qemuSerialChardevName is used to communicate state via qmp between Qemu and LXD.
const qemuSerialChardevName = "qemu_serial-chardev"

// qemuDefaultMemSize is the default memory size for VMs if not limit specified.
const qemuDefaultMemSize = "1GiB"

var errQemuAgentOffline = fmt.Errorf("LXD VM agent isn't currently running")

var vmConsole = map[int]bool{}
var vmConsoleLock sync.Mutex

// qemuLoad creates a Qemu instance from the supplied InstanceArgs.
func qemuLoad(s *state.State, args db.InstanceArgs, profiles []api.Profile) (instance.Instance, error) {
	// Create the instance struct.
	vm := qemuInstantiate(s, args, nil)

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

// qemuInstantiate creates a Qemu struct without expanding config. The expandedDevices argument is
// used during device config validation when the devices have already been expanded and we do not
// have access to the profiles used to do it. This can be safely passed as nil if not required.
func qemuInstantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices) *qemu {
	vm := &qemu{
		common: common{
			dbType:       args.Type,
			architecture: args.Architecture,
			localConfig:  args.Config,
			localDevices: args.Devices,
			project:      args.Project,
			state:        s,
			profiles:     args.Profiles,
		},
		id:           args.ID,
		name:         args.Name,
		description:  args.Description,
		ephemeral:    args.Ephemeral,
		snapshot:     args.Snapshot,
		creationDate: args.CreationDate,
		lastUsedDate: args.LastUsedDate,
		stateful:     args.Stateful,
		node:         args.Node,
		expiryDate:   args.ExpiryDate,
	}

	// Get the architecture name.
	archName, err := osarch.ArchitectureName(vm.architecture)
	if err == nil {
		vm.architectureName = archName
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

// qemuCreate creates a new storage volume record and returns an initialised Instance.
func qemuCreate(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	// Create the instance struct.
	vm := &qemu{
		common: common{
			dbType:       args.Type,
			architecture: args.Architecture,
			localConfig:  args.Config,
			localDevices: args.Devices,
			state:        s,
			profiles:     args.Profiles,
			project:      args.Project,
		},
		id:           args.ID,
		name:         args.Name,
		node:         args.Node,
		description:  args.Description,
		ephemeral:    args.Ephemeral,
		snapshot:     args.Snapshot,
		stateful:     args.Stateful,
		creationDate: args.CreationDate,
		lastUsedDate: args.LastUsedDate,
		expiryDate:   args.ExpiryDate,
	}

	revert := revert.New()
	defer revert.Fail()

	// Use vm.Delete() in revert on error as this function doesn't just create DB records, it can also cause
	// other modifications to the host when devices are added.
	revert.Add(func() { vm.Delete() })

	// Get the architecture name.
	archName, err := osarch.ArchitectureName(vm.architecture)
	if err == nil {
		vm.architectureName = archName
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
	err = vm.init()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to expand config")
	}

	// Validate expanded config.
	err = instance.ValidConfig(s.OS, vm.expandedConfig, false, true)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid config")
	}

	err = instance.ValidDevices(s, s.Cluster, vm.Project(), vm.Type(), vm.expandedDevices, true)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Retrieve the container's storage pool.
	var storageInstance instance.Instance
	if vm.IsSnapshot() {
		parentName, _, _ := shared.InstanceGetParentAndSnapshotName(vm.name)

		// Load the parent.
		storageInstance, err = instance.LoadByProjectAndName(vm.state, vm.project, parentName)
		if err != nil {
			return nil, errors.Wrap(err, "Invalid parent")
		}
	} else {
		storageInstance = vm
	}

	// Retrieve the instance's storage pool.
	_, rootDiskDevice, err := shared.GetRootDiskDevice(storageInstance.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	if rootDiskDevice["pool"] == "" {
		return nil, fmt.Errorf("The instances's root device is missing the pool property")
	}

	// Initialize the storage pool.
	vm.storagePool, err = storagePools.GetPoolByName(vm.state, rootDiskDevice["pool"])
	if err != nil {
		return nil, errors.Wrapf(err, "Failed loading storage pool")
	}

	// Fill default config.
	volumeConfig := map[string]string{}
	err = vm.storagePool.FillInstanceConfig(vm, volumeConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed filling default config")
	}

	// Create a new database entry for the instance's storage volume.
	if vm.IsSnapshot() {
		_, err = s.Cluster.CreateStorageVolumeSnapshot(args.Project, args.Name, "", db.StoragePoolVolumeTypeVM, vm.storagePool.ID(), volumeConfig, time.Time{})

	} else {
		_, err = s.Cluster.CreateStoragePoolVolume(args.Project, args.Name, "", db.StoragePoolVolumeTypeVM, vm.storagePool.ID(), volumeConfig)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "Failed creating storage record")
	}

	if !vm.IsSnapshot() {
		// Update MAAS.
		err = vm.maasUpdate(nil)
		if err != nil {
			return nil, err
		}

		// Add devices to instance.
		for k, m := range vm.expandedDevices {
			err = vm.deviceAdd(k, m, false)
			if err != nil && err != device.ErrUnsupportedDevType {
				return nil, errors.Wrapf(err, "Failed to add device %q", k)
			}
		}
	}

	logger.Info("Created instance", ctxMap)
	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-created", fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)

	revert.Success()
	return vm, nil
}

// qemu is the QEMU virtual machine driver.
type qemu struct {
	common

	// Properties.
	snapshot     bool
	creationDate time.Time
	lastUsedDate time.Time
	ephemeral    bool
	id           int
	name         string
	description  string
	stateful     bool

	// Clustering.
	node string

	// Progress tracking.
	op *operations.Operation

	expiryDate time.Time

	// Cached handles.
	// Do not use these variables directly, instead use their associated get functions so they
	// will be initialised on demand.
	agentClient      *http.Client
	storagePool      storagePools.Pool
	architectureName string
}

// getAgentClient returns the current agent client handle. To avoid TLS setup each time this
// function is called, the handle is cached internally in the Qemu struct.
func (vm *qemu) getAgentClient() (*http.Client, error) {
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
func (vm *qemu) getStoragePool() (storagePools.Pool, error) {
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

func (vm *qemu) getMonitorEventHandler() func(event string, data map[string]interface{}) {
	projectName := vm.Project()
	instanceName := vm.Name()
	state := vm.state

	return func(event string, data map[string]interface{}) {
		if !shared.StringInSlice(event, []string{"SHUTDOWN"}) {
			return
		}

		inst, err := instance.LoadByProjectAndName(state, projectName, instanceName)
		if err != nil {
			logger.Error("Failed to load instance", "project", projectName, "instance", instanceName, "err", err)
			return
		}

		if event == "SHUTDOWN" {
			target := "stop"
			entry, ok := data["reason"]
			if ok && entry == "guest-reset" {
				target = "reboot"
			}

			err = inst.(*qemu).onStop(target)
			if err != nil {
				logger.Error("Failed to cleanly stop instance", "project", projectName, "instance", instanceName, "err", err)
				return
			}
		}
	}
}

// mount the instance's config volume if needed.
func (vm *qemu) mount() (*storagePools.MountInfo, error) {
	var pool storagePools.Pool
	pool, err := vm.getStoragePool()
	if err != nil {
		return nil, err
	}

	if vm.IsSnapshot() {
		mountInfo, err := pool.MountInstanceSnapshot(vm, nil)
		if err != nil {
			return nil, err
		}

		return mountInfo, nil
	}

	mountInfo, err := pool.MountInstance(vm, nil)
	if err != nil {
		return nil, err
	}

	return mountInfo, nil
}

// unmount the instance's config volume if needed.
func (vm *qemu) unmount() (bool, error) {
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
func (vm *qemu) generateAgentCert() (string, string, string, string, error) {
	// Mount the instance's config volume if needed.
	_, err := vm.mount()
	if err != nil {
		return "", "", "", "", err
	}
	defer vm.unmount()

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
func (vm *qemu) Freeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
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

// onStop is run when the instance stops.
func (vm *qemu) onStop(target string) error {
	ctxMap := log.Ctx{
		"project":   vm.project,
		"name":      vm.name,
		"ephemeral": vm.ephemeral,
	}

	// Pick up the existing stop operation lock created in Stop() function.
	op := operationlock.Get(vm.id)
	if op != nil && op.Action() != "stop" {
		return fmt.Errorf("Instance is already running a %s operation", op.Action())
	}

	// Cleanup.
	vm.cleanupDevices()
	os.Remove(vm.pidFilePath())
	os.Remove(vm.monitorPath())
	vm.unmount()

	pidPath := filepath.Join(vm.LogPath(), "virtiofsd.pid")

	proc, err := subprocess.ImportProcess(pidPath)
	if err == nil {
		proc.Stop()
		os.Remove(pidPath)
	}

	// Record power state.
	err = vm.state.Cluster.UpdateInstancePowerState(vm.id, "STOPPED")
	if err != nil {
		if op != nil {
			op.Done(err)
		}
		return err
	}

	// Unload the apparmor profile
	err = apparmor.InstanceUnload(vm.state, vm)
	if err != nil {
		ctxMap["err"] = err
		logger.Error("Failed to unload AppArmor profile", ctxMap)
	}

	if target == "reboot" {
		err = vm.Start(false)
		vm.state.Events.SendLifecycle(vm.project, "virtual-machine-restarted",
			fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)
	} else if vm.ephemeral {
		// Destroy ephemeral virtual machines
		err = vm.Delete()
	}
	if err != nil {
		return err
	}

	if op != nil {
		op.Done(nil)
	}

	return nil
}

// Shutdown shuts the instance down.
func (vm *qemu) Shutdown(timeout time.Duration) error {
	if !vm.IsRunning() {
		return fmt.Errorf("The instance is already stopped")
	}

	// Setup a new operation
	op, err := operationlock.Create(vm.id, "stop", true, true)
	if err != nil {
		return err
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
	if err != nil {
		op.Done(err)
		return err
	}

	// Get the wait channel.
	chDisconnect, err := monitor.Wait()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			op.Done(nil)
			return nil
		}

		op.Done(err)
		return err
	}

	// Send the system_powerdown command.
	err = monitor.Powerdown()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			op.Done(nil)
			return nil
		}

		op.Done(err)
		return err
	}

	// If timeout provided, block until the VM is not running or the timeout has elapsed.
	if timeout > 0 {
		select {
		case <-chDisconnect:
			break
		case <-time.After(timeout):
			op.Done(fmt.Errorf("Instance was not shutdown after timeout"))
			return fmt.Errorf("Instance was not shutdown after timeout")
		}
	} else {
		<-chDisconnect // Block until VM is not running if no timeout provided.
	}

	// Wait for onStop.
	err = op.Wait()
	if err != nil && vm.IsRunning() {
		return err
	}

	op.Done(nil)
	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-shutdown", fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)
	return nil
}

// Restart restart the instance.
func (vm *qemu) Restart(timeout time.Duration) error {
	return vm.common.restart(vm, timeout)
}

func (vm *qemu) ovmfPath() string {
	if os.Getenv("LXD_OVMF_PATH") != "" {
		return os.Getenv("LXD_OVMF_PATH")
	}

	return "/usr/share/OVMF"
}

// Start starts the instance.
func (vm *qemu) Start(stateful bool) error {
	// Ensure the correct vhost_vsock kernel module is loaded before establishing the vsock.
	err := util.LoadModule("vhost_vsock")
	if err != nil {
		return err
	}

	if vm.IsRunning() {
		return fmt.Errorf("The instance is already running")
	}

	// Setup a new operation
	op, err := operationlock.Create(vm.id, "start", false, false)
	if err != nil {
		return errors.Wrap(err, "Create instance start operation")
	}
	defer op.Done(nil)

	revert := revert.New()
	defer revert.Fail()

	// Start accumulating device paths.
	vm.devPaths = []string{}

	// Rotate the log file.
	logfile := vm.LogFilePath()
	if shared.PathExists(logfile) {
		os.Remove(logfile + ".old")
		err := os.Rename(logfile, logfile+".old")
		if err != nil {
			return err
		}
	}

	// Mount the instance's config volume.
	mountInfo, err := vm.mount()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Add(func() { vm.unmount() })

	err = vm.generateConfigShare()
	if err != nil {
		op.Done(err)
		return err
	}

	// Create all needed paths.
	err = os.MkdirAll(vm.LogPath(), 0700)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(vm.DevicesPath(), 0711)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(vm.ShmountsPath(), 0711)
	if err != nil {
		op.Done(err)
		return err
	}

	// Setup virtiofsd for config path.
	sockPath := filepath.Join(vm.LogPath(), "virtio-fs.config.sock")

	// Remove old socket if needed.
	os.Remove(sockPath)

	cmd, err := exec.LookPath("virtiofsd")
	if err != nil {
		if shared.PathExists("/usr/lib/qemu/virtiofsd") {
			cmd = "/usr/lib/qemu/virtiofsd"
		}
	}

	if cmd != "" {
		// Start the virtiofsd process in non-daemon mode.
		proc, err := subprocess.NewProcess(cmd, []string{fmt.Sprintf("--socket-path=%s", sockPath), "-o", fmt.Sprintf("source=%s", filepath.Join(vm.Path(), "config"))}, "", "")
		if err != nil {
			return err
		}

		err = proc.Start()
		if err != nil {
			return err
		}

		revert.Add(func() { proc.Stop() })

		pidPath := filepath.Join(vm.LogPath(), "virtiofsd.pid")

		err = proc.Save(pidPath)
		if err != nil {
			return err
		}

		// Wait for socket file to exist
		for i := 0; i < 20; i++ {
			if shared.PathExists(sockPath) {
				break
			}

			time.Sleep(50 * time.Millisecond)
		}

		if !shared.PathExists(sockPath) {
			return fmt.Errorf("virtiofsd failed to bind socket within 1s")
		}
	} else {
		logger.Warn("Unable to use virtio-fs for config drive, using 9p as a fallback: virtiofsd missing")
	}

	// Generate UUID if not present.
	instUUID := vm.localConfig["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New()
		vm.VolatileSet(map[string]string{"volatile.uuid": instUUID})
	}

	// Copy OVMF settings firmware to nvram file.
	// This firmware file can be modified by the VM so it must be copied from the defaults.
	if !shared.PathExists(vm.nvramPath()) {
		err = vm.setupNvram()
		if err != nil {
			op.Done(err)
			return err
		}
	}

	devConfs := make([]*deviceConfig.RunConfig, 0, len(vm.expandedDevices))

	// Setup devices in sorted order, this ensures that device mounts are added in path order.
	for _, d := range vm.expandedDevices.Sorted() {
		dev := d // Ensure device variable has local scope for revert.

		// Start the device.
		runConf, err := vm.deviceStart(dev.Name, dev.Config, false)
		if err != nil {
			op.Done(err)
			return errors.Wrapf(err, "Failed to start device %q", dev.Name)
		}

		if runConf == nil {
			continue
		}

		revert.Add(func() {
			err := vm.deviceStop(dev.Name, dev.Config, false)
			if err != nil {
				logger.Errorf("Failed to cleanup device %q: %v", dev.Name, err)
			}
		})

		devConfs = append(devConfs, runConf)
	}

	// Get qemu configuration.
	qemuBinary, qemuBus, err := vm.qemuArchConfig()
	if err != nil {
		op.Done(err)
		return err
	}

	// Define a set of files to open and pass their file descriptors to qemu command.
	fdFiles := make([]string, 0)

	confFile, err := vm.generateQemuConfigFile(mountInfo, qemuBus, devConfs, &fdFiles)
	if err != nil {
		op.Done(err)
		return err
	}

	// Check qemu is installed.
	qemuPath, err := exec.LookPath(qemuBinary)
	if err != nil {
		op.Done(err)
		return err
	}

	qemuCmd := []string{
		"--",
		qemuPath,
		"-S",
		"-name", vm.Name(),
		"-uuid", instUUID,
		"-daemonize",
		"-cpu", "host",
		"-nographic",
		"-serial", "chardev:console",
		"-nodefaults",
		"-no-reboot",
		"-no-user-config",
		"-sandbox", "on,obsolete=deny,elevateprivileges=allow,spawn=deny,resourcecontrol=deny",
		"-readconfig", confFile,
		"-pidfile", vm.pidFilePath(),
		"-D", vm.LogFilePath(),
		"-chroot", vm.Path(),
	}

	// SMBIOS only on x86_64 and aarch64.
	if shared.IntInSlice(vm.architecture, []int{osarch.ARCH_64BIT_INTEL_X86, osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN}) {
		qemuCmd = append(qemuCmd, "-smbios", "type=2,manufacturer=Canonical Ltd.,product=LXD")
	}

	// Attempt to drop privileges.
	if vm.state.OS.UnprivUser != "" {
		qemuCmd = append(qemuCmd, "-runas", vm.state.OS.UnprivUser)

		// Change ownership of config directory files so they are accessible to the
		// unprivileged qemu process so that the 9p share can work.
		//
		// Security note: The 9P share will present the UID owner of these files on the host
		// to the VM. In order to ensure that non-root users in the VM cannot access these
		// files be sure to mount the 9P share in the VM with the "access=0" option to allow
		// only root user in VM to access the mounted share.
		err := filepath.Walk(filepath.Join(vm.Path(), "config"),
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					op.Done(err)
					return err
				}

				err = os.Chown(path, int(vm.state.OS.UnprivUID), -1)
				if err != nil {
					op.Done(err)
					return err
				}

				return nil
			})
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Handle hugepages on architectures where we don't set NUMA nodes.
	if vm.architecture != osarch.ARCH_64BIT_INTEL_X86 && shared.IsTrue(vm.expandedConfig["limits.memory.hugepages"]) {
		hugetlb, err := util.HugepagesPath()
		if err != nil {
			op.Done(err)
			return err
		}

		qemuCmd = append(qemuCmd, "-mem-path", hugetlb, "-mem-prealloc")
	}

	if vm.expandedConfig["raw.qemu"] != "" {
		fields, err := shellquote.Split(vm.expandedConfig["raw.qemu"])
		if err != nil {
			op.Done(err)
			return err
		}
		qemuCmd = append(qemuCmd, fields...)
	}

	// Run the qemu command via forklimits so we can selectively increase ulimits.
	forkLimitsCmd := []string{
		"forklimits",
		"limit=memlock:unlimited:unlimited", // Required for PCI passthrough.
	}

	for i := range fdFiles {
		// Pass through any file descriptors as 3+i (as first 3 file descriptors are taken as standard).
		forkLimitsCmd = append(forkLimitsCmd, fmt.Sprintf("fd=%d", 3+i))
	}

	// Setup background process.
	p, err := subprocess.NewProcess(vm.state.OS.ExecPath, append(forkLimitsCmd, qemuCmd...), vm.EarlyLogFilePath(), vm.EarlyLogFilePath())
	if err != nil {
		return err
	}

	// Load the AppArmor profile
	err = apparmor.InstanceLoad(vm.state, vm)
	if err != nil {
		op.Done(err)
		return err
	}

	p.SetApparmor(apparmor.InstanceProfileName(vm))

	// Open any extra files and pass their file handles to qemu command.
	files := []*os.File{}
	for _, file := range fdFiles {
		info, err := os.Stat(file)
		if err != nil {
			err = errors.Wrapf(err, "Error detecting file type %q", file)
			op.Done(err)
			return err
		}

		var f *os.File
		mode := info.Mode()
		if mode&os.ModeSocket != 0 {
			c, err := vm.openUnixSocket(file)
			if err != nil {
				err = errors.Wrapf(err, "Error opening socket file %q", file)
				op.Done(err)
				return err
			}

			f, err = c.File()
			if err != nil {
				err = errors.Wrapf(err, "Error getting socket file descriptor %q", file)
				op.Done(err)
				return err
			}
			defer c.Close()
			defer f.Close() // Close file after qemu has started.
		} else {
			f, err = os.OpenFile(file, os.O_RDWR, 0)
			if err != nil {
				err = errors.Wrapf(err, "Error opening exta file %q", file)
				op.Done(err)
				return err
			}
			defer f.Close() // Close file after qemu has started.
		}

		files = append(files, f)
	}

	err = p.StartWithFiles(files)
	if err != nil {
		return err
	}

	_, err = p.Wait()
	if err != nil {
		stderr, _ := ioutil.ReadFile(vm.EarlyLogFilePath())
		err = errors.Wrapf(err, "Failed to run: %s: %s", strings.Join(p.Args, " "), string(stderr))
		op.Done(err)
		return err
	}

	pid, err := vm.pid()
	if err != nil {
		logger.Errorf(`Failed to get VM process ID "%d"`, pid)
		return err
	}

	revert.Add(func() {
		proc, err := os.FindProcess(pid)
		if err != nil {
			logger.Errorf(`Failed to find VM process "%d"`, pid)
			return
		}

		proc.Kill()
		if err != nil {
			logger.Errorf(`Failed to kill VM process "%d"`, pid)
		}
	})

	// Start QMP monitoring.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
	if err != nil {
		op.Done(err)
		return err
	}

	// Apply CPU pinning.
	cpuLimit, ok := vm.expandedConfig["limits.cpu"]
	if ok && cpuLimit != "" {
		_, err := strconv.Atoi(cpuLimit)
		if err != nil {
			// Expand to a set of CPU identifiers and get the pinning map.
			_, _, _, pins, _, err := vm.cpuTopology(cpuLimit)
			if err != nil {
				op.Done(err)
				return err
			}

			// Get the list of PIDs from the VM.
			pids, err := monitor.GetCPUs()
			if err != nil {
				op.Done(err)
				return err
			}

			// Confirm nothing weird is going on.
			if len(pins) != len(pids) {
				return fmt.Errorf("QEMU has less vCPUs than configured")
			}

			for i, pid := range pids {
				set := unix.CPUSet{}
				set.Set(int(pins[uint64(i)]))

				// Apply the pin.
				err := unix.SchedSetaffinity(pid, &set)
				if err != nil {
					op.Done(err)
					return err
				}
			}
		}
	}

	// Start the VM.
	err = monitor.Start()
	if err != nil {
		op.Done(err)
		return err
	}

	// Database updates
	err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Record current state
		err = tx.UpdateInstancePowerState(vm.id, "RUNNING")
		if err != nil {
			err = errors.Wrap(err, "Error updating instance state")
			op.Done(err)
			return err
		}

		// Update time instance last started time
		err = tx.UpdateInstanceLastUsedDate(vm.id, time.Now().UTC())
		if err != nil {
			err = errors.Wrap(err, "Error updating instance last used")
			op.Done(err)
			return err
		}

		return nil
	})
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Success()
	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-started", fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)
	return nil
}

// openUnixSocket connects to a UNIX socket and returns the connection.
func (vm *qemu) openUnixSocket(sockPath string) (*net.UnixConn, error) {
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		return nil, err
	}

	c, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (vm *qemu) setupNvram() error {
	// UEFI only on x86_64 and aarch64.
	if !shared.IntInSlice(vm.architecture, []int{osarch.ARCH_64BIT_INTEL_X86, osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN}) {
		return nil
	}

	// Mount the instance's config volume.
	_, err := vm.mount()
	if err != nil {
		return err
	}
	defer vm.unmount()

	srcOvmfFile := filepath.Join(vm.ovmfPath(), "OVMF_VARS.fd")
	if vm.expandedConfig["security.secureboot"] == "" || shared.IsTrue(vm.expandedConfig["security.secureboot"]) {
		srcOvmfFile = filepath.Join(vm.ovmfPath(), "OVMF_VARS.ms.fd")
	}

	if !shared.PathExists(srcOvmfFile) {
		return fmt.Errorf("Required EFI firmware settings file missing: %s", srcOvmfFile)
	}

	os.Remove(vm.nvramPath())
	err = shared.FileCopy(srcOvmfFile, vm.nvramPath())
	if err != nil {
		return err
	}

	return nil
}

func (vm *qemu) qemuArchConfig() (string, string, error) {
	if vm.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		return "qemu-system-x86_64", "pcie", nil
	} else if vm.architecture == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN {
		return "qemu-system-aarch64", "pcie", nil
	} else if vm.architecture == osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN {
		return "qemu-system-ppc64", "pci", nil
	} else if vm.architecture == osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		return "qemu-system-s390x", "ccw", nil
	}

	return "", "", fmt.Errorf("Architecture isn't supported for virtual machines")
}

// deviceVolatileGetFunc returns a function that retrieves a named device's volatile config and
// removes its device prefix from the keys.
func (vm *qemu) deviceVolatileGetFunc(devName string) func() map[string]string {
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
func (vm *qemu) deviceVolatileSetFunc(devName string) func(save map[string]string) error {
	return func(save map[string]string) error {
		volatileSave := make(map[string]string)
		for k, v := range save {
			volatileSave[fmt.Sprintf("volatile.%s.%s", devName, k)] = v
		}

		return vm.VolatileSet(volatileSave)
	}
}

// RegisterDevices calls the Register() function on all of the instance's devices.
func (vm *qemu) RegisterDevices() {
	devices := vm.ExpandedDevices()
	for _, dev := range devices.Sorted() {
		d, _, err := vm.deviceLoad(dev.Name, dev.Config)
		if err == device.ErrUnsupportedDevType {
			continue
		}

		if err != nil {
			logger.Error("Failed to load device to register", log.Ctx{"err": err, "instance": vm.Name(), "device": dev.Name})
			continue
		}

		// Check whether device wants to register for any events.
		err = d.Register()
		if err != nil {
			logger.Error("Failed to register device", log.Ctx{"err": err, "instance": vm.Name(), "device": dev.Name})
			continue
		}
	}
}

// SaveConfigFile is not used by VMs.
func (vm *qemu) SaveConfigFile() error {
	return instance.ErrNotImplemented
}

// OnHook is the top-level hook handler.
func (vm *qemu) OnHook(hookName string, args map[string]string) error {
	return instance.ErrNotImplemented
}

// deviceLoad instantiates and validates a new device and returns it along with enriched config.
func (vm *qemu) deviceLoad(deviceName string, rawConfig deviceConfig.Device) (device.Device, deviceConfig.Device, error) {
	var configCopy deviceConfig.Device
	var err error

	// Create copy of config and load some fields from volatile if device is nic or infiniband.
	if shared.StringInSlice(rawConfig["type"], []string{"nic", "infiniband"}) {
		configCopy, err = vm.FillNetworkDevice(deviceName, rawConfig)
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

// deviceStart loads a new device and calls its Start() function.
func (vm *qemu) deviceStart(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) (*deviceConfig.RunConfig, error) {
	logger := logging.AddContext(logger.Log, log.Ctx{"device": deviceName, "type": rawConfig["type"], "project": vm.Project(), "instance": vm.Name()})
	logger.Debug("Starting device")

	d, _, err := vm.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return nil, err
	}

	if instanceRunning && !d.CanHotPlug() {
		return nil, fmt.Errorf("Device cannot be started when instance is running")
	}

	runConf, err := d.Start()
	if err != nil {
		return nil, err
	}

	return runConf, nil
}

// deviceStop loads a new device and calls its Stop() function.
func (vm *qemu) deviceStop(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"device": deviceName, "type": rawConfig["type"], "project": vm.Project(), "instance": vm.Name()})
	logger.Debug("Stopping device")

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
			return fmt.Errorf("Device stop validation failed for %q: %v", deviceName, err)

		}

		logger.Error("Device stop validation failed", log.Ctx{"err": err})
	}

	if instanceRunning && !d.CanHotPlug() {
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
func (vm *qemu) runHooks(hooks []func() error) error {
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

func (vm *qemu) monitorPath() string {
	return filepath.Join(vm.LogPath(), "qemu.monitor")
}

func (vm *qemu) nvramPath() string {
	return filepath.Join(vm.Path(), "qemu.nvram")
}

func (vm *qemu) spicePath() string {
	return filepath.Join(vm.LogPath(), "qemu.spice")
}

// generateConfigShare generates the config share directory that will be exported to the VM via
// a 9P share. Due to the unknown size of templates inside the images this directory is created
// inside the VM's config volume so that it can be restricted by quota.
// Requires the instance be mounted before calling this function.
func (vm *qemu) generateConfigShare() error {
	configDrivePath := filepath.Join(vm.Path(), "config")

	// Create config drive dir.
	os.RemoveAll(configDrivePath)
	err := os.MkdirAll(configDrivePath, 0500)
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
Documentation=https://linuxcontainers.org/lxd
ConditionPathExists=/dev/virtio-ports/org.linuxcontainers.lxd
Wants=lxd-agent-virtiofs.service
After=lxd-agent-virtiofs.service
Wants=lxd-agent-9p.service
After=lxd-agent-9p.service

Before=cloud-init.target cloud-init.service cloud-init-local.service
DefaultDependencies=no

[Service]
Type=notify
WorkingDirectory=/run/lxd_config/drive
ExecStart=/run/lxd_config/drive/lxd-agent
Restart=on-failure
RestartSec=5s
StartLimitInterval=60
StartLimitBurst=10

[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "systemd", "lxd-agent.service"), []byte(lxdAgentServiceUnit), 0400)
	if err != nil {
		return err
	}

	lxdConfigShareMountUnit := `[Unit]
Description=LXD - agent - 9p mount
Documentation=https://linuxcontainers.org/lxd
ConditionPathExists=/dev/virtio-ports/org.linuxcontainers.lxd
After=local-fs.target lxd-agent-virtiofs.service
DefaultDependencies=no
ConditionPathIsMountPoint=!/run/lxd_config/drive

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=-/sbin/modprobe 9pnet_virtio
ExecStartPre=/bin/mkdir -p /run/lxd_config/drive
ExecStartPre=/bin/chmod 0700 /run/lxd_config/
ExecStart=/bin/mount -t 9p config /run/lxd_config/drive -o access=0,trans=virtio

[Install]
WantedBy=multi-user.target
`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "systemd", "lxd-agent-9p.service"), []byte(lxdConfigShareMountUnit), 0400)
	if err != nil {
		return err
	}

	lxdConfigShareMountVirtioFSUnit := `[Unit]
Description=LXD - agent - virtio-fs mount
Documentation=https://linuxcontainers.org/lxd
ConditionPathExists=/dev/virtio-ports/org.linuxcontainers.lxd
After=local-fs.target
Before=lxd-agent-9p.service
DefaultDependencies=no
ConditionPathIsMountPoint=!/run/lxd_config/drive

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=/bin/mkdir -p /run/lxd_config/drive
ExecStartPre=/bin/chmod 0700 /run/lxd_config/
ExecStart=/bin/mount -t virtiofs config /run/lxd_config/drive

[Install]
WantedBy=multi-user.target
	`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "systemd", "lxd-agent-virtiofs.service"), []byte(lxdConfigShareMountVirtioFSUnit), 0400)
	if err != nil {
		return err
	}

	// Udev rules
	err = os.MkdirAll(filepath.Join(configDrivePath, "udev"), 0500)
	if err != nil {
		return err
	}

	lxdAgentRules := `ACTION=="add", SYMLINK=="virtio-ports/org.linuxcontainers.lxd", TAG+="systemd", ACTION=="add", RUN+="/bin/systemctl start lxd-agent.service"`
	err = ioutil.WriteFile(filepath.Join(configDrivePath, "udev", "99-lxd-agent.rules"), []byte(lxdAgentRules), 0400)
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

cp udev/99-lxd-agent.rules /lib/udev/rules.d/
cp systemd/lxd-agent.service /lib/systemd/system/
cp systemd/lxd-agent-9p.service /lib/systemd/system/
cp systemd/lxd-agent-virtiofs.service /lib/systemd/system/
systemctl daemon-reload
systemctl enable lxd-agent.service lxd-agent-9p.service lxd-agent-virtiofs.service

echo ""
echo "LXD agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: systemctl start lxd-agent"
`

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "install.sh"), []byte(lxdConfigShareInstall), 0700)
	if err != nil {
		return err
	}

	// Instance data for devlxd.
	err = vm.writeInstanceData()
	if err != nil {
		return err
	}

	// Templated files.
	err = os.MkdirAll(filepath.Join(configDrivePath, "files"), 0500)
	if err != nil {
		return err
	}

	// Template anything that needs templating.
	key := "volatile.apply_template"
	if vm.localConfig[key] != "" {
		// Run any template that needs running.
		err = vm.templateApplyNow(vm.localConfig[key], filepath.Join(configDrivePath, "files"))
		if err != nil {
			return err
		}

		// Remove the volatile key from the DB.
		err := vm.state.Cluster.DeleteInstanceConfigKey(vm.id, key)
		if err != nil {
			return err
		}
	}

	err = vm.templateApplyNow("start", filepath.Join(configDrivePath, "files"))
	if err != nil {
		return err
	}

	// Copy the template metadata itself too.
	metaPath := filepath.Join(vm.Path(), "metadata.yaml")
	if shared.PathExists(metaPath) {
		err = shared.FileCopy(metaPath, filepath.Join(configDrivePath, "files/metadata.yaml"))
		if err != nil {
			return err
		}
	}

	return nil
}

func (vm *qemu) templateApplyNow(trigger string, path string) error {
	// If there's no metadata, just return.
	fname := filepath.Join(vm.Path(), "metadata.yaml")
	if !shared.PathExists(fname) {
		return nil
	}

	// Parse the metadata.
	content, err := ioutil.ReadFile(fname)
	if err != nil {
		return errors.Wrap(err, "Failed to read metadata")
	}

	metadata := new(api.ImageMetadata)
	err = yaml.Unmarshal(content, &metadata)
	if err != nil {
		return errors.Wrapf(err, "Could not parse %s", fname)
	}

	// Figure out the instance architecture.
	arch, err := osarch.ArchitectureName(vm.architecture)
	if err != nil {
		arch, err = osarch.ArchitectureName(vm.state.OS.Architectures[0])
		if err != nil {
			return errors.Wrap(err, "Failed to detect system architecture")
		}
	}

	// Generate the container metadata.
	instanceMeta := make(map[string]string)
	instanceMeta["name"] = vm.name
	instanceMeta["type"] = "virtual-machine"
	instanceMeta["architecture"] = arch

	if vm.ephemeral {
		instanceMeta["ephemeral"] = "true"
	} else {
		instanceMeta["ephemeral"] = "false"
	}

	// Go through the templates.
	for tplPath, tpl := range metadata.Templates {
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			var w *os.File

			// Check if the template should be applied now.
			found := false
			for _, tplTrigger := range tpl.When {
				if tplTrigger == trigger {
					found = true
					break
				}
			}

			if !found {
				return nil
			}

			// Create the file itself.
			w, err = os.Create(filepath.Join(path, fmt.Sprintf("%s.out", tpl.Template)))
			if err != nil {
				return err
			}

			// Fix ownership and mode.
			w.Chmod(0644)
			defer w.Close()

			// Read the template.
			tplString, err := ioutil.ReadFile(filepath.Join(vm.TemplatesPath(), tpl.Template))
			if err != nil {
				return errors.Wrap(err, "Failed to read template file")
			}

			// Restrict filesystem access to within the container's rootfs.
			tplSet := pongo2.NewSet(fmt.Sprintf("%s-%s", vm.name, tpl.Template), pongoTemplate.ChrootLoader{Path: vm.TemplatesPath()})
			tplRender, err := tplSet.FromString("{% autoescape off %}" + string(tplString) + "{% endautoescape %}")
			if err != nil {
				return errors.Wrap(err, "Failed to render template")
			}

			configGet := func(confKey, confDefault *pongo2.Value) *pongo2.Value {
				val, ok := vm.expandedConfig[confKey.String()]
				if !ok {
					return confDefault
				}

				return pongo2.AsValue(strings.TrimRight(val, "\r\n"))
			}

			// Render the template.
			tplRender.ExecuteWriter(pongo2.Context{"trigger": trigger,
				"path":       tplPath,
				"instance":   instanceMeta,
				"container":  instanceMeta, // FIXME: remove once most images have moved away.
				"config":     vm.expandedConfig,
				"devices":    vm.expandedDevices,
				"properties": tpl.Properties,
				"config_get": configGet}, w)

			return nil
		}(tplPath, tpl)
		if err != nil {
			return err
		}
	}

	return nil
}

// deviceBootPriorities returns a map keyed on device name containing the boot index to use.
// Qemu tries to boot devices in order of boot index (lowest first).
func (vm *qemu) deviceBootPriorities() (map[string]int, error) {
	type devicePrios struct {
		Name     string
		BootPrio uint32
	}

	devices := []devicePrios{}

	for devName, devConf := range vm.expandedDevices {
		if devConf["type"] != "disk" && devConf["type"] != "nic" {
			continue
		}

		bootPrio := uint32(0) // Default to lowest priority.
		if devConf["boot.priority"] != "" {
			prio, err := strconv.ParseInt(devConf["boot.priority"], 10, 32)
			if err != nil {
				return nil, errors.Wrapf(err, "Invalid boot.priority for device %q", devName)
			}
			bootPrio = uint32(prio)
		} else if devConf["path"] == "/" {
			bootPrio = 1 // Set boot priority of root disk higher than any device without a boot prio.
		}

		devices = append(devices, devicePrios{Name: devName, BootPrio: bootPrio})
	}

	sort.SliceStable(devices, func(i, j int) bool { return devices[i].BootPrio > devices[j].BootPrio })

	sortedDevs := make(map[string]int, len(devices))
	for bootIndex, dev := range devices {
		sortedDevs[dev.Name] = bootIndex
	}

	return sortedDevs, nil
}

// generateQemuConfigFile writes the qemu config file and returns its location.
// It writes the config file inside the VM's log path.
func (vm *qemu) generateQemuConfigFile(mountInfo *storagePools.MountInfo, busName string, devConfs []*deviceConfig.RunConfig, fdFiles *[]string) (string, error) {
	var sb *strings.Builder = &strings.Builder{}

	err := qemuBase.Execute(sb, map[string]interface{}{
		"architecture": vm.architectureName,
		"spicePath":    vm.spicePath(),
	})
	if err != nil {
		return "", err
	}

	err = vm.addCPUMemoryConfig(sb)
	if err != nil {
		return "", err
	}

	err = qemuDriveFirmware.Execute(sb, map[string]interface{}{
		"architecture": vm.architectureName,
		"roPath":       filepath.Join(vm.ovmfPath(), "OVMF_CODE.fd"),
		"nvramPath":    vm.nvramPath(),
	})
	if err != nil {
		return "", err
	}

	err = qemuControlSocket.Execute(sb, map[string]interface{}{
		"path": vm.monitorPath(),
	})
	if err != nil {
		return "", err
	}

	// Setup the bus allocator.
	bus := qemuNewBus(busName, sb)

	// Now add the fixed set of devices. The multi-function groups used for these fixed internal devices are
	// specifically chosen to ensure that we consume exactly 4 PCI bus ports (on PCIe bus). This ensures that
	// the first user device NIC added will use the 5th PCI bus port and will be consistently named enp5s0
	// on PCIe (which we need to maintain compatibility with network configuration in our existing VM images).
	// It's also meant to group all low-bandwidth internal devices onto a single address. PCIe bus allows a
	// total of 256 devices, but this assumes 32 chassis * 8 function. By using VFs for the internal fixed
	// devices we avoid consuming a chassis for each one.
	devBus, devAddr, multi := bus.allocate(busFunctionGroupGeneric)
	err = qemuBalloon.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,
	})
	if err != nil {
		return "", err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	err = qemuRNG.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,
	})
	if err != nil {
		return "", err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	err = qemuKeyboard.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,
	})
	if err != nil {
		return "", err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	err = qemuTablet.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,
	})
	if err != nil {
		return "", err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	err = qemuVsock.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,

		"vsockID": vm.vsockID(),
	})
	if err != nil {
		return "", err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	err = qemuSerial.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,

		"chardevName":      qemuSerialChardevName,
		"ringbufSizeBytes": qmp.RingbufSize,
	})
	if err != nil {
		return "", err
	}

	// s390x doesn't really have USB.
	if vm.architecture != osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
		err = qemuUSB.Execute(sb, map[string]interface{}{
			"bus":           bus.name,
			"devBus":        devBus,
			"devAddr":       devAddr,
			"multifunction": multi,
		})
		if err != nil {
			return "", err
		}
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupNone)
	err = qemuSCSI.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,
	})
	if err != nil {
		return "", err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroup9p)
	err = qemuDriveConfig.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,
		"protocol":      "9p",

		"path": filepath.Join(vm.Path(), "config"),
	})
	if err != nil {
		return "", err
	}

	sockPath := filepath.Join(vm.LogPath(), "virtio-fs.config.sock")
	if shared.PathExists(sockPath) {
		devBus, devAddr, multi = bus.allocate(busFunctionGroup9p)
		err = qemuDriveConfig.Execute(sb, map[string]interface{}{
			"bus":           bus.name,
			"devBus":        devBus,
			"devAddr":       devAddr,
			"multifunction": multi,
			"protocol":      "virtio-fs",

			"path": sockPath,
		})
		if err != nil {
			return "", err
		}
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupNone)
	err = qemuGPU.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,

		"architecture": vm.architectureName,
	})
	if err != nil {
		return "", err
	}

	// Dynamic devices.
	bootIndexes, err := vm.deviceBootPriorities()
	if err != nil {
		return "", errors.Wrap(err, "Error calculating boot indexes")
	}

	// Record the mounts we are going to do inside the VM using the agent.
	agentMounts := []instancetype.VMAgentMount{}

	// These devices are sorted so that NICs are added first to ensure that the first NIC can use the 5th
	// PCIe bus port and will be consistently named enp5s0 for compatibility with network configuration in our
	// existing VM images. Even on non-PCIe busses having NICs first means that their names won't change when
	// other devices are added.
	for _, runConf := range devConfs {
		// Add drive devices.
		if len(runConf.Mounts) > 0 {
			for _, drive := range runConf.Mounts {
				if drive.TargetPath == "/" {
					err = vm.addRootDriveConfig(sb, mountInfo, bootIndexes, drive)
				} else if drive.FSType == "9p" {
					err = vm.addDriveDirConfig(sb, bus, fdFiles, &agentMounts, drive)
				} else {
					err = vm.addDriveConfig(sb, bootIndexes, drive)
				}
				if err != nil {
					return "", err
				}
			}
		}

		// Add network device.
		if len(runConf.NetworkInterface) > 0 {
			err = vm.addNetDevConfig(sb, bus, bootIndexes, runConf.NetworkInterface, fdFiles)
			if err != nil {
				return "", err
			}
		}

		// Add GPU device.
		if len(runConf.GPUDevice) > 0 {
			err = vm.addGPUDevConfig(sb, bus, runConf.GPUDevice)
			if err != nil {
				return "", err
			}
		}

		// Add USB device.
		if len(runConf.USBDevice) > 0 {
			err = vm.addUSBDeviceConfig(sb, bus, runConf.USBDevice)
			if err != nil {
				return "", err
			}
		}
	}

	// Write the agent mount config.
	agentMountJSON, err := json.Marshal(agentMounts)
	if err != nil {
		return "", errors.Wrapf(err, "Failed marshalling agent mounts to JSON")
	}

	agentMountFile := filepath.Join(vm.Path(), "config", "agent-mounts.json")
	err = ioutil.WriteFile(agentMountFile, agentMountJSON, 0400)
	if err != nil {
		return "", errors.Wrapf(err, "Failed writing agent mounts file")
	}

	// Write the config file to disk.
	configPath := filepath.Join(vm.LogPath(), "qemu.conf")
	return configPath, ioutil.WriteFile(configPath, []byte(sb.String()), 0640)
}

// addCPUMemoryConfig adds the qemu config required for setting the number of virtualised CPUs and memory.
func (vm *qemu) addCPUMemoryConfig(sb *strings.Builder) error {
	// Default to a single core.
	cpus := vm.expandedConfig["limits.cpu"]
	if cpus == "" {
		cpus = "1"
	}

	ctx := map[string]interface{}{
		"architecture": vm.architectureName,
	}

	cpuCount, err := strconv.Atoi(cpus)
	hostNodes := []uint64{}
	if err == nil {
		// If not pinning, default to exposing cores.
		ctx["cpuCount"] = cpuCount
		ctx["cpuSockets"] = 1
		ctx["cpuCores"] = cpuCount
		ctx["cpuThreads"] = 1
		hostNodes = []uint64{0}
	} else {
		// Expand to a set of CPU identifiers and get the pinning map.
		nrSockets, nrCores, nrThreads, vcpus, numaNodes, err := vm.cpuTopology(cpus)
		if err != nil {
			return err
		}

		// Figure out socket-id/core-id/thread-id for all vcpus.
		vcpuSocket := map[uint64]uint64{}
		vcpuCore := map[uint64]uint64{}
		vcpuThread := map[uint64]uint64{}
		vcpu := uint64(0)
		for i := 0; i < nrSockets; i++ {
			for j := 0; j < nrCores; j++ {
				for k := 0; k < nrThreads; k++ {
					vcpuSocket[vcpu] = uint64(i)
					vcpuCore[vcpu] = uint64(j)
					vcpuThread[vcpu] = uint64(k)
					vcpu++
				}
			}
		}

		// Prepare the NUMA map.
		numa := []map[string]uint64{}
		numaIDs := []uint64{}
		numaNode := uint64(0)
		for hostNode, entry := range numaNodes {
			hostNodes = append(hostNodes, hostNode)

			numaIDs = append(numaIDs, numaNode)
			for _, vcpu := range entry {
				numa = append(numa, map[string]uint64{
					"node":   numaNode,
					"socket": vcpuSocket[vcpu],
					"core":   vcpuCore[vcpu],
					"thread": vcpuThread[vcpu],
				})
			}

			numaNode++
		}

		// Prepare context.
		ctx["cpuCount"] = len(vcpus)
		ctx["cpuSockets"] = nrSockets
		ctx["cpuCores"] = nrCores
		ctx["cpuThreads"] = nrThreads
		ctx["cpuNumaNodes"] = numaIDs
		ctx["cpuNumaMapping"] = numa
		ctx["cpuNumaHostNodes"] = hostNodes
	}

	// Configure memory limit.
	memSize := vm.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = qemuDefaultMemSize // Default if no memory limit specified.
	}

	memSizeBytes, err := units.ParseByteSizeString(memSize)
	if err != nil {
		return fmt.Errorf("limits.memory invalid: %v", err)
	}

	ctx["hugepages"] = ""
	if shared.IsTrue(vm.expandedConfig["limits.memory.hugepages"]) {
		hugetlb, err := util.HugepagesPath()
		if err != nil {
			return err
		}

		ctx["hugepages"] = hugetlb
	}

	// Determine per-node memory limit.
	memSizeBytes = memSizeBytes / 1024 / 1024
	nodeMemory := int64(memSizeBytes / int64(len(hostNodes)))
	memSizeBytes = nodeMemory * int64(len(hostNodes))
	ctx["memory"] = nodeMemory

	err = qemuMemory.Execute(sb, map[string]interface{}{
		"architecture": vm.architectureName,
		"memSizeBytes": memSizeBytes,
	})
	if err != nil {
		return err
	}

	// Configure the CPU limit.
	return qemuCPU.Execute(sb, ctx)
}

// addFileDescriptor adds a file path to the list of files to open and pass file descriptor to qemu.
// Returns the file descriptor number that qemu will receive.
func (vm *qemu) addFileDescriptor(fdFiles *[]string, filePath string) int {
	// Append the tap device file path to the list of files to be opened and passed to qemu.
	*fdFiles = append(*fdFiles, filePath)
	return 2 + len(*fdFiles) // Use 2+fdFiles count, as first user file descriptor is 3.
}

// addRootDriveConfig adds the qemu config required for adding the root drive.
func (vm *qemu) addRootDriveConfig(sb *strings.Builder, mountInfo *storagePools.MountInfo, bootIndexes map[string]int, rootDriveConf deviceConfig.MountEntryItem) error {
	if rootDriveConf.TargetPath != "/" {
		return fmt.Errorf("Non-root drive config supplied")
	}

	pool, err := vm.getStoragePool()
	if err != nil {
		return err
	}

	if mountInfo.DiskPath == "" {
		return fmt.Errorf("No disk path available from mount")
	}

	// Generate a new device config with the root device path expanded.
	driveConf := deviceConfig.MountEntryItem{
		DevName: rootDriveConf.DevName,
		DevPath: mountInfo.DiskPath,
	}

	// If the storage pool is on ZFS and backed by a loop file and we can't use DirectIO, then resort to
	// unsafe async I/O to avoid kernel hangs when running ZFS storage pools in an image file on another FS.
	driverInfo := pool.Driver().Info()
	driverConf := pool.Driver().Config()
	if driverInfo.Name == "zfs" && !driverInfo.DirectIO && shared.PathExists(driverConf["source"]) && !shared.IsBlockdevPath(driverConf["source"]) {
		driveConf.Opts = append(driveConf.Opts, qemuUnsafeIO)
	}

	return vm.addDriveConfig(sb, bootIndexes, driveConf)
}

// addDriveDirConfig adds the qemu config required for adding a supplementary drive directory share.
func (vm *qemu) addDriveDirConfig(sb *strings.Builder, bus *qemuBus, fdFiles *[]string, agentMounts *[]instancetype.VMAgentMount, driveConf deviceConfig.MountEntryItem) error {
	mountTag := fmt.Sprintf("lxd_%s", driveConf.DevName)

	agentMount := instancetype.VMAgentMount{
		Source: mountTag,
		Target: driveConf.TargetPath,
		FSType: driveConf.FSType,
	}

	// If mount type is 9p, we need to specify to use the virtio transport to support more VM guest OSes.
	if agentMount.FSType == "9p" {
		agentMount.Options = append(agentMount.Options, "trans=virtio")
	}

	// Indicate to agent to mount this readonly. Note: This is purely to indicate to VM guest that this is
	// readonly, it should *not* be used as a security measure, as the VM guest could remount it R/W.
	if shared.StringInSlice("ro", driveConf.Opts) {
		agentMount.Options = append(agentMount.Options, "ro")
	}

	// Record the 9p mount for the agent.
	*agentMounts = append(*agentMounts, agentMount)

	sockPath := filepath.Join(vm.Path(), fmt.Sprintf("%s.sock", driveConf.DevName))

	if shared.PathExists(sockPath) {
		devBus, devAddr, multi := bus.allocate(busFunctionGroup9p)

		// Add virtio-fs device as this will be preferred over 9p.
		err := qemuDriveDir.Execute(sb, map[string]interface{}{
			"bus":           bus.name,
			"devBus":        devBus,
			"devAddr":       devAddr,
			"multifunction": multi,

			"devName":  driveConf.DevName,
			"mountTag": mountTag,
			"path":     sockPath,
			"protocol": "virtio-fs",
		})
		if err != nil {
			return err
		}
	}

	devBus, devAddr, multi := bus.allocate(busFunctionGroup9p)

	// For read only shares, do not use proxy.
	if shared.StringInSlice("ro", driveConf.Opts) {
		return qemuDriveDir.Execute(sb, map[string]interface{}{
			"bus":           bus.name,
			"devBus":        devBus,
			"devAddr":       devAddr,
			"multifunction": multi,

			"devName":  driveConf.DevName,
			"mountTag": mountTag,
			"path":     driveConf.DevPath,
			"readonly": true,
			"protocol": "9p",
		})
	}

	// Only use proxy for writable shares.
	proxyFD := vm.addFileDescriptor(fdFiles, driveConf.DevPath)
	return qemuDriveDir.Execute(sb, map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,

		"devName":  driveConf.DevName,
		"mountTag": mountTag,
		"proxyFD":  proxyFD,
		"readonly": false,
		"protocol": "9p",
	})
}

// addDriveConfig adds the qemu config required for adding a supplementary drive.
func (vm *qemu) addDriveConfig(sb *strings.Builder, bootIndexes map[string]int, driveConf deviceConfig.MountEntryItem) error {
	// Use native kernel async IO and O_DIRECT by default.
	aioMode := "native"
	cacheMode := "none" // Bypass host cache, use O_DIRECT semantics.

	// If drive config indicates we need to use unsafe I/O then use it.
	if shared.StringInSlice(qemuUnsafeIO, driveConf.Opts) {
		logger.Warnf("Using unsafe cache I/O with %s", driveConf.DevPath)
		aioMode = "threads"
		cacheMode = "unsafe" // Use host cache, but ignore all sync requests from guest.
	} else if shared.PathExists(driveConf.DevPath) && !shared.IsBlockdevPath(driveConf.DevPath) {
		// Disk dev path is a file, check whether it is located on a ZFS filesystem.
		fsType, err := util.FilesystemDetect(driveConf.DevPath)
		if err != nil {
			return errors.Wrapf(err, "Failed detecting filesystem type of %q", driveConf.DevPath)
		}

		// If backing FS is ZFS or BTRFS, avoid using direct I/O and use host page cache only.
		// We've seen ZFS hangs and BTRFS checksum issues when using direct I/O on image files.
		if fsType == "zfs" || fsType == "btrfs" {
			if driveConf.FSType != "iso9660" {
				// Only warn about using writeback cache if the drive image is writable.
				logger.Warnf("Using writeback cache I/O with %q as backing filesystem is %q", driveConf.DevPath, fsType)
			}

			aioMode = "threads"
			cacheMode = "writeback" // Use host cache, with neither O_DSYNC nor O_DIRECT semantics.
		}
	}

	if !strings.HasPrefix(driveConf.DevPath, "rbd:") {
		vm.devPaths = append(vm.devPaths, driveConf.DevPath)
	}

	return qemuDrive.Execute(sb, map[string]interface{}{
		"devName":   driveConf.DevName,
		"devPath":   driveConf.DevPath,
		"bootIndex": bootIndexes[driveConf.DevName],
		"cacheMode": cacheMode,
		"aioMode":   aioMode,
		"shared":    driveConf.TargetPath != "/" && !strings.HasPrefix(driveConf.DevPath, "rbd:"),
	})
}

// addNetDevConfig adds the qemu config required for adding a network device.
func (vm *qemu) addNetDevConfig(sb *strings.Builder, bus *qemuBus, bootIndexes map[string]int, nicConfig []deviceConfig.RunConfigItem, fdFiles *[]string) error {
	var devName, nicName, devHwaddr, pciSlotName string
	for _, nicItem := range nicConfig {
		if nicItem.Key == "devName" {
			devName = nicItem.Value
		} else if nicItem.Key == "link" {
			nicName = nicItem.Value
		} else if nicItem.Key == "hwaddr" {
			devHwaddr = nicItem.Value
		} else if nicItem.Key == "pciSlotName" {
			pciSlotName = nicItem.Value
		}
	}

	var tpl *template.Template
	tplFields := map[string]interface{}{
		"bus":       bus.name,
		"devName":   devName,
		"devHwaddr": devHwaddr,
		"bootIndex": bootIndexes[devName],
	}

	// Detect MACVTAP interface types and figure out which tap device is being used.
	// This is so we can open a file handle to the tap device and pass it to the qemu process.
	if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/macvtap", nicName)) {
		content, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/ifindex", nicName))
		if err != nil {
			return errors.Wrapf(err, "Error getting tap device ifindex")
		}

		ifindex, err := strconv.Atoi(strings.TrimSpace(string(content)))
		if err != nil {
			return errors.Wrapf(err, "Error parsing tap device ifindex")
		}

		// Append the tap device file path to the list of files to be opened and passed to qemu.
		tplFields["tapFD"] = vm.addFileDescriptor(fdFiles, fmt.Sprintf("/dev/tap%d", ifindex))
		tpl = qemuNetDevTapFD
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/tun_flags", nicName)) {
		// Detect TAP (via TUN driver) device.
		tplFields["ifName"] = nicName
		tpl = qemuNetDevTapTun
	} else if pciSlotName != "" {
		// Detect physical passthrough device.
		tplFields["pciSlotName"] = pciSlotName
		tpl = qemuNetDevPhysical
	}

	devBus, devAddr, multi := bus.allocate(busFunctionGroupNone)
	tplFields["devBus"] = devBus
	tplFields["devAddr"] = devAddr
	tplFields["multifunction"] = multi
	if tpl != nil {
		return tpl.Execute(sb, tplFields)
	}

	return fmt.Errorf("Unrecognised device type")
}

// addGPUDevConfig adds the qemu config required for adding a GPU device.
func (vm *qemu) addGPUDevConfig(sb *strings.Builder, bus *qemuBus, gpuConfig []deviceConfig.RunConfigItem) error {
	var devName, pciSlotName, vgpu string
	for _, gpuItem := range gpuConfig {
		if gpuItem.Key == "devName" {
			devName = gpuItem.Value
		} else if gpuItem.Key == "pciSlotName" {
			pciSlotName = gpuItem.Value
		} else if gpuItem.Key == "vgpu" {
			vgpu = gpuItem.Value
		}
	}

	// Pass-through VGA mode if enabled on the host device and architecture is x86_64.
	vgaMode := shared.PathExists(filepath.Join("/sys/bus/pci/devices", pciSlotName, "boot_vga")) && vm.architecture == osarch.ARCH_64BIT_INTEL_X86

	devBus, devAddr, multi := bus.allocate(fmt.Sprintf("lxd_%s", devName))
	tplFields := map[string]interface{}{
		"bus":           bus.name,
		"devBus":        devBus,
		"devAddr":       devAddr,
		"multifunction": multi,

		"devName":     devName,
		"pciSlotName": pciSlotName,
		"vga":         vgaMode,
		"vgpu":        vgpu,
	}

	// Add main GPU device in VGA mode to qemu config.
	err := qemuGPUDevPhysical.Execute(sb, tplFields)
	if err != nil {
		return err
	}

	var iommuGroupPath string

	if vgpu != "" {
		iommuGroupPath = filepath.Join("/sys/bus/mdev/devices", vgpu, "iommu_group", "devices")
	} else {
		// Add any other related IOMMU VFs as generic PCI devices.
		iommuGroupPath = filepath.Join("/sys/bus/pci/devices", pciSlotName, "iommu_group", "devices")
	}

	if shared.PathExists(iommuGroupPath) {
		// Extract parent slot name by removing any virtual function ID.
		parts := strings.SplitN(pciSlotName, ".", 2)
		prefix := parts[0]

		// Iterate the members of the IOMMU group and override any that match the parent slot name prefix.
		err := filepath.Walk(iommuGroupPath, func(path string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			iommuSlotName := filepath.Base(path) // Virtual function's address is dir name.

			// Match any VFs that are related to the GPU device (but not the GPU device itself).
			if strings.HasPrefix(iommuSlotName, prefix) && iommuSlotName != pciSlotName {
				// Add VF device without VGA mode to qemu config.
				devBus, devAddr, multi := bus.allocate(fmt.Sprintf("lxd_%s", devName))
				tplFields := map[string]interface{}{
					"bus":           bus.name,
					"devBus":        devBus,
					"devAddr":       devAddr,
					"multifunction": multi,

					// Generate associated device name by combining main device name and VF ID.
					"devName":     fmt.Sprintf("%s_%s", devName, devAddr),
					"pciSlotName": iommuSlotName,
					"vga":         false,
				}

				err := qemuGPUDevPhysical.Execute(sb, tplFields)
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (vm *qemu) addUSBDeviceConfig(sb *strings.Builder, bus *qemuBus, usbConfig []deviceConfig.RunConfigItem) error {
	var devName, hostDevice string

	for _, usbItem := range usbConfig {
		if usbItem.Key == "devName" {
			devName = usbItem.Value
		} else if usbItem.Key == "hostDevice" {
			hostDevice = usbItem.Value
		}
	}

	tplFields := map[string]interface{}{
		"hostDevice": hostDevice,
		"devName":    devName,
	}

	err := qemuUSBDev.Execute(sb, tplFields)
	if err != nil {
		return err
	}

	// Add path to devPaths. This way, the path will be included in the apparmor profile.
	vm.devPaths = append(vm.devPaths, hostDevice)

	return nil
}

// pidFilePath returns the path where the qemu process should write its PID.
func (vm *qemu) pidFilePath() string {
	return filepath.Join(vm.LogPath(), "qemu.pid")
}

// pid gets the PID of the running qemu process.
func (vm *qemu) pid() (int, error) {
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

// Stop the VM.
func (vm *qemu) Stop(stateful bool) error {
	// Check that we're not already stopped.
	if !vm.IsRunning() {
		return fmt.Errorf("The instance is already stopped")
	}

	// Check that no stateful stop was requested.
	if stateful {
		return fmt.Errorf("Stateful stop isn't supported for VMs at this time")
	}

	// Setup a new operation.
	op, err := operationlock.Create(vm.id, "stop", false, true)
	if err != nil {
		return err
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
	if err != nil {
		// If we fail to connect, it's most likely because the VM is already off.
		op.Done(nil)
		return nil
	}

	// Get the wait channel.
	chDisconnect, err := monitor.Wait()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			op.Done(nil)
			return nil
		}

		op.Done(err)
		return err
	}

	// Send the quit command.
	err = monitor.Quit()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			op.Done(nil)
			return nil
		}

		op.Done(err)
		return err
	}

	// Wait for QEMU to exit (can take a while if pending I/O).
	<-chDisconnect

	// Wait for onStop.
	err = op.Wait()
	if err != nil && vm.IsRunning() {
		return err
	}

	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-stopped", fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), nil)
	return nil
}

// Unfreeze restores the instance to running.
func (vm *qemu) Unfreeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
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
func (vm *qemu) IsPrivileged() bool {
	return false
}

// Restore restores an instance snapshot.
func (vm *qemu) Restore(source instance.Instance, stateful bool) error {
	if stateful {
		return fmt.Errorf("Stateful snapshots of VMs aren't supported yet")
	}

	var ctxMap log.Ctx

	// Stop the instance.
	wasRunning := false
	if vm.IsRunning() {
		wasRunning = true

		ephemeral := vm.IsEphemeral()
		if ephemeral {
			// Unset ephemeral flag.
			args := db.InstanceArgs{
				Architecture: vm.Architecture(),
				Config:       vm.LocalConfig(),
				Description:  vm.Description(),
				Devices:      vm.LocalDevices(),
				Ephemeral:    false,
				Profiles:     vm.Profiles(),
				Project:      vm.Project(),
				Type:         vm.Type(),
				Snapshot:     vm.IsSnapshot(),
			}

			err := vm.Update(args, false)
			if err != nil {
				return err
			}

			// On function return, set the flag back on.
			defer func() {
				args.Ephemeral = ephemeral
				vm.Update(args, false)
			}()
		}

		// This will unmount the instance storage.
		err := vm.Stop(false)
		if err != nil {
			return err
		}
	}

	ctxMap = log.Ctx{
		"project":   vm.project,
		"name":      vm.name,
		"created":   vm.creationDate,
		"ephemeral": vm.ephemeral,
		"used":      vm.lastUsedDate,
		"source":    source.Name()}

	logger.Info("Restoring instance", ctxMap)

	// Load the storage driver.
	pool, err := storagePools.GetPoolByInstance(vm.state, vm)
	if err != nil {
		return err
	}

	// Ensure that storage is mounted for backup.yaml updates.
	_, err = pool.MountInstance(vm, nil)
	if err != nil {
		return err
	}
	defer pool.UnmountInstance(vm, nil)

	// Restore the rootfs.
	err = pool.RestoreInstanceSnapshot(vm, source, nil)
	if err != nil {
		return err
	}

	// Restore the configuration.
	args := db.InstanceArgs{
		Architecture: source.Architecture(),
		Config:       source.LocalConfig(),
		Description:  source.Description(),
		Devices:      source.LocalDevices(),
		Ephemeral:    source.IsEphemeral(),
		Profiles:     source.Profiles(),
		Project:      source.Project(),
		Type:         source.Type(),
		Snapshot:     source.IsSnapshot(),
	}

	// Don't pass as user-requested as there's no way to fix a bad config.
	err = vm.Update(args, false)
	if err != nil {
		logger.Error("Failed restoring instance configuration", ctxMap)
		return err
	}

	// The old backup file may be out of date (e.g. it doesn't have all the current snapshots of
	// the instance listed); let's write a new one to be safe.
	err = vm.UpdateBackupFile()
	if err != nil {
		return err
	}

	vm.state.Events.SendLifecycle(vm.project, "virtual-machine-snapshot-restored", fmt.Sprintf("/1.0/virtual-machines/%s", vm.name), map[string]interface{}{"snapshot_name": vm.name})

	// Restart the insance.
	if wasRunning {
		logger.Info("Restored instance", ctxMap)
		return vm.Start(false)
	}

	logger.Info("Restored instance", ctxMap)
	return nil
}

// Snapshots returns a list of snapshots.
func (vm *qemu) Snapshots() ([]instance.Instance, error) {
	var snaps []db.Instance

	if vm.IsSnapshot() {
		return []instance.Instance{}, nil
	}

	// Get all the snapshots
	err := vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		snaps, err = tx.GetInstanceSnapshotsWithName(vm.Project(), vm.name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Build the snapshot list
	snapshots, err := instance.LoadAllInternal(vm.state, snaps)
	if err != nil {
		return nil, err
	}

	instances := make([]instance.Instance, len(snapshots))
	for k, v := range snapshots {
		instances[k] = instance.Instance(v)
	}

	return instances, nil
}

// Backups returns a list of backups.
func (vm *qemu) Backups() ([]backup.InstanceBackup, error) {
	return []backup.InstanceBackup{}, nil
}

// Rename the instance.
func (vm *qemu) Rename(newName string) error {
	oldName := vm.Name()
	ctxMap := log.Ctx{
		"project":   vm.project,
		"name":      vm.name,
		"created":   vm.creationDate,
		"ephemeral": vm.ephemeral,
		"used":      vm.lastUsedDate,
		"newname":   newName}

	logger.Info("Renaming instance", ctxMap)

	// Sanity checks.
	err := instance.ValidName(newName, vm.IsSnapshot())
	if err != nil {
		return err
	}

	if vm.IsRunning() {
		return fmt.Errorf("Renaming of running instance not allowed")
	}

	// Clean things up.
	vm.cleanup()

	pool, err := storagePools.GetPoolByInstance(vm.state, vm)
	if err != nil {
		return errors.Wrap(err, "Load instance storage pool")
	}

	if vm.IsSnapshot() {
		_, newSnapName, _ := shared.InstanceGetParentAndSnapshotName(newName)
		err = pool.RenameInstanceSnapshot(vm, newSnapName, nil)
		if err != nil {
			return errors.Wrap(err, "Rename instance snapshot")
		}
	} else {
		err = pool.RenameInstance(vm, newName, nil)
		if err != nil {
			return errors.Wrap(err, "Rename instance")
		}
	}

	if !vm.IsSnapshot() {
		// Rename all the instance snapshot database entries.
		results, err := vm.state.Cluster.GetInstanceSnapshotsNames(vm.project, oldName)
		if err != nil {
			logger.Error("Failed to get instance snapshots", ctxMap)
			return err
		}

		for _, sname := range results {
			// Rename the snapshot.
			oldSnapName := strings.SplitN(sname, shared.SnapshotDelimiter, 2)[1]
			baseSnapName := filepath.Base(sname)
			err := vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
				return tx.RenameInstanceSnapshot(vm.project, oldName, oldSnapName, baseSnapName)
			})
			if err != nil {
				logger.Error("Failed renaming snapshot", ctxMap)
				return err
			}
		}
	}

	// Rename the instance database entry.
	err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		if vm.IsSnapshot() {
			oldParts := strings.SplitN(oldName, shared.SnapshotDelimiter, 2)
			newParts := strings.SplitN(newName, shared.SnapshotDelimiter, 2)
			return tx.RenameInstanceSnapshot(vm.project, oldParts[0], oldParts[1], newParts[1])
		}

		return tx.RenameInstance(vm.project, oldName, newName)
	})
	if err != nil {
		logger.Error("Failed renaming instance", ctxMap)
		return err
	}

	// Rename the logging path.
	newFullName := project.Instance(vm.Project(), vm.Name())
	os.RemoveAll(shared.LogPath(newFullName))
	if shared.PathExists(vm.LogPath()) {
		err := os.Rename(vm.LogPath(), shared.LogPath(newFullName))
		if err != nil {
			logger.Error("Failed renaming instance", ctxMap)
			return err
		}
	}

	// Rename the MAAS entry.
	if !vm.IsSnapshot() {
		err = vm.maasRename(newName)
		if err != nil {
			return err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// Set the new name in the struct.
	vm.name = newName
	revert.Add(func() { vm.name = oldName })

	// Rename the backups.
	backups, err := vm.Backups()
	if err != nil {
		return err
	}

	for _, backup := range backups {
		b := backup
		oldName := b.Name()
		backupName := strings.Split(oldName, "/")[1]
		newName := fmt.Sprintf("%s/%s", newName, backupName)

		err = b.Rename(newName)
		if err != nil {
			return err
		}

		revert.Add(func() { b.Rename(oldName) })
	}

	// Update lease files.
	network.UpdateDNSMasqStatic(vm.state, "")

	err = vm.UpdateBackupFile()
	if err != nil {
		return err
	}

	logger.Info("Renamed instance", ctxMap)

	if vm.IsSnapshot() {
		vm.state.Events.SendLifecycle(vm.project, "virtual-machine-snapshot-renamed",
			fmt.Sprintf("/1.0/virtual-machines/%s", oldName), map[string]interface{}{
				"new_name":      newName,
				"snapshot_name": oldName,
			})
	} else {
		vm.state.Events.SendLifecycle(vm.project, "virtual-machine-renamed",
			fmt.Sprintf("/1.0/virtual-machines/%s", oldName), map[string]interface{}{
				"new_name": newName,
			})
	}

	revert.Success()
	return nil
}

// Update the instance config.
func (vm *qemu) Update(args db.InstanceArgs, userRequested bool) error {
	revert := revert.New()
	defer revert.Fail()

	// Set sane defaults for unset keys.
	if args.Project == "" {
		args.Project = project.Default
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

	if userRequested {
		// Validate the new config.
		err := instance.ValidConfig(vm.state.OS, args.Config, false, false)
		if err != nil {
			return errors.Wrap(err, "Invalid config")
		}

		// Validate the new devices without using expanded devices validation (expensive checks disabled).
		err = instance.ValidDevices(vm.state, vm.state.Cluster, vm.Project(), vm.Type(), args.Devices, false)
		if err != nil {
			return errors.Wrap(err, "Invalid devices")
		}
	}

	// Validate the new profiles.
	profiles, err := vm.state.Cluster.GetProfileNames(args.Project)
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

	// Revert local changes if update fails.
	revert.Add(func() {
		vm.description = oldDescription
		vm.architecture = oldArchitecture
		vm.ephemeral = oldEphemeral
		vm.expandedConfig = oldExpandedConfig
		vm.expandedDevices = oldExpandedDevices
		vm.localConfig = oldLocalConfig
		vm.localDevices = oldLocalDevices
		vm.profiles = oldProfiles
		vm.expiryDate = oldExpiryDate
	})

	// Apply the various changes to local vars.
	vm.description = args.Description
	vm.architecture = args.Architecture
	vm.ephemeral = args.Ephemeral
	vm.localConfig = args.Config
	vm.localDevices = args.Devices
	vm.profiles = args.Profiles
	vm.expiryDate = args.ExpiryDate

	// Expand the config.
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
	removeDevices, addDevices, updateDevices, allUpdatedKeys := oldExpandedDevices.Update(vm.expandedDevices, func(oldDevice deviceConfig.Device, newDevice deviceConfig.Device) []string {
		// This function needs to return a list of fields that are excluded from differences
		// between oldDevice and newDevice. The result of this is that as long as the
		// devices are otherwise identical except for the fields returned here, then the
		// device is considered to be being "updated" rather than "added & removed".
		oldNICType, err := nictype.NICType(vm.state, newDevice)
		if err != nil {
			return []string{} // Cannot hot-update due to config error.
		}

		newNICType, err := nictype.NICType(vm.state, oldDevice)
		if err != nil {
			return []string{} // Cannot hot-update due to config error.
		}

		if oldDevice["type"] != newDevice["type"] || oldNICType != newNICType {
			return []string{} // Device types aren't the same, so this cannot be an update.
		}

		d, err := device.New(vm, vm.state, "", newDevice, nil, nil)
		if err != nil {
			return []string{} // Couldn't create Device, so this cannot be an update.
		}

		return d.UpdatableFields()
	})

	if userRequested {
		// Do some validation of the config diff.
		err = instance.ValidConfig(vm.state.OS, vm.expandedConfig, false, true)
		if err != nil {
			return errors.Wrap(err, "Invalid expanded config")
		}

		// Do full expanded validation of the devices diff.
		err = instance.ValidDevices(vm.state, vm.state.Cluster, vm.Project(), vm.Type(), vm.expandedDevices, true)
		if err != nil {
			return errors.Wrap(err, "Invalid expanded devices")
		}
	}

	isRunning := vm.IsRunning()

	// Use the device interface to apply update changes.
	err = vm.updateDevices(removeDevices, addDevices, updateDevices, oldExpandedDevices, isRunning, userRequested)
	if err != nil {
		return err
	}

	if isRunning {
		// Only certain keys can be changed on a running VM.
		liveUpdateKeys := []string{"limits.memory"}

		// Check only keys that support live update have changed.
		for _, key := range changedConfig {
			if !strings.HasPrefix(key, "user.") && !shared.StringInSlice(key, liveUpdateKeys) {
				return fmt.Errorf("Key %q cannot be updated when VM is running", key)
			}
		}

		// Apply live update for each key.
		for _, key := range changedConfig {
			value := vm.expandedConfig[key]

			if key == "limits.memory" {
				err = vm.updateMemoryLimit(value)
				if err != nil {
					if err != nil {
						return errors.Wrapf(err, "Failed updating memory limit")
					}
				}
			}
		}
	}

	// Update MAAS (must run after the MAC addresses have been generated).
	updateMAAS := false
	for _, key := range []string{"maas.subnet.ipv4", "maas.subnet.ipv6", "ipv4.address", "ipv6.address"} {
		if shared.StringInSlice(key, allUpdatedKeys) {
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
	err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Snapshots should update only their descriptions and expiry date.
		if vm.IsSnapshot() {
			return tx.UpdateInstanceSnapshot(vm.id, vm.description, vm.expiryDate)
		}

		object, err := tx.GetInstance(vm.project, vm.name)
		if err != nil {
			return err
		}

		object.Description = vm.description
		object.Architecture = vm.architecture
		object.Ephemeral = vm.ephemeral
		object.ExpiryDate = vm.expiryDate
		object.Config = vm.localConfig
		object.Profiles = vm.profiles
		object.Devices = vm.localDevices.CloneNative()

		return tx.UpdateInstance(vm.project, vm.name, *object)
	})
	if err != nil {
		return errors.Wrap(err, "Failed to update database")
	}

	err = vm.UpdateBackupFile()
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "Failed to write backup file")
	}

	// Changes have been applied and recorded, do not revert if an error occurs from here.
	revert.Success()

	if isRunning {
		err = vm.writeInstanceData()
		if err != nil {
			return errors.Wrap(err, "Failed to write instance-data file")
		}

		// Send devlxd notifications only for user.* key changes
		for _, key := range changedConfig {
			if !strings.HasPrefix(key, "user.") {
				continue
			}

			msg := map[string]string{
				"key":       key,
				"old_value": oldExpandedConfig[key],
				"value":     vm.expandedConfig[key],
			}

			err = vm.devlxdEventSend("config", msg)
			if err != nil {
				return err
			}
		}
	}

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

// updateMemoryLimit live updates the VM's memory limit by reszing the balloon device.
func (vm *qemu) updateMemoryLimit(newLimit string) error {
	if newLimit == "" {
		return nil
	}

	if shared.IsTrue(vm.expandedConfig["limits.memory.hugepages"]) {
		return fmt.Errorf("Cannot live update memory limit when using huge pages")
	}

	// Check new size string is valid and convert to bytes.
	newSizeBytes, err := units.ParseByteSizeString(newLimit)
	if err != nil {
		return errors.Wrapf(err, "Invalid memory size")
	}
	newSizeMB := newSizeBytes / 1024 / 1024

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
	if err != nil {
		return err // The VM isn't running as no monitor socket available.
	}

	baseSizeBytes, err := monitor.GetMemorySizeBytes()
	if err != nil {
		return err
	}
	baseSizeMB := baseSizeBytes / 1024 / 1024

	curSizeBytes, err := monitor.GetMemoryBalloonSizeBytes()
	if err != nil {
		return err
	}
	curSizeMB := curSizeBytes / 1024 / 1024

	if curSizeMB == newSizeMB {
		return nil
	} else if baseSizeMB < newSizeMB {
		return fmt.Errorf("Cannot increase memory size beyond boot time size when VM is running (Boot time size %dMiB, new size %dMiB)", baseSizeMB, newSizeMB)
	}

	// Set effective memory size.
	err = monitor.SetMemoryBalloonSizeBytes(newSizeBytes)
	if err != nil {
		return err
	}

	// Changing the memory balloon can take time, so poll the effectice size to check it has shrunk within 1%
	// of the target size, which we then take as success (it may still continue to shrink closer to target).
	for i := 0; i < 10; i++ {
		curSizeBytes, err = monitor.GetMemoryBalloonSizeBytes()
		if err != nil {
			return err
		}
		curSizeMB = curSizeBytes / 1024 / 1024

		var diff int64
		if curSizeMB < newSizeMB {
			diff = newSizeMB - curSizeMB
		} else {
			diff = curSizeMB - newSizeMB
		}

		if diff <= (newSizeMB / 100) {
			return nil // We reached to within 1% of our target size.
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("Failed setting memory to %dMiB (currently %dMiB) as it was taking too long", newSizeMB, curSizeMB)
}

func (vm *qemu) updateDevices(removeDevices deviceConfig.Devices, addDevices deviceConfig.Devices, updateDevices deviceConfig.Devices, oldExpandedDevices deviceConfig.Devices, instanceRunning bool, userRequested bool) error {
	// Remove devices in reverse order to how they were added.
	for _, dev := range removeDevices.Reversed() {
		if instanceRunning {
			err := vm.deviceStop(dev.Name, dev.Config, instanceRunning)
			if err == device.ErrUnsupportedDevType {
				continue // No point in trying to remove device below.
			} else if err != nil {
				return errors.Wrapf(err, "Failed to stop device %q", dev.Name)
			}
		}

		err := vm.deviceRemove(dev.Name, dev.Config, instanceRunning)
		if err != nil && err != device.ErrUnsupportedDevType {
			return errors.Wrapf(err, "Failed to remove device %q", dev.Name)
		}

		// Check whether we are about to add the same device back with updated config and
		// if not, or if the device type has changed, then remove all volatile keys for
		// this device (as its an actual removal or a device type change).
		err = vm.deviceResetVolatile(dev.Name, dev.Config, addDevices[dev.Name])
		if err != nil {
			return errors.Wrapf(err, "Failed to reset volatile data for device %q", dev.Name)
		}
	}

	// Add devices in sorted order, this ensures that device mounts are added in path order.
	for _, dev := range addDevices.Sorted() {
		err := vm.deviceAdd(dev.Name, dev.Config, instanceRunning)
		if err == device.ErrUnsupportedDevType {
			continue // No point in trying to start device below.
		} else if err != nil {
			if userRequested {
				return errors.Wrapf(err, "Failed to add device %q", dev.Name)
			}

			// If update is non-user requested (i.e from a snapshot restore), there's nothing we can
			// do to fix the config and we don't want to prevent the snapshot restore so log and allow.
			logger.Error("Failed to add device, skipping as non-user requested", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "device": dev.Name, "err": err})
			continue
		}

		if instanceRunning {
			_, err := vm.deviceStart(dev.Name, dev.Config, instanceRunning)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to start device %q", dev.Name)
			}
		}
	}

	for _, dev := range updateDevices.Sorted() {
		err := vm.deviceUpdate(dev.Name, dev.Config, oldExpandedDevices, instanceRunning)
		if err != nil && err != device.ErrUnsupportedDevType {
			return errors.Wrapf(err, "Failed to update device %q", dev.Name)
		}
	}

	return nil
}

// deviceUpdate loads a new device and calls its Update() function.
func (vm *qemu) deviceUpdate(deviceName string, rawConfig deviceConfig.Device, oldDevices deviceConfig.Devices, instanceRunning bool) error {
	d, _, err := vm.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return err
	}

	err = d.Update(oldDevices, instanceRunning)
	if err != nil {
		return err
	}

	return nil
}

// deviceResetVolatile resets a device's volatile data when its removed or updated in such a way
// that it is removed then added immediately afterwards.
func (vm *qemu) deviceResetVolatile(devName string, oldConfig, newConfig deviceConfig.Device) error {
	volatileClear := make(map[string]string)
	devicePrefix := fmt.Sprintf("volatile.%s.", devName)

	newNICType, err := nictype.NICType(vm.state, newConfig)
	if err != nil {
		return err
	}

	oldNICType, err := nictype.NICType(vm.state, oldConfig)
	if err != nil {
		return err
	}

	// If the device type has changed, remove all old volatile keys.
	// This will occur if the newConfig is empty (i.e the device is actually being removed) or
	// if the device type is being changed but keeping the same name.
	if newConfig["type"] != oldConfig["type"] || newNICType != oldNICType {
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

func (vm *qemu) removeUnixDevices() error {
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

func (vm *qemu) removeDiskDevices() error {
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

func (vm *qemu) cleanup() {
	// Unmount any leftovers
	vm.removeUnixDevices()
	vm.removeDiskDevices()

	// Remove the security profiles
	apparmor.InstanceDelete(vm.state, vm)

	// Remove the devices path
	os.Remove(vm.DevicesPath())

	// Remove the shmounts path
	os.RemoveAll(vm.ShmountsPath())
}

// cleanupDevices performs any needed device cleanup steps when instance is stopped.
func (vm *qemu) cleanupDevices() {
	for _, dev := range vm.expandedDevices.Reversed() {
		// Use the device interface if device supports it.
		err := vm.deviceStop(dev.Name, dev.Config, false)
		if err == device.ErrUnsupportedDevType {
			continue
		} else if err != nil {
			logger.Errorf("Failed to stop device '%s': %v", dev.Name, err)
		}
	}
}

func (vm *qemu) init() error {
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
func (vm *qemu) Delete() error {
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
		return err
	} else if pool != nil {
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
			err = vm.deviceRemove(k, m, false)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to remove device %q", k)
			}
		}

		// Clean things up.
		vm.cleanup()
	}

	// Remove the database record of the instance or snapshot instance.
	if err := vm.state.Cluster.DeleteInstance(vm.Project(), vm.Name()); err != nil {
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

func (vm *qemu) deviceAdd(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) error {
	d, _, err := vm.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return err
	}

	if instanceRunning && !d.CanHotPlug() {
		return fmt.Errorf("Device cannot be added when instance is running")
	}

	return d.Add()
}

func (vm *qemu) deviceRemove(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"device": deviceName, "type": rawConfig["type"], "project": vm.Project(), "instance": vm.Name()})

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
			return fmt.Errorf("Device remove validation failed for %q: %v", deviceName, err)
		}

		logger.Error("Device remove validation failed", log.Ctx{"err": err})
	}

	if instanceRunning && !d.CanHotPlug() {
		return fmt.Errorf("Device cannot be removed when instance is running")
	}

	return d.Remove()
}

// Export publishes the instance.
func (vm *qemu) Export(w io.Writer, properties map[string]string) (api.ImageMetadata, error) {
	ctxMap := log.Ctx{
		"project":   vm.project,
		"name":      vm.name,
		"created":   vm.creationDate,
		"ephemeral": vm.ephemeral,
		"used":      vm.lastUsedDate}

	meta := api.ImageMetadata{}

	if vm.IsRunning() {
		return meta, fmt.Errorf("Cannot export a running instance as an image")
	}

	logger.Info("Exporting instance", ctxMap)

	// Start the storage.
	mountInfo, err := vm.mount()
	if err != nil {
		logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}
	defer vm.unmount()

	// Create the tarball.
	tarWriter := instancewriter.NewInstanceTarWriter(w, nil)

	// Path inside the tar image is the pathname starting after cDir.
	cDir := vm.Path()
	offset := len(cDir) + 1

	writeToTar := func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		err = tarWriter.WriteFile(path[offset:], path, fi, false)
		if err != nil {
			logger.Debugf("Error tarring up %s: %s", path, err)
			return err
		}

		return nil
	}

	// Look for metadata.yaml.
	fnam := filepath.Join(cDir, "metadata.yaml")
	if !shared.PathExists(fnam) {
		// Generate a new metadata.yaml.
		tempDir, err := ioutil.TempDir("", "lxd_lxd_metadata_")
		if err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
		defer os.RemoveAll(tempDir)

		// Get the instance's architecture.
		var arch string
		if vm.IsSnapshot() {
			parentName, _, _ := shared.InstanceGetParentAndSnapshotName(vm.name)
			parent, err := instance.LoadByProjectAndName(vm.state, vm.project, parentName)
			if err != nil {
				tarWriter.Close()
				logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}

			arch, _ = osarch.ArchitectureName(parent.Architecture())
		} else {
			arch, _ = osarch.ArchitectureName(vm.architecture)
		}

		if arch == "" {
			arch, err = osarch.ArchitectureName(vm.state.OS.Architectures[0])
			if err != nil {
				logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}
		}

		// Fill in the metadata.
		meta.Architecture = arch
		meta.CreationDate = time.Now().UTC().Unix()
		meta.Properties = properties

		data, err := yaml.Marshal(&meta)
		if err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		// Write the actual file.
		fnam = filepath.Join(tempDir, "metadata.yaml")
		err = ioutil.WriteFile(fnam, data, 0644)
		if err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		fi, err := os.Lstat(fnam)
		if err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		tmpOffset := len(filepath.Dir(fnam)) + 1
		if err := tarWriter.WriteFile(fnam[tmpOffset:], fnam, fi, false); err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
	} else {
		// Parse the metadata.
		content, err := ioutil.ReadFile(fnam)
		if err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		err = yaml.Unmarshal(content, &meta)
		if err != nil {
			tarWriter.Close()
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		if properties != nil {
			meta.Properties = properties

			// Generate a new metadata.yaml.
			tempDir, err := ioutil.TempDir("", "lxd_lxd_metadata_")
			if err != nil {
				tarWriter.Close()
				logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}
			defer os.RemoveAll(tempDir)

			data, err := yaml.Marshal(&meta)
			if err != nil {
				tarWriter.Close()
				logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}

			// Write the actual file.
			fnam = filepath.Join(tempDir, "metadata.yaml")
			err = ioutil.WriteFile(fnam, data, 0644)
			if err != nil {
				tarWriter.Close()
				logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}
		}

		// Include metadata.yaml in the tarball.
		fi, err := os.Lstat(fnam)
		if err != nil {
			tarWriter.Close()
			logger.Debugf("Error statting %s during export", fnam)
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		if properties != nil {
			tmpOffset := len(filepath.Dir(fnam)) + 1
			err = tarWriter.WriteFile(fnam[tmpOffset:], fnam, fi, false)
		} else {
			err = tarWriter.WriteFile(fnam[offset:], fnam, fi, false)
		}
		if err != nil {
			tarWriter.Close()
			logger.Debugf("Error writing to tarfile: %s", err)
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
	}

	// Convert from raw to qcow2 and add to tarball.
	tmpPath, err := ioutil.TempDir(shared.VarPath("images"), "lxd_export_")
	if err != nil {
		return meta, err
	}
	defer os.RemoveAll(tmpPath)

	if mountInfo.DiskPath == "" {
		return meta, fmt.Errorf("No disk path available from mount")
	}

	fPath := fmt.Sprintf("%s/rootfs.img", tmpPath)
	_, err = shared.RunCommand("qemu-img", "convert", "-c", "-O", "qcow2", mountInfo.DiskPath, fPath)
	if err != nil {
		return meta, fmt.Errorf("Failed converting image to qcow2: %v", err)
	}

	fi, err := os.Lstat(fPath)
	if err != nil {
		return meta, err
	}

	imgOffset := len(tmpPath) + 1
	err = tarWriter.WriteFile(fPath[imgOffset:], fPath, fi, false)
	if err != nil {
		return meta, err
	}

	// Include all the templates.
	fnam = vm.TemplatesPath()
	if shared.PathExists(fnam) {
		err = filepath.Walk(fnam, writeToTar)
		if err != nil {
			logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
	}

	err = tarWriter.Close()
	if err != nil {
		logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}

	logger.Info("Exported instance", ctxMap)
	return meta, nil
}

// Migrate migrates the instance to another node.
func (vm *qemu) Migrate(args *instance.CriuMigrationArgs) error {
	return instance.ErrNotImplemented
}

// CGroupSet is not implemented for VMs.
func (vm *qemu) CGroup() (*cgroup.CGroup, error) {
	return nil, instance.ErrNotImplemented
}

// VolatileSet sets one or more volatile config keys.
func (vm *qemu) VolatileSet(changes map[string]string) error {
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
			return tx.UpdateInstanceSnapshotConfig(vm.id, changes)
		})
	} else {
		err = vm.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.UpdateInstanceConfig(vm.id, changes)
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
func (vm *qemu) FileExists(path string) error {
	return instance.ErrNotImplemented
}

// FilePull retrieves a file from the instance.
func (vm *qemu) FilePull(srcPath string, dstPath string) (int64, int64, os.FileMode, string, []string, error) {
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
func (vm *qemu) FilePush(fileType string, srcPath string, dstPath string, uid int64, gid int64, mode int, write string) error {
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
func (vm *qemu) FileRemove(path string) error {
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
func (vm *qemu) Console(protocol string) (*os.File, chan error, error) {
	switch protocol {
	case instance.ConsoleTypeConsole:
		return vm.console()
	case instance.ConsoleTypeVGA:
		return vm.vga()
	default:
		return nil, nil, fmt.Errorf("Unknown protocol %q", protocol)
	}
}

func (vm *qemu) console() (*os.File, chan error, error) {
	chDisconnect := make(chan error, 1)

	// Avoid duplicate connects.
	vmConsoleLock.Lock()
	if vmConsole[vm.id] {
		vmConsoleLock.Unlock()
		return nil, nil, fmt.Errorf("There is already an active console for this instance")
	}
	vmConsoleLock.Unlock()

	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
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
		delete(vmConsole, vm.id)
		vmConsoleLock.Unlock()
	}()

	return console, chDisconnect, nil
}

func (vm *qemu) vga() (*os.File, chan error, error) {
	// Open the spice socket
	conn, err := net.Dial("unix", vm.spicePath())
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Connect to SPICE socket %q", vm.spicePath())
	}

	file, err := (conn.(*net.UnixConn)).File()
	if err != nil {
		return nil, nil, errors.Wrap(err, "Get socket file")
	}
	conn.Close()

	return file, nil, nil
}

// Exec a command inside the instance.
func (vm *qemu) Exec(req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (instance.Cmd, error) {
	revert := revert.New()
	defer revert.Fail()

	client, err := vm.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", vm.Name(), err)
		return nil, fmt.Errorf("Failed to connect to lxd-agent")
	}
	revert.Add(agent.Disconnect)

	req.WaitForWS = true
	if req.Interactive {
		// Set console to raw.
		oldttystate, err := termios.MakeRaw(int(stdin.Fd()))
		if err != nil {
			return nil, err
		}

		revert.Add(func() { termios.Restore(int(stdin.Fd()), oldttystate) })
	}

	dataDone := make(chan bool)
	controlSendCh := make(chan api.InstanceExecControl)
	controlResCh := make(chan error)

	// This is the signal control handler, it receives signals from lxc CLI and forwards them to the VM agent.
	controlHandler := func(control *websocket.Conn) {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		defer control.WriteMessage(websocket.CloseMessage, closeMsg)

		for {
			select {
			case cmd := <-controlSendCh:
				controlResCh <- control.WriteJSON(cmd)
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
		Control:  controlHandler,
	}

	op, err := agent.ExecInstance("", req, &args)
	if err != nil {
		return nil, err
	}

	instCmd := &qemuCmd{
		cmd:              op,
		attachedChildPid: 0, // Process is not running on LXD host.
		dataDone:         args.DataDone,
		cleanupFunc:      revert.Clone().Fail, // Pass revert function clone as clean up function.
		controlSendCh:    controlSendCh,
		controlResCh:     controlResCh,
	}

	revert.Success()
	return instCmd, nil
}

// Render returns info about the instance.
func (vm *qemu) Render(options ...func(response interface{}) error) (interface{}, interface{}, error) {
	if vm.IsSnapshot() {
		// Prepare the ETag
		etag := []interface{}{vm.expiryDate}

		snapState := api.InstanceSnapshot{
			CreatedAt:       vm.creationDate,
			ExpandedConfig:  vm.expandedConfig,
			ExpandedDevices: vm.expandedDevices.CloneNative(),
			LastUsedAt:      vm.lastUsedDate,
			Name:            strings.SplitN(vm.name, "/", 2)[1],
			Stateful:        vm.stateful,
			Size:            -1, // Default to uninitialised/error state (0 means no CoW usage).
		}
		snapState.Architecture = vm.architectureName
		snapState.Config = vm.localConfig
		snapState.Devices = vm.localDevices.CloneNative()
		snapState.Ephemeral = vm.ephemeral
		snapState.Profiles = vm.profiles
		snapState.ExpiresAt = vm.expiryDate

		for _, option := range options {
			err := option(&snapState)
			if err != nil {
				return nil, nil, err
			}
		}

		return &snapState, etag, nil
	}

	// Prepare the ETag
	etag := []interface{}{vm.architecture, vm.localConfig, vm.localDevices, vm.ephemeral, vm.profiles}

	instState := api.Instance{
		ExpandedConfig:  vm.expandedConfig,
		ExpandedDevices: vm.expandedDevices.CloneNative(),
		Name:            vm.name,
		Status:          vm.statusCode().String(),
		StatusCode:      vm.statusCode(),
		Location:        vm.node,
		Type:            vm.Type().String(),
	}

	instState.Description = vm.description
	instState.Architecture = vm.architectureName
	instState.Config = vm.localConfig
	instState.CreatedAt = vm.creationDate
	instState.Devices = vm.localDevices.CloneNative()
	instState.Ephemeral = vm.ephemeral
	instState.LastUsedAt = vm.lastUsedDate
	instState.Profiles = vm.profiles
	instState.Stateful = vm.stateful

	for _, option := range options {
		err := option(&instState)
		if err != nil {
			return nil, nil, err
		}
	}

	return &instState, etag, nil
}

// RenderFull returns all info about the instance.
func (vm *qemu) RenderFull() (*api.InstanceFull, interface{}, error) {
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
func (vm *qemu) RenderState() (*api.InstanceState, error) {
	statusCode := vm.statusCode()
	pid, _ := vm.pid()

	if statusCode == api.Running {
		// Try and get state info from agent.
		status, err := vm.agentGetState()
		if err != nil {
			if err != errQemuAgentOffline {
				logger.Warn("Could not get VM state from agent", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
			}

			// Fallback data if agent is not reachable.
			status = &api.InstanceState{}
			status.Processes = -1
			networks := map[string]api.InstanceStateNetwork{}
			for k, m := range vm.ExpandedDevices() {
				if m["type"] != "nic" {
					continue
				}

				d, _, err := vm.deviceLoad(k, m)
				if err != nil {
					logger.Warn("Could not load device", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "device": k, "err": err})
					continue
				}

				// Only some NIC types support fallback state mechanisms when there is no agent.
				nic, ok := d.(device.NICState)
				if !ok {
					continue
				}

				network, err := nic.State()
				if err != nil {
					return nil, errors.Wrapf(err, "Failed getting NIC state for %q", k)
				}

				if network != nil {
					networks[k] = *network
				}
			}

			status.Network = networks
		}

		// Populate host_name for network devices.
		for k, m := range vm.ExpandedDevices() {
			// We only care about nics.
			if m["type"] != "nic" {
				continue
			}

			// Get hwaddr from static or volatile config.
			hwaddr := m["hwaddr"]
			if hwaddr == "" {
				hwaddr = vm.localConfig[fmt.Sprintf("volatile.%s.hwaddr", k)]
			}

			// We have to match on hwaddr as device name can be different from the configured device
			// name when reported from the lxd-agent inside the VM (due to the guest OS choosing name).
			for netName, netStatus := range status.Network {
				if netStatus.Hwaddr == hwaddr {
					if netStatus.HostName == "" {
						netStatus.HostName = vm.localConfig[fmt.Sprintf("volatile.%s.host_name", k)]
						status.Network[netName] = netStatus
					}
				}
			}
		}

		status.Pid = int64(pid)
		status.Status = statusCode.String()
		status.StatusCode = statusCode
		status.Disk, err = vm.diskState()
		if err != nil && err != storageDrivers.ErrNotSupported {
			logger.Warn("Error getting disk usage", log.Ctx{"project": vm.Project(), "instance": vm.Name(), "err": err})
		}

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

// diskState gets disk usage info.
func (vm *qemu) diskState() (map[string]api.InstanceStateDisk, error) {
	pool, err := vm.getStoragePool()
	if err != nil {
		return nil, err
	}

	// Get the root disk device config.
	rootDiskName, _, err := shared.GetRootDiskDevice(vm.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	usage, err := pool.GetInstanceUsage(vm)
	if err != nil {
		return nil, err
	}

	disk := map[string]api.InstanceStateDisk{}
	disk[rootDiskName] = api.InstanceStateDisk{Usage: usage}
	return disk, nil
}

// agentGetState connects to the agent inside of the VM and does
// an API call to get the current state.
func (vm *qemu) agentGetState() (*api.InstanceState, error) {
	// Check if the agent is running.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
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
func (vm *qemu) IsRunning() bool {
	state := vm.State()
	return state != "STOPPED"
}

// IsFrozen returns whether the instance frozen or not.
func (vm *qemu) IsFrozen() bool {
	return vm.State() == "FROZEN"
}

// IsEphemeral returns whether the instanc is ephemeral or not.
func (vm *qemu) IsEphemeral() bool {
	return vm.ephemeral
}

// IsSnapshot returns whether instance is snapshot or not.
func (vm *qemu) IsSnapshot() bool {
	return vm.snapshot
}

// IsStateful retuens whether instance is stateful or not.
func (vm *qemu) IsStateful() bool {
	return vm.stateful
}

// DeviceEventHandler handles events occurring on the instance's devices.
func (vm *qemu) DeviceEventHandler(runConf *deviceConfig.RunConfig) error {
	return fmt.Errorf("DeviceEventHandler Not implemented")
}

// ID returns the instance's ID.
func (vm *qemu) ID() int {
	return vm.id
}

// vsockID returns the vsock context ID, 3 being the first ID that can be used.
func (vm *qemu) vsockID() int {
	return vm.id + 3
}

// Location returns instance's location.
func (vm *qemu) Location() string {
	return vm.node
}

// Name returns the instance's name.
func (vm *qemu) Name() string {
	return vm.name
}

// Description returns the instance's description.
func (vm *qemu) Description() string {
	return vm.description
}

// CreationDate returns the instance's creation date.
func (vm *qemu) CreationDate() time.Time {
	return vm.creationDate
}

// LastUsedDate returns the instance's last used date.
func (vm *qemu) LastUsedDate() time.Time {
	return vm.lastUsedDate
}

// Profiles returns the instance's profiles.
func (vm *qemu) Profiles() []string {
	return vm.profiles
}

// InitPID returns the instance's current process ID.
func (vm *qemu) InitPID() int {
	pid, _ := vm.pid()
	return pid
}

func (vm *qemu) statusCode() api.StatusCode {
	// Connect to the monitor.
	monitor, err := qmp.Connect(vm.monitorPath(), qemuSerialChardevName, vm.getMonitorEventHandler())
	if err != nil {
		// If cannot connect to monitor, but qemu process in pid file still exists, then likely qemu
		// has crashed/hung and this instance is in an error state.
		pid, _ := vm.pid()
		if pid > 0 && shared.PathExists(fmt.Sprintf("/proc/%d", pid)) {
			return api.Error
		}

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
func (vm *qemu) State() string {
	return strings.ToUpper(vm.statusCode().String())
}

// ExpiryDate returns when this snapshot expires.
func (vm *qemu) ExpiryDate() time.Time {
	if vm.IsSnapshot() {
		return vm.expiryDate
	}

	// Return zero time if the instance is not a snapshot.
	return time.Time{}
}

// Path returns the instance's path.
func (vm *qemu) Path() string {
	return storagePools.InstancePath(vm.Type(), vm.Project(), vm.Name(), vm.IsSnapshot())
}

// DevicesPath returns the instance's devices path.
func (vm *qemu) DevicesPath() string {
	name := project.Instance(vm.Project(), vm.Name())
	return shared.VarPath("devices", name)
}

// ShmountsPath returns the instance's shared mounts path.
func (vm *qemu) ShmountsPath() string {
	name := project.Instance(vm.Project(), vm.Name())
	return shared.VarPath("shmounts", name)
}

// LogPath returns the instance's log path.
func (vm *qemu) LogPath() string {
	name := project.Instance(vm.Project(), vm.Name())
	return shared.LogPath(name)
}

// EarlyLogFilePath returns the instance's early log path.
func (vm *qemu) EarlyLogFilePath() string {
	return filepath.Join(vm.LogPath(), "qemu.early.log")
}

// LogFilePath returns the instance's log path.
func (vm *qemu) LogFilePath() string {
	return filepath.Join(vm.LogPath(), "qemu.log")
}

// ConsoleBufferLogPath returns the instance's console buffer log path.
func (vm *qemu) ConsoleBufferLogPath() string {
	return filepath.Join(vm.LogPath(), "console.log")
}

// RootfsPath returns the instance's rootfs path.
func (vm *qemu) RootfsPath() string {
	return filepath.Join(vm.Path(), "rootfs")
}

// TemplatesPath returns the instance's templates path.
func (vm *qemu) TemplatesPath() string {
	return filepath.Join(vm.Path(), "templates")
}

// StatePath returns the instance's state path.
func (vm *qemu) StatePath() string {
	return filepath.Join(vm.Path(), "state")
}

// StoragePool returns the name of the instance's storage pool.
func (vm *qemu) StoragePool() (string, error) {
	poolName, err := vm.state.Cluster.GetInstancePool(vm.Project(), vm.Name())
	if err != nil {
		return "", err
	}

	return poolName, nil
}

// SetOperation sets the current operation.
func (vm *qemu) SetOperation(op *operations.Operation) {
	vm.op = op
}

// DeferTemplateApply not used currently.
func (vm *qemu) DeferTemplateApply(trigger string) error {
	err := vm.VolatileSet(map[string]string{"volatile.apply_template": trigger})
	if err != nil {
		return errors.Wrap(err, "Failed to set apply_template volatile key")
	}

	return nil
}

// FillNetworkDevice takes a nic or infiniband device type and enriches it with automatically
// generated name and hwaddr properties if these are missing from the device.
func (vm *qemu) FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
	var err error

	newDevice := m.Clone()
	updateKey := func(key string, value string) error {
		tx, err := vm.state.Cluster.Begin()
		if err != nil {
			return err
		}

		err = db.CreateInstanceConfig(tx, vm.id, map[string]string{key: value})
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

	nicType, err := nictype.NICType(vm.state, m)
	if err != nil {
		return nil, err
	}

	// Fill in the MAC address
	if !shared.StringInSlice(nicType, []string{"physical", "ipvlan", "sriov"}) && m["hwaddr"] == "" {
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
					value, err1 := vm.state.Cluster.GetInstanceConfig(vm.id, configKey)
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
func (vm *qemu) maasInterfaces(devices map[string]map[string]string) ([]maas.ContainerInterface, error) {
	interfaces := []maas.ContainerInterface{}
	for k, m := range devices {
		if m["type"] != "nic" {
			continue
		}

		if m["maas.subnet.ipv4"] == "" && m["maas.subnet.ipv6"] == "" {
			continue
		}

		m, err := vm.FillNetworkDevice(k, m)
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

func (vm *qemu) maasRename(newName string) error {
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

	exists, err := vm.state.MAAS.DefinedContainer(vm)
	if err != nil {
		return err
	}

	if !exists {
		return vm.maasUpdate(nil)
	}

	return vm.state.MAAS.RenameContainer(vm, newName)
}

func (vm *qemu) maasDelete() error {
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

	exists, err := vm.state.MAAS.DefinedContainer(vm)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	return vm.state.MAAS.DeleteContainer(vm)
}

func (vm *qemu) maasUpdate(oldDevices map[string]map[string]string) error {
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

	exists, err := vm.state.MAAS.DefinedContainer(vm)
	if err != nil {
		return err
	}

	if exists {
		if len(interfaces) == 0 && len(oldInterfaces) > 0 {
			return vm.state.MAAS.DeleteContainer(vm)
		}

		return vm.state.MAAS.UpdateContainer(vm, interfaces)
	}

	return vm.state.MAAS.CreateContainer(vm, interfaces)
}

// UpdateBackupFile writes the instance's backup.yaml file to storage.
func (vm *qemu) UpdateBackupFile() error {
	pool, err := vm.getStoragePool()
	if err != nil {
		return err
	}

	return pool.UpdateInstanceBackupFile(vm, nil)
}

// cpuTopology takes a user cpu range and returns the number of sockets, cores and threads to configure
// as well as a map of vcpu to threadid for pinning and a map of numa nodes to vcpus for NUMA layout.
func (vm *qemu) cpuTopology(limit string) (int, int, int, map[uint64]uint64, map[uint64][]uint64, error) {
	// Get CPU topology.
	cpus, err := resources.GetCPU()
	if err != nil {
		return -1, -1, -1, nil, nil, err
	}

	// Expand the pins.
	pins, err := resources.ParseCpuset(limit)
	if err != nil {
		return -1, -1, -1, nil, nil, err
	}

	// Match tracking.
	vcpus := map[uint64]uint64{}
	sockets := map[uint64][]uint64{}
	cores := map[uint64][]uint64{}
	numaNodes := map[uint64][]uint64{}

	// Go through the physical CPUs looking for matches.
	i := uint64(0)
	for _, cpu := range cpus.Sockets {
		for _, core := range cpu.Cores {
			for _, thread := range core.Threads {
				for _, pin := range pins {
					if thread.ID == int64(pin) {
						// Found a matching CPU.
						vcpus[i] = uint64(pin)

						// Track cores per socket.
						_, ok := sockets[cpu.Socket]
						if !ok {
							sockets[cpu.Socket] = []uint64{}
						}
						if !shared.Uint64InSlice(core.Core, sockets[cpu.Socket]) {
							sockets[cpu.Socket] = append(sockets[cpu.Socket], core.Core)
						}

						// Track threads per core.
						_, ok = cores[core.Core]
						if !ok {
							cores[core.Core] = []uint64{}
						}
						if !shared.Uint64InSlice(thread.Thread, cores[core.Core]) {
							cores[core.Core] = append(cores[core.Core], thread.Thread)
						}

						// Record NUMA node for thread.
						_, ok = cores[core.Core]
						if !ok {
							numaNodes[thread.NUMANode] = []uint64{}
						}
						numaNodes[thread.NUMANode] = append(numaNodes[thread.NUMANode], i)

						i++
					}
				}
			}
		}
	}

	// Confirm we're getting the expected number of CPUs.
	if len(pins) != len(vcpus) {
		return -1, -1, -1, nil, nil, fmt.Errorf("Unavailable CPUs requested: %s", limit)
	}

	// Validate the topology.
	valid := true
	nrSockets := 0
	nrCores := 0
	nrThreads := 0

	// Confirm that there is no balancing inconsistencies.
	countCores := -1
	for _, cores := range sockets {
		if countCores != -1 && len(cores) != countCores {
			valid = false
			break
		}

		countCores = len(cores)
	}

	countThreads := -1
	for _, threads := range cores {
		if countThreads != -1 && len(threads) != countThreads {
			valid = false
			break
		}

		countThreads = len(threads)
	}

	// Check against double listing of CPU.
	if len(sockets)*countCores*countThreads != len(vcpus) {
		valid = false
	}

	// Build up the topology.
	if valid {
		// Valid topology.
		nrSockets = len(sockets)
		nrCores = countCores
		nrThreads = countThreads
	} else {
		logger.Warnf("Instance '%s' uses a CPU pinning profile which doesn't match hardware layout", project.Instance(vm.Project(), vm.Name()))

		// Fallback on pretending everything are cores.
		nrSockets = 1
		nrCores = len(vcpus)
		nrThreads = 1
	}

	return nrSockets, nrCores, nrThreads, vcpus, numaNodes, nil
}

func (vm *qemu) expandConfig(profiles []api.Profile) error {
	if profiles == nil && len(vm.profiles) > 0 {
		var err error
		profiles, err = vm.state.Cluster.GetProfiles(vm.project, vm.profiles)
		if err != nil {
			return err
		}
	}

	vm.expandedConfig = db.ExpandInstanceConfig(vm.localConfig, profiles)

	return nil
}

func (vm *qemu) devlxdEventSend(eventType string, eventMessage interface{}) error {
	event := shared.Jmap{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

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

	_, _, err = agent.RawQuery("POST", "/1.0/events", &event, "")
	if err != nil {
		return err
	}

	return nil
}

func (vm *qemu) writeInstanceData() error {
	// Only write instance-data file if security.devlxd is true.
	if !shared.IsTrue(vm.expandedConfig["security.devlxd"]) {
		return nil
	}

	// Instance data for devlxd.
	configDrivePath := filepath.Join(vm.Path(), "config")
	userConfig := make(map[string]string)

	for k, v := range vm.ExpandedConfig() {
		if !strings.HasPrefix(k, "user.") {
			continue
		}

		userConfig[k] = v
	}

	out, err := json.Marshal(struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config,omitempty"`
	}{vm.Name(), userConfig})
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "instance-data"), out, 0600)
	if err != nil {
		return err
	}

	return nil
}
