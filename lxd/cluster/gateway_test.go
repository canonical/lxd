package cluster_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/hashicorp/raft"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

// Basic creation and shutdown. By default, the gateway runs an in-memory gRPC
// server.
func TestGateway_Single(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	handlerFuncs := gateway.HandlerFuncs()
	assert.Len(t, handlerFuncs, 2)
	for endpoint, f := range handlerFuncs {
		c, err := x509.ParseCertificate(cert.KeyPair().Certificate[0])
		require.NoError(t, err)
		w := httptest.NewRecorder()
		r := &http.Request{}
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{c},
		}
		f(w, r)
		assert.Equal(t, 404, w.Code, endpoint)
	}

	dial := gateway.DialFunc()
	conn, err := dial(context.Background(), "")
	assert.NoError(t, err)
	assert.NotNil(t, conn)

	leader, err := gateway.LeaderAddress()
	assert.Equal(t, "", leader)
	assert.EqualError(t, err, "Node is not clustered")
}

// If there's a network address configured, we expose the gRPC endpoint with
// an HTTP handler.
func TestGateway_SingleWithNetworkAddress(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	store := setRaftRole(t, db, address)

	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	driver, err := dqlite.NewDriver(store, dqlite.WithDialFunc(gateway.DialFunc()))
	require.NoError(t, err)

	conn, err := driver.Open("test.db")
	require.NoError(t, err)

	require.NoError(t, conn.Close())

	leader, err := gateway.LeaderAddress()
	require.NoError(t, err)
	assert.Equal(t, address, leader)
}

// When networked, the grpc and raft endpoints requires the cluster
// certificate.
func TestGateway_NetworkAuth(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, db, address)

	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	// Make a request using a certificate different than the cluster one.
	config, err := cluster.TLSClientConfig(shared.TestingAltKeyPair())
	config.InsecureSkipVerify = true // Skip client-side verification
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}

	for path := range gateway.HandlerFuncs() {
		url := fmt.Sprintf("https://%s%s", address, path)
		response, err := client.Head(url)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, response.StatusCode)
	}

}

// RaftNodes returns an error if the underlying raft instance is not the leader.
func TestGateway_RaftNodesNotLeader(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, db, address)

	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	// Get the node immediately, before the election has took place.
	_, err := gateway.RaftNodes()
	assert.Equal(t, raft.ErrNotLeader, err)
}

// Create a new test Gateway with the given parameters, and ensure no error
// happens.
func newGateway(t *testing.T, db *db.Node, certInfo *shared.CertInfo) *cluster.Gateway {
	logging.Testing(t)
	require.NoError(t, os.Mkdir(filepath.Join(db.Dir(), "global"), 0755))
	gateway, err := cluster.NewGateway(
		db, certInfo, cluster.Latency(0.2), cluster.LogLevel("TRACE"))
	require.NoError(t, err)
	return gateway
}
