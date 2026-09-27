package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/linux"
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
	assert.Equal(t, storageDrivers.ErrNotSupported, d.Snapshot(ctx, "snap0", nil, false, "", false, nil))
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

	// Write PID file pointing to a PID above the kernel's maximum, so no such process exists -> returns 0
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

// TestMicroVMWatcherTracking verifies that stopLibkrunMonitor deregisters only its own instance.
func TestMicroVMWatcherTracking(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	d.id = 4201

	const otherID = 4202

	libkrunWatchersLock.Lock()
	libkrunWatchers[d.ID()] = 1234
	libkrunWatchers[otherID] = 5678
	libkrunWatchersLock.Unlock()

	t.Cleanup(func() {
		libkrunWatchersLock.Lock()
		delete(libkrunWatchers, d.ID())
		delete(libkrunWatchers, otherID)
		libkrunWatchersLock.Unlock()
	})

	d.stopLibkrunMonitor()

	libkrunWatchersLock.Lock()
	_, ok := libkrunWatchers[d.ID()]
	otherPID := libkrunWatchers[otherID]
	libkrunWatchersLock.Unlock()

	assert.False(t, ok)
	assert.Equal(t, 5678, otherPID)
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

// TestLibkrunIdentityFromCmdline verifies extracting the project and instance name from the
// command line of a forklibkrun helper process.
func TestLibkrunIdentityFromCmdline(t *testing.T) {
	tests := []struct {
		name        string
		cmdline     string
		wantProject string
		wantInst    string
		wantOK      bool
	}{
		{
			name:        "complete",
			cmdline:     "/usr/bin/lxd\x00forklibkrun\x00--config\x00/logs/m1/microvm.conf\x00--project\x00default\x00--instance\x00m1\x00",
			wantProject: "default",
			wantInst:    "m1",
			wantOK:      true,
		},
		{
			name:        "no trailing NUL",
			cmdline:     "lxd\x00forklibkrun\x00--instance\x00m1\x00--project\x00foo",
			wantProject: "foo",
			wantInst:    "m1",
			wantOK:      true,
		},
		{
			name:    "missing instance",
			cmdline: "lxd\x00forklibkrun\x00--project\x00default\x00",
		},
		{
			name:    "missing project",
			cmdline: "lxd\x00forklibkrun\x00--instance\x00m1\x00",
		},
		{
			name:    "flag without value as last argument",
			cmdline: "lxd\x00forklibkrun\x00--project\x00default\x00--instance\x00",
		},
		{
			name:    "empty values",
			cmdline: "lxd\x00forklibkrun\x00--project\x00\x00--instance\x00\x00",
		},
		{
			name:    "unrelated process",
			cmdline: "/usr/bin/sleep\x0060\x00",
		},
		{
			name:    "empty",
			cmdline: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectName, instName, ok := libkrunIdentityFromCmdline([]byte(tt.cmdline))
			assert.Equal(t, tt.wantOK, ok)

			if tt.wantOK {
				assert.Equal(t, tt.wantProject, projectName)
				assert.Equal(t, tt.wantInst, instName)
			}
		})
	}
}

// TestMicroVMLimits verifies the vCPU count and memory size derived from the instance config.
func TestMicroVMLimits(t *testing.T) {
	tests := []struct {
		name       string
		config     map[string]string
		wantCPUs   uint8
		wantMemMiB uint32
		wantErr    string
	}{
		{
			name:       "defaults",
			config:     map[string]string{},
			wantCPUs:   1,
			wantMemMiB: 1024,
		},
		{
			name:       "explicit limits",
			config:     map[string]string{"limits.cpu": "4", "limits.memory": "512MiB"},
			wantCPUs:   4,
			wantMemMiB: 512,
		},
		{
			name:       "largest CPU count",
			config:     map[string]string{"limits.cpu": "255"},
			wantCPUs:   255,
			wantMemMiB: 1024,
		},
		{
			name:    "zero CPUs",
			config:  map[string]string{"limits.cpu": "0"},
			wantErr: "limits.cpu invalid",
		},
		{
			name:    "too many CPUs",
			config:  map[string]string{"limits.cpu": "256"},
			wantErr: "limits.cpu invalid",
		},
		{
			name:    "CPU range is not supported",
			config:  map[string]string{"limits.cpu": "0-3"},
			wantErr: "CPU pinning is not supported for MicroVM instances",
		},
		{
			name:    "CPU set is not supported",
			config:  map[string]string{"limits.cpu": "1,2"},
			wantErr: "CPU pinning is not supported for MicroVM instances",
		},
		{
			name:    "malformed memory",
			config:  map[string]string{"limits.memory": "bogus"},
			wantErr: "limits.memory invalid",
		},
		{
			name:    "memory below one MiB",
			config:  map[string]string{"limits.memory": "1024"},
			wantErr: "limits.memory invalid: 0 MiB is out of range",
		},
		{
			name:    "memory above the libkrun limit",
			config:  map[string]string{"limits.memory": "5000TiB"},
			wantErr: "is out of range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpus, memMiB, err := microVMLimits(tt.config)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCPUs, cpus)
			assert.Equal(t, tt.wantMemMiB, memMiB)
		})
	}
}

// startTestChild starts a shell running script and returns it with a pidfd referring to it.
// The test is skipped if pidfds are not available.
func startTestChild(t *testing.T, script string) (*exec.Cmd, *os.File) {
	t.Helper()

	cmd := exec.Command("sh", "-c", script)
	require.NoError(t, cmd.Start())

	pidFd, err := linux.PidFdOpen(cmd.Process.Pid, 0)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Skipf("PidFdOpen not supported: %v", err)
	}

	t.Cleanup(func() { _ = pidFd.Close() })

	return cmd, pidFd
}

// skipIfNoPidfdExitInfo skips the test if the running kernel lacks PIDFD_GET_INFO exit info.
func skipIfNoPidfdExitInfo(t *testing.T, err error) {
	t.Helper()

	if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		t.Skipf("PIDFD_GET_INFO ioctl not supported by running kernel: %v", err)
	}
}

// TestLibkrunWaitExit verifies that the exit watcher obtains the exit status of a process that is
// reaped concurrently, some time after it exited, and that the status maps to the right target.
func TestLibkrunWaitExit(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")

	tests := []struct {
		name       string
		script     string
		wantStatus int
		wantSignal syscall.Signal
		wantTarget string
	}{
		{name: "exit 0 is a reboot", script: "exit 0", wantStatus: 0, wantTarget: "reboot"},
		{name: "exit 1 is a stop", script: "exit 1", wantStatus: 1, wantTarget: "stop"},
		{name: "other exit status is a stop", script: "exit 3", wantStatus: 3, wantTarget: "stop"},
		{name: "killed is a stop", script: "kill -KILL $$", wantSignal: syscall.SIGKILL, wantTarget: "stop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, pidFd := startTestChild(t, tt.script)

			// Reap the child with a delay, as the subprocess package does concurrently for the
			// helper. The exit status is not available from the pidfd before that.
			reaped := make(chan struct{})
			go func() {
				defer close(reaped)

				time.Sleep(200 * time.Millisecond)
				_ = cmd.Wait()
			}()

			exitCode, hasExitCode, err := libkrunWaitExit(pidFd, 10*time.Second)
			<-reaped

			skipIfNoPidfdExitInfo(t, err)
			require.NoError(t, err)
			require.True(t, hasExitCode)

			waitStatus := syscall.WaitStatus(exitCode)
			if tt.wantSignal != 0 {
				assert.True(t, waitStatus.Signaled())
				assert.Equal(t, tt.wantSignal, waitStatus.Signal())
			} else {
				assert.True(t, waitStatus.Exited())
				assert.Equal(t, tt.wantStatus, waitStatus.ExitStatus())
			}

			assert.Equal(t, tt.wantTarget, d.libkrunOnStopTarget(cmd.Process.Pid, exitCode, hasExitCode))
		})
	}
}

// TestLibkrunWaitExitTimeout verifies that a process that is not reaped in time is reported
// without an exit status, which is handled as a stop.
func TestLibkrunWaitExitTimeout(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")

	cmd, pidFd := startTestChild(t, "exit 0")

	exitCode, hasExitCode, err := libkrunWaitExit(pidFd, 50*time.Millisecond)

	// Only reap the child now.
	_ = cmd.Wait()

	skipIfNoPidfdExitInfo(t, err)
	require.NoError(t, err)
	assert.False(t, hasExitCode)
	assert.Equal(t, "stop", d.libkrunOnStopTarget(cmd.Process.Pid, exitCode, hasExitCode))
}

// TestMicroVMLibkrunPid verifies the PID file handling and the helper command line check.
func TestMicroVMLibkrunPid(t *testing.T) {
	d, _ := newTestMicroVM(t, "m1")
	pidFile := d.libkrunPidFilePath()

	writePID := func(content string) {
		require.NoError(t, os.WriteFile(pidFile, []byte(content), 0640))
	}

	// No PID file.
	pid, err := d.libkrunPid()
	require.NoError(t, err)
	assert.Equal(t, 0, pid)

	// Malformed PID file.
	writePID("not-a-pid")
	pid, err = d.libkrunPid()
	require.Error(t, err)
	assert.Equal(t, -1, pid)

	// PID of a process that has exited and been reaped.
	gone := exec.Command("true")
	require.NoError(t, gone.Run())
	writePID(strconv.Itoa(gone.Process.Pid))
	pid, err = d.libkrunPid()
	require.NoError(t, err)
	assert.Equal(t, 0, pid)

	// PID of a running process that is not a forklibkrun helper.
	other := exec.Command("sleep", "60")
	require.NoError(t, other.Start())
	t.Cleanup(func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	})

	writePID(strconv.Itoa(other.Process.Pid) + "\n")
	pid, err = d.libkrunPid()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match a libkrun process")
	assert.Equal(t, -1, pid)
	assert.Equal(t, api.Stopped, d.statusCode())

	// PID of a running process with forklibkrun on its command line. The shell blocks reading
	// from a pipe that is kept open until the test finishes.
	helper := exec.Command("sh", "-c", "read -r line", "forklibkrun")
	stdin, err := helper.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, helper.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = helper.Wait()
	})

	writePID(strconv.Itoa(helper.Process.Pid))

	// Start returns as soon as the child calls exec, and for a short moment after that
	// /proc/<pid>/cmdline of the new program is still empty, so wait for it to be filled in.
	require.Eventually(t, func() bool {
		pid, err := d.libkrunPid()
		return err == nil && pid == helper.Process.Pid
	}, 5*time.Second, 10*time.Millisecond)

	assert.Equal(t, helper.Process.Pid, d.InitPID())
}

// resetLibkrunVsockProxy closes the shared proxy and clears its users.
func resetLibkrunVsockProxy() {
	libkrunVsockProxyLock.Lock()
	defer libkrunVsockProxyLock.Unlock()

	if libkrunVsockProxyListener != nil {
		_ = libkrunVsockProxyListener.Close()
		libkrunVsockProxyListener = nil
	}

	libkrunVsockProxySocket = ""
	libkrunVsockProxyUsers = map[int]struct{}{}
}

// libkrunVsockProxyState returns whether the shared proxy is listening and its number of users.
func libkrunVsockProxyState() (listening bool, users int) {
	libkrunVsockProxyLock.Lock()
	defer libkrunVsockProxyLock.Unlock()

	return libkrunVsockProxyListener != nil, len(libkrunVsockProxyUsers)
}

// TestMicroVMVsockProxyUsers verifies that the shared agent proxy stays up while any registered
// instance uses it, and that unmatched or repeated releases do not close it under other users.
func TestMicroVMVsockProxyUsers(t *testing.T) {
	d1, tempDir := newTestMicroVM(t, "m1")
	d1.id = 1

	d2 := &microvm{common: d1.common}
	d2.id = 2
	d2.name = "m2"

	unregistered := &microvm{common: d1.common}
	unregistered.id = 3
	unregistered.name = "m3"

	resetLibkrunVsockProxy()
	t.Cleanup(resetLibkrunVsockProxy)

	ctx := context.Background()
	proxySocket := filepath.Join(tempDir, "libkrun-vsock-proxy.sock")

	// The proxy needs LXD's VM unix socket to forward to.
	_, err := d1.ensureLibkrunVsockProxy(ctx)
	require.Error(t, err)

	listening, users := libkrunVsockProxyState()
	assert.False(t, listening)
	assert.Equal(t, 0, users)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "vsock-unix.socket"), nil, 0600))

	// The first user starts the listener.
	socket, err := d1.ensureLibkrunVsockProxy(ctx)
	require.NoError(t, err)
	assert.Equal(t, proxySocket, socket)
	assert.FileExists(t, proxySocket)

	// Registering the same instance again does not add a user.
	_, err = d1.ensureLibkrunVsockProxy(ctx)
	require.NoError(t, err)

	listening, users = libkrunVsockProxyState()
	assert.True(t, listening)
	assert.Equal(t, 1, users)

	_, err = d2.ensureLibkrunVsockProxy(ctx)
	require.NoError(t, err)

	_, users = libkrunVsockProxyState()
	assert.Equal(t, 2, users)

	// Releasing an instance that never registered must not affect the others.
	unregistered.releaseLibkrunVsockProxy()

	listening, users = libkrunVsockProxyState()
	assert.True(t, listening)
	assert.Equal(t, 2, users)

	// Releasing the same instance twice must not release another user.
	d1.releaseLibkrunVsockProxy()
	d1.releaseLibkrunVsockProxy()

	listening, users = libkrunVsockProxyState()
	assert.True(t, listening)
	assert.Equal(t, 1, users)
	assert.FileExists(t, proxySocket)

	// The last user closes the listener and removes the socket.
	d2.releaseLibkrunVsockProxy()

	listening, users = libkrunVsockProxyState()
	assert.False(t, listening)
	assert.Equal(t, 0, users)
	assert.NoFileExists(t, proxySocket)
}

// TestMicroVMVsockProxyReattach verifies that a running instance unknown to the proxy, as after a
// daemon restart, is registered again and replaces a stale socket left behind by the old daemon.
func TestMicroVMVsockProxyReattach(t *testing.T) {
	d, tempDir := newTestMicroVM(t, "m1")
	d.id = 1

	resetLibkrunVsockProxy()
	t.Cleanup(resetLibkrunVsockProxy)

	proxySocket := filepath.Join(tempDir, "libkrun-vsock-proxy.sock")

	// Without LXD's VM unix socket nothing is registered.
	d.reattachLibkrunVsockProxy()

	listening, users := libkrunVsockProxyState()
	assert.False(t, listening)
	assert.Equal(t, 0, users)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "vsock-unix.socket"), nil, 0600))
	require.NoError(t, os.WriteFile(proxySocket, []byte("stale"), 0600))

	d.reattachLibkrunVsockProxy()

	listening, users = libkrunVsockProxyState()
	assert.True(t, listening)
	assert.Equal(t, 1, users)

	info, err := os.Stat(proxySocket)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSocket)

	// Reattaching again is a no-op.
	d.reattachLibkrunVsockProxy()

	_, users = libkrunVsockProxyState()
	assert.Equal(t, 1, users)

	d.releaseLibkrunVsockProxy()

	listening, _ = libkrunVsockProxyState()
	assert.False(t, listening)
}
