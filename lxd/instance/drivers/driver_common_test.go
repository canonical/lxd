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

func TestLXCRawAppArmorProfile(t *testing.T) {
	tests := []struct {
		name   string
		rawLXC string
		want   bool
	}{
		{name: "empty"},
		{name: "other key", rawLXC: "lxc.mount.entry=/mnt none bind 0 0"},
		{name: "comment", rawLXC: "# lxc.apparmor.profile=unconfined"},
		{name: "unconfined", rawLXC: "lxc.apparmor.profile=unconfined", want: true},
		{name: "custom profile", rawLXC: "lxc.apparmor.profile = custom-profile", want: true},
		{name: "legacy profile", rawLXC: "lxc.aa_profile=unconfined", want: true},
		{name: "legacy custom profile", rawLXC: "lxc.aa_profile = custom-profile", want: true},
		{name: "case insensitive", rawLXC: "LXC.APPARMOR.PROFILE=custom-profile", want: true},
		{name: "multiline", rawLXC: "lxc.mount.entry=/mnt none bind 0 0\nlxc.apparmor.profile=unconfined", want: true},
		{name: "inline text", rawLXC: "lxc.environment=lxc.apparmor.profile=unconfined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, lxcRawAppArmorProfile(tt.rawLXC))
		})
	}
}
