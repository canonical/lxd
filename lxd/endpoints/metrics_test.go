package endpoints_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEndpoints_MetricsRetryAfterFailure verifies that a metrics listener that
// cannot bind reports an error and records nothing, so that it can be started
// again once the address is usable. The daemon relies on this to report the
// problem and carry on rather than refusing to start.
func TestEndpoints_MetricsRetryAfterFailure(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	// The metrics endpoint needs a server to serve with, as the daemon provides.
	config.MetricsServer = newServer()

	require.NoError(t, endpoints.Up(config))

	// Occupy a port so the metrics listener cannot use it.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := occupied.Addr().String()

	err = endpoints.UpMetrics(address)
	require.Error(t, err)
	require.Empty(t, endpoints.MetricsAddress(), "A failed start must record nothing")

	// Starting again on the same address works once it is free.
	require.NoError(t, occupied.Close())
	require.NoError(t, endpoints.UpMetrics(address))
	require.NotEmpty(t, endpoints.MetricsAddress())
}
