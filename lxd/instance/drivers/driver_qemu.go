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
	d := qemuInstantiate(s, args, nil)

	// Expand config and devices.
	err := d.expandConfig(profiles)
	if err != nil {
		return nil, err
	}

	err = d.expandDevices(profiles)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// qemuInstantiate creates a Qemu struct without expanding config. The expandedDevices argument is
// used during device config validation when the devices have already been expanded and we do not
// have access to the profiles used to do it. This can be safely passed as nil if not required.
func qemuInstantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices) *qemu {
	d := &qemu{
		common: common{
			state: s,

			architecture: args.Architecture,
			creationDate: args.CreationDate,
			dbType:       args.Type,
			description:  args.Description,
			ephemeral:    args.Ephemeral,
			expiryDate:   args.ExpiryDate,
			id:           args.ID,
			lastUsedDate: args.LastUsedDate,
			localConfig:  args.Config,
			localDevices: args.Devices,
			name:         args.Name,
			node:         args.Node,
			profiles:     args.Profiles,
			project:      args.Project,
			snapshot:     args.Snapshot,
			stateful:     args.Stateful,
		},
	}

	// Get the architecture name.
	archName, err := osarch.ArchitectureName(d.architecture)
	if err == nil {
		d.architectureName = archName
	}

	// Cleanup the zero values.
	if d.expiryDate.IsZero() {
		d.expiryDate = time.Time{}
	}

	if d.creationDate.IsZero() {
		d.creationDate = time.Time{}
	}

	if d.lastUsedDate.IsZero() {
		d.lastUsedDate = time.Time{}
	}

	// This is passed during expanded config validation.
	if expandedDevices != nil {
		d.expandedDevices = expandedDevices
	}

	return d
}

// qemuCreate creates a new storage volume record and returns an initialised Instance.
func qemuCreate(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	// Create the instance struct.
	d := &qemu{
		common: common{
			state: s,

			architecture: args.Architecture,
			creationDate: args.CreationDate,
			dbType:       args.Type,
			description:  args.Description,
			ephemeral:    args.Ephemeral,
			expiryDate:   args.ExpiryDate,
			id:           args.ID,
			lastUsedDate: args.LastUsedDate,
			localConfig:  args.Config,
			localDevices: args.Devices,
			name:         args.Name,
			node:         args.Node,
			profiles:     args.Profiles,
			project:      args.Project,
			snapshot:     args.Snapshot,
			stateful:     args.Stateful,
		},
	}

	revert := revert.New()
	defer revert.Fail()

	// Use d.Delete() in revert on error as this function doesn't just create DB records, it can also cause
	// other modifications to the host when devices are added.
	revert.Add(func() { d.Delete(true) })

	// Get the architecture name.
	archName, err := osarch.ArchitectureName(d.architecture)
	if err == nil {
		d.architectureName = archName
	}

	// Cleanup the zero values.
	if d.expiryDate.IsZero() {
		d.expiryDate = time.Time{}
	}

	if d.creationDate.IsZero() {
		d.creationDate = time.Time{}
	}

	if d.lastUsedDate.IsZero() {
		d.lastUsedDate = time.Time{}
	}

	ctxMap := log.Ctx{
		"project":   args.Project,
		"name":      d.name,
		"ephemeral": d.ephemeral,
	}

	logger.Info("Creating instance", ctxMap)

	// Load the config.
	err = d.init()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to expand config")
	}

	// Validate expanded config.
	err = instance.ValidConfig(s.OS, d.expandedConfig, false, true)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid config")
	}

	err = instance.ValidDevices(s, s.Cluster, d.Project(), d.Type(), d.expandedDevices, true)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Retrieve the container's storage pool.
	var storageInstance instance.Instance
	if d.IsSnapshot() {
		parentName, _, _ := shared.InstanceGetParentAndSnapshotName(d.name)

		// Load the parent.
		storageInstance, err = instance.LoadByProjectAndName(d.state, d.project, parentName)
		if err != nil {
			return nil, errors.Wrap(err, "Invalid parent")
		}
	} else {
		storageInstance = d
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
	d.storagePool, err = storagePools.GetPoolByName(d.state, rootDiskDevice["pool"])
	if err != nil {
		return nil, errors.Wrapf(err, "Failed loading storage pool")
	}

	// Fill default config.
	volumeConfig := map[string]string{}
	err = d.storagePool.FillInstanceConfig(d, volumeConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed filling default config")
	}

	// Create a new database entry for the instance's storage volume.
	if d.IsSnapshot() {
		_, err = s.Cluster.CreateStorageVolumeSnapshot(args.Project, args.Name, "", db.StoragePoolVolumeTypeVM, d.storagePool.ID(), volumeConfig, time.Time{})

	} else {
		_, err = s.Cluster.CreateStoragePoolVolume(args.Project, args.Name, "", db.StoragePoolVolumeTypeVM, d.storagePool.ID(), volumeConfig)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "Failed creating storage record")
	}

	if !d.IsSnapshot() {
		// Update MAAS.
		err = d.maasUpdate(nil)
		if err != nil {
			return nil, err
		}

		// Add devices to instance.
		for k, m := range d.expandedDevices {
			err = d.deviceAdd(k, m, false)
			if err != nil && err != device.ErrUnsupportedDevType {
				return nil, errors.Wrapf(err, "Failed to add device %q", k)
			}
		}
	}

	logger.Info("Created instance", ctxMap)
	d.state.Events.SendLifecycle(d.project, "virtual-machine-created", fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)

	revert.Success()
	return d, nil
}

// qemu is the QEMU virtual machine driver.
type qemu struct {
	common

	// Cached handles.
	// Do not use these variables directly, instead use their associated get functions so they
	// will be initialised on demand.
	agentClient      *http.Client
	architectureName string
	storagePool      storagePools.Pool
}

// getAgentClient returns the current agent client handle. To avoid TLS setup each time this
// function is called, the handle is cached internally in the Qemu struct.
func (d *qemu) getAgentClient() (*http.Client, error) {
	if d.agentClient != nil {
		return d.agentClient, nil
	}

	// The connection uses mutual authentication, so use the LXD server's key & cert for client.
	agentCert, _, clientCert, clientKey, err := d.generateAgentCert()
	if err != nil {
		return nil, err
	}

	agent, err := vsock.HTTPClient(d.vsockID(), clientCert, clientKey, agentCert)
	if err != nil {
		return nil, err
	}

	return agent, nil
}

// getStoragePool returns the current storage pool handle. To avoid a DB lookup each time this
// function is called, the handle is cached internally in the Qemu struct.
func (d *qemu) getStoragePool() (storagePools.Pool, error) {
	if d.storagePool != nil {
		return d.storagePool, nil
	}

	pool, err := storagePools.GetPoolByInstance(d.state, d)
	if err != nil {
		return nil, err
	}
	d.storagePool = pool

	return d.storagePool, nil
}

func (d *qemu) getMonitorEventHandler() func(event string, data map[string]interface{}) {
	projectName := d.Project()
	instanceName := d.Name()
	state := d.state

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
func (d *qemu) mount() (*storagePools.MountInfo, error) {
	var pool storagePools.Pool
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	if d.IsSnapshot() {
		mountInfo, err := pool.MountInstanceSnapshot(d, nil)
		if err != nil {
			return nil, err
		}

		return mountInfo, nil
	}

	mountInfo, err := pool.MountInstance(d, nil)
	if err != nil {
		return nil, err
	}

	return mountInfo, nil
}

// unmount the instance's config volume if needed.
func (d *qemu) unmount() (bool, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return false, err
	}

	unmounted, err := pool.UnmountInstance(d, nil)
	if err != nil {
		return false, err
	}

	return unmounted, nil
}

// generateAgentCert creates the necessary server key and certificate if needed.
func (d *qemu) generateAgentCert() (string, string, string, string, error) {
	// Mount the instance's config volume if needed.
	_, err := d.mount()
	if err != nil {
		return "", "", "", "", err
	}
	defer d.unmount()

	agentCertFile := filepath.Join(d.Path(), "agent.crt")
	agentKeyFile := filepath.Join(d.Path(), "agent.key")
	clientCertFile := filepath.Join(d.Path(), "agent-client.crt")
	clientKeyFile := filepath.Join(d.Path(), "agent-client.key")

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
func (d *qemu) Freeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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
func (d *qemu) onStop(target string) error {
	var err error

	// Pick up the existing stop operation lock created in Stop() function.
	op := operationlock.Get(d.id)
	if op != nil && !shared.StringInSlice(op.Action(), []string{"stop", "restart"}) {
		return fmt.Errorf("Instance is already running a %s operation", op.Action())
	}

	if op == nil && target == "reboot" {
		op, err = operationlock.Create(d.id, "restart", false, false)
		if err != nil {
			return errors.Wrap(err, "Create restart operation")
		}
	}

	// Reset timeout to 30s.
	op.Reset()

	// Cleanup.
	d.cleanupDevices()
	os.Remove(d.pidFilePath())
	os.Remove(d.monitorPath())
	d.unmount()

	pidPath := filepath.Join(d.LogPath(), "virtiofsd.pid")

	proc, err := subprocess.ImportProcess(pidPath)
	if err == nil {
		proc.Stop()
		os.Remove(pidPath)
	}

	// Record power state.
	err = d.state.Cluster.UpdateInstancePowerState(d.id, "STOPPED")
	if err != nil {
		op.Done(err)
		return err
	}

	// Unload the apparmor profile
	err = apparmor.InstanceUnload(d.state, d)
	if err != nil {
		op.Done(err)
		return err
	}

	if target == "reboot" {
		// Reset timeout to 30s.
		op.Reset()

		err = d.Start(false)
		if err != nil {
			op.Done(err)
			return err
		}

		d.state.Events.SendLifecycle(d.project, "virtual-machine-restarted",
			fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)
	} else if d.ephemeral {
		// Reset timeout to 30s.
		op.Reset()

		// Destroy ephemeral virtual machines
		err = d.Delete(true)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	if target != "reboot" {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-shutdown",
			fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)
	}

	op.Done(nil)
	return nil
}

// Shutdown shuts the instance down.
func (d *qemu) Shutdown(timeout time.Duration) error {
	// Must be run prior to creating the operation lock.
	if !d.IsRunning() {
		return fmt.Errorf("The instance is already stopped")
	}

	// Setup a new operation
	exists, op, err := operationlock.CreateWaitGet(d.id, "stop", []string{"restart"}, true, false)
	if err != nil {
		return err
	}
	if exists {
		// An existing matching operation has now succeeded, return.
		return nil
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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
			err = fmt.Errorf("Instance was not shutdown after timeout")
			op.Done(err)
			return err
		}
	} else {
		<-chDisconnect // Block until VM is not running if no timeout provided.
	}

	// Wait for onStop.
	err = op.Wait()
	if err != nil && d.IsRunning() {
		return err
	}

	if op.Action() == "stop" {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-shutdown", fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)
	}

	return nil
}

// Restart restart the instance.
func (d *qemu) Restart(timeout time.Duration) error {
	err := d.common.restart(d, timeout)
	if err != nil {
		return err
	}

	d.state.Events.SendLifecycle(d.project, "virtual-machine-restarted",
		fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)

	return nil
}

func (d *qemu) ovmfPath() string {
	if os.Getenv("LXD_OVMF_PATH") != "" {
		return os.Getenv("LXD_OVMF_PATH")
	}

	return "/usr/share/OVMF"
}

// Start starts the instance.
func (d *qemu) Start(stateful bool) error {
	// Must be run prior to creating the operation lock.
	if d.IsRunning() {
		return fmt.Errorf("The instance is already running")
	}

	// Setup a new operation
	exists, op, err := operationlock.CreateWaitGet(d.id, "start", []string{"restart"}, false, false)
	if err != nil {
		return errors.Wrap(err, "Create instance start operation")
	}
	if exists {
		// An existing matching operation has now succeeded, return.
		return nil
	}
	defer op.Done(nil)

	// Ensure the correct vhost_vsock kernel module is loaded before establishing the vsock.
	err = util.LoadModule("vhost_vsock")
	if err != nil {
		op.Done(err)
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	// Start accumulating device paths.
	d.devPaths = []string{}

	// Rotate the log file.
	logfile := d.LogFilePath()
	if shared.PathExists(logfile) {
		os.Remove(logfile + ".old")
		err := os.Rename(logfile, logfile+".old")
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Mount the instance's config volume.
	mountInfo, err := d.mount()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Add(func() { d.unmount() })

	err = d.generateConfigShare()
	if err != nil {
		op.Done(err)
		return err
	}

	// Create all needed paths.
	err = os.MkdirAll(d.LogPath(), 0700)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(d.DevicesPath(), 0711)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(d.ShmountsPath(), 0711)
	if err != nil {
		op.Done(err)
		return err
	}

	// Setup virtiofsd for config path.
	sockPath := filepath.Join(d.LogPath(), "virtio-fs.config.sock")

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
		proc, err := subprocess.NewProcess(cmd, []string{fmt.Sprintf("--socket-path=%s", sockPath), "-o", fmt.Sprintf("source=%s", filepath.Join(d.Path(), "config"))}, "", "")
		if err != nil {
			op.Done(err)
			return err
		}

		err = proc.Start()
		if err != nil {
			op.Done(err)
			return err
		}

		revert.Add(func() { proc.Stop() })

		pidPath := filepath.Join(d.LogPath(), "virtiofsd.pid")

		err = proc.Save(pidPath)
		if err != nil {
			op.Done(err)
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
			err = fmt.Errorf("virtiofsd failed to bind socket within 1s")
			op.Done(err)
			return err
		}
	} else {
		logger.Warn("Unable to use virtio-fs for config drive, using 9p as a fallback: virtiofsd missing")
	}

	// Generate UUID if not present.
	instUUID := d.localConfig["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New()
		d.VolatileSet(map[string]string{"volatile.uuid": instUUID})
	}

	// Copy OVMF settings firmware to nvram file.
	// This firmware file can be modified by the VM so it must be copied from the defaults.
	if !shared.PathExists(d.nvramPath()) {
		err = d.setupNvram()
		if err != nil {
			op.Done(err)
			return err
		}
	}

	devConfs := make([]*deviceConfig.RunConfig, 0, len(d.expandedDevices))

	// Setup devices in sorted order, this ensures that device mounts are added in path order.
	for _, entry := range d.expandedDevices.Sorted() {
		dev := entry // Ensure device variable has local scope for revert.

		// Start the device.
		runConf, err := d.deviceStart(dev.Name, dev.Config, false)
		if err != nil {
			op.Done(err)
			return errors.Wrapf(err, "Failed to start device %q", dev.Name)
		}

		if runConf == nil {
			continue
		}

		revert.Add(func() {
			err := d.deviceStop(dev.Name, dev.Config, false)
			if err != nil {
				logger.Errorf("Failed to cleanup device %q: %v", dev.Name, err)
			}
		})

		devConfs = append(devConfs, runConf)
	}

	// Get qemu configuration.
	qemuBinary, qemuBus, err := d.qemuArchConfig()
	if err != nil {
		op.Done(err)
		return err
	}

	// Define a set of files to open and pass their file descriptors to qemu command.
	fdFiles := make([]string, 0)

	confFile, err := d.generateQemuConfigFile(mountInfo, qemuBus, devConfs, &fdFiles)
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
		"-name", d.Name(),
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
		"-pidfile", d.pidFilePath(),
		"-D", d.LogFilePath(),
		"-chroot", d.Path(),
	}

	// SMBIOS only on x86_64 and aarch64.
	if shared.IntInSlice(d.architecture, []int{osarch.ARCH_64BIT_INTEL_X86, osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN}) {
		qemuCmd = append(qemuCmd, "-smbios", "type=2,manufacturer=Canonical Ltd.,product=LXD")
	}

	// Attempt to drop privileges.
	if d.state.OS.UnprivUser != "" {
		qemuCmd = append(qemuCmd, "-runas", d.state.OS.UnprivUser)

		// Change ownership of config directory files so they are accessible to the
		// unprivileged qemu process so that the 9p share can work.
		//
		// Security note: The 9P share will present the UID owner of these files on the host
		// to the VM. In order to ensure that non-root users in the VM cannot access these
		// files be sure to mount the 9P share in the VM with the "access=0" option to allow
		// only root user in VM to access the mounted share.
		err := filepath.Walk(filepath.Join(d.Path(), "config"),
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					op.Done(err)
					return err
				}

				err = os.Chown(path, int(d.state.OS.UnprivUID), -1)
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
	if d.architecture != osarch.ARCH_64BIT_INTEL_X86 && shared.IsTrue(d.expandedConfig["limits.memory.hugepages"]) {
		hugetlb, err := util.HugepagesPath()
		if err != nil {
			op.Done(err)
			return err
		}

		qemuCmd = append(qemuCmd, "-mem-path", hugetlb, "-mem-prealloc")
	}

	if d.expandedConfig["raw.qemu"] != "" {
		fields, err := shellquote.Split(d.expandedConfig["raw.qemu"])
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
	p, err := subprocess.NewProcess(d.state.OS.ExecPath, append(forkLimitsCmd, qemuCmd...), d.EarlyLogFilePath(), d.EarlyLogFilePath())
	if err != nil {
		op.Done(err)
		return err
	}

	// Load the AppArmor profile
	err = apparmor.InstanceLoad(d.state, d)
	if err != nil {
		op.Done(err)
		return err
	}

	p.SetApparmor(apparmor.InstanceProfileName(d))

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
			c, err := d.openUnixSocket(file)
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

	// Reset timeout to 30s.
	op.Reset()

	err = p.StartWithFiles(files)
	if err != nil {
		op.Done(err)
		return err
	}

	_, err = p.Wait()
	if err != nil {
		stderr, _ := ioutil.ReadFile(d.EarlyLogFilePath())
		err = errors.Wrapf(err, "Failed to run: %s: %s", strings.Join(p.Args, " "), string(stderr))
		op.Done(err)
		return err
	}

	pid, err := d.pid()
	if err != nil {
		logger.Errorf(`Failed to get VM process ID "%d"`, pid)
		op.Done(err)
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
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		op.Done(err)
		return err
	}

	// Apply CPU pinning.
	cpuLimit, ok := d.expandedConfig["limits.cpu"]
	if ok && cpuLimit != "" {
		_, err := strconv.Atoi(cpuLimit)
		if err != nil {
			// Expand to a set of CPU identifiers and get the pinning map.
			_, _, _, pins, _, err := d.cpuTopology(cpuLimit)
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
				err = fmt.Errorf("QEMU has less vCPUs than configured")
				op.Done(err)
				return err
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

	// Reset timeout to 30s.
	op.Reset()

	// Start the VM.
	err = monitor.Start()
	if err != nil {
		op.Done(err)
		return err
	}

	// Database updates
	err = d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Record current state
		err = tx.UpdateInstancePowerState(d.id, "RUNNING")
		if err != nil {
			err = errors.Wrap(err, "Error updating instance state")
			op.Done(err)
			return err
		}

		// Update time instance last started time
		err = tx.UpdateInstanceLastUsedDate(d.id, time.Now().UTC())
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

	if op.Action() == "start" {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-started", fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)
	}

	return nil
}

// openUnixSocket connects to a UNIX socket and returns the connection.
func (d *qemu) openUnixSocket(sockPath string) (*net.UnixConn, error) {
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

func (d *qemu) setupNvram() error {
	// UEFI only on x86_64 and aarch64.
	if !shared.IntInSlice(d.architecture, []int{osarch.ARCH_64BIT_INTEL_X86, osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN}) {
		return nil
	}

	// Mount the instance's config volume.
	_, err := d.mount()
	if err != nil {
		return err
	}
	defer d.unmount()

	srcOvmfFile := filepath.Join(d.ovmfPath(), "OVMF_VARS.fd")
	if d.expandedConfig["security.secureboot"] == "" || shared.IsTrue(d.expandedConfig["security.secureboot"]) {
		srcOvmfFile = filepath.Join(d.ovmfPath(), "OVMF_VARS.ms.fd")
	}

	if !shared.PathExists(srcOvmfFile) {
		return fmt.Errorf("Required EFI firmware settings file missing: %s", srcOvmfFile)
	}

	os.Remove(d.nvramPath())
	err = shared.FileCopy(srcOvmfFile, d.nvramPath())
	if err != nil {
		return err
	}

	return nil
}

func (d *qemu) qemuArchConfig() (string, string, error) {
	if d.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		return "qemu-system-x86_64", "pcie", nil
	} else if d.architecture == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN {
		return "qemu-system-aarch64", "pcie", nil
	} else if d.architecture == osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN {
		return "qemu-system-ppc64", "pci", nil
	} else if d.architecture == osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		return "qemu-system-s390x", "ccw", nil
	}

	return "", "", fmt.Errorf("Architecture isn't supported for virtual machines")
}

// RegisterDevices calls the Register() function on all of the instance's devices.
func (d *qemu) RegisterDevices() {
	devices := d.ExpandedDevices()
	for _, entry := range devices.Sorted() {
		dev, _, err := d.deviceLoad(entry.Name, entry.Config)
		if err == device.ErrUnsupportedDevType {
			continue
		}

		if err != nil {
			logger.Error("Failed to load device to register", log.Ctx{"err": err, "instance": d.Name(), "device": entry.Name})
			continue
		}

		// Check whether device wants to register for any events.
		err = dev.Register()
		if err != nil {
			logger.Error("Failed to register device", log.Ctx{"err": err, "instance": d.Name(), "device": entry.Name})
			continue
		}
	}
}

// SaveConfigFile is not used by VMs.
func (d *qemu) SaveConfigFile() error {
	return instance.ErrNotImplemented
}

// OnHook is the top-level hook handler.
func (d *qemu) OnHook(hookName string, args map[string]string) error {
	return instance.ErrNotImplemented
}

// deviceLoad instantiates and validates a new device and returns it along with enriched config.
func (d *qemu) deviceLoad(deviceName string, rawConfig deviceConfig.Device) (device.Device, deviceConfig.Device, error) {
	var configCopy deviceConfig.Device
	var err error

	// Create copy of config and load some fields from volatile if device is nic or infiniband.
	if shared.StringInSlice(rawConfig["type"], []string{"nic", "infiniband"}) {
		configCopy, err = d.FillNetworkDevice(deviceName, rawConfig)
		if err != nil {
			return nil, nil, err
		}
	} else {
		// Othewise copy the config so it cannot be modified by device.
		configCopy = rawConfig.Clone()
	}

	dev, err := device.New(d, d.state, deviceName, configCopy, d.deviceVolatileGetFunc(deviceName), d.deviceVolatileSetFunc(deviceName))

	// Return device and config copy even if error occurs as caller may still use device.
	return dev, configCopy, err
}

// deviceStart loads a new device and calls its Start() function.
func (d *qemu) deviceStart(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) (*deviceConfig.RunConfig, error) {
	logger := logging.AddContext(logger.Log, log.Ctx{"device": deviceName, "type": rawConfig["type"], "project": d.Project(), "instance": d.Name()})
	logger.Debug("Starting device")

	dev, _, err := d.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return nil, err
	}

	if instanceRunning && !dev.CanHotPlug() {
		return nil, fmt.Errorf("Device cannot be started when instance is running")
	}

	runConf, err := dev.Start()
	if err != nil {
		return nil, err
	}

	return runConf, nil
}

// deviceStop loads a new device and calls its Stop() function.
func (d *qemu) deviceStop(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"device": deviceName, "type": rawConfig["type"], "project": d.Project(), "instance": d.Name()})
	logger.Debug("Stopping device")

	dev, _, err := d.deviceLoad(deviceName, rawConfig)

	// If deviceLoad fails with unsupported device type then return.
	if err == device.ErrUnsupportedDevType {
		return err
	}

	// If deviceLoad fails for any other reason then just log the error and proceed, as in the
	// scenario that a new version of LXD has additional validation restrictions than older
	// versions we still need to allow previously valid devices to be stopped.
	if err != nil {
		// If there is no device returned, then we cannot proceed, so return as error.
		if dev == nil {
			return fmt.Errorf("Device stop validation failed for %q: %v", deviceName, err)
		}

		logger.Error("Device stop validation failed", log.Ctx{"err": err})
	}

	if instanceRunning && !dev.CanHotPlug() {
		return fmt.Errorf("Device cannot be stopped when instance is running")
	}

	runConf, err := dev.Stop()
	if err != nil {
		return err
	}

	if runConf != nil {
		// Run post stop hooks irrespective of run state of instance.
		err = d.runHooks(runConf.PostHooks)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *qemu) monitorPath() string {
	return filepath.Join(d.LogPath(), "qemu.monitor")
}

func (d *qemu) nvramPath() string {
	return filepath.Join(d.Path(), "qemu.nvram")
}

func (d *qemu) spicePath() string {
	return filepath.Join(d.LogPath(), "qemu.spice")
}

// generateConfigShare generates the config share directory that will be exported to the VM via
// a 9P share. Due to the unknown size of templates inside the images this directory is created
// inside the VM's config volume so that it can be restricted by quota.
// Requires the instance be mounted before calling this function.
func (d *qemu) generateConfigShare() error {
	configDrivePath := filepath.Join(d.Path(), "config")

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

	if d.ExpandedConfig()["user.user-data"] != "" {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "user-data"), []byte(d.ExpandedConfig()["user.user-data"]), 0400)
		if err != nil {
			return err
		}
	} else {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "user-data"), []byte("#cloud-config\n"), 0400)
		if err != nil {
			return err
		}
	}

	if d.ExpandedConfig()["user.vendor-data"] != "" {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "vendor-data"), []byte(d.ExpandedConfig()["user.vendor-data"]), 0400)
		if err != nil {
			return err
		}
	} else {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "vendor-data"), []byte("#cloud-config\n"), 0400)
		if err != nil {
			return err
		}
	}

	if d.ExpandedConfig()["user.network-config"] != "" {
		err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "network-config"), []byte(d.ExpandedConfig()["user.network-config"]), 0400)
		if err != nil {
			return err
		}
	} else {
		os.Remove(filepath.Join(configDrivePath, "cloud-init", "network-config"))
	}

	// Append any user.meta-data to our predefined meta-data config.
	err = ioutil.WriteFile(filepath.Join(configDrivePath, "cloud-init", "meta-data"), []byte(fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n%s\n", d.Name(), d.Name(), d.ExpandedConfig()["user.meta-data"])), 0400)
	if err != nil {
		return err
	}

	// Add the VM agent.
	path, err := exec.LookPath("lxd-agent")
	if err != nil {
		logger.Warnf("lxd-agent not found, skipping its inclusion in the VM config drive: %v", err)
	} else {
		// Install agent into config drive dir if found.
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}

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

	agentCert, agentKey, clientCert, _, err := d.generateAgentCert()
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
	err = d.writeInstanceData()
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
	if d.localConfig[key] != "" {
		// Run any template that needs running.
		err = d.templateApplyNow(d.localConfig[key], filepath.Join(configDrivePath, "files"))
		if err != nil {
			return err
		}

		// Remove the volatile key from the DB.
		err := d.state.Cluster.DeleteInstanceConfigKey(d.id, key)
		if err != nil {
			return err
		}
	}

	err = d.templateApplyNow("start", filepath.Join(configDrivePath, "files"))
	if err != nil {
		return err
	}

	// Copy the template metadata itself too.
	metaPath := filepath.Join(d.Path(), "metadata.yaml")
	if shared.PathExists(metaPath) {
		err = shared.FileCopy(metaPath, filepath.Join(configDrivePath, "files/metadata.yaml"))
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *qemu) templateApplyNow(trigger string, path string) error {
	// If there's no metadata, just return.
	fname := filepath.Join(d.Path(), "metadata.yaml")
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
	arch, err := osarch.ArchitectureName(d.architecture)
	if err != nil {
		arch, err = osarch.ArchitectureName(d.state.OS.Architectures[0])
		if err != nil {
			return errors.Wrap(err, "Failed to detect system architecture")
		}
	}

	// Generate the container metadata.
	instanceMeta := make(map[string]string)
	instanceMeta["name"] = d.name
	instanceMeta["type"] = "virtual-machine"
	instanceMeta["architecture"] = arch

	if d.ephemeral {
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
			tplString, err := ioutil.ReadFile(filepath.Join(d.TemplatesPath(), tpl.Template))
			if err != nil {
				return errors.Wrap(err, "Failed to read template file")
			}

			// Restrict filesystem access to within the container's rootfs.
			tplSet := pongo2.NewSet(fmt.Sprintf("%s-%s", d.name, tpl.Template), pongoTemplate.ChrootLoader{Path: d.TemplatesPath()})
			tplRender, err := tplSet.FromString("{% autoescape off %}" + string(tplString) + "{% endautoescape %}")
			if err != nil {
				return errors.Wrap(err, "Failed to render template")
			}

			configGet := func(confKey, confDefault *pongo2.Value) *pongo2.Value {
				val, ok := d.expandedConfig[confKey.String()]
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
				"config":     d.expandedConfig,
				"devices":    d.expandedDevices,
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
func (d *qemu) deviceBootPriorities() (map[string]int, error) {
	type devicePrios struct {
		Name     string
		BootPrio uint32
	}

	devices := []devicePrios{}

	for devName, devConf := range d.expandedDevices {
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
func (d *qemu) generateQemuConfigFile(mountInfo *storagePools.MountInfo, busName string, devConfs []*deviceConfig.RunConfig, fdFiles *[]string) (string, error) {
	var sb *strings.Builder = &strings.Builder{}

	err := qemuBase.Execute(sb, map[string]interface{}{
		"architecture": d.architectureName,
		"spicePath":    d.spicePath(),
	})
	if err != nil {
		return "", err
	}

	err = d.addCPUMemoryConfig(sb)
	if err != nil {
		return "", err
	}

	err = qemuDriveFirmware.Execute(sb, map[string]interface{}{
		"architecture": d.architectureName,
		"roPath":       filepath.Join(d.ovmfPath(), "OVMF_CODE.fd"),
		"nvramPath":    d.nvramPath(),
	})
	if err != nil {
		return "", err
	}

	err = qemuControlSocket.Execute(sb, map[string]interface{}{
		"path": d.monitorPath(),
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

		"vsockID": d.vsockID(),
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
	if d.architecture != osarch.ARCH_64BIT_S390_BIG_ENDIAN {
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

		"path": filepath.Join(d.Path(), "config"),
	})
	if err != nil {
		return "", err
	}

	sockPath := filepath.Join(d.LogPath(), "virtio-fs.config.sock")
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

		"architecture": d.architectureName,
	})
	if err != nil {
		return "", err
	}

	// Dynamic devices.
	bootIndexes, err := d.deviceBootPriorities()
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
					err = d.addRootDriveConfig(sb, mountInfo, bootIndexes, drive)
				} else if drive.FSType == "9p" {
					err = d.addDriveDirConfig(sb, bus, fdFiles, &agentMounts, drive)
				} else {
					err = d.addDriveConfig(sb, bootIndexes, drive)
				}
				if err != nil {
					return "", err
				}
			}
		}

		// Add network device.
		if len(runConf.NetworkInterface) > 0 {
			err = d.addNetDevConfig(sb, bus, bootIndexes, runConf.NetworkInterface, fdFiles)
			if err != nil {
				return "", err
			}
		}

		// Add GPU device.
		if len(runConf.GPUDevice) > 0 {
			err = d.addGPUDevConfig(sb, bus, runConf.GPUDevice)
			if err != nil {
				return "", err
			}
		}

		// Add USB device.
		if len(runConf.USBDevice) > 0 {
			err = d.addUSBDeviceConfig(sb, bus, runConf.USBDevice)
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

	agentMountFile := filepath.Join(d.Path(), "config", "agent-mounts.json")
	err = ioutil.WriteFile(agentMountFile, agentMountJSON, 0400)
	if err != nil {
		return "", errors.Wrapf(err, "Failed writing agent mounts file")
	}

	// Write the config file to disk.
	configPath := filepath.Join(d.LogPath(), "qemu.conf")
	return configPath, ioutil.WriteFile(configPath, []byte(sb.String()), 0640)
}

// addCPUMemoryConfig adds the qemu config required for setting the number of virtualised CPUs and memory.
func (d *qemu) addCPUMemoryConfig(sb *strings.Builder) error {
	// Default to a single core.
	cpus := d.expandedConfig["limits.cpu"]
	if cpus == "" {
		cpus = "1"
	}

	ctx := map[string]interface{}{
		"architecture": d.architectureName,
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
		nrSockets, nrCores, nrThreads, vcpus, numaNodes, err := d.cpuTopology(cpus)
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
	memSize := d.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = qemuDefaultMemSize // Default if no memory limit specified.
	}

	memSizeBytes, err := units.ParseByteSizeString(memSize)
	if err != nil {
		return fmt.Errorf("limits.memory invalid: %v", err)
	}

	ctx["hugepages"] = ""
	if shared.IsTrue(d.expandedConfig["limits.memory.hugepages"]) {
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
		"architecture": d.architectureName,
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
func (d *qemu) addFileDescriptor(fdFiles *[]string, filePath string) int {
	// Append the tap device file path to the list of files to be opened and passed to qemu.
	*fdFiles = append(*fdFiles, filePath)
	return 2 + len(*fdFiles) // Use 2+fdFiles count, as first user file descriptor is 3.
}

// addRootDriveConfig adds the qemu config required for adding the root drive.
func (d *qemu) addRootDriveConfig(sb *strings.Builder, mountInfo *storagePools.MountInfo, bootIndexes map[string]int, rootDriveConf deviceConfig.MountEntryItem) error {
	if rootDriveConf.TargetPath != "/" {
		return fmt.Errorf("Non-root drive config supplied")
	}

	pool, err := d.getStoragePool()
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

	return d.addDriveConfig(sb, bootIndexes, driveConf)
}

// addDriveDirConfig adds the qemu config required for adding a supplementary drive directory share.
func (d *qemu) addDriveDirConfig(sb *strings.Builder, bus *qemuBus, fdFiles *[]string, agentMounts *[]instancetype.VMAgentMount, driveConf deviceConfig.MountEntryItem) error {
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

	sockPath := filepath.Join(d.Path(), fmt.Sprintf("%s.sock", driveConf.DevName))

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
	proxyFD := d.addFileDescriptor(fdFiles, driveConf.DevPath)
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
func (d *qemu) addDriveConfig(sb *strings.Builder, bootIndexes map[string]int, driveConf deviceConfig.MountEntryItem) error {
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
		d.devPaths = append(d.devPaths, driveConf.DevPath)
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
func (d *qemu) addNetDevConfig(sb *strings.Builder, bus *qemuBus, bootIndexes map[string]int, nicConfig []deviceConfig.RunConfigItem, fdFiles *[]string) error {
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
		tplFields["tapFD"] = d.addFileDescriptor(fdFiles, fmt.Sprintf("/dev/tap%d", ifindex))
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
func (d *qemu) addGPUDevConfig(sb *strings.Builder, bus *qemuBus, gpuConfig []deviceConfig.RunConfigItem) error {
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
	vgaMode := shared.PathExists(filepath.Join("/sys/bus/pci/devices", pciSlotName, "boot_vga")) && d.architecture == osarch.ARCH_64BIT_INTEL_X86

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
					"vgpu":        "",
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

func (d *qemu) addUSBDeviceConfig(sb *strings.Builder, bus *qemuBus, usbConfig []deviceConfig.RunConfigItem) error {
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
	d.devPaths = append(d.devPaths, hostDevice)

	return nil
}

// pidFilePath returns the path where the qemu process should write its PID.
func (d *qemu) pidFilePath() string {
	return filepath.Join(d.LogPath(), "qemu.pid")
}

// pid gets the PID of the running qemu process.
func (d *qemu) pid() (int, error) {
	pidStr, err := ioutil.ReadFile(d.pidFilePath())
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
func (d *qemu) Stop(stateful bool) error {
	// Must be run prior to creating the operation lock.
	if !d.IsRunning() {
		return fmt.Errorf("The instance is already stopped")
	}

	// Setup a new operation.
	exists, op, err := operationlock.CreateWaitGet(d.id, "stop", []string{"restart"}, false, true)
	if err != nil {
		return err
	}
	if exists {
		// An existing matching operation has now succeeded, return.
		return nil
	}

	// Check that no stateful stop was requested.
	if stateful {
		err = fmt.Errorf("Stateful stop isn't supported for VMs at this time")
		op.Done(err)
		return err
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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
	if err != nil && d.IsRunning() {
		return err
	}

	if op.Action() == "stop" {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-stopped", fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)
	}

	return nil
}

// Unfreeze restores the instance to running.
func (d *qemu) Unfreeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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
func (d *qemu) IsPrivileged() bool {
	return false
}

// Restore restores an instance snapshot.
func (d *qemu) Restore(source instance.Instance, stateful bool) error {
	if stateful {
		return fmt.Errorf("Stateful snapshots of VMs aren't supported yet")
	}

	var ctxMap log.Ctx

	// Stop the instance.
	wasRunning := false
	if d.IsRunning() {
		wasRunning = true

		ephemeral := d.IsEphemeral()
		if ephemeral {
			// Unset ephemeral flag.
			args := db.InstanceArgs{
				Architecture: d.Architecture(),
				Config:       d.LocalConfig(),
				Description:  d.Description(),
				Devices:      d.LocalDevices(),
				Ephemeral:    false,
				Profiles:     d.Profiles(),
				Project:      d.Project(),
				Type:         d.Type(),
				Snapshot:     d.IsSnapshot(),
			}

			err := d.Update(args, false)
			if err != nil {
				return err
			}

			// On function return, set the flag back on.
			defer func() {
				args.Ephemeral = ephemeral
				d.Update(args, false)
			}()
		}

		// This will unmount the instance storage.
		err := d.Stop(false)
		if err != nil {
			return err
		}
	}

	ctxMap = log.Ctx{
		"project":   d.project,
		"name":      d.name,
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"source":    source.Name()}

	logger.Info("Restoring instance", ctxMap)

	// Load the storage driver.
	pool, err := storagePools.GetPoolByInstance(d.state, d)
	if err != nil {
		return err
	}

	// Ensure that storage is mounted for backup.yaml updates.
	_, err = pool.MountInstance(d, nil)
	if err != nil {
		return err
	}
	defer pool.UnmountInstance(d, nil)

	// Restore the rootfs.
	err = pool.RestoreInstanceSnapshot(d, source, nil)
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
	err = d.Update(args, false)
	if err != nil {
		logger.Error("Failed restoring instance configuration", ctxMap)
		return err
	}

	// The old backup file may be out of date (e.g. it doesn't have all the current snapshots of
	// the instance listed); let's write a new one to be safe.
	err = d.UpdateBackupFile()
	if err != nil {
		return err
	}

	d.state.Events.SendLifecycle(d.project, "virtual-machine-snapshot-restored", fmt.Sprintf("/1.0/virtual-machines/%s", d.name), map[string]interface{}{"snapshot_name": d.name})

	// Restart the insance.
	if wasRunning {
		logger.Info("Restored instance", ctxMap)
		return d.Start(false)
	}

	logger.Info("Restored instance", ctxMap)
	return nil
}

// Rename the instance.
func (d *qemu) Rename(newName string) error {
	oldName := d.Name()
	ctxMap := log.Ctx{
		"project":   d.project,
		"name":      d.name,
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"newname":   newName}

	logger.Info("Renaming instance", ctxMap)

	// Sanity checks.
	err := instance.ValidName(newName, d.IsSnapshot())
	if err != nil {
		return err
	}

	if d.IsRunning() {
		return fmt.Errorf("Renaming of running instance not allowed")
	}

	// Clean things up.
	d.cleanup()

	pool, err := storagePools.GetPoolByInstance(d.state, d)
	if err != nil {
		return errors.Wrap(err, "Load instance storage pool")
	}

	if d.IsSnapshot() {
		_, newSnapName, _ := shared.InstanceGetParentAndSnapshotName(newName)
		err = pool.RenameInstanceSnapshot(d, newSnapName, nil)
		if err != nil {
			return errors.Wrap(err, "Rename instance snapshot")
		}
	} else {
		err = pool.RenameInstance(d, newName, nil)
		if err != nil {
			return errors.Wrap(err, "Rename instance")
		}
	}

	if !d.IsSnapshot() {
		// Rename all the instance snapshot database entries.
		results, err := d.state.Cluster.GetInstanceSnapshotsNames(d.project, oldName)
		if err != nil {
			logger.Error("Failed to get instance snapshots", ctxMap)
			return err
		}

		for _, sname := range results {
			// Rename the snapshot.
			oldSnapName := strings.SplitN(sname, shared.SnapshotDelimiter, 2)[1]
			baseSnapName := filepath.Base(sname)
			err := d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
				return tx.RenameInstanceSnapshot(d.project, oldName, oldSnapName, baseSnapName)
			})
			if err != nil {
				logger.Error("Failed renaming snapshot", ctxMap)
				return err
			}
		}
	}

	// Rename the instance database entry.
	err = d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		if d.IsSnapshot() {
			oldParts := strings.SplitN(oldName, shared.SnapshotDelimiter, 2)
			newParts := strings.SplitN(newName, shared.SnapshotDelimiter, 2)
			return tx.RenameInstanceSnapshot(d.project, oldParts[0], oldParts[1], newParts[1])
		}

		return tx.RenameInstance(d.project, oldName, newName)
	})
	if err != nil {
		logger.Error("Failed renaming instance", ctxMap)
		return err
	}

	// Rename the logging path.
	newFullName := project.Instance(d.Project(), d.Name())
	os.RemoveAll(shared.LogPath(newFullName))
	if shared.PathExists(d.LogPath()) {
		err := os.Rename(d.LogPath(), shared.LogPath(newFullName))
		if err != nil {
			logger.Error("Failed renaming instance", ctxMap)
			return err
		}
	}

	// Rename the MAAS entry.
	if !d.IsSnapshot() {
		err = d.maasRename(newName)
		if err != nil {
			return err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// Set the new name in the struct.
	d.name = newName
	revert.Add(func() { d.name = oldName })

	// Rename the backups.
	backups, err := d.Backups()
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
	network.UpdateDNSMasqStatic(d.state, "")

	err = d.UpdateBackupFile()
	if err != nil {
		return err
	}

	logger.Info("Renamed instance", ctxMap)

	if d.IsSnapshot() {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-snapshot-renamed",
			fmt.Sprintf("/1.0/virtual-machines/%s", oldName), map[string]interface{}{
				"new_name":      newName,
				"snapshot_name": oldName,
			})
	} else {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-renamed",
			fmt.Sprintf("/1.0/virtual-machines/%s", oldName), map[string]interface{}{
				"new_name": newName,
			})
	}

	revert.Success()
	return nil
}

// Update the instance config.
func (d *qemu) Update(args db.InstanceArgs, userRequested bool) error {
	revert := revert.New()
	defer revert.Fail()

	// Set sane defaults for unset keys.
	if args.Project == "" {
		args.Project = project.Default
	}

	if args.Architecture == 0 {
		args.Architecture = d.architecture
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
		err := instance.ValidConfig(d.state.OS, args.Config, false, false)
		if err != nil {
			return errors.Wrap(err, "Invalid config")
		}

		// Validate the new devices without using expanded devices validation (expensive checks disabled).
		err = instance.ValidDevices(d.state, d.state.Cluster, d.Project(), d.Type(), args.Devices, false)
		if err != nil {
			return errors.Wrap(err, "Invalid devices")
		}
	}

	// Validate the new profiles.
	profiles, err := d.state.Cluster.GetProfileNames(args.Project)
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
	oldDescription := d.Description()
	oldArchitecture := 0
	err = shared.DeepCopy(&d.architecture, &oldArchitecture)
	if err != nil {
		return err
	}

	oldEphemeral := false
	err = shared.DeepCopy(&d.ephemeral, &oldEphemeral)
	if err != nil {
		return err
	}

	oldExpandedDevices := deviceConfig.Devices{}
	err = shared.DeepCopy(&d.expandedDevices, &oldExpandedDevices)
	if err != nil {
		return err
	}

	oldExpandedConfig := map[string]string{}
	err = shared.DeepCopy(&d.expandedConfig, &oldExpandedConfig)
	if err != nil {
		return err
	}

	oldLocalDevices := deviceConfig.Devices{}
	err = shared.DeepCopy(&d.localDevices, &oldLocalDevices)
	if err != nil {
		return err
	}

	oldLocalConfig := map[string]string{}
	err = shared.DeepCopy(&d.localConfig, &oldLocalConfig)
	if err != nil {
		return err
	}

	oldProfiles := []string{}
	err = shared.DeepCopy(&d.profiles, &oldProfiles)
	if err != nil {
		return err
	}

	oldExpiryDate := d.expiryDate

	// Revert local changes if update fails.
	revert.Add(func() {
		d.description = oldDescription
		d.architecture = oldArchitecture
		d.ephemeral = oldEphemeral
		d.expandedConfig = oldExpandedConfig
		d.expandedDevices = oldExpandedDevices
		d.localConfig = oldLocalConfig
		d.localDevices = oldLocalDevices
		d.profiles = oldProfiles
		d.expiryDate = oldExpiryDate
	})

	// Apply the various changes to local vars.
	d.description = args.Description
	d.architecture = args.Architecture
	d.ephemeral = args.Ephemeral
	d.localConfig = args.Config
	d.localDevices = args.Devices
	d.profiles = args.Profiles
	d.expiryDate = args.ExpiryDate

	// Expand the config.
	err = d.expandConfig(nil)
	if err != nil {
		return errors.Wrap(err, "Expand config")
	}

	err = d.expandDevices(nil)
	if err != nil {
		return errors.Wrap(err, "Expand devices")
	}

	// Diff the configurations.
	changedConfig := []string{}
	for key := range oldExpandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range d.expandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices.
	removeDevices, addDevices, updateDevices, allUpdatedKeys := oldExpandedDevices.Update(d.expandedDevices, func(oldDevice deviceConfig.Device, newDevice deviceConfig.Device) []string {
		// This function needs to return a list of fields that are excluded from differences
		// between oldDevice and newDevice. The result of this is that as long as the
		// devices are otherwise identical except for the fields returned here, then the
		// device is considered to be being "updated" rather than "added & removed".
		oldNICType, err := nictype.NICType(d.state, newDevice)
		if err != nil {
			return []string{} // Cannot hot-update due to config error.
		}

		newNICType, err := nictype.NICType(d.state, oldDevice)
		if err != nil {
			return []string{} // Cannot hot-update due to config error.
		}

		if oldDevice["type"] != newDevice["type"] || oldNICType != newNICType {
			return []string{} // Device types aren't the same, so this cannot be an update.
		}

		dev, err := device.New(d, d.state, "", newDevice, nil, nil)
		if err != nil {
			return []string{} // Couldn't create Device, so this cannot be an update.
		}

		return dev.UpdatableFields()
	})

	if userRequested {
		// Do some validation of the config diff.
		err = instance.ValidConfig(d.state.OS, d.expandedConfig, false, true)
		if err != nil {
			return errors.Wrap(err, "Invalid expanded config")
		}

		// Do full expanded validation of the devices diff.
		err = instance.ValidDevices(d.state, d.state.Cluster, d.Project(), d.Type(), d.expandedDevices, true)
		if err != nil {
			return errors.Wrap(err, "Invalid expanded devices")
		}
	}

	isRunning := d.IsRunning()

	// Use the device interface to apply update changes.
	err = d.updateDevices(removeDevices, addDevices, updateDevices, oldExpandedDevices, isRunning, userRequested)
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
			value := d.expandedConfig[key]

			if key == "limits.memory" {
				err = d.updateMemoryLimit(value)
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

	if !d.IsSnapshot() && updateMAAS {
		err = d.maasUpdate(oldExpandedDevices.CloneNative())
		if err != nil {
			return err
		}
	}

	if shared.StringInSlice("security.secureboot", changedConfig) {
		// Re-generate the NVRAM.
		err = d.setupNvram()
		if err != nil {
			return err
		}
	}

	// Finally, apply the changes to the database.
	err = d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Snapshots should update only their descriptions and expiry date.
		if d.IsSnapshot() {
			return tx.UpdateInstanceSnapshot(d.id, d.description, d.expiryDate)
		}

		object, err := tx.GetInstance(d.project, d.name)
		if err != nil {
			return err
		}

		object.Description = d.description
		object.Architecture = d.architecture
		object.Ephemeral = d.ephemeral
		object.ExpiryDate = d.expiryDate
		object.Config = d.localConfig
		object.Profiles = d.profiles
		object.Devices = d.localDevices.CloneNative()

		return tx.UpdateInstance(d.project, d.name, *object)
	})
	if err != nil {
		return errors.Wrap(err, "Failed to update database")
	}

	err = d.UpdateBackupFile()
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "Failed to write backup file")
	}

	// Changes have been applied and recorded, do not revert if an error occurs from here.
	revert.Success()

	if isRunning {
		err = d.writeInstanceData()
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
				"value":     d.expandedConfig[key],
			}

			err = d.devlxdEventSend("config", msg)
			if err != nil {
				return err
			}
		}
	}

	var endpoint string

	if d.IsSnapshot() {
		parentName, snapName, _ := shared.InstanceGetParentAndSnapshotName(d.name)
		endpoint = fmt.Sprintf("/1.0/virtual-machines/%s/snapshots/%s", parentName, snapName)
	} else {
		endpoint = fmt.Sprintf("/1.0/virtual-machines/%s", d.name)
	}

	d.state.Events.SendLifecycle(d.project, "virtual-machine-updated", endpoint, nil)
	return nil
}

// updateMemoryLimit live updates the VM's memory limit by reszing the balloon device.
func (d *qemu) updateMemoryLimit(newLimit string) error {
	if newLimit == "" {
		return nil
	}

	if shared.IsTrue(d.expandedConfig["limits.memory.hugepages"]) {
		return fmt.Errorf("Cannot live update memory limit when using huge pages")
	}

	// Check new size string is valid and convert to bytes.
	newSizeBytes, err := units.ParseByteSizeString(newLimit)
	if err != nil {
		return errors.Wrapf(err, "Invalid memory size")
	}
	newSizeMB := newSizeBytes / 1024 / 1024

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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

func (d *qemu) updateDevices(removeDevices deviceConfig.Devices, addDevices deviceConfig.Devices, updateDevices deviceConfig.Devices, oldExpandedDevices deviceConfig.Devices, instanceRunning bool, userRequested bool) error {
	// Remove devices in reverse order to how they were added.
	for _, dev := range removeDevices.Reversed() {
		if instanceRunning {
			err := d.deviceStop(dev.Name, dev.Config, instanceRunning)
			if err == device.ErrUnsupportedDevType {
				continue // No point in trying to remove device below.
			} else if err != nil {
				return errors.Wrapf(err, "Failed to stop device %q", dev.Name)
			}
		}

		err := d.deviceRemove(dev.Name, dev.Config, instanceRunning)
		if err != nil && err != device.ErrUnsupportedDevType {
			return errors.Wrapf(err, "Failed to remove device %q", dev.Name)
		}

		// Check whether we are about to add the same device back with updated config and
		// if not, or if the device type has changed, then remove all volatile keys for
		// this device (as its an actual removal or a device type change).
		err = d.deviceResetVolatile(dev.Name, dev.Config, addDevices[dev.Name])
		if err != nil {
			return errors.Wrapf(err, "Failed to reset volatile data for device %q", dev.Name)
		}
	}

	// Add devices in sorted order, this ensures that device mounts are added in path order.
	for _, dev := range addDevices.Sorted() {
		err := d.deviceAdd(dev.Name, dev.Config, instanceRunning)
		if err == device.ErrUnsupportedDevType {
			continue // No point in trying to start device below.
		} else if err != nil {
			if userRequested {
				return errors.Wrapf(err, "Failed to add device %q", dev.Name)
			}

			// If update is non-user requested (i.e from a snapshot restore), there's nothing we can
			// do to fix the config and we don't want to prevent the snapshot restore so log and allow.
			logger.Error("Failed to add device, skipping as non-user requested", log.Ctx{"project": d.Project(), "instance": d.Name(), "device": dev.Name, "err": err})
			continue
		}

		if instanceRunning {
			_, err := d.deviceStart(dev.Name, dev.Config, instanceRunning)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to start device %q", dev.Name)
			}
		}
	}

	for _, dev := range updateDevices.Sorted() {
		err := d.deviceUpdate(dev.Name, dev.Config, oldExpandedDevices, instanceRunning)
		if err != nil && err != device.ErrUnsupportedDevType {
			return errors.Wrapf(err, "Failed to update device %q", dev.Name)
		}
	}

	return nil
}

// deviceUpdate loads a new device and calls its Update() function.
func (d *qemu) deviceUpdate(deviceName string, rawConfig deviceConfig.Device, oldDevices deviceConfig.Devices, instanceRunning bool) error {
	dev, _, err := d.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return err
	}

	err = dev.Update(oldDevices, instanceRunning)
	if err != nil {
		return err
	}

	return nil
}

// deviceResetVolatile resets a device's volatile data when its removed or updated in such a way
// that it is removed then added immediately afterwards.
func (d *qemu) deviceResetVolatile(devName string, oldConfig, newConfig deviceConfig.Device) error {
	volatileClear := make(map[string]string)
	devicePrefix := fmt.Sprintf("volatile.%s.", devName)

	newNICType, err := nictype.NICType(d.state, newConfig)
	if err != nil {
		return err
	}

	oldNICType, err := nictype.NICType(d.state, oldConfig)
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

func (d *qemu) removeUnixDevices() error {
	// Check that we indeed have devices to remove.
	if !shared.PathExists(d.DevicesPath()) {
		return nil
	}

	// Load the directory listing.
	dents, err := ioutil.ReadDir(d.DevicesPath())
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
		devicePath := filepath.Join(d.DevicesPath(), f.Name())
		err := os.Remove(devicePath)
		if err != nil {
			logger.Error("Failed removing unix device", log.Ctx{"err": err, "path": devicePath})
		}
	}

	return nil
}

func (d *qemu) removeDiskDevices() error {
	// Check that we indeed have devices to remove.vm
	if !shared.PathExists(d.DevicesPath()) {
		return nil
	}

	// Load the directory listing.
	dents, err := ioutil.ReadDir(d.DevicesPath())
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
		_ = unix.Unmount(filepath.Join(d.DevicesPath(), f.Name()), unix.MNT_DETACH)

		// Remove the entry
		diskPath := filepath.Join(d.DevicesPath(), f.Name())
		err := os.Remove(diskPath)
		if err != nil {
			logger.Error("Failed to remove disk device path", log.Ctx{"err": err, "path": diskPath})
		}
	}

	return nil
}

func (d *qemu) cleanup() {
	// Unmount any leftovers
	d.removeUnixDevices()
	d.removeDiskDevices()

	// Remove the security profiles
	apparmor.InstanceDelete(d.state, d)

	// Remove the devices path
	os.Remove(d.DevicesPath())

	// Remove the shmounts path
	os.RemoveAll(d.ShmountsPath())
}

// cleanupDevices performs any needed device cleanup steps when instance is stopped.
func (d *qemu) cleanupDevices() {
	for _, dev := range d.expandedDevices.Reversed() {
		// Use the device interface if device supports it.
		err := d.deviceStop(dev.Name, dev.Config, false)
		if err == device.ErrUnsupportedDevType {
			continue
		} else if err != nil {
			logger.Errorf("Failed to stop device '%s': %v", dev.Name, err)
		}
	}
}

func (d *qemu) init() error {
	// Compute the expanded config and device list.
	err := d.expandConfig(nil)
	if err != nil {
		return err
	}

	err = d.expandDevices(nil)
	if err != nil {
		return err
	}

	return nil
}

// Delete the instance.
func (d *qemu) Delete(force bool) error {
	ctxMap := log.Ctx{
		"project":   d.project,
		"name":      d.name,
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	logger.Info("Deleting instance", ctxMap)

	// Check if instance is delete protected.
	if !force && shared.IsTrue(d.expandedConfig["security.protection.delete"]) && !d.IsSnapshot() {
		return fmt.Errorf("Instance is protected")
	}

	// Check if we're dealing with "lxd import".
	// TODO consider lxd import detection for VMs.
	isImport := false

	// Attempt to initialize storage interface for the instance.
	pool, err := d.getStoragePool()
	if err != nil && err != db.ErrNoSuchObject {
		return err
	} else if pool != nil {
		if d.IsSnapshot() {
			if !isImport {
				// Remove snapshot volume and database record.
				err = pool.DeleteInstanceSnapshot(d, nil)
				if err != nil {
					return err
				}
			}
		} else {
			// Remove all snapshots by initialising each snapshot as an Instance and
			// calling its Delete function.
			err := instance.DeleteSnapshots(d.state, d.Project(), d.Name())
			if err != nil {
				return err
			}

			if !isImport {
				// Remove the storage volume, snapshot volumes and database records.
				err = pool.DeleteInstance(d, nil)
				if err != nil {
					return err
				}
			}
		}
	}

	// Perform other cleanup steps if not snapshot.
	if !d.IsSnapshot() {
		// Remove all backups.
		backups, err := d.Backups()
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
		err = d.maasDelete()
		if err != nil {
			logger.Error("Failed deleting instance MAAS record", log.Ctx{"project": d.Project(), "instance": d.Name(), "err": err})
			return err
		}

		// Run device removal function for each device.
		for k, m := range d.expandedDevices {
			err = d.deviceRemove(k, m, false)
			if err != nil && err != device.ErrUnsupportedDevType {
				return errors.Wrapf(err, "Failed to remove device %q", k)
			}
		}

		// Clean things up.
		d.cleanup()
	}

	// Remove the database record of the instance or snapshot instance.
	if err := d.state.Cluster.DeleteInstance(d.Project(), d.Name()); err != nil {
		logger.Error("Failed deleting instance entry", log.Ctx{"project": d.Project(), "instance": d.Name(), "err": err})
		return err
	}

	logger.Info("Deleted instance", ctxMap)

	if d.IsSnapshot() {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-snapshot-deleted",
			fmt.Sprintf("/1.0/virtual-machines/%s", d.name), map[string]interface{}{
				"snapshot_name": d.name,
			})
	} else {
		d.state.Events.SendLifecycle(d.project, "virtual-machine-deleted",
			fmt.Sprintf("/1.0/virtual-machines/%s", d.name), nil)
	}

	return nil
}

func (d *qemu) deviceAdd(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) error {
	dev, _, err := d.deviceLoad(deviceName, rawConfig)
	if err != nil {
		return err
	}

	if instanceRunning && !dev.CanHotPlug() {
		return fmt.Errorf("Device cannot be added when instance is running")
	}

	return dev.Add()
}

func (d *qemu) deviceRemove(deviceName string, rawConfig deviceConfig.Device, instanceRunning bool) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"device": deviceName, "type": rawConfig["type"], "project": d.Project(), "instance": d.Name()})

	dev, _, err := d.deviceLoad(deviceName, rawConfig)

	// If deviceLoad fails with unsupported device type then return.
	if err == device.ErrUnsupportedDevType {
		return err
	}

	// If deviceLoad fails for any other reason then just log the error and proceed, as in the
	// scenario that a new version of LXD has additional validation restrictions than older
	// versions we still need to allow previously valid devices to be stopped.
	if err != nil {
		// If there is no device returned, then we cannot proceed, so return as error.
		if dev == nil {
			return fmt.Errorf("Device remove validation failed for %q: %v", deviceName, err)
		}

		logger.Error("Device remove validation failed", log.Ctx{"err": err})
	}

	if instanceRunning && !dev.CanHotPlug() {
		return fmt.Errorf("Device cannot be removed when instance is running")
	}

	return dev.Remove()
}

// Export publishes the instance.
func (d *qemu) Export(w io.Writer, properties map[string]string) (api.ImageMetadata, error) {
	ctxMap := log.Ctx{
		"project":   d.project,
		"name":      d.name,
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	meta := api.ImageMetadata{}

	if d.IsRunning() {
		return meta, fmt.Errorf("Cannot export a running instance as an image")
	}

	logger.Info("Exporting instance", ctxMap)

	// Start the storage.
	mountInfo, err := d.mount()
	if err != nil {
		logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}
	defer d.unmount()

	// Create the tarball.
	tarWriter := instancewriter.NewInstanceTarWriter(w, nil)

	// Path inside the tar image is the pathname starting after cDir.
	cDir := d.Path()
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
		if d.IsSnapshot() {
			parentName, _, _ := shared.InstanceGetParentAndSnapshotName(d.name)
			parent, err := instance.LoadByProjectAndName(d.state, d.project, parentName)
			if err != nil {
				tarWriter.Close()
				logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}

			arch, _ = osarch.ArchitectureName(parent.Architecture())
		} else {
			arch, _ = osarch.ArchitectureName(d.architecture)
		}

		if arch == "" {
			arch, err = osarch.ArchitectureName(d.state.OS.Architectures[0])
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
	fnam = d.TemplatesPath()
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
func (d *qemu) Migrate(args *instance.CriuMigrationArgs) error {
	return instance.ErrNotImplemented
}

// CGroupSet is not implemented for VMs.
func (d *qemu) CGroup() (*cgroup.CGroup, error) {
	return nil, instance.ErrNotImplemented
}

// FileExists is not implemented for VMs.
func (d *qemu) FileExists(path string) error {
	return instance.ErrNotImplemented
}

// FilePull retrieves a file from the instance.
func (d *qemu) FilePull(srcPath string, dstPath string) (int64, int64, os.FileMode, string, []string, error) {
	client, err := d.getAgentClient()
	if err != nil {
		return 0, 0, 0, "", nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", d.Name(), err)
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
func (d *qemu) FilePush(fileType string, srcPath string, dstPath string, uid int64, gid int64, mode int, write string) error {
	client, err := d.getAgentClient()
	if err != nil {
		return err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", d.Name(), err)
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
func (d *qemu) FileRemove(path string) error {
	// Connect to the agent.
	client, err := d.getAgentClient()
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
func (d *qemu) Console(protocol string) (*os.File, chan error, error) {
	switch protocol {
	case instance.ConsoleTypeConsole:
		return d.console()
	case instance.ConsoleTypeVGA:
		return d.vga()
	default:
		return nil, nil, fmt.Errorf("Unknown protocol %q", protocol)
	}
}

func (d *qemu) console() (*os.File, chan error, error) {
	chDisconnect := make(chan error, 1)

	// Avoid duplicate connects.
	vmConsoleLock.Lock()
	if vmConsole[d.id] {
		vmConsoleLock.Unlock()
		return nil, nil, fmt.Errorf("There is already an active console for this instance")
	}
	vmConsoleLock.Unlock()

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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
	vmConsole[d.id] = true
	vmConsoleLock.Unlock()

	// Handle console disconnection.
	go func() {
		<-chDisconnect

		vmConsoleLock.Lock()
		delete(vmConsole, d.id)
		vmConsoleLock.Unlock()
	}()

	return console, chDisconnect, nil
}

func (d *qemu) vga() (*os.File, chan error, error) {
	// Open the spice socket
	conn, err := net.Dial("unix", d.spicePath())
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Connect to SPICE socket %q", d.spicePath())
	}

	file, err := (conn.(*net.UnixConn)).File()
	if err != nil {
		return nil, nil, errors.Wrap(err, "Get socket file")
	}
	conn.Close()

	return file, nil, nil
}

// Exec a command inside the instance.
func (d *qemu) Exec(req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (instance.Cmd, error) {
	revert := revert.New()
	defer revert.Fail()

	client, err := d.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", d.Name(), err)
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
func (d *qemu) Render(options ...func(response interface{}) error) (interface{}, interface{}, error) {
	if d.IsSnapshot() {
		// Prepare the ETag
		etag := []interface{}{d.expiryDate}

		snapState := api.InstanceSnapshot{
			CreatedAt:       d.creationDate,
			ExpandedConfig:  d.expandedConfig,
			ExpandedDevices: d.expandedDevices.CloneNative(),
			LastUsedAt:      d.lastUsedDate,
			Name:            strings.SplitN(d.name, "/", 2)[1],
			Stateful:        d.stateful,
			Size:            -1, // Default to uninitialised/error state (0 means no CoW usage).
		}
		snapState.Architecture = d.architectureName
		snapState.Config = d.localConfig
		snapState.Devices = d.localDevices.CloneNative()
		snapState.Ephemeral = d.ephemeral
		snapState.Profiles = d.profiles
		snapState.ExpiresAt = d.expiryDate

		for _, option := range options {
			err := option(&snapState)
			if err != nil {
				return nil, nil, err
			}
		}

		return &snapState, etag, nil
	}

	// Prepare the ETag
	etag := []interface{}{d.architecture, d.localConfig, d.localDevices, d.ephemeral, d.profiles}

	instState := api.Instance{
		ExpandedConfig:  d.expandedConfig,
		ExpandedDevices: d.expandedDevices.CloneNative(),
		Name:            d.name,
		Status:          d.statusCode().String(),
		StatusCode:      d.statusCode(),
		Location:        d.node,
		Type:            d.Type().String(),
	}

	instState.Description = d.description
	instState.Architecture = d.architectureName
	instState.Config = d.localConfig
	instState.CreatedAt = d.creationDate
	instState.Devices = d.localDevices.CloneNative()
	instState.Ephemeral = d.ephemeral
	instState.LastUsedAt = d.lastUsedDate
	instState.Profiles = d.profiles
	instState.Stateful = d.stateful

	for _, option := range options {
		err := option(&instState)
		if err != nil {
			return nil, nil, err
		}
	}

	return &instState, etag, nil
}

// RenderFull returns all info about the instance.
func (d *qemu) RenderFull() (*api.InstanceFull, interface{}, error) {
	if d.IsSnapshot() {
		return nil, nil, fmt.Errorf("RenderFull doesn't work with snapshots")
	}

	// Get the Instance struct.
	base, etag, err := d.Render()
	if err != nil {
		return nil, nil, err
	}

	// Convert to InstanceFull.
	vmState := api.InstanceFull{Instance: *base.(*api.Instance)}

	// Add the InstanceState.
	vmState.State, err = d.RenderState()
	if err != nil {
		return nil, nil, err
	}

	// Add the InstanceSnapshots.
	snaps, err := d.Snapshots()
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
	backups, err := d.Backups()
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
func (d *qemu) RenderState() (*api.InstanceState, error) {
	var err error

	status := &api.InstanceState{}
	statusCode := d.statusCode()
	pid, _ := d.pid()

	if statusCode == api.Running {
		// Try and get state info from agent.
		status, err = d.agentGetState()
		if err != nil {
			if err != errQemuAgentOffline {
				logger.Warn("Could not get VM state from agent", log.Ctx{"project": d.Project(), "instance": d.Name(), "err": err})
			}

			// Fallback data if agent is not reachable.
			status = &api.InstanceState{}
			status.Processes = -1
			networks := map[string]api.InstanceStateNetwork{}
			for k, m := range d.ExpandedDevices() {
				if m["type"] != "nic" {
					continue
				}

				dev, _, err := d.deviceLoad(k, m)
				if err != nil {
					logger.Warn("Could not load device", log.Ctx{"project": d.Project(), "instance": d.Name(), "device": k, "err": err})
					continue
				}

				// Only some NIC types support fallback state mechanisms when there is no agent.
				nic, ok := dev.(device.NICState)
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
		for k, m := range d.ExpandedDevices() {
			// We only care about nics.
			if m["type"] != "nic" {
				continue
			}

			// Get hwaddr from static or volatile config.
			hwaddr := m["hwaddr"]
			if hwaddr == "" {
				hwaddr = d.localConfig[fmt.Sprintf("volatile.%s.hwaddr", k)]
			}

			// We have to match on hwaddr as device name can be different from the configured device
			// name when reported from the lxd-agent inside the VM (due to the guest OS choosing name).
			for netName, netStatus := range status.Network {
				if netStatus.Hwaddr == hwaddr {
					if netStatus.HostName == "" {
						netStatus.HostName = d.localConfig[fmt.Sprintf("volatile.%s.host_name", k)]
						status.Network[netName] = netStatus
					}
				}
			}
		}
	}

	status.Pid = int64(pid)
	status.Status = statusCode.String()
	status.StatusCode = statusCode
	status.Disk, err = d.diskState()
	if err != nil && err != storageDrivers.ErrNotSupported {
		logger.Warn("Error getting disk usage", log.Ctx{"project": d.Project(), "instance": d.Name(), "err": err})
	}

	return status, nil
}

// diskState gets disk usage info.
func (d *qemu) diskState() (map[string]api.InstanceStateDisk, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	// Get the root disk device config.
	rootDiskName, _, err := shared.GetRootDiskDevice(d.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	usage, err := pool.GetInstanceUsage(d)
	if err != nil {
		return nil, err
	}

	disk := map[string]api.InstanceStateDisk{}
	disk[rootDiskName] = api.InstanceStateDisk{Usage: usage}
	return disk, nil
}

// agentGetState connects to the agent inside of the VM and does
// an API call to get the current state.
func (d *qemu) agentGetState() (*api.InstanceState, error) {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return nil, err
	}

	if !monitor.AgentReady() {
		return nil, errQemuAgentOffline
	}

	client, err := d.getAgentClient()
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
func (d *qemu) IsRunning() bool {
	state := d.State()
	return state != "STOPPED"
}

// IsFrozen returns whether the instance frozen or not.
func (d *qemu) IsFrozen() bool {
	return d.State() == "FROZEN"
}

// DeviceEventHandler handles events occurring on the instance's devices.
func (d *qemu) DeviceEventHandler(runConf *deviceConfig.RunConfig) error {
	return fmt.Errorf("DeviceEventHandler Not implemented")
}

// vsockID returns the vsock context ID, 3 being the first ID that can be used.
func (d *qemu) vsockID() int {
	return d.id + 3
}

// InitPID returns the instance's current process ID.
func (d *qemu) InitPID() int {
	pid, _ := d.pid()
	return pid
}

func (d *qemu) statusCode() api.StatusCode {
	// Shortcut to avoid spamming QMP during ongoing operations.
	op := operationlock.Get(d.id)
	if op != nil {
		if op.Action() == "start" {
			return api.Stopped
		}

		if op.Action() == "stop" {
			return api.Running
		}
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		// If cannot connect to monitor, but qemu process in pid file still exists, then likely qemu
		// has crashed/hung and this instance is in an error state.
		pid, _ := d.pid()
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
func (d *qemu) State() string {
	return strings.ToUpper(d.statusCode().String())
}

// EarlyLogFilePath returns the instance's early log path.
func (d *qemu) EarlyLogFilePath() string {
	return filepath.Join(d.LogPath(), "qemu.early.log")
}

// LogFilePath returns the instance's log path.
func (d *qemu) LogFilePath() string {
	return filepath.Join(d.LogPath(), "qemu.log")
}

// StoragePool returns the name of the instance's storage pool.
func (d *qemu) StoragePool() (string, error) {
	poolName, err := d.state.Cluster.GetInstancePool(d.Project(), d.Name())
	if err != nil {
		return "", err
	}

	return poolName, nil
}

// FillNetworkDevice takes a nic or infiniband device type and enriches it with automatically
// generated name and hwaddr properties if these are missing from the device.
func (d *qemu) FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
	var err error

	newDevice := m.Clone()
	updateKey := func(key string, value string) error {
		tx, err := d.state.Cluster.Begin()
		if err != nil {
			return err
		}

		err = db.CreateInstanceConfig(tx, d.id, map[string]string{key: value})
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

	nicType, err := nictype.NICType(d.state, m)
	if err != nil {
		return nil, err
	}

	// Fill in the MAC address
	if !shared.StringInSlice(nicType, []string{"physical", "ipvlan", "sriov"}) && m["hwaddr"] == "" {
		configKey := fmt.Sprintf("volatile.%s.hwaddr", name)
		volatileHwaddr := d.localConfig[configKey]
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
					value, err1 := d.state.Cluster.GetInstanceConfig(d.id, configKey)
					if err1 != nil || value == "" {
						return err
					}

					d.localConfig[configKey] = value
					d.expandedConfig[configKey] = value
					return nil
				}

				d.localConfig[configKey] = volatileHwaddr
				d.expandedConfig[configKey] = volatileHwaddr
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
func (d *qemu) maasInterfaces(devices map[string]map[string]string) ([]maas.ContainerInterface, error) {
	interfaces := []maas.ContainerInterface{}
	for k, m := range devices {
		if m["type"] != "nic" {
			continue
		}

		if m["maas.subnet.ipv4"] == "" && m["maas.subnet.ipv6"] == "" {
			continue
		}

		m, err := d.FillNetworkDevice(k, m)
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

func (d *qemu) maasRename(newName string) error {
	maasURL, err := cluster.ConfigGetString(d.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

	if maasURL == "" {
		return nil
	}

	interfaces, err := d.maasInterfaces(d.expandedDevices.CloneNative())
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
		return d.maasUpdate(nil)
	}

	return d.state.MAAS.RenameContainer(d, newName)
}

func (d *qemu) maasDelete() error {
	maasURL, err := cluster.ConfigGetString(d.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

	if maasURL == "" {
		return nil
	}

	interfaces, err := d.maasInterfaces(d.expandedDevices.CloneNative())
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

func (d *qemu) maasUpdate(oldDevices map[string]map[string]string) error {
	// Check if MAAS is configured
	maasURL, err := cluster.ConfigGetString(d.state.Cluster, "maas.api.url")
	if err != nil {
		return err
	}

	if maasURL == "" {
		return nil
	}

	// Check if there's something that uses MAAS
	interfaces, err := d.maasInterfaces(d.expandedDevices.CloneNative())
	if err != nil {
		return err
	}

	var oldInterfaces []maas.ContainerInterface
	if oldDevices != nil {
		oldInterfaces, err = d.maasInterfaces(oldDevices)
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

// UpdateBackupFile writes the instance's backup.yaml file to storage.
func (d *qemu) UpdateBackupFile() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	return pool.UpdateInstanceBackupFile(d, nil)
}

// cpuTopology takes a user cpu range and returns the number of sockets, cores and threads to configure
// as well as a map of vcpu to threadid for pinning and a map of numa nodes to vcpus for NUMA layout.
func (d *qemu) cpuTopology(limit string) (int, int, int, map[uint64]uint64, map[uint64][]uint64, error) {
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
		logger.Warnf("Instance '%s' uses a CPU pinning profile which doesn't match hardware layout", project.Instance(d.Project(), d.Name()))

		// Fallback on pretending everything are cores.
		nrSockets = 1
		nrCores = len(vcpus)
		nrThreads = 1
	}

	return nrSockets, nrCores, nrThreads, vcpus, numaNodes, nil
}

func (d *qemu) devlxdEventSend(eventType string, eventMessage interface{}) error {
	event := shared.Jmap{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

	client, err := d.getAgentClient()
	if err != nil {
		return err
	}

	agent, err := lxdClient.ConnectLXDHTTP(nil, client)
	if err != nil {
		logger.Errorf("Failed to connect to lxd-agent on %s: %v", d.Name(), err)
		return fmt.Errorf("Failed to connect to lxd-agent")
	}
	defer agent.Disconnect()

	_, _, err = agent.RawQuery("POST", "/1.0/events", &event, "")
	if err != nil {
		return err
	}

	return nil
}

func (d *qemu) writeInstanceData() error {
	// Only write instance-data file if security.devlxd is true.
	if !shared.IsTrue(d.expandedConfig["security.devlxd"]) {
		return nil
	}

	// Instance data for devlxd.
	configDrivePath := filepath.Join(d.Path(), "config")
	userConfig := make(map[string]string)

	for k, v := range d.ExpandedConfig() {
		if !strings.HasPrefix(k, "user.") {
			continue
		}

		userConfig[k] = v
	}

	out, err := json.Marshal(struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config,omitempty"`
	}{d.Name(), userConfig})
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(configDrivePath, "instance-data"), out, 0600)
	if err != nil {
		return err
	}

	return nil
}
