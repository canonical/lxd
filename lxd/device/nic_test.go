package device

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNicEffectiveVLAN tests the mapping from a NIC's "vlan" setting to the VLAN it actually
// lands on. An unset VLAN takes the parent bridge's default PVID, which differs by bridge driver.
func TestNicEffectiveVLAN(t *testing.T) {
	tests := []struct {
		name         string
		bridgeDriver string
		vlan         string
		expected     string
	}{
		{"Unset VLAN on a native bridge is the native default VLAN", "native", "", "1"},
		{"Unset VLAN on an openvswitch bridge is the OVS default VLAN", "openvswitch", "", "0"},
		{"Unset VLAN with no driver assumes the native default", "", "", "1"},
		{"Explicit VLAN overrides the default on a native bridge", "native", "511", "511"},
		{"Explicit VLAN overrides the default on an openvswitch bridge", "openvswitch", "511", "511"},
		{"Explicit native default VLAN", "native", "1", "1"},
		{"Explicit openvswitch default VLAN", "openvswitch", "0", "0"},
		{"VLAN none removes membership entirely", "native", "none", "none"},
		{"VLAN none removes membership entirely on openvswitch", "openvswitch", "none", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, nicEffectiveVLAN(tt.bridgeDriver, tt.vlan))
		})
	}
}

// TestNicsUseSameVLAN tests whether two NICs on the same parent bridge share an L2 segment. An
// unset VLAN must match an explicit reference to the bridge's default VLAN, which differs between
// native (1) and openvswitch (0) bridges.
func TestNicsUseSameVLAN(t *testing.T) {
	tests := []struct {
		name         string
		bridgeDriver string
		vlanA        string
		vlanB        string
		expected     bool
	}{
		{"Both unset on a native bridge", "native", "", "", true},
		{"Unset matches the explicit native default", "native", "", "1", true},
		{"Unset matches the explicit native default, reversed", "native", "1", "", true},
		{"Both explicit native default", "native", "1", "1", true},
		{"Unset matches the explicit OVS default", "openvswitch", "", "0", true},
		{"OVS default does not match VLAN 1", "openvswitch", "0", "1", false},
		{"Native default does not match VLAN 0", "native", "", "0", false},
		{"Same tagged VLAN matches", "native", "511", "511", true},
		{"Different tagged VLANs do not match", "native", "511", "512", false},
		{"Tagged VLAN differs from the default", "native", "511", "", false},
		{"VLAN none only matches VLAN none", "native", "none", "none", true},
		{"VLAN none does not match the default", "native", "none", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, nicsUseSameVLAN(tt.bridgeDriver, tt.vlanA, tt.vlanB))
		})
	}
}

// TestNicUsesDefaultVLAN tests whether a NIC's "vlan" setting is recognised as leaving it on its
// bridge's default VLAN. Only NICs on the default VLAN can reach the bridge's dnsmasq, so only
// they can conflict over a DNS name.
func TestNicUsesDefaultVLAN(t *testing.T) {
	tests := []struct {
		name         string
		bridgeDriver string
		vlan         string
		expected     bool
	}{
		{"Unset VLAN is the default on a native bridge", "native", "", true},
		{"Native default VLAN 1 is the default", "native", "1", true},
		{"Unset VLAN is the default on an openvswitch bridge", "openvswitch", "", true},
		{"VLAN 0 is the default on an openvswitch bridge", "openvswitch", "0", true},
		{"VLAN none removes default membership", "native", "none", false},
		{"A tagged VLAN is not the default", "native", "511", false},
		{"A tagged VLAN is not the default on openvswitch", "openvswitch", "511", false},
		{"The maximum VLAN ID is not the default", "native", "4094", false},
		{"VLAN 0 is not the default on a native bridge", "native", "0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, nicUsesDefaultVLAN(tt.bridgeDriver, tt.vlan))
		})
	}
}
