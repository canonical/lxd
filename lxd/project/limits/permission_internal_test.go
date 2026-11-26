package limits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
)

func TestParseHostIDMapRange(t *testing.T) {
	for _, mode := range []string{"uid", "gid", "both"} {
		var isUID, isGID bool
		switch mode {
		case "uid":
			isUID = true
		case "gid":
			isGID = true
		case "both":
			isUID = true
			isGID = true
		}

		idmaps, err := parseHostIDMapRange(isUID, isGID, "foo")
		assert.Error(t, err)
		assert.Nil(t, idmaps)

		idmaps, err = parseHostIDMapRange(isUID, isGID, "1000")
		expected := []idmap.IdmapEntry{
			{
				Isuid:    isUID,
				Isgid:    isGID,
				Hostid:   1000,
				Maprange: 1,
				Nsid:     -1,
			},
		}

		assert.NoError(t, err)
		assert.Equal(t, expected, idmaps)

		idmaps, err = parseHostIDMapRange(isUID, isGID, "1000-1001")
		expected = []idmap.IdmapEntry{
			{
				Isuid:    isUID,
				Isgid:    isGID,
				Hostid:   1000,
				Maprange: 2,
				Nsid:     -1,
			},
		}

		assert.NoError(t, err)
		assert.Equal(t, expected, idmaps)

		idmaps, err = parseHostIDMapRange(isUID, isGID, "1000-1001,1002")
		expected = []idmap.IdmapEntry{
			{
				Isuid:    isUID,
				Isgid:    isGID,
				Hostid:   1000,
				Maprange: 2,
				Nsid:     -1,
			},
			{
				Isuid:    isUID,
				Isgid:    isGID,
				Hostid:   1002,
				Maprange: 1,
				Nsid:     -1,
			},
		}

		assert.NoError(t, err)
		assert.Equal(t, expected, idmaps)
	}
}

func checkProfileRestrictions(projectConfig map[string]string, profileConfig map[string]string) error {
	proj := api.Project{
		Name:   "proj1",
		Config: projectConfig,
	}

	prof := api.Profile{
		Name:   "prof1",
		Config: profileConfig,
	}

	return checkInstanceRestrictions(proj, []api.Instance{}, []api.Profile{prof})
}

func TestProjectLowLevelRestrictions(t *testing.T) {
	err := checkProfileRestrictions(
		map[string]string{},
		map[string]string{
			"boot.host_shutdown_timeout": "15",
		})
	require.ErrorContains(t, err, "forbidden")

	err = checkProfileRestrictions(
		map[string]string{
			"restricted":                     "true",
			"restricted.containers.lowlevel": "allow",
		},
		map[string]string{
			"security.devlxd.images": "true",
		})
	require.NoError(t, err)

	err = checkProfileRestrictions(
		map[string]string{
			"restricted":                           "true",
			"restricted.virtual-machines.lowlevel": "allow",
		},
		map[string]string{
			"limits.memory.hugepages": "true",
		})
	require.NoError(t, err)

	// Configuration raw.apparmor must be forbidden for VMs when
	// restricted.virtual-machines.lowlevel is blocked (default).
	err = checkProfileRestrictions(
		map[string]string{
			"restricted": "true",
		},
		map[string]string{
			"raw.apparmor": "/some/path r,",
		})
	require.ErrorContains(t, err, "forbidden")

	// Configuration raw.qemu.conf must be forbidden for VMs when
	// restricted.virtual-machines.lowlevel is blocked (default).
	err = checkProfileRestrictions(
		map[string]string{
			"restricted": "true",
		},
		map[string]string{
			"raw.qemu.conf": "[drive]\nfile=/etc/secret\n",
		})
	require.ErrorContains(t, err, "forbidden")

	// Verify raw.apparmor and raw.qemu.conf are allowed when both lowlevel settings are allowed.
	// Both are needed because profiles apply to both container and VM instance types.
	err = checkProfileRestrictions(
		map[string]string{
			"restricted":                           "true",
			"restricted.containers.lowlevel":       "allow",
			"restricted.virtual-machines.lowlevel": "allow",
		},
		map[string]string{
			"raw.apparmor": "/etc/secrets r,",
		})
	require.NoError(t, err)

	err = checkProfileRestrictions(
		map[string]string{
			"restricted":                           "true",
			"restricted.virtual-machines.lowlevel": "allow",
		},
		map[string]string{
			"raw.qemu.conf": "[drive]\nfile=/etc/secret\n",
		})
	require.NoError(t, err)
}

// checkContainerInstanceRestrictions runs the project restriction checks against a single
// container instance carrying the given config.
func checkContainerInstanceRestrictions(projectConfig map[string]string, instanceConfig map[string]string) error {
	proj := api.Project{
		Name:   "proj1",
		Config: projectConfig,
	}

	inst := api.Instance{
		Name: "c1",
		Type: string(api.InstanceTypeContainer),
	}

	inst.Config = instanceConfig

	return checkInstanceRestrictions(proj, []api.Instance{inst}, []api.Profile{})
}

func TestProjectRestrictedContainerPrivilegeIsolated(t *testing.T) {
	isolatedProject := map[string]string{
		"restricted":                      "true",
		"restricted.containers.privilege": "isolated",
	}

	// A container that omits "security.idmap.isolated" must be rejected, because the key
	// defaults to non-isolated and the restriction must not be bypassable by simply
	// leaving it unset.
	err := checkContainerInstanceRestrictions(isolatedProject, map[string]string{})
	require.ErrorContains(t, err, "Non-isolated containers are forbidden")

	// An explicit "security.idmap.isolated=false" must be rejected.
	err = checkContainerInstanceRestrictions(isolatedProject, map[string]string{
		"security.idmap.isolated": "false",
	})
	require.ErrorContains(t, err, "Non-isolated containers are forbidden")

	// An explicit empty "security.idmap.isolated" must be rejected.
	err = checkContainerInstanceRestrictions(isolatedProject, map[string]string{
		"security.idmap.isolated": "",
	})
	require.ErrorContains(t, err, "Non-isolated containers are forbidden")

	// An isolated container must be allowed.
	err = checkContainerInstanceRestrictions(isolatedProject, map[string]string{
		"security.idmap.isolated": "true",
	})
	require.NoError(t, err)
}

// checkContainerInstanceDeviceRestrictions runs the project restriction checks against a
// single container instance carrying the given device.
func checkContainerInstanceDeviceRestrictions(projectConfig map[string]string, device map[string]string) error {
	proj := api.Project{
		Name:   "proj1",
		Config: projectConfig,
	}

	inst := api.Instance{
		Name:    "c1",
		Type:    string(api.InstanceTypeContainer),
		Devices: map[string]map[string]string{"eth1": device},
	}

	return checkInstanceRestrictions(proj, []api.Instance{inst}, []api.Profile{})
}

func TestProjectRestrictedManagedNIC(t *testing.T) {
	projectConfig := map[string]string{
		"restricted":             "true",
		"restricted.devices.nic": "managed",
	}

	tests := []struct {
		name          string
		device        map[string]string
		expectedError string
	}{
		{
			name:   "Managed network allowed",
			device: map[string]string{"type": "nic", "network": "lxdbr0"},
		},
		{
			name:   "Unmanaged p2p allowed when peered",
			device: map[string]string{"type": "nic", "nictype": "p2p", "peer": "c2:eth1"},
		},
		{
			name:          "Unmanaged p2p refused when not peered",
			device:        map[string]string{"type": "nic", "nictype": "p2p"},
			expectedError: "Only managed network devices are allowed",
		},
		{
			name:          "Peer option on another NIC type does not bypass the check",
			device:        map[string]string{"type": "nic", "nictype": "bridged", "peer": "c2:eth1"},
			expectedError: "Only managed network devices are allowed",
		},
		{
			name:          "Peered p2p without an explicit NIC type refused",
			device:        map[string]string{"type": "nic", "peer": "c2:eth1"},
			expectedError: "Only managed network devices are allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkContainerInstanceDeviceRestrictions(projectConfig, tt.device)
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
		})
	}

	// Even a peered p2p NIC must be refused when the restriction blocks network devices.
	blockedProject := map[string]string{
		"restricted":             "true",
		"restricted.devices.nic": "block",
	}

	err := checkContainerInstanceDeviceRestrictions(blockedProject, map[string]string{"type": "nic", "nictype": "p2p", "peer": "c2:eth1"})
	require.ErrorContains(t, err, "Network devices are forbidden")
}

func TestCheckRestrictionsOnVolatileConfigRemoval(t *testing.T) {
	proj := api.Project{
		Name:   "proj1",
		Config: map[string]string{"restricted": "true"},
	}

	currentConfig := map[string]string{
		"volatile.eth1.peer.link_id": "link-id",
		"volatile.eth1.host_name":    "veth1234",
		"volatile.apply_template":    "create",
	}

	// A user update cannot remove a protected volatile key.
	err := checkRestrictionsOnVolatileConfig(proj, instancetype.Container, "c1", map[string]string{}, currentConfig, false, false)
	require.ErrorContains(t, err, "Removing")

	// Keeping the keys is allowed.
	err = checkRestrictionsOnVolatileConfig(proj, instancetype.Container, "c1", currentConfig, currentConfig, false, false)
	require.NoError(t, err)

	// Safe keys can still be removed.
	safeRemoved := map[string]string{
		"volatile.eth1.peer.link_id": "link-id",
		"volatile.eth1.host_name":    "veth1234",
	}

	err = checkRestrictionsOnVolatileConfig(proj, instancetype.Container, "c1", safeRemoved, currentConfig, false, false)
	require.NoError(t, err)

	// An update driven by LXD, such as a snapshot restore, can remove them.
	err = checkRestrictionsOnVolatileConfig(proj, instancetype.Container, "c1", map[string]string{}, currentConfig, false, true)
	require.NoError(t, err)

	// Creation strips protected keys instead of rejecting them.
	stripConfig := map[string]string{"volatile.eth1.peer.link_id": "link-id"}
	err = checkRestrictionsOnVolatileConfig(proj, instancetype.Container, "c1", stripConfig, map[string]string{}, true, false)
	require.NoError(t, err)
	require.NotContains(t, stripConfig, "volatile.eth1.peer.link_id")
}
