package cluster_test

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

// newTriggerTestGateway creates a minimal gateway suitable for triggerUpdate tests.
func newTriggerTestGateway(t *testing.T) *cluster.Gateway {
	t.Helper()
	node, cleanup := db.NewTestNode(t)
	t.Cleanup(cleanup)

	cert := shared.TestingKeyPair()
	s := &state.State{ServerCert: func() *shared.CertInfo { return cert }}
	gateway := newGateway(t, node, cert, s)
	t.Cleanup(func() { _ = gateway.Shutdown() })
	return gateway
}

// TestGateway_TriggerUpdate_OnlyOnce verifies that the update function is called
// at most once even when triggerUpdate is invoked concurrently by multiple goroutines.
func TestGateway_TriggerUpdate_OnlyOnce(t *testing.T) {
	gateway := newTriggerTestGateway(t)

	var callCount atomic.Int64
	gateway.SetUpdateFunc(func() error {
		callCount.Add(1)
		return nil
	})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			gateway.TriggerUpdate()
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(1), callCount.Load())
	assert.True(t, gateway.UpgradeTriggered())
}

// TestGateway_TriggerUpdate_RollbackOnFailure verifies that the upgradeTriggered flag
// is reset when the update function returns an error, allowing a subsequent call to retry.
func TestGateway_TriggerUpdate_RollbackOnFailure(t *testing.T) {
	gateway := newTriggerTestGateway(t)

	var callCount atomic.Int64
	gateway.SetUpdateFunc(func() error {
		if callCount.Add(1) == 1 {
			return errors.New("update failed")
		}

		return nil
	})

	// First call: update fails, flag should be rolled back.
	gateway.TriggerUpdate()
	assert.False(t, gateway.UpgradeTriggered(), "upgradeTriggered flag should be reset after failure")

	// Second call: update succeeds, flag should remain set.
	gateway.TriggerUpdate()
	assert.True(t, gateway.UpgradeTriggered(), "upgradeTriggered flag should be set after success")

	assert.Equal(t, int64(2), callCount.Load())
}

// TestGateway_TriggerUpdate_LockReleasedDuringUpdate verifies that g.lock is not held
// while the update function is running, so concurrent requests are not starved.
func TestGateway_TriggerUpdate_LockReleasedDuringUpdate(t *testing.T) {
	gateway := newTriggerTestGateway(t)

	started := make(chan struct{})
	unblock := make(chan struct{})
	gateway.SetUpdateFunc(func() error {
		close(started)
		<-unblock
		return nil
	})

	go gateway.TriggerUpdate()

	// Wait until the update function has started.
	<-started

	// While the update function is running, the read lock must be acquirable.
	require.True(t, gateway.TryRLock(), "g.lock must not be held while the update function is running")
	gateway.RUnlock()

	close(unblock)
}

// TestHandlerFuncs_DqliteVersionTooNew verifies that a request advertising a dqlite
// version newer than the local one receives a 503 response and triggers the update function.
func TestHandlerFuncs_DqliteVersionTooNew(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	c, err := x509.ParseCertificate(cert.KeyPair().Certificate[0])
	require.NoError(t, err)

	s := &state.State{ServerCert: func() *shared.CertInfo { return cert }}
	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.Shutdown() }()

	var called atomic.Bool
	gateway.SetUpdateFunc(func() error {
		called.Store(true)
		return nil
	})

	for endpoint, handler := range gateway.HandlerFuncs(nil, &identity.Cache{}) {
		w := httptest.NewRecorder()
		r := &http.Request{Header: http.Header{}}
		// dqliteVersion is 1; send 2 to simulate a newer peer.
		r.Header.Set("X-Dqlite-Version", "2")
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}}
		handler(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, endpoint)
	}

	assert.True(t, called.Load(), "update function should have been called on dqlite version mismatch")
}
