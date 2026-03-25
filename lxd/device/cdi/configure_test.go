package cdi

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/instance"
)

// mockInstance is a simple mock for instance.Instance.
type mockInstance struct {
	instance.Instance
	rootfsPath string
}

func (m *mockInstance) RootfsPath() string {
	return m.rootfsPath
}

func TestGenerateFromCDI(t *testing.T) {
	// Setup temp dir for mounts
	tmpDir, err := os.MkdirTemp("", "lxd-cdi-test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a dummy host path for mount test
	hostPath := filepath.Join(tmpDir, "host-path")
	err = os.Mkdir(hostPath, 0755)
	require.NoError(t, err)

	hostDevicePath := filepath.Join(tmpDir, "dev-zero")
	f, err := os.Create(hostDevicePath)
	require.NoError(t, err)
	f.Close()

	// Save original generateSpec and restore after
	origGenerateSpec := generateSpec
	defer func() { generateSpec = origGenerateSpec }()
	t.Run("Success with Specific Device", func(t *testing.T) {
		generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
			return &specs.Spec{
				Version: "0.5.0",
				Kind:    "nvidia.com/gpu",
				Devices: []specs.Device{
					{
						Name: "gpu0",
						ContainerEdits: specs.ContainerEdits{
							DeviceNodes: []*specs.DeviceNode{
								{Path: "/dev/mydevice", HostPath: hostDevicePath, Major: 1, Minor: 5},
							},
						},
					},
					{
						Name: "gpu1", // Should be ignored
						ContainerEdits: specs.ContainerEdits{
							DeviceNodes: []*specs.DeviceNode{
								{Path: "/dev/other"},
							},
						},
					},
				},
				ContainerEdits: specs.ContainerEdits{ // General edits
					Hooks: []*specs.Hook{
						{
							HookName: "create-symlinks",
							Args:     []string{"create-symlinks", "--link", "/target::/link"},
						},
					},
					Mounts: []*specs.Mount{
						{
							HostPath:      hostPath,
							ContainerPath: "/mnt/container",
							Options:       []string{"ro", "bind"},
						},
					},
				},
			}, nil
		}

		inst := &mockInstance{rootfsPath: "/tmp/rootfs"}
		cdiID := ID{Vendor: NVIDIA, Class: GPU, Name: "gpu0"} // Matching "gpu0"

		config, hooks, err := GenerateFromCDI(false, inst, cdiID)
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.NotNil(t, hooks)

		// Check Hooks
		assert.Equal(t, "/tmp/rootfs", hooks.ContainerRootFS)
		// General edit hook
		assert.Contains(t, hooks.Symlinks, SymlinkEntry{Target: "/target", Link: "/link"})

		// Check ConfigDevices
		// Device from specific "gpu0"
		assert.Len(t, config.UnixCharDevs, 1)
		assert.Equal(t, "/dev/mydevice", config.UnixCharDevs[0]["path"])
		assert.Equal(t, hostDevicePath, config.UnixCharDevs[0]["source"])
		assert.Equal(t, "1", config.UnixCharDevs[0]["major"])
		assert.Equal(t, "5", config.UnixCharDevs[0]["minor"])

		// Mount from general edits
		assert.Len(t, config.BindMounts, 1)
		assert.Equal(t, hostPath, config.BindMounts[0]["source"])
		assert.Equal(t, "/mnt/container", config.BindMounts[0]["path"])
	})

	t.Run("Device Name Mismatch", func(t *testing.T) {
		// If device name doesn't match, specific edits shouldn't be applied
		generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
			return &specs.Spec{
				Version: "0.5.0",
				Kind:    "nvidia.com/gpu",
				Devices: []specs.Device{
					{
						Name: "gpu0",
						ContainerEdits: specs.ContainerEdits{
							DeviceNodes: []*specs.DeviceNode{
								{Path: "/dev/mydevice", HostPath: hostDevicePath, Major: 1, Minor: 5},
							},
						},
					},
				},
			}, nil
		}

		inst := &mockInstance{rootfsPath: "/tmp/rootfs"}
		cdiID := ID{Vendor: NVIDIA, Class: GPU, Name: "gpu1"} // Mismatch

		config, _, err := GenerateFromCDI(false, inst, cdiID)
		assert.NoError(t, err)
		assert.Empty(t, config.UnixCharDevs)
	})

	t.Run("GenerateSpec Failure", func(t *testing.T) {
		generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
			return nil, errors.New("mock error")
		}

		inst := &mockInstance{rootfsPath: "/tmp/rootfs"}
		cdiID := ID{Vendor: NVIDIA, Class: GPU, Name: "gpu0"}
		_, _, err := GenerateFromCDI(false, inst, cdiID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mock error")
	})

	t.Run("Device with UID and GID", func(t *testing.T) {
		uid := uint32(1000)
		gid := uint32(44)
		generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
			return &specs.Spec{
				Version: "0.5.0",
				Kind:    "nvidia.com/gpu",
				Devices: []specs.Device{
					{
						Name: "gpu0",
						ContainerEdits: specs.ContainerEdits{
							DeviceNodes: []*specs.DeviceNode{
								{
									Path:     "/dev/dri/card0",
									HostPath: hostDevicePath,
									Major:    226,
									Minor:    0,
									UID:      &uid,
									GID:      &gid,
								},
							},
						},
					},
				},
			}, nil
		}

		inst := &mockInstance{rootfsPath: "/tmp/rootfs"}
		cdiID := ID{Vendor: NVIDIA, Class: GPU, Name: "gpu0"}

		config, _, err := GenerateFromCDI(false, inst, cdiID)
		assert.NoError(t, err)
		assert.NotNil(t, config)

		// Check that UID and GID are properly set
		assert.Len(t, config.UnixCharDevs, 1)
		assert.Equal(t, "/dev/dri/card0", config.UnixCharDevs[0]["path"])
		assert.Equal(t, "226", config.UnixCharDevs[0]["major"])
		assert.Equal(t, "0", config.UnixCharDevs[0]["minor"])
		assert.Equal(t, "1000", config.UnixCharDevs[0]["uid"])
		assert.Equal(t, "44", config.UnixCharDevs[0]["gid"])
	})

	t.Run("Device without UID and GID", func(t *testing.T) {
		generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
			return &specs.Spec{
				Version: "0.5.0",
				Kind:    "amd.com/gpu",
				Devices: []specs.Device{
					{
						Name: "gpu0",
						ContainerEdits: specs.ContainerEdits{
							DeviceNodes: []*specs.DeviceNode{
								{
									Path:     "/dev/kfd",
									HostPath: hostDevicePath,
									Major:    509,
									Minor:    0,
									// No UID or GID set
								},
							},
						},
					},
				},
			}, nil
		}

		inst := &mockInstance{rootfsPath: "/tmp/rootfs"}
		cdiID := ID{Vendor: NVIDIA, Class: GPU, Name: "gpu0"}

		config, _, err := GenerateFromCDI(false, inst, cdiID)
		assert.NoError(t, err)
		assert.NotNil(t, config)

		// Check that device is created without UID/GID fields
		assert.Len(t, config.UnixCharDevs, 1)
		assert.Equal(t, "/dev/kfd", config.UnixCharDevs[0]["path"])
		assert.NotContains(t, config.UnixCharDevs[0], "uid")
		assert.NotContains(t, config.UnixCharDevs[0], "gid")
		assert.Equal(t, "509", config.UnixCharDevs[0]["major"])
		assert.Equal(t, "0", config.UnixCharDevs[0]["minor"])
	})

	t.Run("Multiple Devices with Mixed UID/GID", func(t *testing.T) {
		uid1 := uint32(1000)
		gid1 := uint32(44)
		// Second device has no UID/GID
		generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
			return &specs.Spec{
				Version: "0.5.0",
				Kind:    "nvidia.com/gpu",
				Devices: []specs.Device{
					{
						Name: "all",
						ContainerEdits: specs.ContainerEdits{
							DeviceNodes: []*specs.DeviceNode{
								{
									Path:     "/dev/dri/card0",
									HostPath: hostDevicePath,
									Major:    226,
									Minor:    0,
									UID:      &uid1,
									GID:      &gid1,
								},
								{
									Path:     "/dev/dri/renderD128",
									HostPath: hostDevicePath,
									Major:    226,
									Minor:    128,
									// No UID/GID
								},
							},
						},
					},
				},
			}, nil
		}

		inst := &mockInstance{rootfsPath: "/tmp/rootfs"}
		cdiID := ID{Vendor: NVIDIA, Class: GPU, Name: "all"}

		config, _, err := GenerateFromCDI(false, inst, cdiID)
		assert.NoError(t, err)
		assert.NotNil(t, config)

		// Check that we have two devices
		assert.Len(t, config.UnixCharDevs, 2)

		// First device should have UID/GID
		assert.Equal(t, "/dev/dri/card0", config.UnixCharDevs[0]["path"])
		assert.Equal(t, "1000", config.UnixCharDevs[0]["uid"])
		assert.Equal(t, "44", config.UnixCharDevs[0]["gid"])

		// Second device should not have UID/GID
		assert.Equal(t, "/dev/dri/renderD128", config.UnixCharDevs[1]["path"])
		assert.NotContains(t, config.UnixCharDevs[1], "uid")
		assert.NotContains(t, config.UnixCharDevs[1], "gid")
	})
}

func TestSpecMountToInstanceDev_SymlinkHostPath(t *testing.T) {
	// Scenario 1:
	// Setup virtual filesystem as follows;
	//  symlink: /lib -> /usr/lib dir
	//  file: /usr/lib/mylib
	//  file: /bar
	//  symlink: /lib/foo -> /bar file

	configDevices := &ConfigDevices{}
	mounts := make([]*specs.Mount, 0, 2)

	tmpDir, err := os.MkdirTemp("", "lxd-cdi-mount-symlink-test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Setup /usr/lib dir.
	err = os.MkdirAll(filepath.Join(tmpDir, "usr", "lib"), 0755)
	require.NoError(t, err)

	// Symlink /lib to /usr/lib.
	err = os.Symlink(filepath.Join(tmpDir, "usr", "lib"), filepath.Join(tmpDir, "lib"))
	require.NoError(t, err)

	// Create /usr/lib/mylib1.
	cdiSpecHostPath1 := filepath.Join(tmpDir, "lib", "mylib") // Host path from CDI spec uses /lib.
	fd, err := os.Create(cdiSpecHostPath1)
	require.NoError(t, err)
	fd.Close()

	expectedHostPath1 := filepath.Join(tmpDir, "usr", "lib", "mylib") // After symlink deref it should use /usr/lib.

	mounts = append(mounts, &specs.Mount{
		HostPath:      cdiSpecHostPath1,
		ContainerPath: cdiSpecHostPath1,
		Options:       []string{"foo", "bar"},
	})

	// Scenario 2:
	// Setup virtual filesystem as follows:
	//  symlink: /lib -> /usr/lib dir
	//  file: /mylib2
	//  symlink: /lib/myfoolib2 -> /mylib2 file

	// Create /mylib2.
	expectedHostPath2 := filepath.Join(tmpDir, "mylib2")
	cdiSpecHostPath2 := filepath.Join(tmpDir, "lib", "myfoolib2") // Host path from CDI spec uses /lib.
	expectedContainerSymlinkPath2 := filepath.Join(tmpDir, "usr", "lib", "myfoolib2")
	fd, err = os.Create(expectedHostPath2)
	require.NoError(t, err)
	fd.Close()

	// Symlink /lib/myfoolib2 to /mylib2.
	err = os.Symlink(expectedHostPath2, cdiSpecHostPath2)
	require.NoError(t, err)

	mounts = append(mounts, &specs.Mount{
		HostPath:      cdiSpecHostPath2,
		ContainerPath: cdiSpecHostPath2,
		Options:       []string{"foo", "foo", "bar"},
	})

	indirectSymlinks, err := specMountToInstanceDev(configDevices, ID{Vendor: AMD, Class: GPU, Name: "gpu0"}, mounts)
	require.NoError(t, err)

	require.Len(t, configDevices.BindMounts, 2)
	assert.Equal(t, expectedHostPath1, configDevices.BindMounts[0]["source"])
	assert.Equal(t, expectedHostPath1, configDevices.BindMounts[0]["path"])
	assert.Equal(t, "foo,bar", configDevices.BindMounts[0]["raw.mount.options"])

	assert.Equal(t, expectedHostPath2, configDevices.BindMounts[1]["source"])
	assert.Equal(t, expectedHostPath2, configDevices.BindMounts[1]["path"])
	assert.Equal(t, "foo,bar", configDevices.BindMounts[1]["raw.mount.options"])

	require.Len(t, indirectSymlinks, 1)
	assert.Equal(t, SymlinkEntry{Target: expectedHostPath2, Link: expectedContainerSymlinkPath2}, indirectSymlinks[0])
}
