package drivers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/state"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// newTestMicroVM creates a microvm struct for unit tests.
func newTestMicroVM(t *testing.T, name string) (*microvm, string) {
	t.Helper()

	tempDir := t.TempDir()
	t.Setenv("LXD_DIR", tempDir)

	d := &microvm{
		common: common{
			state:        &state.State{},
			architecture: 1, // ARCH_64BIT_INTEL_X86
			dbType:       instancetype.MicroVM,
			name:         name,
			project:      api.Project{Name: api.ProjectDefaultName},
			logger:       logger.Log,
			localConfig:  map[string]string{},
		},
	}

	instancePath := d.Path()
	require.NoError(t, os.MkdirAll(instancePath, 0700))
	require.NoError(t, os.MkdirAll(d.LogPath(), 0700))

	return d, tempDir
}

// TestMicroVMType confirms that MicroVM returns instancetype.MicroVM.
func TestMicroVMType(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	assert.Equal(t, instancetype.MicroVM, d.Type())
}

// TestMicroVMUnsupportedOperations verifies that unsupported operations return ErrNotSupported.
func TestMicroVMUnsupportedOperations(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	ctx := context.Background()

	assert.Equal(t, storageDrivers.ErrNotSupported, d.Migrate(nil))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.MigrateSend(ctx, instance.MigrateSendArgs{}, nil))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.MigrateReceive(ctx, instance.MigrateReceiveArgs{}, nil))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.Snapshot(ctx, "snap0", nil, false, "", nil))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.Freeze(ctx))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.Unfreeze(ctx))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.Rebuild(ctx, nil, nil))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.Restore(ctx, nil, false, "", nil))
	_, err := d.Export(nil, nil, time.Time{}, nil)
	assert.Equal(t, storageDrivers.ErrNotSupported, err)
	assert.Equal(t, storageDrivers.ErrNotSupported, d.ConversionReceive(instance.ConversionReceiveArgs{}, nil))
	_, err = d.UEFIVars()
	assert.Equal(t, storageDrivers.ErrNotSupported, err)
	assert.Equal(t, storageDrivers.ErrNotSupported, d.UEFIVarsUpdate(api.InstanceUEFIVars{}))
	assert.Equal(t, storageDrivers.ErrNotSupported, d.SetAffinity([]string{"0"}))
	_, err = d.CGroup()
	assert.Equal(t, storageDrivers.ErrNotSupported, err)
	assert.Equal(t, storageDrivers.ErrNotSupported, d.OnHook("start", nil))
	_, err = d.Metrics(nil)
	assert.Equal(t, storageDrivers.ErrNotSupported, err)
	_, err = d.FileSFTPConn()
	assert.Equal(t, storageDrivers.ErrNotSupported, err)
	_, err = d.FileSFTP()
	assert.Equal(t, storageDrivers.ErrNotSupported, err)

	canMigrate, live := d.CanMigrate()
	assert.False(t, canMigrate)
	assert.False(t, live)
	assert.False(t, d.IsPrivileged())
	assert.Empty(t, d.FirmwarePath())
}

// TestMicroVMStatusCodeStoppedWhenNoPID tests that statusCode() returns api.Stopped when no process runs.
func TestMicroVMStatusCodeStoppedWhenNoPID(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	assert.Equal(t, api.Stopped, d.statusCode())
	assert.False(t, d.IsRunning())
}

// TestMicroVMShutdownStopped verifies that shutting down a stopped MicroVM is rejected.
func TestMicroVMShutdownStopped(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	assert.ErrorIs(t, d.Shutdown(context.Background(), time.Second), ErrInstanceIsStopped)
}

// TestMicroVMInitPID verifies InitPID() reads libkrun.pid.
func TestMicroVMInitPID(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")

	// No PID file -> returns 0
	assert.Equal(t, 0, d.InitPID())

	// Write PID file pointing to current process, but command line won't match forklibkrun -> returns 0
	pidFile := d.libkrunPidFilePath()
	require.NoError(t, os.WriteFile(pidFile, []byte("1234567"), 0640))
	assert.Equal(t, 0, d.InitPID())
}

// TestMicroVMConsoleInvalidProtocol tests that unknown console protocols are rejected.
func TestMicroVMConsoleInvalidProtocol(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	ctx := context.Background()

	_, _, err := d.Console(ctx, instance.ConsoleTypeVGA)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unknown protocol")

	_, _, err = d.Console(ctx, instance.ConsoleTypeConsole)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed connecting to console socket")
}

// TestMicroVMWatcherTracking verifies monitorLibkrunProcess watcher registration.
func TestMicroVMWatcherTracking(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")

	key := d.ID()

	d.stopLibkrunMonitor()

	libkrunWatchersLock.Lock()
	_, ok := libkrunWatchers[key]
	libkrunWatchersLock.Unlock()

	assert.False(t, ok)
}

// TestMicroVMPeerAddrPrefix verifies the peer address constant formatting.
func TestMicroVMPeerAddrPrefix(t *testing.T) {
	assert.Equal(t, "@lxd-microvm:", MicroVMPeerAddrPrefix)
}

// TestMicroVMOnStopTarget verifies reboot vs stop exit code translation.
func TestMicroVMOnStopTarget(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")

	// Fallback exit code paths.
	assert.Equal(t, "stop", d.libkrunOnStopTarget(0, 0, false))
	assert.Equal(t, "reboot", d.libkrunOnStopTarget(0, int(syscall.WaitStatus(0)), true))
	assert.Equal(t, "stop", d.libkrunOnStopTarget(0, int(syscall.WaitStatus(1<<8)), true))
	assert.Equal(t, "stop", d.libkrunOnStopTarget(0, int(syscall.WaitStatus(syscall.SIGKILL)), true))

	// Cleanup removes runtime files.
	d.cleanupLibkrunRuntimeFiles()
}

// TestMicroVMDeviceName verifies MicroVM mount tags retain QEMU's encoding and length behavior.
func TestMicroVMDeviceName(t *testing.T) {
	for _, name := range []string{"data", "path/with-hyphen", strings.Repeat("long", 16)} {
		expected := qemuDeviceNameOrID(microvmDeviceNamePrefix, name, "", microvmDeviceNameMaxLength)
		assert.Equal(t, expected, microVMDeviceName(name))
	}
}

// TestMicroVMPathBuilders verifies per-instance runtime file paths for libkrun.
func TestMicroVMPathBuilders(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")

	assert.Equal(t, d.LogPath()+"/libkrun.pid", d.libkrunPidFilePath())
	assert.Equal(t, d.LogPath()+"/libkrun.console", d.libkrunConsolePath())
	assert.Equal(t, d.LogPath()+"/libkrun.agent.sock", d.libkrunAgentSocketPath())
	assert.Equal(t, d.LogPath()+"/microvm.conf", d.microVMConfigPath())
	assert.Equal(t, d.LogPath()+"/microvm.log", d.LogFilePath())
}

// TestMicroVMGetKernelPath verifies kernel path detection for vmlinuz, vmlinux, symlinks, and fallback.
func TestMicroVMGetKernelPath(t *testing.T) {
	d, tempDir := newTestMicroVM(t, "m1")
	microvmDir := filepath.Join(tempDir, "microvm")
	require.NoError(t, os.MkdirAll(microvmDir, 0700))

	// Neither file exists: returns empty string.
	assert.Empty(t, d.getKernelPath())

	// Only vmlinux exists.
	vmlinuxPath := filepath.Join(microvmDir, "vmlinux")
	require.NoError(t, os.WriteFile(vmlinuxPath, []byte("fake-vmlinux"), 0644))
	assert.Equal(t, vmlinuxPath, d.getKernelPath())
	assert.Equal(t, vmlinuxPath, d.KernelPath())

	// Both exist: vmlinuz takes precedence (first candidate).
	vmlinuzPath := filepath.Join(microvmDir, "vmlinuz")
	require.NoError(t, os.WriteFile(vmlinuzPath, []byte("fake-vmlinuz"), 0644))
	assert.Equal(t, vmlinuzPath, d.getKernelPath())

	// Symlink resolution: target is returned when symlink points to real file.
	require.NoError(t, os.Remove(vmlinuzPath))
	require.NoError(t, os.Symlink(vmlinuxPath, vmlinuzPath))
	assert.Equal(t, vmlinuxPath, d.getKernelPath())
}

// TestMicroVMGetLibkrunPath verifies libkrun dynamic library discovery and LIBKRUN_PATH override.
func TestMicroVMGetLibkrunPath(t *testing.T) {
	d, tempDir := newTestMicroVM(t, "m1")
	libkrunDir := filepath.Join(tempDir, "libkrun")
	require.NoError(t, os.MkdirAll(libkrunDir, 0700))

	// No env, no file present: returns empty string.
	t.Setenv("LIBKRUN_PATH", "")
	assert.Empty(t, d.getLibkrunPath())

	// LIBKRUN_PATH env override takes precedence.
	t.Setenv("LIBKRUN_PATH", "/custom/libkrun.so.2.0.0")
	assert.Equal(t, "/custom/libkrun.so.2.0.0", d.getLibkrunPath())
	t.Setenv("LIBKRUN_PATH", "")

	// Discovers libkrun.so.2 in $LXD_DIR/libkrun/.
	so2Path := filepath.Join(libkrunDir, "libkrun.so.2")
	require.NoError(t, os.WriteFile(so2Path, []byte("fake-libkrun-so-2"), 0644))
	assert.Equal(t, so2Path, d.getLibkrunPath())

	// libkrun.so takes precedence over libkrun.so.2 (candidate order).
	soPath := filepath.Join(libkrunDir, "libkrun.so")
	require.NoError(t, os.WriteFile(soPath, []byte("fake-libkrun-so"), 0644))
	assert.Equal(t, soPath, d.getLibkrunPath())

	// Symlink resolution: target is returned when symlink points to actual file.
	require.NoError(t, os.Remove(soPath))
	require.NoError(t, os.Symlink(so2Path, soPath))
	assert.Equal(t, so2Path, d.getLibkrunPath())
}

// TestMicroVMValidateStartup verifies the constraints checked before starting a MicroVM.
func TestMicroVMValidateStartup(t *testing.T) {
	rootDisk := deviceConfig.Devices{"root": {"type": "disk", "path": "/", "pool": "default"}}

	tests := []struct {
		name       string
		devices    deviceConfig.Devices
		config     map[string]string
		stateful   bool
		statusCode api.StatusCode
		wantErr    string
	}{
		{
			name:       "stopped instance can start",
			devices:    rootDisk,
			statusCode: api.Stopped,
		},
		{
			name:       "missing root disk",
			devices:    deviceConfig.Devices{},
			statusCode: api.Stopped,
			wantErr:    api.ErrNoRootDisk.Error(),
		},
		{
			name:       "running instance",
			devices:    rootDisk,
			statusCode: api.Running,
			wantErr:    "The instance is already running",
		},
		{
			name:       "errored instance",
			devices:    rootDisk,
			statusCode: api.Error,
			wantErr:    "The instance cannot be started as in Error status",
		},
		{
			name:       "stateful start rejected",
			devices:    rootDisk,
			stateful:   true,
			statusCode: api.Stopped,
			wantErr:    "Stateful start is not supported for MicroVM instances",
		},
		{
			name:       "start protection",
			devices:    rootDisk,
			config:     map[string]string{"security.protection.start": "true"},
			statusCode: api.Stopped,
			wantErr:    "Instance is protected from being started",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := newTestMicroVM(t, "m1")
			d.expandedDevices = tt.devices
			d.expandedConfig = tt.config

			err := d.validateStartup(tt.stateful, tt.statusCode)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}

			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestMicroVMGenerateAgentMountsFile verifies the lxd-agent mount configuration written for disk shares.
func TestMicroVMGenerateAgentMountsFile(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	configPath := filepath.Join(d.Path(), "config")
	require.NoError(t, os.MkdirAll(configPath, 0700))
	agentMountsPath := filepath.Join(configPath, "agent-mounts.json")

	readMounts := func() []instancetype.VMAgentMount {
		t.Helper()

		content, err := os.ReadFile(agentMountsPath)
		require.NoError(t, err)

		var mounts []instancetype.VMAgentMount
		require.NoError(t, json.Unmarshal(content, &mounts))

		return mounts
	}

	// Test 1: Only filesystem shares are exposed, not the root disk, block volumes or NICs.
	d.expandedDevices = deviceConfig.Devices{
		"root":  {"type": "disk", "path": "/", "pool": "default"},
		"host":  {"type": "disk", "path": "/mnt/host", "source": "/srv/data"},
		"vol":   {"type": "disk", "path": "/mnt/vol", "source": "vol1", "pool": "default", "readonly": "true"},
		"block": {"type": "disk", "source": "vol2", "pool": "default"},
		"eth0":  {"type": "nic", "network": "lxdbr0"},
	}

	require.NoError(t, d.generateAgentMountsFile())
	mounts := readMounts()
	require.Len(t, mounts, 2)
	assert.ElementsMatch(t, []instancetype.VMAgentMount{
		{Source: microVMDeviceName("host"), Target: "/mnt/host", FSType: "virtiofs"},
		{Source: microVMDeviceName("vol"), Target: "/mnt/vol", FSType: "virtiofs", Options: []string{"ro"}},
	}, mounts)

	// Test 2: The file is not rewritten when the mount set is unchanged.
	oldTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(agentMountsPath, oldTime, oldTime))
	require.NoError(t, d.generateAgentMountsFile())
	info, err := os.Stat(agentMountsPath)
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(oldTime))

	// Test 3: Removing all shares writes an empty list rather than leaving stale mounts.
	d.expandedDevices = deviceConfig.Devices{"root": {"type": "disk", "path": "/", "pool": "default"}}
	// The file is written read-only, allow rewriting it when the test is not run as root.
	require.NoError(t, os.Chmod(agentMountsPath, 0600))
	require.NoError(t, d.generateAgentMountsFile())
	assert.Empty(t, readMounts())
}
