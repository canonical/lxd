package device

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/instance/instancetype"
)

func TestNICParsePeer(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		expected      *nicP2PPeer
		expectedError bool
	}{
		{name: "Empty", value: "", expected: nil},
		{name: "Valid", value: "c2:eth0", expected: &nicP2PPeer{Instance: "c2", Device: "eth0"}},
		{name: "Device name with colon", value: "c2:p2p:1", expected: &nicP2PPeer{Instance: "c2", Device: "p2p:1"}},
		{name: "Missing separator", value: "c2/eth0", expectedError: true},
		{name: "Missing instance", value: ":eth0", expectedError: true},
		{name: "Missing device", value: "c2:", expectedError: true},
		{name: "Snapshot reference", value: "c2/snap:eth0", expectedError: true},
		{name: "Instance name with dot", value: "c2.example:eth0", expectedError: true},
		{name: "Device name with space", value: "c2:eth 0", expectedError: true},
		{name: "Device name starting with dot", value: "c2:..", expectedError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peer, err := nicP2PParsePeer(tt.value)
			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, peer)
		})
	}
}

func TestNICPeerNames(t *testing.T) {
	// Both sides compute the same bridge and bridge alias from the shared link ID.
	c1 := nicP2PPeerNamesFor(1, "link-a")
	c2 := nicP2PPeerNamesFor(2, "link-a")

	assert.Equal(t, c1.Bridge, c2.Bridge)
	assert.Equal(t, c1.BridgeAlias, c2.BridgeAlias)
	assert.NotEqual(t, c1.PortAlias, c2.PortAlias)
	assert.Equal(t, nicP2PPortAliasPrefix+"link-a:1", c1.PortAlias)
	assert.Equal(t, nicP2PPortAliasPrefix+"link-a:2", c2.PortAlias)

	assert.LessOrEqual(t, len(c1.Bridge), 15)
	assert.Contains(t, c1.Bridge, "lxdp")

	// Names must be stable.
	assert.Equal(t, c1, nicP2PPeerNamesFor(1, "link-a"))

	// A different link between the same instances must get a different bridge.
	otherLink := nicP2PPeerNamesFor(1, "link-b")
	assert.NotEqual(t, c1.Bridge, otherLink.Bridge)
	assert.NotEqual(t, c1.BridgeAlias, otherLink.BridgeAlias)
	assert.NotEqual(t, c1.PortAlias, otherLink.PortAlias)
}

func TestNICPeerPortPrefix(t *testing.T) {
	assert.Equal(t, "veth", nicP2PPeerPortPrefix(instancetype.Container))
	assert.Equal(t, "tap", nicP2PPeerPortPrefix(instancetype.VM))
}

func TestNICPeerLockName(t *testing.T) {
	a, err := nicP2PPeerLockName("default", "c1", "eth1", "c2:eth2")
	require.NoError(t, err)

	b, err := nicP2PPeerLockName("default", "c2", "eth2", "c1:eth1")
	require.NoError(t, err)
	assert.Equal(t, a, b)

	c, err := nicP2PPeerLockName("default", "c1", "eth2", "c2:eth1")
	require.NoError(t, err)
	assert.NotEqual(t, a, c)

	d, err := nicP2PPeerLockName("other", "c1", "eth1", "c2:eth2")
	require.NoError(t, err)
	assert.NotEqual(t, a, d)

	// An invalid peer reference cannot produce a lock name.
	_, err = nicP2PPeerLockName("default", "c1", "eth1", "invalid")
	assert.Error(t, err)
}

func TestNICPeerSameMember(t *testing.T) {
	tests := []struct {
		name          string
		localLocation string
		peerLocation  string
		expectedError bool
	}{
		{name: "Same member", localLocation: "node1", peerLocation: "node1"},
		{name: "Different members", localLocation: "node1", peerLocation: "node2", expectedError: true},
		{name: "Unknown local location", localLocation: "", peerLocation: "node2"},
		{name: "Unknown peer location", localLocation: "node1", peerLocation: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := nicP2PCheckSameMember(tt.localLocation, tt.peerLocation)
			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestNICPeerNamesIgnoreUserNames(t *testing.T) {
	// The naming function takes only the LXD-generated link ID and the instance's own
	// ID, so user-controlled instance, project and device names cannot influence the
	// host interface names. The port name itself is random.
	a := nicP2PPeerNamesFor(10, "link-a")
	b := nicP2PPeerNamesFor(20, "link-a")
	c := nicP2PPeerNamesFor(10, "link-b")

	assert.Equal(t, a.Bridge, b.Bridge)
	assert.NotEqual(t, a.Bridge, c.Bridge)
	assert.NotEqual(t, a.PortAlias, b.PortAlias)
}
