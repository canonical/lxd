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

// TestCheckRestrictionsOnVolatileConfig checks which volatile keys a restricted project keeps when an
// instance is created by a copy or a migration. QEMU requires identical device properties on both
// ends of a live migration, so the volatile keys that record the disk properties must be kept.
func TestCheckRestrictionsOnVolatileConfig(t *testing.T) {
	project := api.Project{
		Name:   "restricted",
		Config: map[string]string{"restricted": "true"},
	}

	testCases := []struct {
		name     string
		config   map[string]string
		strip    bool
		expected map[string]string
		err      bool
	}{
		{
			name:     "Strip keeps the disk property keys",
			config:   map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1", "volatile.uuid.generation": "abc"},
			strip:    true,
			expected: map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1", "volatile.uuid.generation": "abc"},
		},
		{
			name:     "Strip removes other volatile keys",
			config:   map[string]string{"volatile.disk_queues": "4", "volatile.last_state.idmap": "[]", "limits.cpu": "2"},
			strip:    true,
			expected: map[string]string{"volatile.disk_queues": "4", "limits.cpu": "2"},
		},
		{
			name:     "Setting the disk property keys is allowed",
			config:   map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			expected: map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			name:   "Setting other volatile keys is forbidden",
			config: map[string]string{"volatile.last_state.idmap": "[]"},
			err:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := map[string]string{}
			for k, v := range tc.config {
				config[k] = v
			}

			err := checkRestrictionsOnVolatileConfig(project, instancetype.VM, "v1", config, map[string]string{}, tc.strip)
			if tc.err {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, config)
		})
	}
}
