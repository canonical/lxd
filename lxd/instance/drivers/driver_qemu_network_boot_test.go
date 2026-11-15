package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/osarch"
)

func TestNetworkFamilyIncludes(t *testing.T) {
	tests := []struct {
		name          string
		networkFamily string
		pxeFamily     string
		expected      bool
	}{
		{"both includes ipv4", instancetype.NetworkFamilyBoth, instancetype.NetworkFamilyIPv4, true},
		{"both includes ipv6", instancetype.NetworkFamilyBoth, instancetype.NetworkFamilyIPv6, true},
		{"both includes none", instancetype.NetworkFamilyBoth, instancetype.NetworkFamilyNone, true},
		{"ipv4 includes ipv4", instancetype.NetworkFamilyIPv4, instancetype.NetworkFamilyIPv4, true},
		{"ipv4 excludes ipv6", instancetype.NetworkFamilyIPv4, instancetype.NetworkFamilyIPv6, false},
		{"ipv4 excludes both", instancetype.NetworkFamilyIPv4, instancetype.NetworkFamilyBoth, false},
		{"ipv4 includes none", instancetype.NetworkFamilyIPv4, instancetype.NetworkFamilyNone, true},
		{"ipv6 includes ipv6", instancetype.NetworkFamilyIPv6, instancetype.NetworkFamilyIPv6, true},
		{"ipv6 excludes ipv4", instancetype.NetworkFamilyIPv6, instancetype.NetworkFamilyIPv4, false},
		{"ipv6 excludes both", instancetype.NetworkFamilyIPv6, instancetype.NetworkFamilyBoth, false},
		{"ipv6 includes none", instancetype.NetworkFamilyIPv6, instancetype.NetworkFamilyNone, true},
		{"none includes none", instancetype.NetworkFamilyNone, instancetype.NetworkFamilyNone, true},
		{"none excludes ipv4", instancetype.NetworkFamilyNone, instancetype.NetworkFamilyIPv4, false},
		{"none excludes ipv6", instancetype.NetworkFamilyNone, instancetype.NetworkFamilyIPv6, false},
		{"none excludes both", instancetype.NetworkFamilyNone, instancetype.NetworkFamilyBoth, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, networkFamilyIncludes(tt.networkFamily, tt.pxeFamily))
		})
	}
}

func TestNetworkBootFirmwareArgs(t *testing.T) {
	fwCfg := func(name string) []string {
		return []string{"-fw_cfg", "name=opt/org.tianocore/" + name + ",string=no"}
	}

	tests := []struct {
		name     string
		arch     int
		config   map[string]string
		expected []string
		err      string
	}{
		{
			name: "unset",
			arch: osarch.ARCH_64BIT_INTEL_X86,
		},
		{
			name: "default both",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyBoth,
			},
		},
		{
			name: "ipv6 only",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyIPv6,
			},
			expected: append(append([]string{}, fwCfg("IPv4Support")...), fwCfg("IPv4PXESupport")...),
		},
		{
			name: "pxe ipv6 only",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.pxe.family": instancetype.NetworkFamilyIPv6,
			},
			expected: fwCfg("IPv4PXESupport"),
		},
		{
			name: "no pxe",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.pxe.family": instancetype.NetworkFamilyNone,
			},
			expected: append(append([]string{}, fwCfg("IPv4PXESupport")...), fwCfg("IPv6PXESupport")...),
		},
		{
			name: "no network",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyNone,
			},
			expected: append(append(append(append([]string{}, fwCfg("IPv4Support")...), fwCfg("IPv6Support")...), fwCfg("IPv4PXESupport")...), fwCfg("IPv6PXESupport")...),
		},
		{
			name: "pxe family not in network family",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyIPv4,
				"boot.pxe.family":     instancetype.NetworkFamilyIPv6,
			},
			err: "includes a family disabled by",
		},
		{
			name: "pxe with network none",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyNone,
				"boot.pxe.family":     instancetype.NetworkFamilyBoth,
			},
			err: "includes a family disabled by",
		},
		{
			name: "bios",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.mode":           instancetype.BootModeBIOS,
				"boot.network.family": instancetype.NetworkFamilyIPv4,
			},
			err: "require UEFI firmware",
		},
		{
			name: "non x86_64",
			arch: osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyIPv4,
			},
			err: "only supported on x86_64",
		},
		{
			name: "raw.qemu conflict",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyIPv6,
				"raw.qemu":            "-fw_cfg name=opt/org.tianocore/IPv6Support,string=no",
			},
			err: "cannot be combined with raw.qemu",
		},
		{
			name: "raw.qemu conflict with shell quoting",
			arch: osarch.ARCH_64BIT_INTEL_X86,
			config: map[string]string{
				"boot.network.family": instancetype.NetworkFamilyIPv6,
				"raw.qemu":            `-fw_cfg name=opt/org.tianocore/IPv6""Support,string=no`,
			},
			err: "cannot be combined with raw.qemu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &qemu{
				common: common{
					architecture:   tt.arch,
					expandedConfig: tt.config,
				},
			}

			args, err := d.networkBootFirmwareArgs()
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, args)
		})
	}
}
