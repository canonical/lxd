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

// TestRemovePrefixByEntityOwner verifies that removing one of a network's owner
// identifiers only removes its own prefixes: each entity type has a distinct
// owner, and the instance NIC owner is not nested under the network's own owner
// for removal purposes.
func TestRemovePrefixByEntityOwner(t *testing.T) {
	s := NewServer()

	netOwner := OwnerNetwork(10)
	forwardsOwner := OwnerNetworkForwards(netOwner)
	lbOwner := OwnerNetworkLoadBalancers(netOwner)
	nicOwner := OwnerInstanceNetwork(netOwner, 3, "eth0")
	siblingForwardsOwner := OwnerNetworkForwards(OwnerNetwork(100))

	for _, tc := range []struct {
		owner  string
		prefix string
	}{
		{netOwner, "10.10.0.0/24"},
		{forwardsOwner, "10.10.1.0/24"},
		{lbOwner, "10.10.2.0/24"},
		{nicOwner, "10.10.3.0/24"},
		{siblingForwardsOwner, "10.100.1.0/24"},
	} {
		err := s.AddPrefix(mustParseCIDR(tc.prefix), mustParseIP("192.168.1.1"), tc.owner)
		require.NoError(t, err)
	}

	// Removing the forwards removes only network_10's forwards: the network's
	// own prefixes, its load balancers, its instance NICs and a sibling
	// network's forwards are all left in place.
	err := s.RemovePrefixByOwner(forwardsOwner)
	require.NoError(t, err)

	remaining := map[string]string{}
	for _, p := range s.paths {
		remaining[p.prefix.String()] = p.owner
	}

	require.Equal(t, map[string]string{
		"10.10.0.0/24":  netOwner,
		"10.10.2.0/24":  lbOwner,
		"10.10.3.0/24":  nicOwner,
		"10.100.1.0/24": siblingForwardsOwner,
	}, remaining)

	// Removing the network's own prefixes leaves the instance NIC's external
	// routes alone: the NIC owner is nested under the network owner for export,
	// but is removed by the device that added it, not with the network.
	err = s.RemovePrefixByOwner(netOwner)
	require.NoError(t, err)
	require.Len(t, s.paths, 3)
}

// TestAddRemovePeer verifies that a peer can be added and then removed.
func TestAddRemovePeer(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)
	require.Len(t, s.peers, 1)

	err = s.RemovePeer(addr, "owner")
	require.NoError(t, err)
	require.Empty(t, s.peers)
}

// TestRemovePeerNotFound verifies that removing a peer that was never added
// returns ErrPeerNotFound.
func TestRemovePeerNotFound(t *testing.T) {
	s := NewServer()

	err := s.RemovePeer(mustParseIP("192.168.1.1"), "owner")
	require.ErrorIs(t, err, ErrPeerNotFound)
}

// TestAddPeerRefcount verifies that adding the same peer multiple times for one
// owner increments a reference count and that the peer is only removed after all
// references are released.
func TestAddPeerRefcount(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)
	require.Equal(t, 1, s.peers[addr.String()].owners["owner"])

	err = s.AddPeer(addr, 65000, "", 0, "owner")
	require.NoError(t, err)
	require.Equal(t, 2, s.peers[addr.String()].owners["owner"])

	// First removal only decrements the refcount.
	err = s.RemovePeer(addr, "owner")
	require.NoError(t, err)
	require.Len(t, s.peers, 1)
	require.Equal(t, 1, s.peers[addr.String()].owners["owner"])

	// Second removal actually deletes the peer.
	err = s.RemovePeer(addr, "owner")
	require.NoError(t, err)
	require.Empty(t, s.peers)
}

// TestAddPeerSharedOwners verifies that several networks can share one BGP peer
// address (e.g. two uplinks peering with the same upstream router), that the
// session is only torn down once every owner has released it, and that each
// owner's routes are advertised over the shared session with their own
// next-hop.
func TestAddPeerSharedOwners(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	addr := mustParseIP("192.168.1.1")

	// Two networks configure the same peer with the same parameters.
	err = s.AddPeer(addr, 65000, "secret", 30, "network_1")
	require.NoError(t, err)
	err = s.AddPeer(addr, 65000, "secret", 30, "network_2")
	require.NoError(t, err)

	// The shared address is one entry, owned by both networks.
	require.Len(t, s.peers, 1)
	require.Equal(t, []string{"network_1", "network_2"}, s.peers[addr.String()].ownerNames())

	// Each network owns a prefix, advertised over the shared session.
	err = s.AddPrefix(mustParseCIDR("10.1.0.0/24"), mustParseIP("192.0.2.10"), "network_1")
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.2.0.0/24"), mustParseIP("192.0.2.11"), "network_2")
	require.NoError(t, err)

	debug := s.Debug()
	require.Len(t, debug.Peers, 1)
	require.Equal(t, []string{"network_1", "network_2"}, debug.Peers[0].Owners)

	nexthops := map[string]string{}
	for _, r := range debug.Peers[0].Routes {
		nexthops[r.Prefix] = r.Nexthop
	}

	require.Equal(t, map[string]string{
		"10.1.0.0/24": "192.0.2.10",
		"10.2.0.0/24": "192.0.2.11",
	}, nexthops)

	// Removing one owner leaves the session up for the other, which keeps its
	// routes.
	err = s.RemovePeer(addr, "network_1")
	require.NoError(t, err)
	require.Len(t, s.peers, 1)
	require.Equal(t, []string{"network_2"}, s.peers[addr.String()].ownerNames())

	debug = s.Debug()
	require.Len(t, debug.Peers, 1)
	require.Len(t, debug.Peers[0].Routes, 1)
	require.Equal(t, "10.2.0.0/24", debug.Peers[0].Routes[0].Prefix)

	// Removing the last owner tears the session down.
	err = s.RemovePeer(addr, "network_2")
	require.NoError(t, err)
	require.Empty(t, s.peers)
}

// TestStartFailureLeavesNothingBehind verifies that a listener that fails to
// start leaves no server recorded. Otherwise the next configuration change would
// be told the listener is already running and BGP would stay unusable until the
// daemon restarted.
func TestStartFailureLeavesNothingBehind(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{"port not numeric", "127.0.0.1:notaport"},
		{"port out of range", "127.0.0.1:99999"},
		{"address not usable", "192.0.2.99:1790"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := NewServer()

			err := s.start(test.address, 65000, mustParseIP("192.0.2.1"))
			require.Error(t, err)
			require.Nil(t, s.bgp, "A failed start must not leave a server recorded")

			// The next start must not be refused as already running, which is
			// what a recorded server would cause. This uses the same
			// no-listener form as the other tests, so it does not depend on
			// which ports happen to be free.
			err = s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
			require.NoError(t, err)
			require.NotNil(t, s.bgp)

			err = s.stop()
			require.NoError(t, err)
			require.Nil(t, s.bgp)
		})
	}
}

// TestPolicySnapshotIndependent verifies that the snapshot taken for a policy
// rebuild shares nothing mutable with the live peer list. The snapshot is
// iterated after the server lock is released, so a peer's owners can be mutated
// by RemovePeer while a rebuild reads them; a shared owners map would race, and
// Go aborts the process on a concurrent map iteration and map write rather than
// returning an error.
func TestPolicySnapshotIndependent(t *testing.T) {
	s := NewServer()

	addr := mustParseIP("127.0.0.2")
	ownerA := OwnerNetwork(1)
	ownerB := OwnerNetwork(2)

	err := s.AddPeer(addr, 65000, "", 30, ownerA)
	require.NoError(t, err)
	err = s.AddPeer(addr, 65000, "", 30, ownerB)
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.0.0.0/24"), mustParseIP("192.0.2.10"), ownerA)
	require.NoError(t, err)

	// Take the snapshot a policy rebuild would use.
	_, _, peers, paths := s.policySnapshot()
	require.Len(t, peers, 1)
	require.Equal(t, []string{ownerA, ownerB}, peers[addr.String()].ownerNames())
	require.Len(t, paths, 1)

	// Change the live state, as one network stopping and another starting would.
	err = s.RemovePeer(addr, ownerB)
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.1.0.0/24"), mustParseIP("192.0.2.11"), ownerB)
	require.NoError(t, err)

	// The changes are applied to the live state.
	require.Equal(t, []string{ownerA}, s.peers[addr.String()].ownerNames())
	require.Len(t, s.paths, 2)

	// The snapshot is unaffected by either, so a rebuild reading it cannot race
	// a concurrent change.
	require.Equal(t, []string{ownerA, ownerB}, peers[addr.String()].ownerNames())
	require.Len(t, paths, 1)
}

// TestAddPrefixNexthopConflict verifies that a prefix already advertised to a
// peer with one next-hop cannot be advertised to that peer with another, as the
// export policy can only accept one of them.
func TestAddPrefixNexthopConflict(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	addr := mustParseIP("127.0.0.2")

	// Two networks sharing one peer, each advertising the same prefix with its
	// own next-hop.
	err = s.AddPeer(addr, 65000, "", 30, OwnerNetwork(1))
	require.NoError(t, err)
	err = s.AddPeer(addr, 65000, "", 30, OwnerNetwork(2))
	require.NoError(t, err)

	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("192.0.2.10"), OwnerNetwork(1))
	require.NoError(t, err)

	// The first network re-advertising its prefix is allowed, as is another
	// owner advertising it with the same next-hop.
	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("192.0.2.10"), OwnerNetwork(1))
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("192.0.2.10"), OwnerNetworkOVN(2, 20))
	require.NoError(t, err)

	// A different next-hop for the same prefix is rejected.
	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("192.0.2.11"), OwnerNetworkOVN(2, 21))
	require.ErrorContains(t, err, "more than one next-hop")

	// The rejected prefix is not advertised.
	debug := s.Debug()
	require.Len(t, debug.Peers, 1)
	require.NotEmpty(t, debug.Peers[0].Routes)

	for _, r := range debug.Peers[0].Routes {
		require.Equal(t, "192.0.2.10", r.Nexthop)
	}
}

// TestAddPrefixNexthopDifferentPeers verifies that two networks advertising the
// same prefix with different next-hops are accepted while they do not share a
// peer, as each peer's policy only advertises its own routes.
func TestAddPrefixNexthopDifferentPeers(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	err = s.AddPeer(mustParseIP("127.0.0.2"), 65000, "", 30, OwnerNetwork(1))
	require.NoError(t, err)
	err = s.AddPeer(mustParseIP("127.0.0.3"), 65001, "", 30, OwnerNetwork(2))
	require.NoError(t, err)

	err = s.AddPrefix(mustParseCIDR("0.0.0.0/0"), mustParseIP("192.0.2.10"), OwnerNetwork(1))
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("0.0.0.0/0"), mustParseIP("192.0.2.11"), OwnerNetwork(2))
	require.NoError(t, err)

	debug := s.Debug()
	require.Len(t, debug.Peers, 2)

	for _, p := range debug.Peers {
		require.Len(t, p.Routes, 1)
		require.Equal(t, "0.0.0.0/0", p.Routes[0].Prefix)
	}
}

// TestAddPeerPrefixConflict verifies that a network cannot start sharing a peer
// when doing so would advertise a prefix the peer already advertises through a
// different next-hop: a session advertises a prefix once, so the second route
// would replace the first rather than be advertised alongside it. This is the
// counterpart of the check AddPrefix makes, for the sharing arriving after the
// routes are already in place.
func TestAddPeerPrefixConflict(t *testing.T) {
	s := NewServer()

	// Each network advertises the same prefix, with its own next-hop, over its
	// own peer. That is allowed while they do not share a peer.
	err := s.AddPeer(mustParseIP("127.0.0.2"), 65000, "", 30, OwnerNetwork(1))
	require.NoError(t, err)
	err = s.AddPeer(mustParseIP("127.0.0.3"), 65000, "", 30, OwnerNetwork(2))
	require.NoError(t, err)

	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("192.0.2.10"), OwnerNetwork(1))
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("203.0.113.101"), OwnerNetwork(2))
	require.NoError(t, err)

	// network_2 moves to network_1's peer address, as two uplinks peering with
	// the same upstream router do. The shared session could only advertise one of
	// the next-hops, so this is refused.
	err = s.RemovePeer(mustParseIP("127.0.0.3"), OwnerNetwork(2))
	require.NoError(t, err)

	err = s.AddPeer(mustParseIP("127.0.0.2"), 65000, "", 30, OwnerNetwork(2))
	require.ErrorContains(t, err, "more than one next-hop")

	// The refused network is not recorded against the peer, and its routes are
	// left as they were.
	require.Equal(t, []string{"network_1"}, s.peers["127.0.0.2"].ownerNames())
	require.Len(t, s.paths, 2)

	// Advertising the prefix with the same next-hop may share the peer, as the
	// session advertises one route either way.
	err = s.RemovePrefixByOwner(OwnerNetwork(2))
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), mustParseIP("192.0.2.10"), OwnerNetwork(2))
	require.NoError(t, err)
	err = s.AddPeer(mustParseIP("127.0.0.2"), 65000, "", 30, OwnerNetwork(2))
	require.NoError(t, err)
	require.Equal(t, []string{"network_1", "network_2"}, s.peers["127.0.0.2"].ownerNames())
}

// TestDebugPeerRoutesDeduplicated verifies that the routes reported for a peer
// are the routes the export policy advertises to it. The same route can be
// recorded more than once, but the peer receives it once.
func TestDebugPeerRoutesDeduplicated(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	netOwner := OwnerNetwork(1)
	err = s.AddPeer(mustParseIP("127.0.0.2"), 65000, "", 30, netOwner)
	require.NoError(t, err)

	subnet := mustParseCIDR("10.20.0.0/24")
	nexthop := mustParseIP("192.0.2.10")

	// The network's own prefix, and a sub-owner adding the same route again.
	err = s.AddPrefix(subnet, nexthop, netOwner)
	require.NoError(t, err)
	err = s.AddPrefix(subnet, nexthop, OwnerNetworkForwards(netOwner))
	require.NoError(t, err)
	require.Len(t, s.paths, 2)

	// The peer receives one route, reported once.
	debug := s.Debug()
	require.Len(t, debug.Peers, 1)
	require.Len(t, debug.Peers[0].Routes, 1)
	require.Equal(t, DebugInfoPrefix{Owner: netOwner, Prefix: "10.20.0.0/24", Nexthop: "192.0.2.10"}, debug.Peers[0].Routes[0])
}

// TestRemovePeerUnknownOwner verifies that releasing a peer for an owner that
// never registered it is a no-op. That is what happens when a network
// configures a peer the server refuses to add, because it does not match one
// that another network already configured: the refused network still has to be
// able to stop or update.
func TestRemovePeerUnknownOwner(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "secret", 30, "network_1")
	require.NoError(t, err)

	// A second network configures the same address with a different ASN, so it
	// is refused and never becomes an owner.
	err = s.AddPeer(addr, 65001, "secret", 30, "network_2")
	require.Error(t, err)

	// Releasing the refused network leaves the session and its owner alone.
	err = s.RemovePeer(addr, "network_2")
	require.NoError(t, err)
	require.Len(t, s.peers, 1)
	require.Equal(t, []string{"network_1"}, s.peers[addr.String()].ownerNames())
}

// TestAddPeerConflictHoldTime verifies that adding the same peer address with a
// different hold time returns an error, as the single shared session can only
// use one of them.
func TestAddPeerConflictHoldTime(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "", 30, "network_1")
	require.NoError(t, err)

	err = s.AddPeer(addr, 65000, "", 60, "network_2")
	require.Error(t, err)
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
// and nothing recorded when the BGP server has never been started. A listener
// that was stopped keeps reporting the peers it is configured with; see
// TestDebugReportsRecordedPeersWhenStopped.
func TestDebugNotRunning(t *testing.T) {
	s := NewServer()
	debug := s.Debug()
	require.False(t, debug.Server.Running)
	require.Empty(t, debug.Peers)
	require.Empty(t, debug.Prefixes)
}

// TestDebugReportsRecordedPeersWhenStopped verifies that stopping the listener
// does not make the endpoint look as though BGP were unconfigured. The peers and
// the routes they would be advertised are still reported, so that a stopped
// listener can be told apart from one that was never configured, and Running says
// that nothing is being advertised. The prefixes are not reported, as they are
// read from a RIB that does not exist while the listener is down.
func TestDebugReportsRecordedPeersWhenStopped(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)

	owner := OwnerNetwork(1)
	err = s.AddPeer(mustParseIP("127.0.0.2"), 65001, "", 30, owner)
	require.NoError(t, err)
	err = s.AddPrefix(mustParseCIDR("10.0.0.0/24"), mustParseIP("192.0.2.10"), owner)
	require.NoError(t, err)

	debug := s.Debug()
	require.True(t, debug.Server.Running)
	require.Len(t, debug.Peers, 1)
	require.Len(t, debug.Prefixes, 1)

	err = s.stop()
	require.NoError(t, err)

	debug = s.Debug()
	require.False(t, debug.Server.Running)

	// The configuration that is still recorded is reported.
	require.Len(t, debug.Peers, 1)
	require.Equal(t, []string{owner}, debug.Peers[0].Owners)
	require.Len(t, debug.Peers[0].Routes, 1)
	require.Equal(t, "10.0.0.0/24", debug.Peers[0].Routes[0].Prefix)
	require.Equal(t, "192.0.2.10", debug.Peers[0].Routes[0].Nexthop)

	// Nothing is programmed, and no listener is configured.
	require.Empty(t, debug.Prefixes)
	require.Empty(t, debug.Server.Address)
	require.Zero(t, debug.Server.ASN)
	require.Empty(t, debug.Server.RouterID)
}

// TestStopClearsStateOnFailure verifies that a teardown that fails part way still
// clears the recorded listener, so that it is not left reported as running and
// refusing to start, and that the failure is reported to the caller.
//
// The failure is constructed by recording a peer that the BGP server does not
// know about, which makes removing it fail. That is a server-side inconsistency
// rather than something the API can produce; the point of the test is that the
// teardown completes regardless.
func TestStopClearsStateOnFailure(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)

	addr := mustParseIP("127.0.0.2")
	s.mu.Lock()
	s.peers[addr.String()] = peer{address: addr, asn: 65001, owners: map[string]int{OwnerNetwork(1): 1}}
	s.mu.Unlock()

	err = s.stop()
	require.Error(t, err)

	// The listener is no longer recorded, so it can be used and reconfigured.
	require.Nil(t, s.bgp)
	require.False(t, s.Debug().Server.Running)

	err = s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	require.NoError(t, s.stop())
}

// TestDebugRunningState verifies that the debug endpoint reports the listener as
// running only when it is. A server that was recorded without serving used to
// still be reported as running, which made the endpoint misleading when
// diagnosing why routes were not being advertised.
func TestDebugRunningState(t *testing.T) {
	s := NewServer()

	// A server that was never started is not running.
	require.False(t, s.Debug().Server.Running)

	// A listener that could not be started is not running, and reports none of
	// the configuration it never applied.
	err := s.start("127.0.0.1:notaport", 65000, mustParseIP("192.0.2.1"))
	require.Error(t, err)

	debug := s.Debug()
	require.False(t, debug.Server.Running)
	require.Empty(t, debug.Server.Address)
	require.Zero(t, debug.Server.ASN)

	// A started server is running, and is not once stopped.
	err = s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	require.True(t, s.Debug().Server.Running)

	err = s.stop()
	require.NoError(t, err)
	require.False(t, s.Debug().Server.Running)
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

// TestOVNNetworkExportPolicy verifies that an OVN network's own prefixes, its
// forwards, its load balancers and its instance NIC external routes are all
// advertised to the peer configured on the uplink network the OVN network is
// attached to, and are not advertised to a peer on a different uplink.
func TestOVNNetworkExportPolicy(t *testing.T) {
	s := NewServer()
	err := s.start("127.0.0.1:-1", 65000, mustParseIP("192.0.2.1"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.stop()
	})

	const uplink1ID = int64(10)
	const uplink2ID = int64(11)
	const ovnNetworkID = int64(20)

	uplink1Nexthop := mustParseIP("192.0.2.10")
	uplink2Nexthop := mustParseIP("192.0.2.11")
	uplink1Peer := mustParseIP("127.0.0.2")
	uplink2Peer := mustParseIP("127.0.0.3")

	// A peer on each of the two uplinks. The OVN network is only attached to
	// uplink 1, so only uplink 1's peer should receive its routes.
	err = s.AddPeer(uplink1Peer, 65001, "", 30, OwnerNetwork(uplink1ID))
	require.NoError(t, err)
	err = s.AddPeer(uplink2Peer, 65002, "", 30, OwnerNetwork(uplink2ID))
	require.NoError(t, err)

	ovnOwner := OwnerNetworkOVN(uplink1ID, ovnNetworkID)

	// The OVN network's own subnet, exported when ipvN.nat=false.
	err = s.AddPrefix(mustParseCIDR("10.20.0.0/24"), uplink1Nexthop, ovnOwner)
	require.NoError(t, err)

	// An OVN network forward, owned by the forward entity.
	fwdOwner := OwnerNetworkForwards(ovnOwner)
	err = s.AddPrefix(mustParseCIDR("198.51.100.1/32"), uplink1Nexthop, fwdOwner)
	require.NoError(t, err)

	// An OVN network load balancer, owned by the load balancer entity.
	lbOwner := OwnerNetworkLoadBalancers(ovnOwner)
	err = s.AddPrefix(mustParseCIDR("203.0.113.1/32"), uplink1Nexthop, lbOwner)
	require.NoError(t, err)

	// An instance NIC external route, registered under the OVN network's owner.
	err = s.AddPrefix(mustParseCIDR("0.0.0.0/0"), uplink1Nexthop, OwnerInstanceNetwork(ovnOwner, 1, "eth0"))
	require.NoError(t, err)

	// A route owned by the other uplink, which must not leak to uplink 1's peer.
	err = s.AddPrefix(mustParseCIDR("192.0.2.0/24"), uplink2Nexthop, OwnerNetwork(uplink2ID))
	require.NoError(t, err)

	debug := s.Debug()
	require.Len(t, debug.Peers, 2)

	var uplink1Routes, uplink2Routes []DebugInfoPrefix
	for _, p := range debug.Peers {
		switch p.Address {
		case uplink1Peer.String():
			uplink1Routes = p.Routes
		case uplink2Peer.String():
			uplink2Routes = p.Routes
		}
	}

	// Uplink 1's peer receives every route owned by the OVN network, each with
	// the OVN router's next-hop.
	advertised := map[string]string{}
	for _, r := range uplink1Routes {
		advertised[r.Prefix] = r.Owner
		require.Equal(t, uplink1Nexthop.String(), r.Nexthop, "Route %q should use the uplink 1 next-hop", r.Prefix)
	}

	require.Equal(t, map[string]string{
		"10.20.0.0/24":    ovnOwner,
		"198.51.100.1/32": fwdOwner,
		"203.0.113.1/32":  lbOwner,
		"0.0.0.0/0":       OwnerInstanceNetwork(ovnOwner, 1, "eth0"),
	}, advertised)

	// Uplink 2's peer receives only its own route.
	require.Len(t, uplink2Routes, 1)
	require.Equal(t, "192.0.2.0/24", uplink2Routes[0].Prefix)
	require.Equal(t, OwnerNetwork(uplink2ID), uplink2Routes[0].Owner)
}
