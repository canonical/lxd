package cluster

import (
	"net"
	"time"

	"github.com/CanonicalLtd/go-grpc-sql"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
)

// NewGateway creates a new Gateway for managing access to the dqlite cluster.
//
// When a new gateway is created, the node-level database is queried to check
// what kind of role this node plays and if it's exposed over the network. It
// will initialize internal data structures accordingly, for example starting a
// dqlite driver if this node is a database node.
//
// After creation, the Daemon is expected to expose whatever http handlers the
// HandlerFuncs method returns and to access the dqlite cluster using the gRPC
// dialer returned by the Dialer method.
func NewGateway(db *db.Node, cert *shared.CertInfo, latency float64) (*Gateway, error) {
	gateway := &Gateway{
		db:      db,
		cert:    cert,
		latency: latency,
	}

	err := gateway.init()
	if err != nil {
		return nil, err
	}

	return gateway, nil
}

// Gateway mediates access to the dqlite cluster using a gRPC SQL client, and
// possibly runs a dqlite replica on this LXD node (if we're configured to do
// so).
type Gateway struct {
	db      *db.Node
	cert    *shared.CertInfo
	latency float64

	// The gRPC server exposing the dqlite driver created by this
	// gateway. It's nil if this LXD node is not supposed to be part of the
	// raft cluster.
	server *grpc.Server

	// A dialer that will connect to the gRPC server using an in-memory
	// net.Conn. It's non-nil when clustering is not enabled on this LXD
	// node, and so we don't expose any dqlite or raft network endpoint,
	// but still we want to use dqlite as backend for the "cluster"
	// database, to minimize the difference between code paths in
	// clustering and non-clustering modes.
	memoryDial func() (*grpc.ClientConn, error)
}

// Dialer returns a gRPC dial function that can be used to connect to one of
// the dqlite nodes via gRPC.
func (g *Gateway) Dialer() grpcsql.Dialer {
	return func() (*grpc.ClientConn, error) {
		// Memory connection.
		return g.memoryDial()
	}
}

// Shutdown this gateway, stopping the gRPC server and possibly the raft factory.
func (g *Gateway) Shutdown() error {
	if g.server != nil {
		g.server.Stop()
		// Unset the memory dial, since Shutdown() is also called for
		// switching between in-memory and network mode.
		g.memoryDial = nil
	}
	return nil
}

// Initialize the gateway, creating a new raft factory and gRPC server (if this
// node is a database node), and a gRPC dialer.
func (g *Gateway) init() error {
	g.server = grpcsql.NewServer(&sqlite3.SQLiteDriver{})
	listener, dial := util.InMemoryNetwork()
	go g.server.Serve(listener)
	g.memoryDial = grpcMemoryDial(dial)
	return nil
}

// Convert a raw in-memory dial function into a gRPC one.
func grpcMemoryDial(dial func() net.Conn) func() (*grpc.ClientConn, error) {
	options := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(string, time.Duration) (net.Conn, error) {
			return dial(), nil
		}),
	}
	return func() (*grpc.ClientConn, error) {
		return grpc.Dial("", options...)
	}
}
