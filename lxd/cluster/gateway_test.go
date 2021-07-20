package cluster_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/go-dqlite/driver"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Basic creation and shutdown. By default, the gateway runs an in-memory gRPC
// server.
func TestGateway_Single(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	gateway := newGateway(t, node, cert, cert)
	defer gateway.Shutdown()

	trustedCerts := func() map[db.CertificateType]map[string]x509.Certificate {
		return nil
	}

	handlerFuncs := gateway.HandlerFuncs(nil, trustedCerts)
	assert.Len(t, handlerFuncs, 1)
	for endpoint, f := range handlerFuncs {
		c, err := x509.ParseCertificate(cert.KeyPair().Certificate[0])
		require.NoError(t, err)
		w := httptest.NewRecorder()
		r := &http.Request{}
		r.Header = http.Header{}
		r.Header.Set("X-Dqlite-Version", "1")
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{c},
		}
		f(w, r)
		assert.Equal(t, 404, w.Code, endpoint)
	}

	dial := gateway.DialFunc()
	netConn, err := dial(context.Background(), "")
	assert.NoError(t, err)
	assert.NotNil(t, netConn)
	require.NoError(t, netConn.Close())

	leader, err := gateway.LeaderAddress()
	assert.Equal(t, "", leader)
	assert.EqualError(t, err, cluster.ErrNodeIsNotClustered.Error())

	driver, err := driver.New(
		gateway.NodeStore(),
		driver.WithDialFunc(gateway.DialFunc()),
	)
	require.NoError(t, err)

	conn, err := driver.Open("test.db")
	require.NoError(t, err)

	require.NoError(t, conn.Close())
}

// If there's a network address configured, we expose the dqlite endpoint with
// an HTTP handler.
func TestGateway_SingleWithNetworkAddress(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	gateway := newGateway(t, node, cert, cert)
	defer gateway.Shutdown()

	trustedCerts := func() map[db.CertificateType]map[string]x509.Certificate {
		return nil
	}

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	driver, err := driver.New(
		gateway.NodeStore(),
		driver.WithDialFunc(gateway.DialFunc()),
	)
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
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	gateway := newGateway(t, node, cert, cert)
	defer gateway.Shutdown()

	trustedCerts := func() map[db.CertificateType]map[string]x509.Certificate {
		return nil
	}

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	// Make a request using a certificate different than the cluster one.
	certAlt := shared.TestingAltKeyPair()
	config, err := cluster.TLSClientConfig(certAlt, certAlt)
	config.InsecureSkipVerify = true // Skip client-side verification
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}

	for path := range gateway.HandlerFuncs(nil, trustedCerts) {
		url := fmt.Sprintf("https://%s%s", address, path)
		response, err := client.Head(url)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, response.StatusCode)
	}

}

// RaftNodes returns all nodes of the cluster.
func TestGateway_RaftNodesNotLeader(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	gateway := newGateway(t, node, cert, cert)
	defer gateway.Shutdown()

	nodes, err := gateway.RaftNodes()
	require.NoError(t, err)

	assert.Len(t, nodes, 1)
	assert.Equal(t, nodes[0].ID, uint64(1))
	assert.Equal(t, nodes[0].Address, address)
}

// Create a new test Gateway with the given parameters, and ensure no error happens.
func newGateway(t *testing.T, node *db.Node, networkCert *shared.CertInfo, serverCert *shared.CertInfo) *cluster.Gateway {
	logging.Testing(t)
	require.NoError(t, os.Mkdir(filepath.Join(node.Dir(), "global"), 0755))
	serverCertFunc := func() *shared.CertInfo { return serverCert }
	gateway, err := cluster.NewGateway(node, networkCert, serverCertFunc, cluster.Latency(0.2), cluster.LogLevel("TRACE"))
	require.NoError(t, err)
	return gateway
}
