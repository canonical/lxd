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

	err := s.AddPeer(addr, 65000, "", 0)
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

	err := s.AddPeer(addr, 65000, "", 0)
	require.NoError(t, err)
	require.Equal(t, 1, s.peers[addr.String()].count)

	err = s.AddPeer(addr, 65000, "", 0)
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

	err := s.AddPeer(addr, 65000, "", 0)
	require.NoError(t, err)

	err = s.AddPeer(addr, 65001, "", 0)
	require.Error(t, err)
}

// TestAddPeerConflictPassword verifies that adding the same peer address with a
// different password returns an error.
func TestAddPeerConflictPassword(t *testing.T) {
	s := NewServer()
	addr := mustParseIP("192.168.1.1")

	err := s.AddPeer(addr, 65000, "secret", 0)
	require.NoError(t, err)

	err = s.AddPeer(addr, 65000, "different", 0)
	require.Error(t, err)
}
