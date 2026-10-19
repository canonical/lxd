package dns

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStartUnusableAddress verifies that a listener that cannot bind reports an
// error rather than only logging it, and leaves nothing recorded, so that the
// daemon can report the problem and start the listener again later.
func TestStartUnusableAddress(t *testing.T) {
	// Find a free port once, so that the same address is used for both failures
	// and for the successful start afterwards, without depending on which
	// addresses happen to be assigned on this machine.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := probe.Addr().String()

	err = probe.Close()
	require.NoError(t, err)

	s := NewServer(nil, nil)

	// A TCP occupier makes the first bind fail.
	tcpOccupied, err := net.Listen("tcp", address)
	require.NoError(t, err)

	err = s.start(address)
	require.Error(t, err)
	require.Empty(t, s.address, "A failed start must not record the address")
	require.Nil(t, s.tcpDNS)
	require.Nil(t, s.udpDNS)

	err = tcpOccupied.Close()
	require.NoError(t, err)

	// A UDP occupier makes the second bind fail, after the TCP listener has
	// been bound, which must then be released again.
	udpOccupied, err := net.ListenPacket("udp", address)
	require.NoError(t, err)

	err = s.start(address)
	require.Error(t, err)
	require.Empty(t, s.address, "A failed start must not record the address")
	require.Nil(t, s.tcpDNS)
	require.Nil(t, s.udpDNS)

	err = udpOccupied.Close()
	require.NoError(t, err)

	// The listener must still be startable on the previously unusable port.
	err = s.start(address)
	require.NoError(t, err)
	require.Equal(t, address, s.address)

	err = s.stop()
	require.NoError(t, err)
	require.Empty(t, s.address)
}

// TestStartRejectsEmptyAddress verifies that the listener is not started on every
// interface by an empty address. An empty address is canonicalised to the default
// port on all interfaces, which no caller wants: a reconfiguration restores the
// previous address on failure, and that is empty when the listener was not
// running.
//
// The specific error is asserted, rather than just that one occurred, because
// binding the default port fails without privileges: the test would otherwise
// pass for that reason rather than because the address was rejected.
func TestStartRejectsEmptyAddress(t *testing.T) {
	s := NewServer(nil, nil)

	err := s.start("")
	require.ErrorContains(t, err, "without an address")
	require.Empty(t, s.address, "A rejected start must not record the address")
	require.Nil(t, s.tcpDNS)
	require.Nil(t, s.udpDNS)
}

// TestStopReleasesAddress verifies that stopping a listener releases its address,
// so it can be started again on the same one.
func TestStopReleasesAddress(t *testing.T) {
	// Bind a port to find one that is free, then release it for the test.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := probe.Addr().String()

	err = probe.Close()
	require.NoError(t, err)

	s := NewServer(nil, nil)

	err = s.start(address)
	require.NoError(t, err)

	err = s.stop()
	require.NoError(t, err)

	// If the sockets were left bound, this fails.
	err = s.start(address)
	require.NoError(t, err)

	err = s.stop()
	require.NoError(t, err)
}
