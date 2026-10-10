package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMicroVMConfigRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, MicroVMConfigFileName)

	cfg := MicroVMConfigV1{
		CPUs:      2,
		MemoryMiB: 1024,
		Kernel: MicroVMConfigKernel{
			Path:    "/path/to/vmlinux",
			Format:  "auto",
			Cmdline: "console=hvc0 root=/dev/vda rw",
		},
		RootDisk:    "/path/to/root.img",
		ConfigDrive: "/path/to/config.mount",
		Console:     "/path/to/libkrun.console",
		NICs: []MicroVMConfigNIC{
			{
				Tap:    "tap0",
				HWAddr: "00:16:3e:11:22:33",
			},
		},
		Vsock: &MicroVMConfigVsock{
			AgentSocket: "/path/to/agent.sock",
			LXDPort:     8443,
			LXDSocket:   "/path/to/lxd.sock",
		},
	}

	err := WriteMicroVMConfig(configPath, cfg)
	require.NoError(t, err)

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0640), info.Mode().Perm())

	loaded, err := ReadMicroVMConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, &cfg, loaded)
}

func TestMicroVMConfigValidate(t *testing.T) {
	baseValid := func() MicroVMConfigV1 {
		return MicroVMConfigV1{
			CPUs:      1,
			MemoryMiB: 512,
			Kernel: MicroVMConfigKernel{
				Path:    "/vmlinux",
				Format:  "auto",
				Cmdline: "console=hvc0",
			},
			RootDisk:    "/root.img",
			ConfigDrive: "/config",
			Console:     "/console",
		}
	}

	tests := []struct {
		name    string
		wantErr string
		mutate  func(c *MicroVMConfigV1)
	}{
		{
			name:    "valid base",
			mutate:  func(_ *MicroVMConfigV1) {},
			wantErr: "",
		},
		{
			name: "missing cpus",
			mutate: func(c *MicroVMConfigV1) {
				c.CPUs = 0
			},
			wantErr: "cpus",
		},
		{
			name: "missing memory",
			mutate: func(c *MicroVMConfigV1) {
				c.MemoryMiB = 0
			},
			wantErr: "memory_mib",
		},
		{
			name: "missing kernel path",
			mutate: func(c *MicroVMConfigV1) {
				c.Kernel.Path = ""
			},
			wantErr: "kernel.path",
		},
		{
			name: "missing root disk",
			mutate: func(c *MicroVMConfigV1) {
				c.RootDisk = ""
			},
			wantErr: "root_disk",
		},
		{
			name: "missing config drive",
			mutate: func(c *MicroVMConfigV1) {
				c.ConfigDrive = ""
			},
			wantErr: "config_drive",
		},
		{
			name: "missing console",
			mutate: func(c *MicroVMConfigV1) {
				c.Console = ""
			},
			wantErr: "console",
		},
		{
			name: "invalid nic missing tap",
			mutate: func(c *MicroVMConfigV1) {
				c.NICs = []MicroVMConfigNIC{{HWAddr: "00:16:3e:11:22:33"}}
			},
			wantErr: "missing tap or hwaddr",
		},
		{
			name: "invalid nic missing hwaddr",
			mutate: func(c *MicroVMConfigV1) {
				c.NICs = []MicroVMConfigNIC{{Tap: "tap0"}}
			},
			wantErr: "missing tap or hwaddr",
		},
		{
			name: "incomplete vsock missing lxd socket",
			mutate: func(c *MicroVMConfigV1) {
				c.Vsock = &MicroVMConfigVsock{
					AgentSocket: "/agent.sock",
					LXDPort:     1234,
					LXDSocket:   "",
				}
			},
			wantErr: "vsock",
		},
		{
			name: "incomplete vsock missing agent socket",
			mutate: func(c *MicroVMConfigV1) {
				c.Vsock = &MicroVMConfigVsock{
					LXDPort:   1234,
					LXDSocket: "/lxd.sock",
				}
			},
			wantErr: "vsock",
		},
		{
			name: "incomplete vsock missing lxd port",
			mutate: func(c *MicroVMConfigV1) {
				c.Vsock = &MicroVMConfigVsock{
					AgentSocket: "/agent.sock",
					LXDSocket:   "/lxd.sock",
				}
			},
			wantErr: "vsock",
		},
		{
			name: "valid with vsock and nic",
			mutate: func(c *MicroVMConfigV1) {
				c.NICs = []MicroVMConfigNIC{{Tap: "tap0", HWAddr: "00:16:3e:11:22:33"}}
				c.Vsock = nil
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseValid()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestReadMicroVMConfigErrors verifies the error paths of reading a MicroVM config file.
func TestReadMicroVMConfigErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "malformed JSON",
			content: `{"version": 1,`,
			wantErr: "Failed decoding MicroVM config",
		},
		{
			name:    "empty file",
			content: "",
			wantErr: "Failed decoding MicroVM config",
		},
		{
			name:    "missing version",
			content: `{"body": {}}`,
			wantErr: `Unknown MicroVM config version "0"`,
		},
		{
			name:    "unknown version",
			content: `{"version": 2, "body": {}}`,
			wantErr: `Unknown MicroVM config version "2"`,
		},
		{
			name:    "missing body",
			content: `{"version": 1}`,
			wantErr: "Failed decoding MicroVM configuration (v1)",
		},
		{
			name:    "body of the wrong type",
			content: `{"version": 1, "body": "not-an-object"}`,
			wantErr: "Failed decoding MicroVM configuration (v1)",
		},
		{
			name:    "field of the wrong type",
			content: `{"version": 1, "body": {"cpus": "two"}}`,
			wantErr: "Failed decoding MicroVM configuration (v1)",
		},
		{
			name:    "body failing validation",
			content: `{"version": 1, "body": {"cpus": 1}}`,
			wantErr: `"memory_mib"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), MicroVMConfigFileName)
			require.NoError(t, os.WriteFile(configPath, []byte(tt.content), 0640))

			cfg, err := ReadMicroVMConfig(configPath)
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("missing file", func(t *testing.T) {
		cfg, err := ReadMicroVMConfig(filepath.Join(t.TempDir(), MicroVMConfigFileName))
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "Failed opening MicroVM config")
	})
}

// TestReadMicroVMConfigUnknownFields verifies that fields unknown to this version are ignored.
func TestReadMicroVMConfigUnknownFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), MicroVMConfigFileName)
	content := `{"version": 1, "future": true, "body": {"cpus": 1, "memory_mib": 512, "kernel": {"path": "/vmlinux", "extra": 1}, "root_disk": "/root.img", "config_drive": "/config", "console": "/console", "future": "x"}}`
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0640))

	cfg, err := ReadMicroVMConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, uint8(1), cfg.CPUs)
	assert.Equal(t, uint32(512), cfg.MemoryMiB)
	assert.Equal(t, "/vmlinux", cfg.Kernel.Path)
}
