package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	grpcsql "github.com/CanonicalLtd/go-grpc-sql"
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
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	handlerFuncs := gateway.HandlerFuncs()
	assert.Len(t, handlerFuncs, 2)
	for endpoint, f := range handlerFuncs {
		w := httptest.NewRecorder()
		r := &http.Request{}
		f(w, r)
		assert.Equal(t, 404, w.Code, endpoint)
	}

	dialer := gateway.Dialer()
	conn, err := dialer()
	assert.NoError(t, err)
	assert.NotNil(t, conn)
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
	setRaftRole(t, db, address)

	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	driver := grpcsql.NewDriver(gateway.Dialer())
	conn, err := driver.Open("test.db")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

// Create a new test Gateway with the given parameters, and ensure no error
// happens.
func newGateway(t *testing.T, db *db.Node, certInfo *shared.CertInfo) *cluster.Gateway {
	logging.Testing(t)
	require.NoError(t, os.Mkdir(filepath.Join(db.Dir(), "raft"), 0755))
	gateway, err := cluster.NewGateway(db, certInfo, 0.2)
	require.NoError(t, err)
	return gateway
}
