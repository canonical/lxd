package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
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
