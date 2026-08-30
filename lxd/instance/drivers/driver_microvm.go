package drivers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/device"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instance/operationlock"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/subprocess"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
)

// libkrunWatchers tracks active pidfd exit-watcher goroutines for libkrun helper processes.
// The map key is "project/instance" and the value is the PID being watched.
// This ensures that when a forklibkrun process exits unexpectedly, LXD detects it and
// triggers onStop to clean up instance resources on the host — mirroring how QEMU's
// QMP socket disconnect fires a synthetic SHUTDOWN event that calls onStop.
var (
	libkrunWatchers     = map[string]int{}
	libkrunWatchersLock sync.Mutex
)

// libkrunVsockProxy is a single shared unix socket proxy that accepts connections from libkrun
// guest agents (agent→LXD direction) and forwards them to LXD's dedicated VM unix socket
// listener. All libkrun VMs on a host share this one listener; the TLS certificate carried
// inside each connection identifies the individual VM to the LXD daemon, matching the behaviour
// of native kernel vsock used by QEMU VMs.
var (
	libkrunVsockProxyOnce   sync.Once
	libkrunVsockProxySocket string // absolute path of the shared unix socket
)

// libkrunVsockProxyPort is the guest-facing vsock port intercepted by libkrun
// and forwarded to the shared unix proxy socket on the host (agent→LXD path).
// Keep this stable and non-reserved so the guest never depends on host-side
// dynamic vsock endpoint ports.
const libkrunVsockProxyPort uint32 = 8444

// microvmLoad creates a MicroVM instance from the supplied InstanceArgs.
func microvmLoad(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, error) {
	// Create the instance struct.
	d := microvmInstantiate(s, args, nil, p)

	// Expand config and devices.
	err := d.expandConfig()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// microvmInstantiate creates a MicroVM struct without expanding config.
func microvmInstantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices, p api.Project) *microvm {
	d := &microvm{
		qemu: qemu{
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
				logger:       logger.AddContext(logger.Ctx{"instanceType": args.Type, "instance": args.Name, "project": args.Project}),
				name:         args.Name,
				node:         args.Node,
				profiles:     args.Profiles,
				project:      p,
				isSnapshot:   args.Snapshot,
				stateful:     args.Stateful,
			},
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

// microvmCreate creates a new storage volume record and returns an initialised Instance.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func microvmCreate(ctx context.Context, s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the instance struct.
	d := &microvm{
		qemu: qemu{
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
				logger:       logger.AddContext(logger.Ctx{"instanceType": args.Type, "instance": args.Name, "project": args.Project}),
				name:         args.Name,
				node:         args.Node,
				profiles:     args.Profiles,
				project:      p,
				isSnapshot:   args.Snapshot,
				stateful:     args.Stateful,
			},
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

	if args.Snapshot {
		d.logger.Info("Creating instance snapshot", logger.Ctx{"ephemeral": d.ephemeral})
	} else {
		d.logger.Info("Creating instance", logger.Ctx{"ephemeral": d.ephemeral})
	}

	// Load the config.
	err = d.init()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed expanding config: %w", err)
	}

	// When not a snapshot, perform full validation.
	if !args.Snapshot {
		// Validate expanded config (allows mixed instance types for profiles).
		err = instance.ValidConfig(s.OS, d.expandedConfig, true, instancetype.Any)
		if err != nil {
			return nil, nil, fmt.Errorf("Invalid config: %w", err)
		}

		err = instance.ValidDevices(s, d.project, d.Type(), d.localDevices, d.expandedDevices)
		if err != nil {
			return nil, nil, fmt.Errorf("Invalid devices: %w", err)
		}
	}

	// Retrieve the instance's storage pool.
	_, rootDiskDevice, err := d.getRootDiskDevice()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting root disk: %w", err)
	}

	if rootDiskDevice["pool"] == "" {
		return nil, nil, errors.New("The instance's root device is missing the pool property")
	}

	// Initialize the storage pool.
	d.storagePool, err = storagePools.LoadByName(d.state, rootDiskDevice["pool"])
	if err != nil {
		return nil, nil, fmt.Errorf("Failed loading storage pool: %w", err)
	}

	// Validate that the storage pool supports MicroVM.
	if d.storagePool.Driver().Info().Name != "dir" {
		return nil, nil, errors.New("MicroVM instances are only supported on dir storage pools")
	}

	volType, err := storagePools.InstanceTypeToVolumeType(d.Type())
	if err != nil {
		return nil, nil, err
	}

	storagePoolSupported := slices.Contains(d.storagePool.Driver().Info().VolumeTypes, volType)

	if !storagePoolSupported {
		return nil, nil, errors.New("Storage pool does not support instance type")
	}

	if !d.IsSnapshot() {
		// Add devices to instance.
		cleanup, err := d.devicesAdd(d, false)
		if err != nil {
			return nil, nil, err
		}

		revert.Add(cleanup)
	}

	if d.isSnapshot {
		d.logger.Info("Created instance snapshot", logger.Ctx{"ephemeral": d.ephemeral})
	} else {
		d.logger.Info("Created instance", logger.Ctx{"ephemeral": d.ephemeral})
	}

	if d.isSnapshot {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotCreated.Event(ctx, d, nil))
	} else {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceCreated.Event(ctx, d, map[string]any{
			"type":         api.InstanceTypeMicroVM,
			"storage-pool": d.storagePool.Name(),
			"location":     d.Location(),
		}))
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return d, cleanup, err
}

// microvm is backed by libkrun.
type microvm struct {
	qemu
}

// Type returns the instance type.
func (d *microvm) Type() instancetype.Type {
	return instancetype.MicroVM
}

// getKernelPath returns the path to the kernel to use for booting.
func (d *microvm) getKernelPath() string {
	kernelPath := d.expandedConfig["microvm.kernel_path"]
	if kernelPath == "" {
		// Default to the host's current kernel.
		kernelPath = "/boot/vmlinuz"
	}

	// Resolve symlinks to get the actual kernel file.
	resolved, err := filepath.EvalSymlinks(kernelPath)
	if err == nil {
		kernelPath = resolved
	}

	return kernelPath
}

// KernelPath returns the path to the kernel to use for booting.
func (d *microvm) KernelPath() string {
	return d.getKernelPath()
}

// getInitrdPath returns the path to the initrd to use for booting.
func (d *microvm) getInitrdPath() string {
	initrdPath := d.expandedConfig["microvm.initrd_path"]
	switch initrdPath {
	case "":
		initrdPath = "/boot/initrd.img"
	case "none":
		return ""
	}

	// Resolve symlinks to get the actual initrd file.
	resolved, err := filepath.EvalSymlinks(initrdPath)
	if err == nil {
		initrdPath = resolved
	}

	return initrdPath
}

// InitrdPath returns the path to the initrd to use for booting.
func (d *microvm) InitrdPath() string {
	return d.getInitrdPath()
}

// getKernelAppend returns additional kernel command line arguments.
func (d *microvm) getKernelAppend() string {
	return d.expandedConfig["microvm.kernel_append"]
}

// libkrunPidFilePath returns the path to the libkrun helper PID file.
func (d *microvm) libkrunPidFilePath() string {
	return filepath.Join(d.LogPath(), "libkrun.pid")
}

// libkrunConsolePath returns the path to the libkrun console socket bridged by the helper.
func (d *microvm) libkrunConsolePath() string {
	return filepath.Join(d.LogPath(), "libkrun.console")
}

// libkrunAgentSocketPath returns the per-VM unix socket path that libkrun creates for the
// LXD→agent vsock bridge. The forklibkrun helper passes this to libkrun's AddVsockPort2 so
// that the LXD daemon can connect here to reach the in-guest lxd-agent.
func (d *microvm) libkrunAgentSocketPath() string {
	return filepath.Join(d.LogPath(), "libkrun.agent.sock")
}

// ensureLibkrunVsockProxy starts the shared unix socket proxy for agent→LXD vsock
// traffic exactly once per daemon lifetime. It returns the socket path and the vsock port
// that forklibkrun should tell guests to dial. If the LXD vsock endpoint is not available
// it returns empty strings and a zero port.
func (d *microvm) ensureLibkrunVsockProxy() (socketPath string, guestProxyPort uint32) {
	vsockUnixSocket := shared.VarPath("vsock-unix.socket")
	if !shared.PathExists(vsockUnixSocket) {
		d.logger.Warn("LXD VM unix socket not available; libkrun agent→LXD proxy not started", logger.Ctx{"path": vsockUnixSocket})
		return "", 0
	}

	libkrunVsockProxyOnce.Do(func() {
		socketPath := shared.VarPath("libkrun-vsock-proxy.sock")
		_ = os.Remove(socketPath)

		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			d.logger.Error("Failed creating libkrun vsock proxy socket", logger.Ctx{"path": socketPath, "err": err})
			return
		}

		libkrunVsockProxySocket = socketPath

		d.logger.Debug("Started libkrun vsock proxy", logger.Ctx{"socket": socketPath, "backend": vsockUnixSocket})

		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}

				go libkrunVsockProxyBridge(conn, vsockUnixSocket)
			}
		}()
	})

	return libkrunVsockProxySocket, libkrunVsockProxyPort
}

// libkrunVsockProxyBridge proxies a single connection from a libkrun guest agent to
// LXD's VM unix socket listener, which is already a native TLS endpoint.
func libkrunVsockProxyBridge(conn net.Conn, vsockUnixSocket string) {
	defer func() { _ = conn.Close() }()

	unixConn, err := net.Dial("unix", vsockUnixSocket)
	if err != nil {
		return
	}

	defer func() { _ = unixConn.Close() }()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(unixConn, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, unixConn)
		done <- struct{}{}
	}()

	<-done
}

// Start starts the MicroVM instance using using libkrun with direct kernel boot.
func (d *microvm) Start(ctx context.Context, stateful bool, progressReporter ioprogress.ProgressReporter) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	d.logger.Debug("Start started", logger.Ctx{"stateful": stateful})
	defer d.logger.Debug("Start finished", logger.Ctx{"stateful": stateful})

	// Check that we are startable before creating an operation lock.
	err = d.validateStartup(stateful, d.statusCode())
	if err != nil {
		return err
	}

	// MicroVM only supports x86_64.
	if d.architecture != osarch.ARCH_64BIT_INTEL_X86 {
		return errors.New("MicroVM is only supported on x86_64 architecture")
	}

	// MicroVM does not support stateful snapshots.
	if stateful {
		return errors.New("MicroVM does not support stateful snapshots")
	}

	// Validate kernel and initrd paths.
	kernelPath := d.getKernelPath()
	if !shared.PathExists(kernelPath) {
		return fmt.Errorf("Kernel not found at %q", kernelPath)
	}

	initrdPath := d.getInitrdPath()
	if initrdPath != "" && !shared.PathExists(initrdPath) {
		return fmt.Errorf("Initrd not found at %q", initrdPath)
	}

	// Setup a new operation.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStart, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, false)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusableSucceeded) {
			// An existing matching operation has now succeeded, return.
			return nil
		}

		return fmt.Errorf("Failed creating instance start operation: %w", err)
	}

	defer op.Done(err)

	revert := revert.New()
	defer revert.Fail()

	// Rotate the log file.
	logfile := d.LogFilePath()
	err = os.Rename(logfile, logfile+".old")
	if err != nil && !os.IsNotExist(err) {
		op.Done(err)
		return err
	}

	// Remove old pid file if needed.
	pidFilePath := d.pidFilePath()
	err = os.Remove(pidFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		op.Done(err)
		return fmt.Errorf("Failed removing old PID file %q: %w", pidFilePath, err)
	}

	// Mount the instance's config volume.
	mountInfo, err := d.mount()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Add(func() { _ = d.unmount() })

	// libkrun does not consume the host vhost-vsock FD, but the ID is still recorded
	// in volatile so a running VM survives a DB record deletion.
	vsockID, vsockF, err := d.nextVsockID()
	if err != nil {
		return err
	}

	defer func() { _ = vsockF.Close() }()

	volatileSet := make(map[string]string)

	// Update vsock ID in volatile if needed for recovery.
	oldVsockID := d.localConfig["volatile.vsock_id"]
	newVsockID := strconv.FormatUint(uint64(vsockID), 10)
	if oldVsockID != newVsockID {
		volatileSet["volatile.vsock_id"] = newVsockID
	}

	// Generate UUID if not present.
	instUUID := d.localConfig["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New().String()
		volatileSet["volatile.uuid"] = instUUID
	}

	// Generate the config drive.
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

	// Apply any volatile changes that need to be made.
	err = d.VolatileSet(volatileSet)
	if err != nil {
		op.Done(err)
		return err
	}

	devConfs := make([]*deviceConfig.RunConfig, 0, len(d.expandedDevices))
	postStartHooks := []func() error{}

	sortedDevices := d.expandedDevices.Sorted()
	startDevices := make([]device.Device, 0, len(sortedDevices))

	// Load devices in sorted order, this ensures that device mounts are added in path order.
	for _, entry := range sortedDevices {
		dev, err := d.deviceLoad(d, entry.Name, entry.Config)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue // Skip unsupported device.
			}

			err = fmt.Errorf("Failed start validation for device %q: %w", entry.Name, err)
			op.Done(err)
			return err
		}

		// Run pre-start of check all devices before starting any device.
		err = dev.PreStartCheck()
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed pre-start check for device %q: %w", dev.Name(), err)
		}

		startDevices = append(startDevices, dev)
	}

	// Start devices in order.
	for i := range startDevices {
		dev := startDevices[i]

		// Start the device.
		runConf, err := d.deviceStart(dev, false)
		if err != nil {
			err = fmt.Errorf("Failed starting device %q: %w", dev.Name(), err)
			op.Done(err)
			return err
		}

		revert.Add(func() {
			err := d.deviceStop(dev, false, "")
			if err != nil {
				d.logger.Error("Failed cleaning up device", logger.Ctx{"device": dev.Name(), "err": err})
			}
		})

		if runConf == nil {
			continue
		}

		if runConf.Revert != nil {
			revert.Add(runConf.Revert)
		}

		// Add post-start hooks
		if len(runConf.PostHooks) > 0 {
			postStartHooks = append(postStartHooks, runConf.PostHooks...)
		}

		devConfs = append(devConfs, runConf)
	}

	// Setup the config drive readonly bind mount.
	configMntPath := d.configDriveMountPath()
	err = d.configDriveMountPathClear()
	if err != nil {
		err = fmt.Errorf("Failed cleaning config drive mount path %q: %w", configMntPath, err)
		op.Done(err)
		return err
	}

	err = os.Mkdir(configMntPath, 0700)
	if err != nil {
		err = fmt.Errorf("Failed creating device mount path %q for config drive: %w", configMntPath, err)
		op.Done(err)
		return err
	}

	revert.Add(func() { _ = d.configDriveMountPathClear() })

	// Mount the config drive device as readonly.
	configSrcPath := filepath.Join(d.Path(), "config")
	err = device.DiskMount(configSrcPath, configMntPath, false, "", []string{"ro"}, "none")
	if err != nil {
		err = fmt.Errorf("Failed mounting device mount path %q for config drive: %w", configMntPath, err)
		op.Done(err)
		return err
	}

	// Get the root disk path.
	rootDiskPath := ""
	for _, runConf := range devConfs {
		for _, mount := range runConf.Mounts {
			if mount.TargetPath == "/" {
				devSource, isPath := mountInfo.DevSource.(deviceConfig.DevSourcePath)
				if isPath {
					rootDiskPath = devSource.Path
				}

				break
			}
		}
	}

	if rootDiskPath == "" {
		err = errors.New("No root disk found")
		op.Done(err)
		return err
	}

	// Collect NIC configurations and open TAP file handles.
	var nics []microVMNIC
	for _, runConf := range devConfs {
		if len(runConf.NetworkInterface) > 0 {
			var devName, nicName, hwaddr string
			for _, nicItem := range runConf.NetworkInterface {
				switch nicItem.Key {
				case "devName":
					devName = nicItem.Value
				case "link":
					nicName = nicItem.Value
				case "hwaddr":
					hwaddr = nicItem.Value
				}
			}

			if nicName == "" || hwaddr == "" {
				continue
			}

			// libkrun opens the host TAP device by name itself (inside the forklibkrun child),
			// so LXD does not pre-open a TAP file descriptor for it. The TAP is also created as
			// single-queue for libkrun, so opening it here with IFF_MULTI_QUEUE would fail.
			nics = append(nics, microVMNIC{
				devName: devName,
				nicName: nicName,
				hwaddr:  hwaddr,
			})
		}
	}

	// Configure memory limit.
	memSize := d.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = QEMUDefaultMemSize
	}

	// Parse memory size to bytes and convert to MB.
	memSizeBytes, err := parseMemoryStr(memSize)
	if err != nil {
		err = fmt.Errorf("limits.memory invalid: %w", err)
		op.Done(err)
		return err
	}

	memSizeMB := memSizeBytes / 1024 / 1024

	// Build kernel command line.
	// The microvm machine type has no legacy ISA 8250 UART, so the console must use the
	// virtio-console (hvc0) device wired up by nforklibkrun. Avoid earlyprintk=virtio
	// (not a valid earlyprintk backend) and avoid reboot=t/panic=-1, which would silently
	// triple-fault reboot-loop (100% CPU, no output) if the guest panics before hvc0 comes up.
	kernelAppend := "console=hvc0 root=/dev/vda rootfstype=ext4 rw dummy.numdummies=0"
	extraAppend := d.getKernelAppend()
	if extraAppend != "" {
		kernelAppend = kernelAppend + " " + extraAppend
	}

	return d.startLibkrun(ctx, op, revert, kernelPath, initrdPath, rootDiskPath, nics, memSizeMB, kernelAppend, postStartHooks)
}

// startLibkrun starts the MicroVM instance using libkrun via the forklibkrun helper subcommand.
// libkrun's krun_start_enter() takes over the calling process and never returns, so it must run
// in a dedicated child process rather than inside the LXD daemon.
func (d *microvm) startLibkrun(ctx context.Context, op *operationlock.InstanceOperation, revert *revert.Reverter, kernelPath string, initrdPath string, rootDiskPath string, nics []microVMNIC, memSizeMB int64, kernelCmdline string, postStartHooks []func() error) error {
	// Configure CPU count, default to 1.
	cpuCount := d.expandedConfig["limits.cpu"]
	if cpuCount == "" {
		cpuCount = "1"
	}

	consolePath := d.libkrunConsolePath()

	// Remove old console socket and PID file if they exist.
	_ = os.Remove(consolePath)
	_ = os.Remove(d.libkrunPidFilePath())

	// Set up vsock for lxd-agent connectivity.
	// Load vhost_vsock so the proxy goroutine can use vsock loopback to reach the LXD
	// vsock server from the host side (agent→LXD direction).
	err := util.LoadModule("vhost_vsock")
	if err != nil {
		d.logger.Warn("Failed loading vhost_vsock module; lxd-agent vsock connectivity may be unavailable", logger.Ctx{"err": err})
	}

	agentSocketPath := d.libkrunAgentSocketPath()
	_ = os.Remove(agentSocketPath)

	lxdProxySocket, guestProxyPort := d.ensureLibkrunVsockProxy()

	// Build the forklibkrun helper command.
	forkArgs := []string{
		"forklibkrun",
		"--cpus", cpuCount,
		"--memory", strconv.FormatInt(memSizeMB, 10),
		"--kernel", kernelPath,
		"--cmdline", kernelCmdline,
		"--root-disk", rootDiskPath,
		"--config-drive", d.configDriveMountPath(),
		"--console", consolePath,
		"--lxd-path", shared.VarPath(""),
		"--project", d.project.Name,
		"--instance", d.Name(),
	}

	if initrdPath != "" {
		forkArgs = append(forkArgs, "--initrd", initrdPath)
	}

	// Pass vsock socket paths when the proxy is available.
	if lxdProxySocket != "" && guestProxyPort != 0 {
		forkArgs = append(forkArgs,
			"--vsock-agent-socket", agentSocketPath,
			"--vsock-lxd-port", strconv.FormatUint(uint64(guestProxyPort), 10),
			"--vsock-lxd-socket", lxdProxySocket,
		)
	}

	// Add NIC configurations. libkrun's tap backend opens the host TAP device by name itself
	// (inside the forklibkrun child, which runs as root), so the TAP device name and hardware
	// address are passed through rather than a pre-opened file descriptor as used by QEMU.
	// Interfaces appear in the guest as eth0, eth1, ... in the order added.
	for _, nic := range nics {
		forkArgs = append(forkArgs, "--net", fmt.Sprintf("%s,%s", nic.nicName, nic.hwaddr))
	}

	d.logger.Debug("Starting libkrun", logger.Ctx{"cmd": strings.Join(forkArgs, " ")})

	// Setup the process using the subprocess package.
	logFilePath := d.LogFilePath()
	p, err := subprocess.NewProcess(d.state.OS.ExecPath, forkArgs, logFilePath, logFilePath)
	if err != nil {
		err = fmt.Errorf("Failed creating libkrun process: %w", err)
		op.Done(err)
		return err
	}

	err = p.Start(context.Background())
	if err != nil {
		err = fmt.Errorf("Failed starting libkrun: %w", err)
		op.Done(err)
		return err
	}

	pid := int(p.PID)

	// Write PID file.
	err = os.WriteFile(d.libkrunPidFilePath(), []byte(strconv.Itoa(pid)), 0640)
	if err != nil {
		_ = p.Stop()
		err = fmt.Errorf("Failed writing PID file: %w", err)
		op.Done(err)
		return err
	}

	// Subscribe to process exit via a pidfd so that an unexpected VM crash triggers
	// host-side resource cleanup without requiring a poll loop or daemon restart.
	d.monitorLibkrunProcess(pid)

	revert.Add(func() {
		d.stopLibkrunMonitor()
		_ = p.Stop()
	})

	// Wait for the console socket to appear, indicating the helper has configured the VM.
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for !shared.PathExists(consolePath) {
		// Check if process exited early.
		pidErr := p.Signal(0)
		if pidErr != nil {
			logContent, _ := os.ReadFile(logFilePath)
			err = fmt.Errorf("libkrun process exited unexpectedly\nLog: %s", string(logContent))
			op.Done(err)
			return err
		}

		select {
		case <-ctxTimeout.Done():
			err = fmt.Errorf("Timed out waiting for libkrun console socket: %w", ctxTimeout.Err())
			op.Done(err)
			return err
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Record last state.
	err = d.recordLastState()
	if err != nil {
		op.Done(err)
		return err
	}

	// Run any post-start hooks.
	err = d.runHooks(postStartHooks)
	if err != nil {
		op.Done(err)
		return err
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStarted.Event(ctx, d, nil))

	revert.Success()

	d.logger.Info("Started libkrun instance", logger.Ctx{"pid": pid})

	// Start the agent readiness poller in the background. There is no QMP control channel
	// for libkrun, so we poll the agent unix socket until the lxd-agent accepts a TLS
	// connection, then advertise the LXD vsock address so the agent can connect back.
	if lxdProxySocket != "" && guestProxyPort != 0 {
		go d.waitForLibkrunAgent(agentSocketPath, guestProxyPort)
	}

	return nil
}

// waitForLibkrunAgent polls the per-VM agent unix socket until the lxd-agent inside the
// libkrun VM has started and is accepting TLS connections, then advertises the LXD vsock
// address so the agent can initiate its own connection back (devlxd etc.).
// This replaces the QMP EventAgentStarted→advertiseVsockAddress path used by QEMU.
func (d *microvm) waitForLibkrunAgent(agentSocketPath string, lxdVsockPort uint32) {
	const (
		pollInterval = 5 * time.Second
		pollTimeout  = 3 * time.Minute
	)

	deadline := time.Now().Add(pollTimeout)

	for time.Now().Before(deadline) {
		// The agent socket is created by libkrun when the VM starts, but the agent only
		// begins accepting connections once it has fully initialised inside the guest.
		// A successful net.Dial proves libkrun's side is up; a successful TLS handshake
		// (done inside libkrunAgentHTTPClient → ConnectLXDHTTPWithContext) proves the
		// agent itself is ready.
		_, err := net.DialTimeout("unix", agentSocketPath, time.Second)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		err = d.libkrunAdvertiseVsockAddress(lxdVsockPort)
		if err != nil {
			d.logger.Warn("Failed advertising vsock address to libkrun agent, retrying", logger.Ctx{"err": err})
			time.Sleep(pollInterval)
			continue
		}

		d.logger.Debug("lxd-agent ready in libkrun VM")
		return
	}

	d.logger.Warn("Timed out waiting for lxd-agent to become ready in libkrun VM")
}

// Info returns the driver info for microvm instances.
func (d *microvm) Info() instance.Info {
	data := instance.Info{
		Name:     "libkrun",
		Features: make(map[string]any),
		Type:     instancetype.MicroVM,
		Error:    errors.New("Unknown error"),
	}

	if !shared.PathExists("/dev/kvm") {
		data.Error = errors.New("KVM support is missing (no /dev/kvm)")
		return data
	}

	data.Error = nil
	data.Version = "unknown"

	return data
}

// libkrunAdvertiseVsockAddress sends the LXD vsock CID and port to lxd-agent so
// the agent can connect back to the LXD daemon for devlxd and server-initiated operations.
func (d *microvm) libkrunAdvertiseVsockAddress(lxdVsockPort uint32) error {
	httpClient, err := d.getAgentClient()
	if err != nil {
		return fmt.Errorf("Failed getting agent client: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), agentConnectTimeout)
	defer cancel()

	agent, err := lxd.ConnectLXDHTTPWithContext(connectCtx, nil, httpClient)
	if err != nil {
		return fmt.Errorf("Failed connecting to lxd-agent: %w", err)
	}

	defer agent.Disconnect()

	connInfo, err := d.getAgentConnectionInfo()
	if err != nil {
		return err
	}

	if connInfo == nil {
		return nil
	}

	// Override the port with the vsock port that libkrun will bridge to the shared proxy.
	// The CID remains vsock.Host (2) since that is what the guest's kernel expects for the
	// hypervisor/host, and libkrun intercepts vsock.Dial(2, lxdVsockPort) via AddVsockPort.
	connInfo.Port = lxdVsockPort

	_, _, err = agent.RawQuery(http.MethodPut, "/1.0", connInfo, "")
	if err != nil {
		return fmt.Errorf("Failed sending vsock address to lxd-agent: %w", err)
	}

	return nil
}

// libkrunWatcherKey returns the map key used to track a libkrun watcher for this instance.
func (d *microvm) libkrunWatcherKey() string {
	return d.project.Name + "/" + d.Name()
}

// monitorLibkrunProcess opens a pidfd for the given forklibkrun helper PID and starts a
// goroutine that blocks until the process exits. When an unexpected exit is detected (i.e.
// stopLibkrunMonitor has not been called to deregister the watcher), onStop is called to
// perform full host-side resource cleanup. This mirrors how QEMU's QMP socket disconnect
// synthesises a SHUTDOWN event that triggers onStop.
//
// The call is idempotent: if a watcher is already registered for this PID it returns
// immediately, so it is safe to call from both startLibkrun and statusCode.
func (d *microvm) monitorLibkrunProcess(pid int) {
	key := d.libkrunWatcherKey()

	libkrunWatchersLock.Lock()
	existing, ok := libkrunWatchers[key]
	if ok && existing == pid {
		// Already watching this exact PID.
		libkrunWatchersLock.Unlock()
		return
	}

	libkrunWatchers[key] = pid
	libkrunWatchersLock.Unlock()

	// Open a pidfd for the process. This will fail if the process has already exited.
	pidFdFile, err := linux.PidFdOpen(pid, 0)
	if err != nil {
		d.logger.Warn("Failed opening pidfd for libkrun process, exit detection unavailable", logger.Ctx{"pid": pid, "err": err})

		libkrunWatchersLock.Lock()
		if libkrunWatchers[key] == pid {
			delete(libkrunWatchers, key)
		}

		libkrunWatchersLock.Unlock()
		return
	}

	d.logger.Debug("Monitoring libkrun process via pidfd", logger.Ctx{"pid": pid})

	go func() {
		defer func() { _ = pidFdFile.Close() }()

		// Poll the pidfd until POLLIN becomes set, which the kernel guarantees when
		// the process exits. Retry on EINTR (signal delivery to the daemon).
		fds := []unix.PollFd{{Fd: int32(pidFdFile.Fd()), Events: unix.POLLIN}}
		for {
			_, err := unix.Poll(fds, -1)
			if err == unix.EINTR {
				continue
			}

			break
		}

		// The process has exited. Check whether stopLibkrunMonitor already deregistered
		// us, which means a normal Stop() is in progress and will handle cleanup itself.
		libkrunWatchersLock.Lock()
		registered := libkrunWatchers[key] == pid
		if registered {
			delete(libkrunWatchers, key)
		}

		libkrunWatchersLock.Unlock()

		if !registered {
			// Normal stop path deregistered the watcher before killing the process;
			// stopLibkrun will call onStop directly.
			return
		}

		exitCtx := logger.Ctx{"pid": pid}
		exitCode, hasExitCode, infoErr := linux.PidfdGetExitInfo(int(pidFdFile.Fd()))
		if infoErr != nil {
			exitCtx["exitInfoErr"] = infoErr
		} else if hasExitCode {
			exitCtx["exitCode"] = exitCode
			waitStatus := syscall.WaitStatus(exitCode)

			if waitStatus.Exited() {
				exitCtx["exitStatus"] = waitStatus.ExitStatus()
			}

			if waitStatus.Signaled() {
				signal := waitStatus.Signal()
				exitCtx["exitSignal"] = signal
				exitCtx["exitSignalName"] = unix.SignalName(signal)
			}
		}

		target := d.libkrunOnStopTarget(exitCode, hasExitCode)
		exitCtx["target"] = target

		// Unexpected exit: trigger full instance cleanup. onStopOperationSetup will
		// create a new instance-initiated operation since no Stop() is in flight.
		d.logger.Debug("libkrun process exited unexpectedly, triggering instance cleanup", exitCtx)

		err = d.onStop(context.Background(), target)
		if err != nil {
			d.logger.Error("Failed running onStop after unexpected libkrun exit", logger.Ctx{"err": err})
		}
	}()
}

// libkrunOnStopTarget returns the onStop target for a libkrun helper exit.
// Exit status 0 is treated as a reboot, while exit status 1 and all other
// exit conditions are treated as a stop.
func (d *microvm) libkrunOnStopTarget(exitCode int, hasExitCode bool) string {
	if !hasExitCode {
		return "stop"
	}

	waitStatus := syscall.WaitStatus(exitCode)
	if waitStatus.Exited() && waitStatus.ExitStatus() == 0 {
		return "reboot"
	}

	return "stop"
}

// cleanupLibkrunRuntimeFiles removes the helper runtime files created for a libkrun VM.
func (d *microvm) cleanupLibkrunRuntimeFiles() {
	_ = os.Remove(d.libkrunPidFilePath())
	_ = os.Remove(d.libkrunConsolePath())
	_ = os.Remove(d.libkrunAgentSocketPath())
}

// onStop is run when the instance stops.
func (d *microvm) onStop(ctx context.Context, target string) error {
	d.logger.Debug("onStop hook started", logger.Ctx{"target": target})
	defer d.logger.Debug("onStop hook finished", logger.Ctx{"target": target})

	// Create/pick up operation.
	op, err := d.onStopOperationSetup(target)
	if err != nil {
		return err
	}

	// Unlock on return.
	defer op.Done(nil)

	d.cleanupLibkrunRuntimeFiles()

	// Wait for the VM process to finish (to avoid racing start when restarting).
	d.logger.Debug("Waiting for VM process to finish")
	waitTimeout := time.Minute * 5
	if d.pidWait(waitTimeout) {
		d.logger.Debug("VM process finished")
	} else {
		// Log a warning, but continue clean up as best we can.
		d.logger.Error("VM process failed stopping", logger.Ctx{"timeout": waitTimeout})
	}

	// Record power state.
	err = d.VolatileSet(map[string]string{
		"volatile.last_state.power": instance.PowerStateStopped,
		"volatile.last_state.ready": "false",
	})
	if err != nil {
		// Don't return an error here as we still want to cleanup the instance even if DB not available.
		d.logger.Error("Failed recording last power state", logger.Ctx{"err": err})
	}

	// Cleanup.
	d.cleanupDevices() // Must be called before unmount.
	_ = os.Remove(d.pidFilePath())

	// Stop the storage for the instance.
	err = d.unmount()
	if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
		// If we are migrating an instance and receive status locked error (indicating the device or
		// resource is busy) during unmount while LXD_TEST_LIVE_MIGRATION_ON_THE_SAME_HOST is set, we
		// ignore the error.
		isLiveMigrationTest := shared.IsTrue(os.Getenv("LXD_TEST_LIVE_MIGRATION_ON_THE_SAME_HOST"))

		//nolint:revive // Ignore early-return for clarity.
		if isLiveMigrationTest && op.Action() == operationlock.ActionMigrate && api.StatusErrorCheck(err, http.StatusLocked) {
			d.logger.Warn("Failed unmounting source instance during migration", logger.Ctx{"err": err})
		} else {
			err = fmt.Errorf("Failed unmounting instance: %w", err)
			op.Done(err)
			return err
		}
	}

	// Log and emit lifecycle if not user triggered.
	if op.GetInstanceInitiated() {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceShutdown.Event(ctx, d, nil))
	} else if op.Action() != operationlock.ActionMigrate {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStopped.Event(ctx, d, nil))
	}

	// Reboot the instance.
	if target == "reboot" {
		// Progress tracking here is not useful. We are in the on stop hook, which is called via lxc hook, so
		// progress reporting would not be returned to the original client.
		err = d.Start(ctx, false, nil)
		if err != nil {
			op.Done(err)
			return err
		}

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRestarted.Event(ctx, d, nil))
	} else if d.ephemeral {
		// Destroy ephemeral virtual machines.
		err = d.delete(ctx, true)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	return nil
}

// stopLibkrunMonitor deregisters the active pidfd watcher for this instance so that a
// concurrent goroutine in monitorLibkrunProcess does not also call onStop when the process
// is killed by the normal stopLibkrun code path.
func (d *microvm) stopLibkrunMonitor() {
	key := d.libkrunWatcherKey()

	libkrunWatchersLock.Lock()
	delete(libkrunWatchers, key)
	libkrunWatchersLock.Unlock()
}

// killVMMProcess kills the VMM helper process by PID.
func (d *microvm) killVMMProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return proc.Kill()
}

// processExists checks if a process with the given PID exists.
func (d *microvm) processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, FindProcess always succeeds. We need to send signal 0 to check if the process exists.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// libkrunPid gets the PID of the running libkrun helper process. Returns 0 if PID file or process not found.
func (d *microvm) libkrunPid() (int, error) {
	pidStr, err := os.ReadFile(d.libkrunPidFilePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}

		return -1, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidStr)))
	if err != nil {
		return -1, err
	}

	// Check if the process is still running and is the libkrun helper.
	cmdLineProcFilePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	cmdLine, err := os.ReadFile(cmdLineProcFilePath)
	if err != nil {
		return 0, nil // Process has gone.
	}

	if !bytes.Contains(cmdLine, []byte("forklibkrun")) {
		return -1, errors.New("PID does not match a libkrun process")
	}

	return pid, nil
}

// stopLibkrun stops a libkrun instance by terminating the helper process.
// libkrun has no external control channel, so the VM is stopped by killing the helper.
func (d *microvm) stopLibkrun(ctx context.Context, op *operationlock.InstanceOperation) error {
	// Deregister the pidfd watcher before killing the process so the goroutine in
	// monitorLibkrunProcess does not also call onStop when it observes the exit.
	d.stopLibkrunMonitor()

	pid, _ := d.libkrunPid()
	if pid > 0 {
		err := d.killVMMProcess(pid)
		if err != nil {
			d.logger.Warn("Failed killing libkrun process", logger.Ctx{"err": err})
		}

		// Wait up to 30 seconds for the process to exit.
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for d.processExists(pid) {
			select {
			case <-ctxTimeout.Done():
				d.logger.Warn("Timed out waiting for libkrun to exit")
			case <-time.After(100 * time.Millisecond):
				continue
			}

			break
		}
	}

	// Clean up PID file, console socket, and per-VM agent socket.
	d.cleanupLibkrunRuntimeFiles()

	// Wait for onStop to complete device cleanup.
	err := d.onStop(ctx, "stop")
	if err != nil {
		op.Done(err)
		return err
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStopped.Event(ctx, d, nil))

	op.Done(nil)
	return nil
}

// statusCode overrides the qemu statusCode method as libkrun has no QMP monitor to query.
func (d *microvm) statusCode() api.StatusCode {
	// Shortcut to avoid spamming during ongoing operations.
	operationStatus := d.operationStatusCode()
	if operationStatus != nil {
		return *operationStatus
	}

	// For libkrun, there is no control channel, so status is derived from the helper process.
	pid, _ := d.libkrunPid()
	if pid > 0 {
		// Re-subscribe to process exit via pidfd on every statusCode call that finds a
		// running process. This is a no-op if already monitoring this PID, but re-establishes
		// the watcher after a daemon restart (mirrors how QEMU's statusCode reconnects the
		// QMP socket as a side effect so the disconnect event fires on unexpected VM exit).
		d.monitorLibkrunProcess(pid)

		if shared.IsTrue(d.LocalConfig()["volatile.last_state.ready"]) {
			return api.Ready
		}

		return api.Running
	}

	return api.Stopped
}

// State overrides the qemu State method to use microvm's statusCode.
func (d *microvm) State() string {
	return strings.ToUpper(d.statusCode().String())
}

// IsRunning overrides the qemu IsRunning method to use microvm's statusCode.
func (d *microvm) IsRunning() bool {
	return d.isRunningStatusCode(d.statusCode())
}

// IsFrozen overrides the qemu IsFrozen method to use microvm's statusCode.
func (d *microvm) IsFrozen() bool {
	return d.statusCode() == api.Frozen
}

// Render overrides the qemu Render method to use microvm's statusCode.
func (d *microvm) Render(options ...func(response any) error) (state any, etag any, err error) {
	profileNames := make([]string, 0, len(d.profiles))
	for _, profile := range d.profiles {
		profileNames = append(profileNames, profile.Name)
	}

	if d.IsSnapshot() {
		// Prepare the ETag
		etag := []any{d.expiryDate}

		snapState := api.InstanceSnapshot{
			Name:            strings.SplitN(d.name, "/", 2)[1],
			Architecture:    d.architectureName,
			Profiles:        profileNames,
			Config:          d.localConfig,
			ExpandedConfig:  d.expandedConfig,
			Devices:         d.localDevices.CloneNative(),
			ExpandedDevices: d.expandedDevices.CloneNative(),
			CreatedAt:       d.creationDate,
			LastUsedAt:      d.lastUsedDate,
			ExpiresAt:       d.expiryDate,
			Ephemeral:       d.ephemeral,
			Stateful:        d.stateful,

			// Default to uninitialised/error state (0 means no CoW usage).
			// The size can then be populated optionally via the options argument.
			Size: -1,
		}

		for _, option := range options {
			err := option(&snapState)
			if err != nil {
				return nil, nil, err
			}
		}

		return &snapState, etag, nil
	}

	// Prepare the ETag
	etag = []any{d.architecture, d.localConfig, d.localDevices, d.ephemeral, d.profiles}

	instState := api.Instance{
		Name:            d.name,
		Description:     d.description,
		Architecture:    d.architectureName,
		Profiles:        profileNames,
		Config:          d.localConfig,
		ExpandedConfig:  d.expandedConfig,
		Devices:         d.localDevices.CloneNative(),
		ExpandedDevices: d.expandedDevices.CloneNative(),
		CreatedAt:       d.creationDate,
		LastUsedAt:      d.lastUsedDate,
		Ephemeral:       d.ephemeral,
		Stateful:        d.stateful,
		Project:         d.project.Name,
		Location:        d.node,
		Type:            d.Type().String(),
		StatusCode:      api.Error, // Default to error status for remote instances that are unreachable.
	}

	// If instance is local then request status.
	if d.state.ServerName == d.Location() {
		instState.StatusCode = d.statusCode()
	}

	instState.Status = instState.StatusCode.String()

	for _, option := range options {
		err := option(&instState)
		if err != nil {
			return nil, nil, err
		}
	}

	return &instState, etag, nil
}

// RenderFull overrides the qemu RenderFull method to use microvm's Render.
func (d *microvm) RenderFull(_ []net.Interface, opts ...instance.StateRenderOptions) (*api.InstanceFull, any, error) {
	if d.IsSnapshot() {
		return nil, nil, errors.New("RenderFull does not work with snapshots")
	}

	// Get the Instance struct.
	base, etag, err := d.Render()
	if err != nil {
		return nil, nil, err
	}

	// Convert to InstanceFull.
	vmState := api.InstanceFull{Instance: *base.(*api.Instance)}

	// Add the InstanceState (pass through opts).
	vmState.State, err = d.renderState(vmState.StatusCode, opts...)
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

// RenderState overrides the qemu RenderState method to use microvm's statusCode.
func (d *microvm) RenderState(_ []net.Interface, opts ...instance.StateRenderOptions) (*api.InstanceState, error) {
	return d.renderState(d.statusCode(), opts...)
}

// microVMNIC represents a NIC configuration for MicroVM.
type microVMNIC struct {
	devName string
	nicName string
	hwaddr  string
}

// Migrate is not supported for MicroVM instances.
func (d *microvm) Migrate(args *instance.CriuMigrationArgs) error {
	return storageDrivers.ErrNotSupported
}

// MigrateSend is not supported for MicroVM instances.
func (d *microvm) MigrateSend(ctx context.Context, args instance.MigrateSendArgs, progressReporter ioprogress.ProgressReporter) error {
	return storageDrivers.ErrNotSupported
}

// MigrateReceive is not supported for MicroVM instances.
func (d *microvm) MigrateReceive(ctx context.Context, args instance.MigrateReceiveArgs, progressReporter ioprogress.ProgressReporter) error {
	return storageDrivers.ErrNotSupported
}

// Snapshot is not supported for MicroVM instances initially.
func (d *microvm) Snapshot(ctx context.Context, name string, expiry *time.Time, stateful bool, diskVolumesMode string, progressReporter ioprogress.ProgressReporter) error {
	return storageDrivers.ErrNotSupported
}

// Shutdown attempts to gracefully shutdown the instance, but microvm doesn't support ACPI,
// so this falls back to an immediate Stop().
func (d *microvm) Shutdown(ctx context.Context, timeout time.Duration) error {
	d.logger.Debug("Shutdown requested, using Stop (microvm has no ACPI support)")
	return d.Stop(ctx, false)
}

// Stop stops the MicroVM instance.
func (d *microvm) Stop(ctx context.Context, stateful bool) error {
	d.logger.Debug("Stop started", logger.Ctx{"stateful": stateful})
	defer d.logger.Debug("Stop finished", logger.Ctx{"stateful": stateful})

	// Must be run prior to creating the operation lock.
	statusCode := d.statusCode()
	if !d.isRunningStatusCode(statusCode) && statusCode != api.Error && statusCode != api.Frozen {
		return ErrInstanceIsStopped
	}

	// MicroVM doesn't support stateful stop.
	if stateful {
		return errors.New("Stateful stop is not supported for MicroVM instances")
	}

	// Setup a new operation.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStop, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, true)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusableSucceeded) {
			return nil
		}

		return err
	}

	return d.stopLibkrun(ctx, op)
}

// Restart restart the instance.
func (d *microvm) Restart(ctx context.Context, timeout time.Duration, progressReporter ioprogress.ProgressReporter) error {
	return d.restartCommon(ctx, d, timeout, progressReporter)
}

// Console overrides the qemu Console method as libkrun bridges the guest console to its own unix socket.
func (d *microvm) Console(ctx context.Context, protocol string) (*os.File, chan error, error) {
	if protocol != instance.ConsoleTypeConsole {
		return nil, nil, fmt.Errorf("Unknown protocol %q", protocol)
	}

	chDisconnect := make(chan error, 1)

	conn, err := net.Dial("unix", d.libkrunConsolePath())
	if err != nil {
		return nil, nil, fmt.Errorf("Failed connecting to console socket %q: %w", d.libkrunConsolePath(), err)
	}

	file, err := (conn.(*net.UnixConn)).File()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting socket file: %w", err)
	}

	_ = conn.Close()

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceConsole.Event(ctx, d, logger.Ctx{"type": protocol}))

	return file, chDisconnect, nil
}
