package limits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/idmap"
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
