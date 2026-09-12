package cluster

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

func TestAddressSetChanged(t *testing.T) {
	tests := []struct {
		name    string
		current []string
		updated []string
		want    bool
	}{
		{
			name:    "identical slices",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    false,
		},
		{
			name:    "same addresses different order",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.2:8443", "10.0.0.1:8443"},
			want:    false,
		},
		{
			name:    "address added",
			current: []string{"10.0.0.1:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    true,
		},
		{
			name:    "address removed",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "address replaced",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.3:8443"},
			want:    true,
		},
		{
			name:    "both empty",
			current: []string{},
			updated: []string{},
			want:    false,
		},
		{
			name:    "current empty updated non-empty",
			current: []string{},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "current non-empty updated empty",
			current: []string{"10.0.0.1:8443"},
			updated: []string{},
			want:    true,
		},
		{
			name:    "nil slices",
			current: nil,
			updated: nil,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addressSetChanged(tc.current, tc.updated)
			assert.Equal(t, tc.want, got)
		})
	}
}

// testLXDServer is a TLS server answering the /1.0 request that the LXD client performs while connecting.
type testLXDServer struct {
	// address is the "host:port" the server is listening on.
	address string

	// canceled is closed when a request handler gave up because its context was canceled.
	canceled chan struct{}
}

// newTestLXDServer starts a TLS server behaving like a minimal LXD server. If block is non-nil the
// handler waits for it to be closed before responding, which allows controlling which address connects first.
func newTestLXDServer(t *testing.T, block <-chan struct{}) *testLXDServer {
	t.Helper()

	server := &testLXDServer{canceled: make(chan struct{})}

	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block != nil {
			select {
			case <-block:
			case <-r.Context().Done():
				once.Do(func() { close(server.canceled) })
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ResponseRaw{
			Type:       api.SyncResponse,
			Status:     api.Success.String(),
			StatusCode: int(api.Success),
			Metadata:   api.Server{},
		})
	})

	httpServer := httptest.NewTLSServer(handler)
	t.Cleanup(httpServer.Close)

	server.address = strings.TrimPrefix(httpServer.URL, "https://")

	return server
}

// newUnreachableAddress returns an address nothing is listening on.
func newUnreachableAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

// clientAddress returns the address the given client is connected to.
func clientAddress(t *testing.T, client lxd.InstanceServer) string {
	t.Helper()

	info, err := client.GetConnectionInfo()
	require.NoError(t, err)

	return strings.TrimPrefix(info.URL, "https://")
}

func TestGetNextMemberClient(t *testing.T) {
	args := &lxd.ConnectionArgs{InsecureSkipVerify: true}

	t.Run("hands out the fastest client first and the slower ones on retry", func(t *testing.T) {
		// Block two of the three servers until the fastest one has been handed out, so the order in
		// which the generator returns the clients is deterministic.
		released := make(chan struct{})
		fastest := newTestLXDServer(t, nil)
		addresses := []string{newTestLXDServer(t, released).address, fastest.address, newTestLXDServer(t, released).address}

		next, cleanup := GetNextMemberClient(context.Background(), addresses, args)
		defer cleanup()

		client, address, err := next()
		require.NoError(t, err)
		assert.Equal(t, fastest.address, address)
		assert.Equal(t, fastest.address, clientAddress(t, client))

		close(released)

		seen := make([]string, 0, len(addresses))
		seen = append(seen, address)
		for range 2 {
			client, address, err := next()
			require.NoError(t, err)
			assert.Equal(t, address, clientAddress(t, client))
			seen = append(seen, address)
		}

		assert.ElementsMatch(t, addresses, seen)

		// Every address has been handed out, so there is nothing left to retry with.
		_, _, err = next()
		assert.ErrorContains(t, err, "Failed connecting to any remaining cluster member")
	})

	t.Run("skips addresses that fail to connect", func(t *testing.T) {
		reachable := newTestLXDServer(t, nil)
		addresses := []string{newUnreachableAddress(t), newUnreachableAddress(t), reachable.address}

		next, cleanup := GetNextMemberClient(context.Background(), addresses, args)
		defer cleanup()

		_, address, err := next()
		require.NoError(t, err)
		assert.Equal(t, reachable.address, address)
	})

	t.Run("reports all connection failures once exhausted", func(t *testing.T) {
		addresses := []string{newUnreachableAddress(t), newUnreachableAddress(t), newUnreachableAddress(t)}

		next, cleanup := GetNextMemberClient(context.Background(), addresses, args)
		defer cleanup()

		_, _, err := next()
		require.Error(t, err)
		assert.ErrorContains(t, err, "Failed connecting to any remaining cluster member")
		for _, address := range addresses {
			assert.ErrorContains(t, err, address)
		}
	})

	t.Run("cleanup aborts pending attempts and disconnects handed out clients", func(t *testing.T) {
		released := make(chan struct{})
		defer close(released)

		pending := newTestLXDServer(t, released)
		addresses := []string{newTestLXDServer(t, nil).address, pending.address}

		next, cleanup := GetNextMemberClient(context.Background(), addresses, args)

		client, _, err := next()
		require.NoError(t, err)

		cleanup()

		// The handed out client is no longer usable.
		_, _, err = client.GetServer()
		assert.Error(t, err)

		// The attempt that was never handed out got aborted.
		<-pending.canceled
	})

	t.Run("no addresses", func(t *testing.T) {
		next, cleanup := GetNextMemberClient(context.Background(), nil, args)
		defer cleanup()

		_, _, err := next()
		assert.ErrorContains(t, err, "Failed connecting to any remaining cluster member")
	})
}
