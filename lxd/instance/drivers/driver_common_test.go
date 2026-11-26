package drivers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
)

func TestDeviceVolatile(t *testing.T) {
	d := &common{
		localConfig: map[string]string{
			"volatile.eth0.hwaddr":           "00:16:3e:00:00:01",
			"volatile.eth0.last_state.mtu":   "1500",
			"volatile.eth0.100.hwaddr":       "00:16:3e:00:00:02",
			"volatile.eth0.100.host_name":    "veth100",
			"volatile.eth0.removed.hwaddr":   "00:16:3e:00:00:03",
			"volatile.eth1.hwaddr":           "00:16:3e:00:00:04",
			"volatile.base_image":            "abc",
			"volatile.eth0.100.last_state.x": "y",
		},
		localDevices: deviceConfig.Devices{
			"eth0": {"type": "nic"},
		},
		expandedDevices: deviceConfig.Devices{
			"eth0":     {"type": "nic"},
			"eth0.100": {"type": "nic"},
			"eth1":     {"type": "nic"},
		},
	}

	// Keys of a device that no longer exists are still attributed to the shorter device name.
	assert.Equal(t, map[string]string{
		"hwaddr":         "00:16:3e:00:00:01",
		"last_state.mtu": "1500",
		"removed.hwaddr": "00:16:3e:00:00:03",
	}, d.deviceVolatile("eth0"))

	assert.Equal(t, map[string]string{
		"hwaddr":       "00:16:3e:00:00:02",
		"host_name":    "veth100",
		"last_state.x": "y",
	}, d.deviceVolatile("eth0.100"))

	assert.Equal(t, map[string]string{"hwaddr": "00:16:3e:00:00:04"}, d.deviceVolatile("eth1"))
	assert.Empty(t, d.deviceVolatile("eth2"))
}

// instanceVolatileKeysWithDeviceName returns the volatile keys of the instance configuration registry whose
// name has the form volatile.<name>.<rest>, together with the device name <name> that would claim them.
func instanceVolatileKeysWithDeviceName() map[string]string {
	keys := map[string]string{}

	for _, registry := range []map[string]func(string) error{
		instancetype.InstanceConfigKeysAny,
		instancetype.InstanceConfigKeysVM,
		instancetype.InstanceConfigKeysContainer,
	} {
		for key := range registry {
			name, found := strings.CutPrefix(key, "volatile.")
			if !found {
				continue
			}

			name, _, found = strings.Cut(name, ".")
			if !found {
				continue
			}

			keys[key] = name
		}
	}

	return keys
}

// TestDeviceVolatileKeepsInstanceKeys checks that no instance-wide volatile key is attributed to a device
// that is named like the second component of the key (the key volatile.cpu.maxcpus and a device "cpu").
// The keys are taken from the instance config registry, so a new key with a dot is covered as well.
func TestDeviceVolatileKeepsInstanceKeys(t *testing.T) {
	keys := instanceVolatileKeysWithDeviceName()
	require.NotEmpty(t, keys)

	for key, name := range keys {
		t.Run(key, func(t *testing.T) {
			d := &common{
				localConfig:     map[string]string{key: "value", "volatile." + name + ".hwaddr": "00:16:3e:00:00:01"},
				expandedDevices: deviceConfig.Devices{name: {"type": "disk"}},
			}

			// Only the key of the device is returned, never the instance-wide key.
			volatile := d.deviceVolatile(name)
			if key == "volatile."+name+".hwaddr" {
				return
			}

			assert.Equal(t, map[string]string{"hwaddr": "00:16:3e:00:00:01"}, volatile)
		})
	}

	// The keys that are known to clash with a device name must be in the registry. If this list
	// shrinks, the check can be reconsidered.
	assert.Contains(t, keys, "volatile.cpu.maxcpus")
	assert.Contains(t, keys, "volatile.last_state.power")
}

// TestDeviceVolatileResetKeepsInstanceKeys checks that removing a device named like the second component of
// an instance-wide volatile key does not clear that key, while the keys of the device are cleared.
func TestDeviceVolatileResetKeepsInstanceKeys(t *testing.T) {
	keys := instanceVolatileKeysWithDeviceName()
	require.NotEmpty(t, keys)

	for key, name := range keys {
		t.Run(key, func(t *testing.T) {
			deviceKey := "volatile." + name + ".hwaddr"
			if key == deviceKey {
				return
			}

			d := &common{
				volatileSetPersistDisable: true,
				localConfig:               map[string]string{key: "value", deviceKey: "00:16:3e:00:00:01"},
				expandedConfig:            map[string]string{key: "value", deviceKey: "00:16:3e:00:00:01"},
				expandedDevices:           deviceConfig.Devices{name: {"type": "disk"}},
			}

			err := d.deviceVolatileReset(name, deviceConfig.Device{"type": "disk"}, deviceConfig.Device{})
			require.NoError(t, err)

			assert.Equal(t, "value", d.localConfig[key])
			assert.Equal(t, "value", d.expandedConfig[key])
			assert.NotContains(t, d.localConfig, deviceKey)
		})
	}
}
