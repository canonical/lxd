package bgp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustParseCIDR parses the given CIDR string and returns the network.
func mustParseCIDR(s string) net.IPNet {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		panic("invalid CIDR: " + s)
	}

	return *network
}

// mustParseIP parses the given IP string.
func mustParseIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("invalid IP: " + s)
	}

	return ip
}

// TestAddRemovePrefix verifies that prefixes can be added and then removed, for
// both IPv4 and IPv6.
func TestAddRemovePrefix(t *testing.T) {
	tests := []struct {
		name    string
		subnet  net.IPNet
		nexthop net.IP
	}{
		{
			name:    "IPv4",
			subnet:  mustParseCIDR("10.0.0.0/24"),
			nexthop: mustParseIP("192.168.1.1"),
		},
		{
			name:    "IPv6",
			subnet:  mustParseCIDR("2001:db8::/32"),
			nexthop: mustParseIP("2001:db8::1"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()

			err := s.AddPrefix(tc.subnet, tc.nexthop, "owner")
			require.NoError(t, err)
			require.Len(t, s.paths, 1)

			err = s.RemovePrefix(tc.subnet, tc.nexthop)
			require.NoError(t, err)
			require.Empty(t, s.paths)
		})
	}
}

// TestAddRemovePrefixRunningServer verifies that prefixes can be added to and
// removed from a running BGP server for both IPv4 and IPv6.
func TestAddRemovePrefixRunningServer(t *testing.T) {
	s := NewServer()
	// Disabling the listener avoids needing elevated privileges to bind to a low port (179).
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		err := s.stop()
		require.NoError(t, err)
	})

	tests := []struct {
		name    string
		subnet  net.IPNet
		nexthop net.IP
	}{
		{
			name:    "IPv4",
			subnet:  mustParseCIDR("10.0.0.0/24"),
			nexthop: mustParseIP("192.168.1.1"),
		},
		{
			name:    "IPv6",
			subnet:  mustParseCIDR("2001:db8::/32"),
			nexthop: mustParseIP("2001:db8::1"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.AddPrefix(tc.subnet, tc.nexthop, "owner")
			require.NoError(t, err)
			require.Len(t, s.paths, 1)

			err = s.RemovePrefix(tc.subnet, tc.nexthop)
			require.NoError(t, err)
			require.Empty(t, s.paths)
		})
	}
}

// TestRemovePrefixNotFound verifies that removing a prefix that was never added
// returns ErrPrefixNotFound.
func TestRemovePrefixNotFound(t *testing.T) {
	s := NewServer()

	err := s.RemovePrefix(mustParseCIDR("10.0.0.0/24"), mustParseIP("192.168.1.1"))
	require.ErrorIs(t, err, ErrPrefixNotFound)
}

// TestRemovePrefixWrongNexthop verifies that removing a prefix with a
// mismatched nexthop returns ErrPrefixNotFound.
func TestRemovePrefixWrongNexthop(t *testing.T) {
	s := NewServer()
	subnet := mustParseCIDR("10.0.0.0/24")

	err := s.AddPrefix(subnet, mustParseIP("192.168.1.1"), "owner")
	require.NoError(t, err)

	// Different nexthop — should not match.
	err = s.RemovePrefix(subnet, mustParseIP("192.168.1.2"))
	require.ErrorIs(t, err, ErrPrefixNotFound)

	// The original prefix must still be present.
	require.Len(t, s.paths, 1)
}

// TestRemovePrefixByOwner verifies that only the prefixes belonging to the
// given owner are removed.
func TestRemovePrefixByOwner(t *testing.T) {
	s := NewServer()

	err := s.AddPrefix(mustParseCIDR("10.0.0.0/24"), mustParseIP("192.168.1.1"), "owner-a")
	require.NoError(t, err)

	err = s.AddPrefix(mustParseCIDR("10.0.1.0/24"), mustParseIP("192.168.1.1"), "owner-b")
	require.NoError(t, err)

	err = s.AddPrefix(mustParseCIDR("10.0.2.0/24"), mustParseIP("192.168.1.1"), "owner-a")
	require.NoError(t, err)

	require.Len(t, s.paths, 3)

	err = s.RemovePrefixByOwner("owner-a")
	require.NoError(t, err)

	// Only owner-b's prefix should remain.
	require.Len(t, s.paths, 1)
	for _, p := range s.paths {
		require.Equal(t, "owner-b", p.owner)
	}
}

// TestRemovePrefixByOwnerNoMatch verifies that RemovePrefixByOwner is a no-op
// when no prefix matches the given owner.
func TestRemovePrefixByOwnerNoMatch(t *testing.T) {
	s := NewServer()

	err := s.AddPrefix(mustParseCIDR("10.0.0.0/24"), mustParseIP("192.168.1.1"), "owner-a")
	require.NoError(t, err)

	err = s.RemovePrefixByOwner("owner-b")
	require.NoError(t, err)
	require.Len(t, s.paths, 1)
}

// TestAddRemovePeer verifies that a peer can be added and then removed.
func TestAddRemovePeer(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)
	require.Len(t, s.peers, 1)

	err = s.RemovePeer(addr)
	require.NoError(t, err)
	require.Empty(t, s.peers)
}

// TestRemovePeerNotFound verifies that removing a peer that was never added
// returns ErrPeerNotFound.
func TestRemovePeerNotFound(t *testing.T) {
	s := NewServer()

	err := s.RemovePeer(mustParseIP("192.168.1.1"))
	require.ErrorIs(t, err, ErrPeerNotFound)
}

// TestAddPeerRefcount verifies that adding the same peer multiple times
// increments a reference count and that the peer is only removed after all
// references are released.
func TestAddPeerRefcount(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)
	require.Equal(t, 1, s.peers[addr.String()].count)

	err = s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)
	require.Equal(t, 2, s.peers[addr.String()].count)

	// First removal only decrements the refcount.
	err = s.RemovePeer(addr)
	require.NoError(t, err)
	require.Len(t, s.peers, 1)
	require.Equal(t, 1, s.peers[addr.String()].count)

	// Second removal actually deletes the peer.
	err = s.RemovePeer(addr)
	require.NoError(t, err)
	require.Empty(t, s.peers)
}

// TestAddPeerConflictASN verifies that adding the same peer address with a
// different ASN returns an error.
func TestAddPeerConflictASN(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)

	err = s.AddPeer(addr, 65001, "", 0, "owner")
	require.Error(t, err)
}

// TestAddPeerConflictPassword verifies that adding the same peer address with a
// different password returns an error.
func TestAddPeerConflictPassword(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "secret", 0, "owner")
	require.NoError(t, err)

	err = s.AddPeer(addr, 65000, "different", 0, "owner")
	require.Error(t, err)
}

// TestDebugNotRunning verifies that Debug returns a payload with Running=false
// when the BGP server is not running.
func TestDebugNotRunning(t *testing.T) {
	s := NewServer()
	debug := s.Debug()
	require.False(t, debug.Server.Running)
	require.Empty(t, debug.Peers)
	require.Empty(t, debug.Prefixes)
}

// TestDebugRunning verifies that Debug returns accurate GoBGP RIB paths,
// peers, and server status when the server is running.
func TestDebugRunning(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	v4Subnet := mustParseCIDR("192.0.2.0/24")
	v4Nexthop := mustParseIP("192.0.2.1")
	v6Subnet := mustParseCIDR("2001:db8:bbbb::/64")
	v6Nexthop := mustParseIP("2001:db8:bbbb::1")
	peerAddr := mustParseIP("127.0.0.2")

	err = s.AddPrefix(v4Subnet, v4Nexthop, "test-owner")
	require.NoError(t, err)

	err = s.AddPrefix(v6Subnet, v6Nexthop, "test-owner")
	require.NoError(t, err)

	err = s.AddPeer(peerAddr, 65001, "secret", 30, "test-owner")
	require.NoError(t, err)

	debug := s.Debug()

	require.True(t, debug.Server.Running)
	require.Equal(t, uint32(65000), debug.Server.ASN)
	require.Equal(t, "192.0.2.1", debug.Server.RouterID)

	require.Len(t, debug.Peers, 1)
	require.Equal(t, peerAddr.String(), debug.Peers[0].Address)
	require.Equal(t, uint32(65001), debug.Peers[0].ASN)
	require.Equal(t, "secret", debug.Peers[0].Password)
	require.Equal(t, uint64(30), debug.Peers[0].HoldTime)

	require.Len(t, debug.Prefixes, 2)
	prefixMap := map[string]DebugInfoPrefix{}
	for _, p := range debug.Prefixes {
		prefixMap[p.Prefix] = p
	}

	p4, ok := prefixMap["192.0.2.0/24"]
	require.True(t, ok)
	require.Equal(t, "192.0.2.1", p4.Nexthop)
	require.Equal(t, "test-owner", p4.Owner)

	p6, ok := prefixMap["2001:db8:bbbb::/64"]
	require.True(t, ok)
	require.Equal(t, "2001:db8:bbbb::1", p6.Nexthop)
	require.Equal(t, "test-owner", p6.Owner)
}

// TestMultiNetworkExportPolicy verifies that routes from multiple networks with different
// next-hops are properly isolated and associated per peer based on network owner as reported
// by Server.Debug().
func TestMultiNetworkExportPolicy(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	net1Subnet := mustParseCIDR("10.10.0.0/24")
	net1Default := mustParseCIDR("0.0.0.0/0")
	net1Nexthop := mustParseIP("192.168.1.1")
	peer1Addr := mustParseIP("127.0.0.2")
	net1Owner := OwnerNetwork(1)

	net2Subnet := mustParseCIDR("10.20.0.0/24")
	net2Default := mustParseCIDR("0.0.0.0/0")
	net2Nexthop := mustParseIP("192.168.2.1")
	peer2Addr := mustParseIP("127.0.0.3")
	net2Owner := OwnerNetwork(2)

	// Add prefixes for network 1
	err = s.AddPrefix(net1Subnet, net1Nexthop, net1Owner)
	require.NoError(t, err)
	err = s.AddPrefix(net1Default, net1Nexthop, net1Owner)
	require.NoError(t, err)

	// Add prefixes for network 2
	err = s.AddPrefix(net2Subnet, net2Nexthop, net2Owner)
	require.NoError(t, err)
	err = s.AddPrefix(net2Default, net2Nexthop, net2Owner)
	require.NoError(t, err)

	// Add peers for network 1 and network 2
	err = s.AddPeer(peer1Addr, 65001, "", 30, net1Owner)
	require.NoError(t, err)
	err = s.AddPeer(peer2Addr, 65002, "", 30, net2Owner)
	require.NoError(t, err)

	debug := s.Debug()
	require.Len(t, debug.Peers, 2)

	// Verify peer 1 only receives network 1 routes with network 1 next-hop.
	var peer1Routes, peer2Routes []DebugInfoPrefix
	for _, p := range debug.Peers {
		switch p.Address {
		case "127.0.0.2":
			peer1Routes = p.Routes
		case "127.0.0.3":
			peer2Routes = p.Routes
		}
	}

	require.Len(t, peer1Routes, 2)
	require.Contains(t, peer1Routes, DebugInfoPrefix{Owner: net1Owner, Prefix: "10.10.0.0/24", Nexthop: "192.168.1.1"})
	require.Contains(t, peer1Routes, DebugInfoPrefix{Owner: net1Owner, Prefix: "0.0.0.0/0", Nexthop: "192.168.1.1"})

	// Verify peer 2 only receives network 2 routes with network 2 next-hop.
	require.Len(t, peer2Routes, 2)
	require.Contains(t, peer2Routes, DebugInfoPrefix{Owner: net2Owner, Prefix: "10.20.0.0/24", Nexthop: "192.168.2.1"})
	require.Contains(t, peer2Routes, DebugInfoPrefix{Owner: net2Owner, Prefix: "0.0.0.0/0", Nexthop: "192.168.2.1"})

	// Also test IPv6 default route (::/0) isolation.
	net1DefaultV6 := mustParseCIDR("::/0")
	net1NexthopV6 := mustParseIP("2001:db8:1111::1")
	net2DefaultV6 := mustParseCIDR("::/0")
	net2NexthopV6 := mustParseIP("2001:db8:2222::1")

	err = s.AddPrefix(net1DefaultV6, net1NexthopV6, net1Owner)
	require.NoError(t, err)
	err = s.AddPrefix(net2DefaultV6, net2NexthopV6, net2Owner)
	require.NoError(t, err)

	debug = s.Debug()

	for _, p := range debug.Peers {
		switch p.Address {
		case "127.0.0.2":
			require.Contains(t, p.Routes, DebugInfoPrefix{Owner: net1Owner, Prefix: "::/0", Nexthop: "2001:db8:1111::1"})
		case "127.0.0.3":
			require.Contains(t, p.Routes, DebugInfoPrefix{Owner: net2Owner, Prefix: "::/0", Nexthop: "2001:db8:2222::1"})
		}
	}
}
