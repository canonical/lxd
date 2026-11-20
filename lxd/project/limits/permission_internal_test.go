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

// checkRestrictedDisk runs the project restriction checks against a container instance carrying one extra device.
func checkRestrictedDisk(projectConfig map[string]string, name string, device map[string]string) error {
	proj := api.Project{Name: "proj1", Config: projectConfig}
	inst := api.Instance{
		Name:    "c1",
		Type:    string(api.InstanceTypeContainer),
		Config:  map[string]string{"security.idmap.isolated": "true"},
		Devices: map[string]map[string]string{name: device},
	}

	return checkInstanceRestrictions(proj, []api.Instance{inst}, []api.Profile{})
}

func restrictedProject(diskPolicy string) map[string]string {
	return map[string]string{
		"restricted":                      "true",
		"restricted.devices.disk":         diskPolicy,
		"restricted.devices.disk.paths":   "/srv",
		"restricted.containers.privilege": "isolated",
	}
}

func TestProjectRestrictedDiskPropagation(t *testing.T) {
	for _, policy := range []string{"managed", "allow"} {
		for _, mode := range []string{"shared", "rshared"} {
			device := map[string]string{"type": "disk", "path": "/mnt", "source": "/srv/data", "propagation": mode}
			if policy == "managed" {
				device["pool"] = "default"
			}

			err := checkRestrictedDisk(restrictedProject(policy), "data", device)
			require.Error(t, err, "policy %s mode %s", policy, mode)
			assert.Contains(t, err.Error(), "propagation", "policy %s mode %s", policy, mode)
		}
	}
}

func TestProjectRestrictedDiskBlockStillForbidsEveryDisk(t *testing.T) {
	safe := map[string]string{"type": "disk", "path": "/mnt", "source": "/srv/data", "propagation": "slave"}
	require.ErrorContains(t, checkRestrictedDisk(restrictedProject("block"), "data", safe), "Disk devices are forbidden")

	risky := map[string]string{"type": "disk", "path": "/mnt", "source": "/srv/data", "propagation": "rshared"}
	require.Error(t, checkRestrictedDisk(restrictedProject("block"), "data", risky))
}

func TestProjectRestrictedDiskPropagationAllowsSafeModes(t *testing.T) {
	for _, mode := range []string{"", "private", "slave", "unbindable", "rprivate", "rslave", "runbindable"} {
		device := map[string]string{"type": "disk", "path": "/mnt", "source": "/srv/data"}
		if mode != "" {
			device["propagation"] = mode
		}

		err := checkRestrictedDisk(restrictedProject("allow"), "data", device)
		assert.NoError(t, err, "mode %q", mode)
	}
}

func TestProjectRestrictedDiskPropagationAppliesToProfiles(t *testing.T) {
	proj := api.Project{Name: "proj1", Config: restrictedProject("allow")}
	profile := api.Profile{
		Name:    "p1",
		Devices: map[string]map[string]string{"data": {"type": "disk", "path": "/mnt", "source": "/srv/data", "propagation": "rshared"}},
	}

	err := checkInstanceRestrictions(proj, []api.Instance{}, []api.Profile{profile})
	require.ErrorContains(t, err, "propagation")
}

func TestProjectRestrictedDiskPropagationIgnoresOtherDevices(t *testing.T) {
	proj := api.Project{Name: "proj1", Config: restrictedProject("allow")}
	inst := api.Instance{
		Name:   "c1",
		Type:   string(api.InstanceTypeContainer),
		Config: map[string]string{"security.idmap.isolated": "true"},
		Devices: map[string]map[string]string{
			"root": {"type": "disk", "path": "/", "pool": "default"},
			"eth0": {"type": "nic", "network": "lxdbr0", "propagation": "rshared"}, // not a disk: not ours to judge
		},
	}

	require.NoError(t, checkInstanceRestrictions(proj, []api.Instance{inst}, []api.Profile{}))
}

func TestProjectRestrictedDiskPropagationNamesTheDevice(t *testing.T) {
	device := map[string]string{"type": "disk", "path": "/mnt", "source": "/srv/data", "propagation": "rshared"}
	err := checkRestrictedDisk(restrictedProject("allow"), "data", device)
	require.ErrorContains(t, err, `Invalid device "data" on container "c1" of project "proj1"`)
	require.ErrorContains(t, err, "mounts made in the instance would appear on the host")
}
