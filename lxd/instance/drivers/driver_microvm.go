package drivers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/flosch/pongo2"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/mdlayher/vsock"
	"github.com/pkg/sftp"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	agentAPI "github.com/canonical/lxd/lxd-agent/api"
	"github.com/canonical/lxd/lxd/apparmor"
	"github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/cgroup"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/device"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/device/nictype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instance/operationlock"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/lxd/subprocess"
	"github.com/canonical/lxd/lxd/ucred"
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
// triggers onStop to clean up instance resources on the host - mirroring how QEMU's
// QMP socket disconnect fires a synthetic SHUTDOWN event that calls onStop.
var (
	libkrunWatchers     = map[int]int{}
	libkrunWatchersLock sync.Mutex
)

// libkrunVsockProxy is a single shared unix socket proxy that accepts connections from libkrun
// guest agents (agent->LXD direction) and forwards them to LXD's dedicated VM unix socket
// listener. All libkrun VMs on a host share this one listener; the TLS certificate carried
// inside each connection identifies the individual VM to the LXD daemon, matching the behaviour
// of native kernel vsock used by QEMU VMs.
var (
	libkrunVsockProxyLock     sync.Mutex
	libkrunVsockProxyListener net.Listener
	libkrunVsockProxySocket   string // absolute path of the shared unix socket
	libkrunVsockProxyUsers    int    // count of active MicroVM instances using the proxy
)

// libkrunVsockProxyPort is the guest-facing vsock port intercepted by libkrun
// and forwarded to the shared unix proxy socket on the host (agent→LXD path).
// It operates purely in the AF_VSOCK address family with TSI disabled (AddVsock(0)),
// so it does not intercept or block guest TCP/IP traffic on port 8444.
const libkrunVsockProxyPort uint32 = 8444

// MicroVMDefaultCPUCores defines the default number of cores a MicroVM will get if no limit specified.
const MicroVMDefaultCPUCores = "1"

// MicroVMDefaultMemSize is the default memory size for MicroVMs if no limit specified.
const MicroVMDefaultMemSize = "1GiB"

// microvmDeviceNamePrefix used as prefix for generated device names.
const microvmDeviceNamePrefix = "lxd_"

// microvmDeviceNameMaxLength is the maximum length of a device ID.
const microvmDeviceNameMaxLength = 36

// microvm is backed by libkrun.
type microvm struct {
	common

	// Cached handles.
	architectureName string
}

var _ instance.Instance = &microvm{}
var _ instance.VM = &microvm{}

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

func (d *microvm) init() error {
	// Compute the expanded config and device list.
	err := d.expandConfig()
	if err != nil {
		return err
	}

	return nil
}

// microvmInstantiate creates a MicroVM struct without expanding config.
func microvmInstantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices, p api.Project) *microvm {
	d := &microvm{
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

// Type returns the instance type.
func (d *microvm) Type() instancetype.Type {
	// Usually this functions return d.dbType.
	// In this case this would return container's type
	// and that's the reason why we need to overwrite it.
	return instancetype.MicroVM
}

// getKernelPath returns the path to the kernel to use for booting. MicroVM kernels are not
// user-configurable: the user copies or bind-mounts the host kernel to a fixed, snap-aware
// location under LXD's var directory.
func (d *microvm) getKernelPath() string {
	for _, name := range []string{"vmlinuz", "vmlinux"} {
		p := shared.VarPath("microvm", name)
		if shared.PathExists(p) {
			resolved, err := filepath.EvalSymlinks(p)
			if err == nil {
				return resolved
			}

			return p
		}
	}

	return ""
}

// getLibkrunPath returns the path to the libkrun dynamic library.
// If LIBKRUN_PATH is set in the environment, that path is used.
// Otherwise, it checks for a libkrun library under LXD's var directory (e.g. $LXD_DIR/libkrun/libkrun.so).
func (d *microvm) getLibkrunPath() string {
	override := os.Getenv("LIBKRUN_PATH")
	if override != "" {
		return override
	}

	for _, name := range []string{"libkrun.so", "libkrun.so.2", "libkrun.so.1", "libkrun.so.0"} {
		p := shared.VarPath("libkrun", name)
		if shared.PathExists(p) {
			resolved, err := filepath.EvalSymlinks(p)
			if err == nil {
				return resolved
			}

			return p
		}
	}

	return ""
}

// KernelPath returns the path to the kernel to use for booting.
func (d *microvm) KernelPath() string {
	return d.getKernelPath()
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
// LXD->agent vsock bridge. The forklibkrun helper passes this to libkrun's AddVsockPort2 so
// that the LXD daemon can connect here to reach the in-guest lxd-agent.
func (d *microvm) libkrunAgentSocketPath() string {
	return filepath.Join(d.LogPath(), "libkrun.agent.sock")
}

// microVMConfigPath returns the path to the generated libkrun VM config file.
func (d *microvm) microVMConfigPath() string {
	return filepath.Join(d.LogPath(), MicroVMConfigFileName)
}

// ensureLibkrunVsockProxy ensures that the shared unix socket proxy for agent->LXD vsock
// traffic is running. It first validates that required prerequisites exist and retries socket
// creation until it succeeds or times out / the context is cancelled.
func (d *microvm) ensureLibkrunVsockProxy(ctx context.Context) (string, uint32, error) {
	vsockUnixSocket := shared.VarPath("vsock-unix.socket")
	if !shared.PathExists(vsockUnixSocket) {
		return "", 0, fmt.Errorf("LXD VM unix socket not available at %q", vsockUnixSocket)
	}

	libkrunVsockProxyLock.Lock()
	defer libkrunVsockProxyLock.Unlock()

	if libkrunVsockProxyListener == nil {
		socketPath := shared.VarPath("libkrun-vsock-proxy.sock")

		timeout := time.After(5 * time.Second)
		var ln net.Listener
		var err error

		for {
			_ = os.Remove(socketPath)

			ln, err = net.Listen("unix", socketPath)
			if err == nil {
				break
			}

			select {
			case <-ctx.Done():
				return "", 0, fmt.Errorf("Context cancelled while creating libkrun vsock proxy socket: %w", ctx.Err())
			case <-timeout:
				return "", 0, fmt.Errorf("Timed out creating libkrun vsock proxy socket: %w", err)
			case <-time.After(100 * time.Millisecond):
			}
		}

		libkrunVsockProxyListener = ln
		libkrunVsockProxySocket = socketPath

		d.logger.Debug("Started libkrun vsock proxy", logger.Ctx{"socket": socketPath, "backend": vsockUnixSocket})

		go func(l net.Listener) {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}

				go libkrunVsockProxyBridge(conn, vsockUnixSocket)
			}
		}(ln)
	}

	libkrunVsockProxyUsers++
	return libkrunVsockProxySocket, libkrunVsockProxyPort, nil
}

// releaseLibkrunVsockProxy decrements the active user count of the shared proxy
// and closes the listener when no running MicroVM instances remain.
func (d *microvm) releaseLibkrunVsockProxy() {
	libkrunVsockProxyLock.Lock()
	defer libkrunVsockProxyLock.Unlock()

	if libkrunVsockProxyUsers > 0 {
		libkrunVsockProxyUsers--
	}

	if libkrunVsockProxyUsers == 0 && libkrunVsockProxyListener != nil {
		_ = libkrunVsockProxyListener.Close()
		libkrunVsockProxyListener = nil
		_ = os.Remove(libkrunVsockProxySocket)
		libkrunVsockProxySocket = ""
		d.logger.Debug("Stopped libkrun vsock proxy")
	}
}

// MicroVMPeerAddrPrefix marks a dial local address as tagged with the identity of the
// MicroVM resolved by libkrunVsockProxyIdentity, so that the eventual HTTP-level
// authenticator (lxd/api_vsock.go) can look up a single candidate instance instead of
// iterating every running MicroVM's certificate.
const MicroVMPeerAddrPrefix = "@lxd-microvm:"

// libkrunVsockProxyIdentity resolves the project and instance name of the libkrun helper
// process connecting to conn, by reading its PID (via SO_PEERCRED) and then that process's
// /proc/<pid>/cmdline for the --project/--instance arguments forklibkrun was started with.
// Returns ok=false if the identity could not be determined.
func libkrunVsockProxyIdentity(conn net.Conn) (projectName string, instName string, ok bool) {
	unixConn, isUnix := conn.(*net.UnixConn)
	if !isUnix {
		return "", "", false
	}

	cred, err := ucred.GetCred(unixConn)
	if err != nil {
		return "", "", false
	}

	cmdlineBytes, err := os.ReadFile("/proc/" + strconv.Itoa(int(cred.Pid)) + "/cmdline")
	if err != nil {
		return "", "", false
	}

	args := strings.Split(strings.TrimSuffix(string(cmdlineBytes), "\x00"), "\x00")
	for i, arg := range args {
		if i+1 >= len(args) {
			continue
		}

		switch arg {
		case "--project":
			projectName = args[i+1]
		case "--instance":
			instName = args[i+1]
		}
	}

	if projectName == "" || instName == "" {
		return "", "", false
	}

	return projectName, instName, true
}

// libkrunVsockProxyBridge proxies a single connection from a libkrun guest agent to
// LXD's VM unix socket listener, which is already a native TLS endpoint. The dial is tagged
// with the connecting MicroVM's identity (resolved from its PID) so the HTTP-level
// authenticator can verify a single candidate's certificate instead of every running
// MicroVM's, which would not scale with the number of MicroVMs on the host.
func libkrunVsockProxyBridge(conn net.Conn, vsockUnixSocket string) {
	defer func() { _ = conn.Close() }()

	var laddr *net.UnixAddr
	projectName, instName, ok := libkrunVsockProxyIdentity(conn)
	if ok {
		laddr = &net.UnixAddr{Name: MicroVMPeerAddrPrefix + projectName + "/" + instName, Net: "unix"}
	}

	unixConn, err := net.DialUnix("unix", laddr, &net.UnixAddr{Name: vsockUnixSocket, Net: "unix"})
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
	<-done
}

// validateStartup checks any constraints that would prevent start up from succeeding under normal circumstances.
func (d *microvm) validateStartup(stateful bool, statusCode api.StatusCode) error {
	err := d.common.validateStartup(statusCode)
	if err != nil {
		return err
	}

	// MicroVM does not support stateful start.
	if stateful {
		return errors.New("Stateful start is not supported for MicroVM instances")
	}

	// Check if instance is start protected.
	if shared.IsTrue(d.expandedConfig["security.protection.start"]) {
		return errors.New("Instance is protected from being started")
	}

	return nil
}

// Start starts the MicroVM instance using libkrun with direct kernel boot.
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

	// Validate the kernel path.
	kernelPath := d.getKernelPath()
	if !shared.PathExists(kernelPath) {
		return fmt.Errorf("Kernel not found at %q", kernelPath)
	}

	// Validate the libkrun library path if an explicit path is configured or present.
	libkrunPath := d.getLibkrunPath()
	if !shared.PathExists(libkrunPath) {
		return fmt.Errorf("libkrun library not found at %q", libkrunPath)
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

	volatileSet := make(map[string]string)

	// Generate UUID if not present.
	instUUID := d.localConfig["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New().String()
		volatileSet["volatile.uuid"] = instUUID
	}

	// Resolve the host lxd-agent once so the config drive placeholder and the bind mount
	// below agree on the same binary. Empty if the agent is not installed.
	lxdAgentSrcPath, err := d.lxdAgentSourcePath()
	if err != nil {
		err = fmt.Errorf("Failed resolving lxd-agent path: %w", err)
		op.Done(err)
		return err
	}

	// Generate the config drive.
	err = d.generateConfigShare(lxdAgentSrcPath)
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
				continue
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

	// Bind-mount the host lxd-agent over the placeholder so its bytes come from the host,
	// not the quota'd config volume (see generateConfigShare). This intentionally pins the
	// running daemon's agent revision for the VM's lifetime, as the daemon does for itself.
	if lxdAgentSrcPath != "" {
		agentDstPath := filepath.Join(configMntPath, "lxd-agent")
		err = device.DiskMount(lxdAgentSrcPath, agentDstPath, false, "", []string{"ro"}, "none")
		if err != nil {
			err = fmt.Errorf("Failed mounting lxd-agent into config drive: %w", err)
			op.Done(err)
			return err
		}
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
		memSize = MicroVMDefaultMemSize
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

	return d.startLibkrun(ctx, op, revert, kernelPath, rootDiskPath, nics, memSizeMB, kernelAppend, postStartHooks)
}

// startLibkrun starts the MicroVM instance using libkrun via the forklibkrun helper subcommand.
// libkrun's krun_start_enter() takes over the calling process and never returns, so it must run
// in a dedicated child process rather than inside the LXD daemon.
func (d *microvm) startLibkrun(ctx context.Context, op *operationlock.InstanceOperation, revert *revert.Reverter, kernelPath string, rootDiskPath string, nics []microVMNIC, memSizeMB int64, kernelCmdline string, postStartHooks []func() error) error {
	// Configure CPU count, default to 1.
	cpuCount := d.expandedConfig["limits.cpu"]
	if cpuCount == "" {
		cpuCount = MicroVMDefaultCPUCores
	}

	cpus, err := strconv.ParseUint(cpuCount, 10, 8)
	if err != nil {
		err = fmt.Errorf("limits.cpu invalid: %w", err)
		op.Done(err)
		return err
	}

	if memSizeMB <= 0 || memSizeMB > math.MaxUint32 {
		err = fmt.Errorf("limits.memory invalid: %d MiB is out of range", memSizeMB)
		op.Done(err)
		return err
	}

	consolePath := d.libkrunConsolePath()

	// Remove old console socket, PID file, and exit file if they exist.
	_ = os.Remove(consolePath)
	_ = os.Remove(d.libkrunPidFilePath())

	agentSocketPath := d.libkrunAgentSocketPath()
	_ = os.Remove(agentSocketPath)

	lxdProxySocket, guestProxyPort, err := d.ensureLibkrunVsockProxy(ctx)
	if err != nil {
		d.logger.Warn("Failed establishing libkrun vsock proxy; lxd-agent will be unavailable", logger.Ctx{"err": err})
	} else {
		revert.Add(func() { d.releaseLibkrunVsockProxy() })
	}

	// Build the MicroVM configuration.
	configNICs := make([]MicroVMConfigNIC, 0, len(nics))
	for _, nic := range nics {
		configNICs = append(configNICs, MicroVMConfigNIC{
			Tap:    nic.nicName,
			HWAddr: nic.hwaddr,
		})
	}

	cfg := MicroVMConfigV1{
		CPUs:      uint8(cpus),
		MemoryMiB: uint32(memSizeMB),
		Kernel: MicroVMConfigKernel{
			Path:    kernelPath,
			Format:  "auto",
			Cmdline: kernelCmdline,
		},
		RootDisk:    rootDiskPath,
		ConfigDrive: d.configDriveMountPath(),
		Console:     consolePath,
		NICs:        configNICs,
	}

	if lxdProxySocket != "" && guestProxyPort != 0 {
		cfg.Vsock = &MicroVMConfigVsock{
			AgentSocket: agentSocketPath,
			LXDPort:     uint32(guestProxyPort),
			LXDSocket:   lxdProxySocket,
		}
	}

	configPath := d.microVMConfigPath()
	err = WriteMicroVMConfig(configPath, cfg)
	if err != nil {
		op.Done(err)
		return err
	}

	// Build the forklibkrun helper command.
	forkArgs := []string{
		"forklibkrun",
		"--config", configPath,
		"--project", d.project.Name,
		"--instance", d.Name(),
	}

	d.logger.Debug("Starting libkrun", logger.Ctx{"config": configPath, "cmd": strings.Join(forkArgs, " ")})

	// Setup the process using the subprocess package.
	logFilePath := d.LogFilePath()
	p, err := subprocess.NewProcess(d.state.OS.ExecPath, forkArgs, logFilePath, logFilePath)
	if err != nil {
		err = fmt.Errorf("Failed creating libkrun process: %w", err)
		op.Done(err)
		return err
	}

	// Use context.Background() because the VM process must outlive the start operation and HTTP
	// request context. Binding to ctx would cause exec.CommandContext to kill the VM once the request completes.
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
	d.monitorLibkrunProcess(p, pid)

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
		// (done inside libkrunAgentHTTPClient -> ConnectLXDHTTPWithContext) proves the
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

// monitorLibkrunProcess monitors the forklibkrun helper process and starts a
// goroutine that blocks until the process exits. When an unexpected exit is detected (i.e.
// stopLibkrunMonitor has not been called to deregister the watcher), onStop is called to
// perform full host-side resource cleanup. This mirrors how QEMU's QMP socket disconnect
// synthesises a SHUTDOWN event that triggers onStop.
//
// When p is non-nil (the process was spawned by the current daemon), p.Wait() is used to
// reliably obtain the exit status on all Linux kernel versions. When p is nil (recovering
// after a daemon restart), pidfd polling is used as a fallback.
//
// The call is idempotent: if a watcher is already registered for this PID it returns
// immediately, so it is safe to call from both startLibkrun and statusCode.
func (d *microvm) monitorLibkrunProcess(p *subprocess.Process, pid int) {
	key := d.ID()

	libkrunWatchersLock.Lock()
	existing, ok := libkrunWatchers[key]
	if ok && existing == pid {
		// Already watching this exact PID.
		libkrunWatchersLock.Unlock()
		return
	}

	libkrunWatchers[key] = pid
	libkrunWatchersLock.Unlock()

	d.logger.Debug("Monitoring libkrun process", logger.Ctx{"pid": pid})

	go func() {
		var exitCode int
		var hasExitCode bool

		// Pidfd fallback for processes recovered after a daemon restart.
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

		rawCode, hasCode, infoErr := linux.PidfdGetExitInfo(int(pidFdFile.Fd()))
		if infoErr != nil {
			d.logger.Debug("PidfdGetExitInfo unavailable for libkrun process", logger.Ctx{"pid": pid, "err": infoErr})
		} else if hasCode {
			exitCode = rawCode
			hasExitCode = true
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
		if hasExitCode {
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

		target := d.libkrunOnStopTarget(pid, exitCode, hasExitCode)
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
// The process exit code 0 is treated as a reboot, while all other conditions are
// treated as a stop.
func (d *microvm) libkrunOnStopTarget(pid int, exitCode int, hasExitCode bool) string {
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
	d.releaseLibkrunVsockProxy()

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
	libkrunWatchersLock.Lock()
	delete(libkrunWatchers, d.ID())
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

// waitProcessExit waits up to timeout for the process to exit and reports whether it did.
func (d *microvm) waitProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for d.processExists(pid) {
		if time.Now().After(deadline) {
			return false
		}

		time.Sleep(100 * time.Millisecond)
	}

	return true
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
		return 0, nil
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
		// SIGTERM lets the forklibkrun supervisor kill and reap the VM process before exiting itself.
		err := unix.Kill(pid, unix.SIGTERM)
		if err != nil {
			d.logger.Warn("Failed signaling libkrun process", logger.Ctx{"err": err})
		}

		if !d.waitProcessExit(pid, 10*time.Second) {
			d.logger.Warn("Timed out waiting for libkrun to terminate, killing it")

			err = d.killVMMProcess(pid)
			if err != nil {
				d.logger.Warn("Failed killing libkrun process", logger.Ctx{"err": err})
			}

			if !d.waitProcessExit(pid, 30*time.Second) {
				d.logger.Warn("Timed out waiting for libkrun to exit")
			}
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

// statusCode derives the instance status from the libkrun helper process.
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
		d.monitorLibkrunProcess(nil, pid)

		if shared.IsTrue(d.LocalConfig()["volatile.last_state.ready"]) {
			return api.Ready
		}

		return api.Running
	}

	return api.Stopped
}

// State returns the instance's state code.
func (d *microvm) State() string {
	return strings.ToUpper(d.statusCode().String())
}

// IsRunning returns whether or not the instance is running.
func (d *microvm) IsRunning() bool {
	return d.isRunningStatusCode(d.statusCode())
}

// InitPID returns the instance's current process ID.
func (d *microvm) InitPID() int {
	pid, _ := d.libkrunPid()
	return pid
}

// IsFrozen returns whether the instance is frozen or not.
func (d *microvm) IsFrozen() bool {
	return d.statusCode() == api.Frozen
}

// Render returns info about the instance.
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

// RenderFull returns all info about the instance.
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

// RenderState returns just state info about the instance.
func (d *microvm) RenderState(_ []net.Interface, opts ...instance.StateRenderOptions) (*api.InstanceState, error) {
	return d.renderState(d.statusCode(), opts...)
}

func (d *microvm) renderState(statusCode api.StatusCode, opts ...instance.StateRenderOptions) (*api.InstanceState, error) {
	var err error

	// Determine which fields to include
	options := instance.DefaultStateRenderOptions()
	if len(opts) > 0 {
		options = opts[0]
	}

	status := &api.InstanceState{}
	pid := d.InitPID()

	if d.isRunningStatusCode(statusCode) {
		status.Processes = -1

		if options.IncludeNetwork {
			status.Network, err = d.getNetworkState()
			if err != nil {
				return nil, err
			}
		} else {
			status.Network = nil
		}
	}

	status.Pid = int64(pid)
	status.Status = statusCode.String()
	status.StatusCode = statusCode

	// Disk - conditionally fetch (expensive operation)
	if options.IncludeDisk {
		status.Disk, err = d.diskState()
		if err != nil && !errors.Is(err, storageDrivers.ErrNotSupported) {
			d.logger.Info("Cannot get disk usage", logger.Ctx{"err": err})
		}
	} else {
		status.Disk = nil
	}

	return status, nil
}

func (d *microvm) getNetworkState() (map[string]api.InstanceStateNetwork, error) {
	networks := map[string]api.InstanceStateNetwork{}
	for k, m := range d.ExpandedDevices() {
		if m["type"] != "nic" {
			continue
		}

		dev, err := d.deviceLoad(d, k, m)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue
			}

			d.logger.Warn("Failed state validation for device", logger.Ctx{"device": k, "err": err})
			continue
		}

		nic, ok := dev.(device.NICState)
		if !ok {
			continue
		}

		network, err := nic.State()
		if err != nil {
			return nil, fmt.Errorf("Failed getting NIC state for %q: %w", k, err)
		}

		if network != nil {
			networks[k] = *network
		}
	}

	return networks, nil
}

func (d *microvm) diskState() (map[string]api.InstanceStateDisk, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	// Get the root disk device config.
	rootDiskName, _, err := d.getRootDiskDevice()
	if err != nil {
		return nil, err
	}

	usage, err := pool.GetInstanceUsage(d)
	if err != nil {
		return nil, err
	}

	disk := map[string]api.InstanceStateDisk{}
	disk[rootDiskName] = api.InstanceStateDisk{
		Usage: usage.Used,
		Total: usage.Total,
	}

	return disk, nil
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

// Snapshot is not supported for MicroVM instances.
func (d *microvm) Snapshot(ctx context.Context, name string, expiry *time.Time, stateful bool, diskVolumesMode string, progressReporter ioprogress.ProgressReporter) error {
	return storageDrivers.ErrNotSupported
}

// Shutdown shuts the instance down.
// libkrun has no ACPI support, so the guest is asked to power off through lxd-agent.
func (d *microvm) Shutdown(ctx context.Context, timeout time.Duration) error {
	d.logger.Debug("Shutdown started", logger.Ctx{"timeout": timeout})
	defer d.logger.Debug("Shutdown finished", logger.Ctx{"timeout": timeout})

	// Must be run prior to creating the operation lock.
	statusCode := d.statusCode()
	if !d.isRunningStatusCode(statusCode) {
		if statusCode == api.Error {
			return fmt.Errorf("The instance cannot be cleanly shutdown as in %s status", statusCode)
		}

		return ErrInstanceIsStopped
	}

	// Setup a new operation.
	// Allow inheriting of ongoing restart operation (we are called from restartCommon).
	// Allow reuse when creating a new stop operation. This allows the Stop() function to inherit operation.
	// Allow reuse of a reusable ongoing stop operation as Shutdown() may be called earlier, which allows reuse
	// of its operations. This allow for multiple Shutdown() attempts.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStop, []operationlock.Action{operationlock.ActionRestart}, true, true)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusableSucceeded) {
			// An existing matching operation has now succeeded, return.
			return nil
		}

		return err
	}

	client, err := d.getAgentClient()
	if err != nil {
		err = fmt.Errorf("Failed requesting guest power off: %w", err)
		op.Done(err)
		return err
	}

	agent, err := lxd.ConnectLXDHTTP(nil, client)
	if err != nil {
		op.Done(err)
		return err
	}

	defer agent.Disconnect()

	// Indicate to the onStop hook that if the VM stops it was due to a clean shutdown because the VM responded
	// to the power off request.
	op.SetInstanceInitiated(true)

	req := api.InstanceExecPost{Command: []string{"poweroff"}}

	// Always needed for VM exec, as even for non-websocket requests from the client we need to connect the
	// websockets for control and for capturing output to a file on the LXD server.
	req.WaitForWS = true

	// Similarly, output recording is performed on the host rather than in the guest, so clear that bit from the request.
	req.RecordOutput = false

	// The guest halts, forklibkrun exits with status 1 and the libkrun monitor runs onStop("stop").
	_, err = agent.ExecInstance("", req, nil)
	if err != nil {
		err = fmt.Errorf("Failed requesting guest power off: %w", err)
		op.Done(err)
		return err
	}

	d.logger.Debug("Shutdown request sent to instance")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for operation lock to be Done or context to timeout. The operation lock is normally completed by
	// onStop which picks up the same lock and then marks it as Done after the instance stops and the devices
	// have been cleaned up. However if the operation has failed for another reason we collect the error here.
	err = op.Wait(ctx)
	status := d.statusCode()
	if status != api.Stopped {
		errPrefix := fmt.Errorf("Failed shutting down instance, status is %q", status)

		if err != nil {
			return fmt.Errorf("%s: %w", errPrefix.Error(), err)
		}

		return errPrefix
	}

	// Now handle errors from shutdown sequence and return to caller if wasn't completed cleanly.
	if err != nil {
		return err
	}

	return nil
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

// Restart restarts the instance.
func (d *microvm) Restart(ctx context.Context, timeout time.Duration, progressReporter ioprogress.ProgressReporter) error {
	return d.restartCommon(ctx, d, timeout, progressReporter)
}

// Console gets access to the instance's console. libkrun bridges the guest console to its own unix socket.
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

// Update updates the instance config.
func (d *microvm) Update(ctx context.Context, args db.InstanceArgs, actionType instance.UpdateAction) error {
	userRequested := d.isUserRequested(actionType)

	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	// Setup a new operation.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionUpdate, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, false)
	if err != nil {
		return fmt.Errorf("Failed creating instance update operation: %w", err)
	}

	defer op.Done(nil)

	// Setup the reverter.
	revert := revert.New()
	defer revert.Fail()

	// Set sane defaults for unset keys.
	if args.Project == "" {
		args.Project = api.ProjectDefaultName
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
		args.Profiles = []api.Profile{}
	}

	if userRequested {
		// Validate the new config.
		err := instance.ValidConfig(d.state.OS, args.Config, false, d.dbType)
		if err != nil {
			return fmt.Errorf("Invalid config: %w", err)
		}

		// Validate the new devices without using expanded devices validation (expensive checks disabled).
		err = instance.ValidDevices(d.state, d.project, d.Type(), args.Devices, nil)
		if err != nil {
			return fmt.Errorf("Invalid devices: %w", err)
		}
	}

	var profiles []string

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Validate the new profiles.
		profiles, err = tx.GetProfileNames(ctx, args.Project)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting profiles: %w", err)
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !slices.Contains(profiles, profile.Name) {
			return fmt.Errorf("Requested profile %q does not exist", profile.Name)
		}

		if slices.Contains(checkedProfiles, profile.Name) {
			return errors.New("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile.Name)
	}

	// Validate the new architecture.
	if args.Architecture != 0 {
		_, err = osarch.ArchitectureName(args.Architecture)
		if err != nil {
			return fmt.Errorf("Invalid architecture ID: %w", err)
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

	oldProfiles := []api.Profile{}
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
	err = d.expandConfig()
	if err != nil {
		return err
	}

	// Diff the configurations.
	changedConfig := []string{}
	for key := range oldExpandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !slices.Contains(changedConfig, key) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range d.expandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !slices.Contains(changedConfig, key) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices.
	removeDevices, addDevices, updateDevices, allUpdatedDeviceKeys := oldExpandedDevices.Update(d.expandedDevices, func(oldDevice deviceConfig.Device, newDevice deviceConfig.Device) []string {
		// This function needs to return a list of fields that are excluded from differences
		// between oldDevice and newDevice. The result of this is that as long as the
		// devices are otherwise identical except for the fields returned here, then the
		// device is considered to be being "updated" rather than "added & removed".
		oldDevType, err := device.LoadByType(d.state, d.Project().Name, oldDevice)
		if err != nil {
			return []string{} // Could not create Device, so this cannot be an update.
		}

		newDevType, err := device.LoadByType(d.state, d.Project().Name, newDevice)
		if err != nil {
			return []string{} // Could not create Device, so this cannot be an update.
		}

		return newDevType.UpdatableFields(oldDevType)
	})

	err = d.validateConfig(allUpdatedDeviceKeys, addDevices, removeDevices, oldExpandedDevices, changedConfig, oldExpandedConfig, actionType)
	if err != nil {
		return err
	}

	// If apparmor changed, re-validate the apparmor profile (even if not running).
	if slices.Contains(changedConfig, "raw.apparmor") {
		err = apparmor.InstanceValidate(d.state.OS, d)
		if err != nil {
			return fmt.Errorf("Parse AppArmor profile: %w", err)
		}
	}

	isRunning := d.IsRunning()

	// Use the device interface to apply update changes.
	devlxdEvents, err := d.devicesUpdate(d, removeDevices, addDevices, updateDevices, oldExpandedDevices, isRunning, userRequested)
	if err != nil {
		return err
	}

	if isRunning {
		// Re-generate the agent mounts file so that it reflects the current devices set.
		// This way if a directory disk is added immediately after VM start but before the lxd-agent has
		// started in the guest (such that it misses the devlxd notification event), the agent will still
		// be able to see the mount config for the new disk when it starts.
		err = d.generateAgentMountsFile()
		if err != nil {
			return fmt.Errorf("Failed generating agent mounts file: %w", err)
		}

		// Only certain keys can be changed on a running VM.
		liveUpdateKeys := []string{
			"cluster.evacuate",
			"security.devlxd",
			"security.devlxd.images",
			"security.devlxd.management.volumes",
		}

		liveUpdateKeyPrefixes := []string{
			"boot.",
			"cloud-init.",
			"environment.",
			"image.",
			"snapshots.",
			"user.",
			"volatile.",
		}

		isLiveUpdatable := func(key string) bool {
			// Containers / metadata / user / environment keys allowed
			if slices.Contains(liveUpdateKeys, key) || shared.StringHasPrefix(key, liveUpdateKeyPrefixes...) {
				return true
			}
			// Everything else (like limits.cpu, limits.memory) is rejected while running
			return false
		}

		// Check only keys that support live update have changed.
		for _, key := range changedConfig {
			if !isLiveUpdatable(key) {
				return fmt.Errorf("Key %q cannot be updated when VM is running", key)
			}
		}

		// Apply live update for each key.
		for _, key := range changedConfig {
			switch key {
			case "security.devlxd":
				err = d.advertiseVsockAddress()
				if err != nil {
					return err
				}
			}
		}
	}

	// Re-generate the instance-id if needed.
	if !d.IsSnapshot() && d.needsNewInstanceID(changedConfig, oldExpandedDevices) {
		err = d.resetInstanceID()
		if err != nil {
			return err
		}
	}

	// If the instance is now assigned to a "placement.group", remove any previous "volatile.cluster.group".
	// This ensures the placement group takes precedence and avoids stale cluster group targeting during evacuation.
	if d.expandedConfig["placement.group"] != "" {
		if oldLocalConfig["volatile.cluster.group"] != "" {
			delete(d.localConfig, "volatile.cluster.group")
		}
	}
	// Finally, apply the changes to the database.
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Snapshots should update only their descriptions and expiry date.
		if d.IsSnapshot() {
			return tx.UpdateInstanceSnapshot(d.id, d.description, d.expiryDate)
		}

		object, err := dbCluster.GetInstance(ctx, tx.Tx(), d.project.Name, d.name)
		if err != nil {
			return err
		}

		object.Description = d.description
		object.Architecture = d.architecture
		object.Ephemeral = d.ephemeral
		object.ExpiryDate = sql.NullTime{Time: d.expiryDate, Valid: true}

		err = dbCluster.UpdateInstance(ctx, tx.Tx(), d.project.Name, d.name, *object)
		if err != nil {
			return err
		}

		err = dbCluster.UpdateInstanceConfig(ctx, tx.Tx(), int64(object.ID), d.localConfig)
		if err != nil {
			return err
		}

		// Do not store initial.* device config keys in database.
		initialDevicesConfig := d.localDevices.CutInitialConfig()
		defer func() { initialDevicesConfig.Copy(d.localDevices) }() // Restore after DB transaction.

		devices, err := dbCluster.APIToDevices(d.localDevices.CloneNative())
		if err != nil {
			return err
		}

		err = dbCluster.UpdateInstanceDevices(ctx, tx.Tx(), int64(object.ID), devices)
		if err != nil {
			return err
		}

		profileNames := make([]string, 0, len(d.profiles))
		for _, profile := range d.profiles {
			profileNames = append(profileNames, profile.Name)
		}

		return dbCluster.UpdateInstanceProfiles(ctx, tx.Tx(), object.ID, object.Project, profileNames)
	})
	if err != nil {
		return fmt.Errorf("Failed updating database: %w", err)
	}

	err = d.UpdateBackupFile()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed writing backup file: %w", err)
	}

	// Changes have been applied and recorded, do not revert if an error occurs from here.
	revert.Success()

	if isRunning {
		// Send devlxd notifications only for user.* key changes
		for _, key := range changedConfig {
			if !strings.HasPrefix(key, "user.") {
				continue
			}

			msg := map[string]any{
				"key":       key,
				"old_value": oldExpandedConfig[key],
				"value":     d.expandedConfig[key],
			}

			err = d.devlxdEventSend("config", msg)
			if err != nil {
				return err
			}
		}

		// Device events.
		for _, event := range devlxdEvents {
			err = d.devlxdEventSend("device", event)
			if err != nil {
				return err
			}
		}
	}

	if userRequested {
		if d.isSnapshot {
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotUpdated.Event(ctx, d, nil))
		} else {
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceUpdated.Event(ctx, d, nil))
		}
	}

	return nil
}

// Delete deletes the instance.
func (d *microvm) Delete(ctx context.Context, force bool, diskVolumesMode string, progressReporter ioprogress.ProgressReporter) error {
	return d.deleteCommon(ctx, d, force, diskVolumesMode, progressReporter)
}

func (d *microvm) delete(ctx context.Context, force bool) error {
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
	}

	if d.isSnapshot {
		d.logger.Info("Deleting instance snapshot", ctxMap)
	} else {
		d.logger.Info("Deleting instance", ctxMap)
	}

	// Check if instance is delete protected.
	if !force && shared.IsTrue(d.expandedConfig["security.protection.delete"]) && !d.IsSnapshot() {
		return errors.New("Instance is protected from being deleted")
	}

	err := d.checkRootVolumeNotInUse()
	if err != nil {
		return err
	}

	// Delete any persistent warnings for instance.
	err = d.warningsDelete()
	if err != nil {
		return err
	}

	// Attempt to initialize storage interface for the instance.
	pool, err := d.getStoragePool()
	if err != nil && !response.IsNotFoundError(err) {
		return err
	} else if pool != nil {
		if d.IsSnapshot() {
			// Remove snapshot volume and database record.
			err = pool.DeleteInstanceSnapshot(d, nil)
			if err != nil {
				return err
			}
		} else {
			// Remove all snapshots.
			err := d.deleteSnapshots(func(snapInst instance.Instance) error {
				return snapInst.(*microvm).delete(ctx, true) // Internal delete function that does not lock.
			})
			if err != nil {
				return fmt.Errorf("Failed deleting instance snapshots: %w", err)
			}

			// Remove the storage volume and database records.
			err = pool.DeleteInstance(d, nil)
			if err != nil {
				return err
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
			err = backup.Delete(ctx)
			if err != nil {
				return err
			}
		}

		// Run device removal function for each device.
		d.devicesRemove(d)
		// Clean up libkrun runtime files and stop the libkrun monitor.
		d.cleanupLibkrunRuntimeFiles()
		d.stopLibkrunMonitor()
		// Clean things up.
		d.cleanup()
		// Remove the log directory. Not handled by cleanup() as that is
		// also called during Rename() where logs should be preserved.
		_ = os.RemoveAll(d.LogPath())
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Remove the database record of the instance or snapshot instance.
		return tx.DeleteInstance(ctx, d.Project().Name, d.Name())
	})
	if err != nil {
		d.logger.Error("Failed deleting instance entry", logger.Ctx{"project": d.Project().Name})
		return err
	}

	if d.isSnapshot {
		d.logger.Info("Deleted instance snapshot", ctxMap)
	} else {
		d.logger.Info("Deleted instance", ctxMap)
	}

	if d.isSnapshot {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotDeleted.Event(ctx, d, nil))
	} else {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceDeleted.Event(ctx, d, nil))
	}

	return nil
}

// Rename the instance. Accepts an argument to enable applying deferred TemplateTriggerRename.
func (d *microvm) Rename(ctx context.Context, newName string, applyTemplateTrigger bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	oldName := d.Name()
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"newname":   newName}

	d.logger.Info("Renaming instance", ctxMap)

	// Quick checks.
	err = instancetype.ValidName(newName, d.IsSnapshot())
	if err != nil {
		return err
	}

	err = d.checkRootVolumeNotInUse()
	if err != nil {
		return err
	}

	if d.IsRunning() {
		return errors.New("Renaming of running instance not allowed")
	}

	// Clean things up.
	d.cleanup()

	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	if d.IsSnapshot() {
		_, newSnapName, _ := api.GetParentAndSnapshotName(newName)
		err = pool.RenameInstanceSnapshot(d, newSnapName, nil)
		if err != nil {
			return fmt.Errorf("Rename instance snapshot: %w", err)
		}
	} else {
		err = pool.RenameInstance(d, newName, nil)
		if err != nil {
			return fmt.Errorf("Rename instance: %w", err)
		}

		if applyTemplateTrigger {
			err = d.DeferTemplateApply(instance.TemplateTriggerRename)
			if err != nil {
				return err
			}
		}
	}

	if !d.IsSnapshot() {
		var results []string

		err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			// Rename all the instance snapshot database entries.
			results, err = tx.GetInstanceSnapshotsNames(ctx, d.project.Name, oldName)
			if err != nil {
				d.logger.Error("Failed getting instance snapshots", ctxMap)
				return fmt.Errorf("Failed getting instance snapshots: Failed getting instance snapshot names: %w", err)
			}

			for _, sname := range results {
				// Rename the snapshot.
				_, oldSnapName, _ := strings.Cut(sname, shared.SnapshotDelimiter)
				baseSnapName := filepath.Base(sname)

				err := dbCluster.RenameInstanceSnapshot(ctx, tx.Tx(), d.project.Name, oldName, oldSnapName, baseSnapName)
				if err != nil {
					d.logger.Error("Failed renaming snapshot", ctxMap)
					return err
				}
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	// Rename the instance database entry.
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		if d.IsSnapshot() {
			oldParent, oldSnap, _ := strings.Cut(oldName, shared.SnapshotDelimiter)
			_, newSnap, _ := strings.Cut(newName, shared.SnapshotDelimiter)
			return dbCluster.RenameInstanceSnapshot(ctx, tx.Tx(), d.project.Name, oldParent, oldSnap, newSnap)
		}

		return dbCluster.RenameInstance(ctx, tx.Tx(), d.project.Name, oldName, newName)
	})
	if err != nil {
		d.logger.Error("Failed renaming instance", ctxMap)
		return err
	}

	// Rename the logging path.
	newFullName := project.Instance(d.Project().Name, d.Name())
	_ = os.RemoveAll(shared.LogPath(newFullName))
	err = os.Rename(d.LogPath(), shared.LogPath(newFullName))
	if err != nil && !os.IsNotExist(err) {
		d.logger.Error("Failed renaming instance", ctxMap)
		return err
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
		_, backupName, _ := strings.Cut(oldName, "/")
		newName := newName + "/" + backupName

		err = b.Rename(ctx, newName)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = b.Rename(context.Background(), oldName) })
	}

	// Update lease files.
	err = network.UpdateDNSMasqStatic(d.state, "")
	if err != nil {
		return err
	}

	// Reset cloud-init instance-id (causes a re-run on name changes).
	if !d.IsSnapshot() {
		err = d.resetInstanceID()
		if err != nil {
			return err
		}
	}

	// Update the backup file.
	err = d.UpdateBackupFile()
	if err != nil {
		return err
	}

	d.logger.Info("Renamed instance", ctxMap)

	if d.isSnapshot {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotRenamed.Event(ctx, d, map[string]any{"old_name": oldName}))
	} else {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRenamed.Event(ctx, d, map[string]any{"old_name": oldName}))
	}

	revert.Success()
	return nil
}

// microVMDeviceName generates a bounded device name for the MicroVM guest.
func microVMDeviceName(deviceName string) string {
	baseName := filesystem.PathNameEncode(deviceName)
	maxNameLength := microvmDeviceNameMaxLength - len(microvmDeviceNamePrefix)
	if len(baseName) > maxNameLength {
		hash := sha256.Sum256([]byte(baseName))
		baseName = base64.RawURLEncoding.EncodeToString(hash[:])[:maxNameLength]
	}

	return microvmDeviceNamePrefix + baseName
}

// generateAgentMountsFile generates the agent mounts file if disk config has changed.
func (d *microvm) generateAgentMountsFile() error {
	drives := d.expandedDevices.Filter(filters.Or(filters.IsCustomVolumeFilesystemDisk, filters.IsHostFilesystemShareDisk)).Sorted()
	agentMounts := make([]instancetype.VMAgentMount, 0, len(drives))

	for _, drive := range drives {
		agentMount := instancetype.VMAgentMount{
			Source: microVMDeviceName(drive.Name),
			Target: drive.Config["path"],
			FSType: "virtiofs",
		}

		if shared.IsTrue(drive.Config["readonly"]) {
			agentMount.Options = append(agentMount.Options, "ro")
		}

		agentMounts = append(agentMounts, agentMount)
	}

	newAgentMountJSON, err := json.Marshal(agentMounts)
	if err != nil {
		return fmt.Errorf("Failed marshalling agent mounts to JSON: %w", err)
	}

	agentMountFile := filepath.Join(d.Path(), "config", "agent-mounts.json")
	curAgentMountJSON, _ := os.ReadFile(agentMountFile)
	if bytes.Equal(curAgentMountJSON, newAgentMountJSON) {
		return nil
	}

	d.logger.Debug("Writing agent mounts config", logger.Ctx{"file": agentMountFile})
	err = os.WriteFile(agentMountFile, newAgentMountJSON, 0400)
	if err != nil {
		return fmt.Errorf("Failed writing agent mounts file: %w", err)
	}

	return nil
}

// UpdateBackupFile writes the instance's backup.yaml file to storage.
func (d *microvm) UpdateBackupFile() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	volBackupConf, err := pool.GenerateInstanceCustomVolumeBackupConfig(d, nil, true, nil)
	if err != nil {
		return fmt.Errorf("Failed generating instance custom volume config: %w", err)
	}

	// Use the global metadata version.
	return pool.UpdateInstanceBackupFile(d, true, volBackupConf, config.DefaultMetadataVersion, nil)
}

// getAgentClient returns the current agent client handle.
// Callers should check that the instance is running (and therefore mounted) before calling this function,
// otherwise the qmp.Connect call will fail to use the monitor socket file.
func (d *microvm) getAgentClient() (*http.Client, error) {
	// libkrun microVMs do not expose a QMP monitor socket, so reach the agent over the
	// per-VM unix socket bridge created by forklibkrun instead of kernel vsock.
	agentSocketPath := filepath.Join(d.LogPath(), "libkrun.agent.sock")

	agentCert, _, clientCert, clientKey, err := d.generateAgentCert()
	if err != nil {
		return nil, err
	}

	tlsConfig, err := shared.GetTLSConfigMem(clientCert, clientKey, "", agentCert, false)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", agentSocketPath)
			},
			DisableKeepAlives:     true,
			ExpectContinueTimeout: 30 * time.Second,
			ResponseHeaderTimeout: time.Hour,
			TLSHandshakeTimeout:   5 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header = via[len(via)-1].Header
			return nil
		},
	}

	agent, err := lxd.ConnectLXDHTTP(nil, httpClient)
	if err != nil {
		return nil, err
	}

	defer agent.Disconnect()

	_, _, err = agent.RawQuery(http.MethodGet, "/1.0", nil, "")
	if err != nil {
		return nil, errQemuAgentOffline
	}

	return httpClient, nil
}

// mount the instance's config volume if needed.
func (d *microvm) mount() (*storagePools.MountInfo, error) {
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
func (d *microvm) unmount() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	err = pool.UnmountInstance(d, nil)
	if err != nil {
		return err
	}

	return nil
}

// readAgentCert reads the instance's existing agent certificates, without ever
// creating them.
//
// It is meant for callers that run while the instance is already up, where the
// certificates are expected to have been created at startup. Those callers must
// not fall back to generating a fresh set.
//
// These live on the instance's config volume, which for a running instance is of
// course mounted -- but not necessarily in the mount namespace of the daemon
// asking. After a snap refresh the daemon restarts into a new namespace, where
// the config volume is not mounted until RegisterDevices re-establishes it, and
// agent requests can arrive before that happens.
// Generating then writes a stray set into the *underlying* instance directory and
// breaks the agent connection, which still trusts the original certificates. The
// stray files get shadowed once the config volume is mounted over them, and
// resurface at deletion time as "Failed removing ... directory not empty".
func (d *microvm) readAgentCert() (agentCert string, clientCert string, clientKey string, err error) {
	instancePath := d.Path()

	for _, f := range []struct {
		out  *string
		name string
	}{
		{&agentCert, "agent.crt"},
		{&clientCert, "agent-client.crt"},
		{&clientKey, "agent-client.key"},
	} {
		contents, err := os.ReadFile(filepath.Join(instancePath, f.name))
		if err != nil {
			return "", "", "", fmt.Errorf("Failed reading agent TLS material %q (the instance's config volume may not be mounted in this mount namespace): %w", f.name, err)
		}

		*f.out = string(contents)
	}

	return agentCert, clientCert, clientKey, nil
}

// generateAgentCert creates the necessary server key and certificate if needed.
//
// This writes into the instance's directory, so it must only be called on paths
// where the instance's volume is known to be mounted (e.g. instance startup).
// Callers that merely need to read the certificates of a running instance must
// use readAgentCert instead.
func (d *microvm) generateAgentCert() (agentCert string, agentKey string, clientCert string, clientKey string, err error) {
	instancePath := d.Path()
	agentCertFile := filepath.Join(instancePath, "agent.crt")
	agentKeyFile := filepath.Join(instancePath, "agent.key")
	clientCertFile := filepath.Join(instancePath, "agent-client.crt")
	clientKeyFile := filepath.Join(instancePath, "agent-client.key")

	// Create server certificate.
	err = shared.FindOrGenCert(agentCertFile, agentKeyFile, false, shared.CertOptions{})
	if err != nil {
		return "", "", "", "", err
	}

	// Create client certificate.
	err = shared.FindOrGenCert(clientCertFile, clientKeyFile, true, shared.CertOptions{})
	if err != nil {
		return "", "", "", "", err
	}

	// Read back what was just created. Everything but the server key is shared
	// with the read-only path.
	agentCert, clientCert, clientKey, err = d.readAgentCert()
	if err != nil {
		return "", "", "", "", err
	}

	agentKeyBytes, err := os.ReadFile(agentKeyFile)
	if err != nil {
		return "", "", "", "", err
	}

	return agentCert, string(agentKeyBytes), clientCert, clientKey, nil
}

// configDriveMountPath returns the path for the config drive bind mount.
func (d *microvm) configDriveMountPath() string {
	return filepath.Join(d.DevicesPath(), "config.mount")
}

// configDriveMountPathClear attempts to unmount the config drive bind mount and remove the directory.
func (d *microvm) configDriveMountPathClear() error {
	// Unmount the nested lxd-agent bind mount first, otherwise the parent mount is busy.
	agentMntPath := filepath.Join(d.configDriveMountPath(), "lxd-agent")
	if filesystem.IsMountPoint(agentMntPath) {
		err := storageDrivers.TryUnmount(agentMntPath, unix.MNT_DETACH)
		if err != nil {
			return fmt.Errorf("Failed unmounting lxd-agent bind mount %q: %w", agentMntPath, err)
		}
	}

	return device.DiskMountClear(d.configDriveMountPath())
}

// advertiseVsockAddress advertises the CID and port to the VM.
func (d *microvm) advertiseVsockAddress() error {
	client, err := d.getAgentClient()
	if err != nil {
		return fmt.Errorf("Failed getting agent client handle: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentConnectTimeout)
	defer cancel()

	agent, err := lxd.ConnectLXDHTTPWithContext(ctx, nil, client)
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

	_, _, err = agent.RawQuery(http.MethodPut, "/1.0", connInfo, "")
	if err != nil {
		return fmt.Errorf("Failed sending VM sock address to lxd-agent: %w", err)
	}

	return nil
}

// lxdAgentSourcePath returns the resolved path to the host lxd-agent binary, or an empty
// string if it is not installed (the VM then starts without an up-to-date agent).
func (d *microvm) lxdAgentSourcePath() (string, error) {
	srcPath, err := exec.LookPath("lxd-agent")
	if err != nil {
		return "", nil
	}

	return filepath.EvalSymlinks(srcPath)
}

// generateConfigShare generates the config share directory that will be exported to the VM via
// a 9P share. Due to the unknown size of templates inside the images this directory is created
// inside the VM's config volume so that it can be restricted by quota.
// Requires the instance be mounted before calling this function.
func (d *microvm) generateConfigShare(lxdAgentSrcPath string) error {
	configDrivePath := filepath.Join(d.Path(), "config")

	// Create config drive dir if doesn't exist, if it does exist, leave it around so we don't regenerate all
	// files causing unnecessary config drive snapshot usage.
	err := os.MkdirAll(configDrivePath, 0500)
	if err != nil {
		return err
	}

	// Keep only a placeholder here; the real binary is bind-mounted over it in start() so it
	// never occupies the quota'd config volume. Only touch it when a host agent exists to mount
	// later, else leave any existing binary in place.
	if lxdAgentSrcPath == "" {
		d.logger.Warn("lxd-agent not found, skipping its inclusion in the VM config drive")
	} else {
		// O_TRUNC reclaims the space used by a full binary copied by an older LXD version.
		lxdAgentInstallPath := filepath.Join(configDrivePath, "lxd-agent")
		f, err := os.OpenFile(lxdAgentInstallPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0500)
		if err != nil {
			return fmt.Errorf("Failed creating lxd-agent placeholder %q: %w", lxdAgentInstallPath, err)
		}

		_ = f.Close()
	}

	agentCert, agentKey, clientCert, _, err := d.generateAgentCert()
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "server.crt"), []byte(clientCert), 0400)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "agent.crt"), []byte(agentCert), 0400)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "agent.key"), []byte(agentKey), 0400)
	if err != nil {
		return err
	}

	// Systemd units.
	systemdPath := filepath.Join(configDrivePath, "systemd")
	err = os.MkdirAll(systemdPath, 0500)
	if err != nil {
		return err
	}

	lxdAgentServiceUnit := `# Systemd unit for lxd-agent. It ensures the lxd-agent is copied from the shared filesystem before
# it is started. The service is triggered dynamically when the lxd-agent-generator is run inside a
# LXD VM, rather than being enabled at boot.
[Unit]
Description=LXD - agent
Documentation=https://canonical.com/lxd/docs/latest/
Before=multi-user.target cloud-init-local.service shutdown.target umount.target
After=local-fs.target systemd-journald.socket
Conflicts=shutdown.target
DefaultDependencies=no

# Containers see their host's DMI information, so the generator may add
# lxd-agent.service to the boot transaction if the container's host is a LXD VM
# with systemd older than 251. Prevent this by requiring a VM (systemd 244+).
ConditionVirtualization=vm

[Service]
Type=notify
RuntimeDirectory=lxd_agent
WorkingDirectory=-/run/lxd_agent
ExecStartPre=/usr/lib/systemd/lxd-agent-setup
ExecStart=/run/lxd_agent/lxd-agent
Restart=on-failure
RestartSec=5s
StartLimitInterval=60
StartLimitBurst=10
`

	// Service units are meant to be world-readable. Trying to restrict access
	// is ineffective as there are other means to access their content. This is
	// not an issue as the lxd-agent.service unit doesn't contain any sensitive
	// information.
	err = os.WriteFile(filepath.Join(systemdPath, "lxd-agent.service"), []byte(lxdAgentServiceUnit), 0644)
	if err != nil {
		return err
	}

	// Setup script for lxd-agent that is executed by the lxd-agent systemd unit before lxd-agent is started.
	// The script sets up a temporary mount point, copies data from the mount (including lxd-agent binary),
	// and then unmounts it. It also ensures appropriate permissions for the LXD agent's runtime directory.
	lxdAgentSetupScript := `#!/bin/sh
set -eu
PREFIX="/run/lxd_agent"

fail() {
    umount -l "${PREFIX}" >/dev/null 2>&1 || true
    rmdir "${PREFIX}" >/dev/null 2>&1 || true
    echo "${1}"
    exit 1
}

# Setup the mount target.
umount -l "${PREFIX}" >/dev/null 2>&1 || true
mkdir -p "${PREFIX}"
mount -t tmpfs tmpfs "${PREFIX}" -o mode=0700,nodev,nosuid,noatime,size=50M
mkdir -p "${PREFIX}/.mnt"

mount -t 9p config "${PREFIX}/.mnt" -o ro,access=0,trans=virtio,size=1048576 >/dev/null 2>&1 || fail "Could not mount 9p, failing."
cp -Ra --no-preserve=ownership "${PREFIX}/.mnt/"* "${PREFIX}"
umount "${PREFIX}/.mnt"
rmdir "${PREFIX}/.mnt"
restorecon -R "${PREFIX}" >/dev/null 2>&1 || true
`

	err = os.WriteFile(filepath.Join(systemdPath, "lxd-agent-setup"), []byte(lxdAgentSetupScript), 0500)
	if err != nil {
		return err
	}

	// The `lxd-agent.service` unit needs to only start when executing inside a LXD VM.
	// To achieve this, we use a systemd generator that checks for LXD-specific
	// DMI information and only adds the `lxd-agent.service` to the boot
	// transaction if it is running inside a LXD VM. However, some architectures
	// (like s390x) do not support DMI, so udev rules are used to trigger
	// the `lxd-agent.service` when either of the virtio ports is detected.
	// Udev rules are deployed unconditionally as a fallback for all architectures.
	udevPath := filepath.Join(configDrivePath, "udev")
	err = os.MkdirAll(udevPath, 0500)
	if err != nil {
		return err
	}

	// udev conditions are evaluated sequentially so the order matters.
	// The SUBSYSTEM is part of the event so it is the cheapest check to perform.
	// The ATTR{name} requires a file read under `/sys`, so it should come last.

	// Udev rules to start the lxd-agent.service when QEMU serial devices (virtio-ports) appear.
	lxdAgentRules := `# This rule acts as the primary trigger for architectures without DMI
# (where the systemd generator is skipped). On architectures with DMI, this
# rule will also fire, but systemd will safely deduplicate the start request.
SUBSYSTEM=="virtio-ports", \
ATTR{name}=="com.canonical.lxd|org.linuxcontainers.lxd", \
TAG+="systemd", \
ENV{SYSTEMD_WANTS}+="lxd-agent.service"
`

	err = os.WriteFile(filepath.Join(udevPath, "99-lxd-agent.rules"), []byte(lxdAgentRules), 0400)
	if err != nil {
		return err
	}

	// system generator to start the lxd-agent.service when LXD VMs are detected via DMI `board_name`.
	lxdAgentGenerator := `#!/bin/sh

# $1 = normal, $2 = early, $3 = late
OUT_DIR="${2}"
UNIT_NAME="lxd-agent.service"
SOURCE_UNIT="/usr/lib/systemd/system/${UNIT_NAME}"
TARGET_DIR="${OUT_DIR}/multi-user.target.wants"

# SYSTEMD_VIRTUALIZATION was added in version 251
[ "${SYSTEMD_VIRTUALIZATION:-vm:kvm}" = "vm:kvm" ] || exit 0

# In a LXD VM, the board name is set to "LXD"
f="/sys/class/dmi/id/board_name"
[ -r "${f}" ] || exit 0

read -r board_name < "${f}" || true
if [ "${board_name}" = "LXD" ]; then
[ -d "${TARGET_DIR}" ] || mkdir -p "${TARGET_DIR}"
ln -sf "${SOURCE_UNIT}" "${TARGET_DIR}/${UNIT_NAME}"
fi
`

	// System generators need to be executable as they are executed directly by systemd to determine which units to enable.
	err = os.WriteFile(filepath.Join(systemdPath, "lxd-agent-generator"), []byte(lxdAgentGenerator), 0500)
	if err != nil {
		return err
	}

	// Install script for manual installs.
	lxdConfigShareInstall := `#!/bin/sh
if [ ! -e "systemd" ] || [ ! -e "lxd-agent" ]; then
    echo "This script must be run from within the config mount"
    exit 1
fi

# systemd systems always have /run/systemd/system/ created on boot.
if [ ! -d "/run/systemd/system/" ]; then
    echo "This script only works on systemd systems"
    exit 1
fi

for path in "/usr/lib/systemd" "/lib/systemd"; do
    [ -d "${path}/system" ] || continue
    LIB_SYSTEMD="${path}"
    break
done

if [ ! -d "${LIB_SYSTEMD:-}" ]; then
    echo "Could not find path to systemd"
    exit 1
fi

for path in "/usr/lib/udev" "/lib/udev"; do
    [ -d "${path}/rules.d/" ] || continue
    LIB_UDEV="${path}"
    break
done

if [ ! -d "${LIB_UDEV:-}" ]; then
    echo "Could not find path to udev"
    exit 1
fi

# Cleanup former units.
rm -f "${LIB_SYSTEMD}/system/lxd-agent-9p.service" \
    "${LIB_SYSTEMD}/system/lxd-agent-virtiofs.service" \
    /usr/lib/udev/rules.d/99-lxd-agent.rules \
    /lib/udev/rules.d/99-lxd-agent.rules \
    /etc/systemd/system/multi-user.target.wants/lxd-agent-9p.service \
    /etc/systemd/system/multi-user.target.wants/lxd-agent-virtiofs.service \
    /etc/systemd/system/multi-user.target.wants/lxd-agent.service

# Install the units.
if [ -e udev/99-lxd-agent.rules ]; then
  cp udev/99-lxd-agent.rules "${LIB_UDEV}/rules.d/"
fi
cp systemd/lxd-agent-setup "${LIB_SYSTEMD}/"
cp systemd/lxd-agent.service "${LIB_SYSTEMD}/system/"
mkdir -p "${LIB_SYSTEMD}/system-generators"
cp systemd/lxd-agent-generator "${LIB_SYSTEMD}/system-generators/"

# Adapt paths for systemd's lib location if needed.
if [ "/usr/lib/systemd" != "${LIB_SYSTEMD}" ]; then
    sed -i "s|/usr/lib/systemd|${LIB_SYSTEMD}|g" "${LIB_SYSTEMD}/system/lxd-agent.service" "${LIB_SYSTEMD}/system-generators/lxd-agent-generator"
fi

systemctl daemon-reload

# SELinux handling.
if getenforce >/dev/null 2>&1; then
    semanage fcontext -a -t bin_t /var/run/lxd_agent/lxd-agent
fi

echo ""
echo "LXD agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: systemctl start lxd-agent"
`

	err = os.WriteFile(filepath.Join(configDrivePath, "install.sh"), []byte(lxdConfigShareInstall), 0700)
	if err != nil {
		return err
	}

	// Templated files.
	templateFilesPath := filepath.Join(configDrivePath, "files")

	// Clear path and recreate.
	_ = os.RemoveAll(templateFilesPath)
	err = os.MkdirAll(templateFilesPath, 0500)
	if err != nil {
		return err
	}

	// Template anything that needs templating.
	key := "volatile.apply_template"
	if d.localConfig[key] != "" {
		// Run any template that needs running.
		err = d.templateApplyNow(instance.TemplateTrigger(d.localConfig[key]), templateFilesPath)
		if err != nil {
			return fmt.Errorf("Failed applying template: %w", err)
		}

		err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Remove the volatile key from the DB.
			return tx.DeleteInstanceConfigKey(ctx, int64(d.id), key)
		})
		if err != nil {
			return err
		}
	}

	err = d.templateApplyNow("start", templateFilesPath)
	if err != nil {
		return fmt.Errorf("Failed applying template: %w", err)
	}

	// Copy the template metadata itself too.
	metaPath := filepath.Join(d.Path(), "metadata.yaml")
	err = shared.FileCopy(metaPath, filepath.Join(templateFilesPath, "metadata.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Clear NICConfigDir to ensure that no leftover configuration is erroneously applied by the agent.
	nicConfigPath := filepath.Join(configDrivePath, deviceConfig.NICConfigDir)
	_ = os.RemoveAll(nicConfigPath)
	err = os.MkdirAll(nicConfigPath, 0500)
	if err != nil {
		return err
	}

	// Writing the connection info the config drive allows the lxd-agent to start devlxd very
	// early. This is important for systemd services which want or require /dev/lxd/sock.
	connInfo, err := d.getAgentConnectionInfo()
	if err != nil {
		return err
	}

	if connInfo != nil {
		err = d.saveConnectionInfo(connInfo)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *microvm) templateApplyNow(trigger instance.TemplateTrigger, path string) error {
	instanceRoot, err := d.OpenRoot()
	if err != nil {
		return err
	}

	defer func() { _ = instanceRoot.Close() }()

	metadataFile, err := instanceRoot.Open("metadata.yaml")
	if err != nil {
		// If there's no metadata, just return.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	defer func() { _ = metadataFile.Close() }()

	metadata, err := ParseImageMetadataFile(metadataFile)
	if err != nil {
		return fmt.Errorf("Failed reading metadata: %w", err)
	}

	// Figure out the instance architecture.
	arch, err := osarch.ArchitectureName(d.architecture)
	if err != nil {
		arch, err = osarch.ArchitectureName(d.state.OS.Architectures[0])
		if err != nil {
			return fmt.Errorf("Failed detecting system architecture: %w", err)
		}
	}

	// Generate the instance metadata.
	instanceMeta := make(map[string]string)
	instanceMeta["name"] = d.name
	instanceMeta["type"] = "micro-virtual-machine"
	instanceMeta["architecture"] = arch
	instanceMeta["ephemeral"] = strconv.FormatBool(d.ephemeral)

	templatesRoot, err := d.OpenTemplates()
	if err != nil {
		return err
	}

	defer func() { _ = templatesRoot.Close() }()

	// Open the output directory as a confined *os.Root so that a template's
	// attacker-influenced source name cannot be used to escape the config
	// drive's files directory when computing the ".out" target path.
	outputRoot, err := os.OpenRoot(path)
	if err != nil {
		return err
	}

	defer func() { _ = outputRoot.Close() }()

	// Go through the templates.
	for tplPath, tpl := range metadata.Templates {
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			var w *os.File

			// Check if the template should be applied now.
			found := slices.Contains(tpl.When, string(trigger))
			if !found {
				return nil
			}

			// Create the file itself. The confined *os.Root prevents the target
			// path from escaping the output directory.
			relPath := filepath.Clean(tpl.Template + ".out")

			w, err = outputRoot.OpenFile(relPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("Failed creating template file %q: %w", tpl.Template, err)
			}

			defer func() { _ = w.Close() }()

			// Fix ownership and mode.
			err = w.Chmod(0644)
			if err != nil {
				return err
			}

			// Read the template.
			tplString, err := templatesRoot.ReadFile(tpl.Template)
			if err != nil {
				return fmt.Errorf("Failed reading template file: %w", err)
			}

			configGet := func(confKey, confDefault *pongo2.Value) *pongo2.Value {
				val, ok := d.expandedConfig[confKey.String()]
				if !ok {
					return confDefault
				}

				return pongo2.AsValue(strings.TrimRight(val, "\r\n"))
			}

			// Render the template.
			err = shared.RenderTemplateFile(w, string(tplString), pongo2.Context{
				"trigger":    trigger,
				"path":       tplPath,
				"instance":   instanceMeta,
				"container":  instanceMeta, // FIXME: remove once most images have moved away.
				"config":     d.expandedConfig,
				"devices":    d.expandedDevices,
				"properties": tpl.Properties,
				"config_get": configGet,
			})
			if err != nil {
				return fmt.Errorf("Failed rendering template: %w", err)
			}

			return w.Close()
		}(tplPath, tpl)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *microvm) cleanup() {
	// Unmount any leftovers
	_ = d.removeUnixDevices()
	_ = d.removeDiskDevices()

	// Remove the security profiles
	_ = apparmor.InstanceDelete(d.state.OS, d)

	// Remove the devices path
	_ = os.Remove(d.DevicesPath())

	// Remove the shmounts path
	_ = os.RemoveAll(d.ShmountsPath())
}

func (d *microvm) saveConnectionInfo(connInfo *agentAPI.API10Put) error {
	f, err := os.Create(filepath.Join(d.Path(), "config", "agent.conf"))
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
	}()

	err = json.NewEncoder(f).Encode(connInfo)
	if err != nil {
		return err
	}

	return nil
}

// getAgentConnectionInfo returns the connection info the lxd-agent needs to connect to the LXD
// server.
func (d *microvm) getAgentConnectionInfo() (*agentAPI.API10Put, error) {
	req := agentAPI.API10Put{
		Certificate: string(d.state.Endpoints.NetworkCert().PublicKey()),
		Devlxd:      shared.IsTrueOrEmpty(d.expandedConfig["security.devlxd"]),
		CID:         vsock.Host,
		Port:        libkrunVsockProxyPort,
	}

	return &req, nil
}

func (d *microvm) devlxdEventSend(eventType string, eventMessage map[string]any) error {
	event := shared.Jmap{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

	client, err := d.getAgentClient()
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentConnectTimeout)
	defer cancel()

	agent, err := lxd.ConnectLXDHTTPWithContext(ctx, nil, client)
	if err != nil {
		d.logger.Error("Failed connecting to lxd-agent", logger.Ctx{"err": err})
		return errors.New("Failed connecting to lxd-agent")
	}

	defer agent.Disconnect()

	_, _, err = agent.RawQuery(http.MethodPost, "/1.0/events", &event, "")
	if err != nil {
		return err
	}

	return nil
}

// pidWait waits for the Libkrun process to exit. Does this in a way that doesn't require the LXD process to be a
// parent of the Libkrun process (in order to allow for LXD to be restarted after the microVM was started).
// Returns true if process stopped, false if timeout was exceeded.
func (d *microvm) pidWait(timeout time.Duration) bool {
	waitUntil := time.Now().Add(timeout)
	for {
		pid, _ := d.libkrunPid()
		if pid <= 0 {
			break
		}

		if time.Now().After(waitUntil) {
			return false
		}

		time.Sleep(time.Millisecond * 250)
	}

	return true
}

// pidFilePath returns the path where the qemu process should write its PID.
func (d *microvm) pidFilePath() string {
	return filepath.Join(d.LogPath(), "libkrun.pid")
}

// LogFilePath returns the instance's log path.
func (d *microvm) LogFilePath() string {
	return filepath.Join(d.LogPath(), "microvm.log")
}

// cleanupDevices performs any needed device cleanup steps when instance is stopped.
// Must be called before root volume is unmounted.
func (d *microvm) cleanupDevices() {
	err := d.configDriveMountPathClear()
	if err != nil {
		d.logger.Warn("Failed cleaning up config drive mount", logger.Ctx{"err": err})
	}

	for _, entry := range d.expandedDevices.Reversed() {
		dev, err := d.deviceLoad(d, entry.Name, entry.Config)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue
			}

			// Just log an error, but still allow the device to be stopped if usable device returned.
			d.logger.Error("Failed stop validation for device", logger.Ctx{"device": entry.Name, "err": err})
		}

		// If a usable device was returned from deviceLoad try to stop anyway, even if validation fails.
		// This allows for the scenario where a new version of LXD has additional validation restrictions
		// than older versions and we still need to allow previously valid devices to be stopped even if
		// they are no longer considered valid.
		if dev != nil {
			err = d.deviceStop(dev, false, "")
			if err != nil {
				d.logger.Error("Failed stopping device", logger.Ctx{"device": dev.Name(), "err": err})
			}
		}
	}
}

func (d *microvm) deviceStart(dev device.Device, instanceRunning bool) (*deviceConfig.RunConfig, error) {
	configCopy := dev.Config()
	l := d.logger.AddContext(logger.Ctx{"device": dev.Name(), "type": configCopy["type"]})
	l.Debug("Starting device")

	revert := revert.New()
	defer revert.Fail()

	if instanceRunning && !dev.CanHotPlug() {
		return nil, errors.New("Device cannot be started when instance is running")
	}

	runConf, err := dev.Start()
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		runConf, _ := dev.Stop()
		if runConf != nil {
			_ = d.runHooks(runConf.PostHooks)
		}
	})

	if runConf != nil && instanceRunning {
		err = d.runHooks(runConf.PostHooks)
		if err != nil {
			return nil, err
		}
	}

	revert.Success()
	return runConf, nil
}

func (d *microvm) deviceStop(dev device.Device, instanceRunning bool, _ string) error {
	configCopy := dev.Config()
	l := d.logger.AddContext(logger.Ctx{"device": dev.Name(), "type": configCopy["type"]})
	l.Debug("Stopping device")

	if instanceRunning && !dev.CanHotPlug() {
		return errors.New("Device cannot be stopped when instance is running")
	}

	runConf, err := dev.Stop()
	if err != nil {
		return err
	}

	if runConf != nil {
		err = d.runHooks(runConf.PostHooks)
		if err != nil {
			return err
		}
	}

	return nil
}

// RegisterDevices calls the Register() function on all of the instance's devices.
func (d *microvm) RegisterDevices() {
	d.devicesRegister(d)
}

// FillNetworkDevice enriches network devices with MAC and name properties.
func (d *microvm) FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
	var err error

	newDevice := m.Clone()

	nicType, err := nictype.NICType(d.state, d.Project().Name, m)
	if err != nil {
		return nil, err
	}

	// Fill in the MAC address.
	if !slices.Contains([]string{"physical", "ipvlan", "sriov"}, nicType) && m["hwaddr"] == "" {
		configKey := "volatile." + name + ".hwaddr"
		volatileHwaddr := d.localConfig[configKey]
		if volatileHwaddr == "" {
			// Generate a new MAC address.
			volatileHwaddr, err = instance.DeviceNextInterfaceHWAddr()
			if err != nil || volatileHwaddr == "" {
				return nil, fmt.Errorf("Failed generating %q: %w", configKey, err)
			}

			// Update the database and update volatileHwaddr with stored value.
			volatileHwaddr, err = d.insertConfigkey(configKey, volatileHwaddr)
			if err != nil {
				return nil, fmt.Errorf("Failed storing generated config key %q: %w", configKey, err)
			}

			// Set stored value into current instance config.
			d.localConfig[configKey] = volatileHwaddr
			d.expandedConfig[configKey] = volatileHwaddr
		}

		if volatileHwaddr == "" {
			return nil, fmt.Errorf("Failed getting %q", configKey)
		}

		newDevice["hwaddr"] = volatileHwaddr
	}

	return newDevice, nil
}

// FirmwarePath returns an empty path because MicroVM uses direct kernel boot.
func (d *microvm) FirmwarePath() string {
	return ""
}

// UEFIVars reads UEFI Variables for instance.
func (d *microvm) UEFIVars() (*api.InstanceUEFIVars, error) {
	return nil, storageDrivers.ErrNotSupported
}

// UEFIVarsUpdate updates UEFI Variables for instance.
func (d *microvm) UEFIVarsUpdate(newUEFIVarsSet api.InstanceUEFIVars) error {
	return storageDrivers.ErrNotSupported
}

// Freeze is not supported for MicroVM instances.
func (d *microvm) Freeze(ctx context.Context) error {
	return storageDrivers.ErrNotSupported
}

// Unfreeze is not supported for MicroVM instances.
func (d *microvm) Unfreeze(ctx context.Context) error {
	return storageDrivers.ErrNotSupported
}

// Rebuild is not supported for MicroVM instances.
func (d *microvm) Rebuild(ctx context.Context, img *api.Image, op *operations.Operation) error {
	return storageDrivers.ErrNotSupported
}

// Restore is not supported for MicroVM instances.
func (d *microvm) Restore(ctx context.Context, source instance.Instance, stateful bool, diskVolumesMode string, progressReporter ioprogress.ProgressReporter) error {
	return storageDrivers.ErrNotSupported
}

// Export is not supported for MicroVM instances.
func (d *microvm) Export(w io.Writer, properties map[string]string, expiration time.Time, tracker *ioprogress.ProgressTracker) (api.ImageMetadata, error) {
	return api.ImageMetadata{}, storageDrivers.ErrNotSupported
}

// ConversionReceive is not supported for MicroVM instances.
func (d *microvm) ConversionReceive(args instance.ConversionReceiveArgs, progressReporter ioprogress.ProgressReporter) error {
	return storageDrivers.ErrNotSupported
}

// IsPrivileged returns whether the instance is privileged (always false for VMs).
func (d *microvm) IsPrivileged() bool {
	return false
}

// CanMigrate returns whether the instance can be migrated (always false for MicroVMs).
func (d *microvm) CanMigrate() (canMigrate bool, live bool) {
	return false, false
}

// SetAffinity is not supported for MicroVM instances.
func (d *microvm) SetAffinity(set []string) error {
	return storageDrivers.ErrNotSupported
}

// CGroup is not implemented for MicroVM instances.
func (d *microvm) CGroup() (*cgroup.CGroup, error) {
	return nil, storageDrivers.ErrNotSupported
}

// OnHook is not implemented for MicroVM instances.
func (d *microvm) OnHook(_ string, _ map[string]string) error {
	return storageDrivers.ErrNotSupported
}

// AgentCertificate returns the server certificate of the lxd-agent.
func (d *microvm) AgentCertificate() *x509.Certificate {
	agentCert := filepath.Join(d.Path(), "config", "agent.crt")
	cert, err := shared.ReadCert(agentCert)
	if err != nil {
		return nil
	}

	return cert
}

// Metrics is not supported for MicroVM instances.
func (d *microvm) Metrics(_ []net.Interface) (*metrics.MetricSet, error) {
	return nil, storageDrivers.ErrNotSupported
}

// DeviceEventHandler handles events occurring on the instance's devices.
func (d *microvm) DeviceEventHandler(runConf *deviceConfig.RunConfig) error {
	return nil
}

// FileSFTPConn is not supported for MicroVM instances.
func (d *microvm) FileSFTPConn() (net.Conn, error) {
	return nil, storageDrivers.ErrNotSupported
}

// FileSFTP is not supported for MicroVM instances.
func (d *microvm) FileSFTP() (*sftp.Client, error) {
	return nil, storageDrivers.ErrNotSupported
}

// Exec runs a command inside the MicroVM through lxd-agent.
func (d *microvm) Exec(ctx context.Context, req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (instance.Cmd, error) {
	revert := revert.New()
	defer revert.Fail()

	client, err := d.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := lxd.ConnectLXDHTTP(nil, client)
	if err != nil {
		d.logger.Error("Failed connecting to lxd-agent", logger.Ctx{"err": err})
		return nil, errors.New("Failed connecting to lxd-agent")
	}

	revert.Add(agent.Disconnect)

	dataDone := make(chan bool)
	controlSendCh := make(chan api.InstanceExecControl)
	controlResCh := make(chan error)

	controlHandler := func(control *websocket.Conn) {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		defer func() { _ = control.WriteMessage(websocket.CloseMessage, closeMsg) }()

		for {
			select {
			case cmd := <-controlSendCh:
				controlResCh <- control.WriteJSON(cmd)
			case <-dataDone:
				return
			}
		}
	}

	args := lxd.InstanceExecArgs{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   stderr,
		DataDone: dataDone,
		Control:  controlHandler,
	}

	// Always needed for VM exec, as even for non-websocket requests from the client we need to connect the
	// websockets for control and for capturing output to a file on the LXD server.
	req.WaitForWS = true

	// Similarly, output recording is performed on the host rather than in the guest, so clear that bit from the request.
	req.RecordOutput = false

	op, err := agent.ExecInstance("", req, &args)
	if err != nil {
		return nil, err
	}

	instCmd := &qemuCmd{
		cmd:              op,
		attachedChildPid: 0,
		dataDone:         args.DataDone,
		cleanupFunc:      revert.Clone().Fail,
		controlSendCh:    controlSendCh,
		controlResCh:     controlResCh,
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceExec.Event(ctx, d, logger.Ctx{"command": req.Command}))

	revert.Success()
	return instCmd, nil
}

// LockExclusive attempts to get exclusive access to the instance's root volume.
func (d *microvm) LockExclusive() (*operationlock.InstanceOperation, error) {
	if d.IsRunning() {
		return nil, errors.New("Instance is running")
	}

	// Prevent concurrent operations the instance.
	op, err := operationlock.Create(d.Project().Name, d.Name(), operationlock.ActionCreate, false, false)
	if err != nil {
		return nil, err
	}

	return op, err
}
