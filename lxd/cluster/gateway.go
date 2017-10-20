package cluster

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/CanonicalLtd/dqlite"
	"github.com/CanonicalLtd/go-grpc-sql"
	"github.com/hashicorp/raft"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

	// The raft instance to use for creating the dqlite driver. It's nil if
	// this LXD node is not supposed to be part of the raft cluster.
	raft *raftInstance

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

// HandlerFuncs returns the HTTP handlers that should be added to the REST API
// endpoint in order to handle database-related requests.
//
// There are two handlers, one for the /internal/raft endpoint and the other
// for /internal/db, which handle respectively raft and gRPC-SQL requests.
//
// These handlers might return 404, either because this LXD node is a
// non-clustered node not available over the network or because it is not a
// database node part of the dqlite cluster.
func (g *Gateway) HandlerFuncs() map[string]http.HandlerFunc {
	grpc := func(w http.ResponseWriter, r *http.Request) {
		if g.server == nil || g.memoryDial != nil {
			http.NotFound(w, r)
			return
		}

		if !tlsCheckCert(r, g.cert) {
			http.Error(w, "403 invalid client certificate", http.StatusForbidden)
			return
		}

		// Before actually establishing the gRPC SQL connection, our
		// dialer probes the node to see if it's currently the leader
		// (otherwise it tries with another node or retry later).
		if r.Method == "HEAD" {
			if g.raft.Raft().State() != raft.Leader {
				http.Error(w, "503 not leader", http.StatusServiceUnavailable)
				return
			}
			return
		}

		g.server.ServeHTTP(w, r)
	}
	raft := func(w http.ResponseWriter, r *http.Request) {
		if g.raft == nil || g.raft.HandlerFunc() == nil {
			http.NotFound(w, r)
			return
		}
		g.raft.HandlerFunc()(w, r)
	}

	return map[string]http.HandlerFunc{
		grpcEndpoint: grpc,
		raftEndpoint: raft,
	}
}

// Dialer returns a gRPC dial function that can be used to connect to one of
// the dqlite nodes via gRPC.
func (g *Gateway) Dialer() grpcsql.Dialer {
	return func() (*grpc.ClientConn, error) {
		// Memory connection.
		if g.memoryDial != nil {
			return g.memoryDial()
		}

		// Network connection.
		addresses, err := g.raftNodes()
		if err != nil {
			return nil, err
		}

		// FIXME: timeout should be configurable
		remaining := 10 * time.Second
		for remaining > 0 {
			for _, address := range addresses {
				var conn *grpc.ClientConn
				conn, err = grpcNetworkDial(address, g.cert, time.Second)
				if err == nil {
					return conn, nil
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		return nil, err
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
	if g.raft == nil {
		return nil
	}
	return g.raft.Shutdown()
}

// Initialize the gateway, creating a new raft factory and gRPC server (if this
// node is a database node), and a gRPC dialer.
func (g *Gateway) init() error {
	raft, err := newRaft(g.db, g.cert, g.latency)
	if err != nil {
		return errors.Wrap(err, "failed to create raft factory")
	}

	// If the resulting raft instance is not nil, it means that this node
	// should serve as database node, so create a dqlite driver to be
	// exposed it over gRPC.
	if raft != nil {
		driver, err := dqlite.NewDriver(raft.FSM(), raft.Raft(), dqlite.LogFunc(dqliteLog))
		if err != nil {
			return errors.Wrap(err, "failed to create dqlite driver")
		}
		server := grpcsql.NewServer(driver)
		if raft.HandlerFunc() == nil {
			// If no raft http handler is set, it means we are in
			// single node mode and we don't have a network
			// endpoint, so let's spin up a fully in-memory gRPC
			// server.
			listener, dial := util.InMemoryNetwork()
			go server.Serve(listener)
			g.memoryDial = grpcMemoryDial(dial)
		}

		g.server = server
		g.raft = raft
	}
	return nil
}

// Wait for the raft node to become leader. Should only be used by Bootstrap,
// since we know that we'll self elect.
func (g *Gateway) waitLeadership() error {
	for i := 0; i < 20; i++ {
		if g.raft.raft.State() == raft.Leader {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("raft node did not self-elect within 5 seconds")
}

// Return the addresses of the current raft nodes.
func (g *Gateway) raftNodes() ([]string, error) {
	var addresses []string
	err := g.db.Transaction(func(tx *db.NodeTx) error {
		var err error
		addresses, err = tx.RaftNodeAddresses()
		return err
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch raft nodes")
	}
	return addresses, nil
}

func grpcNetworkDial(addr string, cert *shared.CertInfo, t time.Duration) (*grpc.ClientConn, error) {
	config, err := tlsClientConfig(cert)
	if err != nil {
		return nil, err
	}

	// Make a probe HEAD request to check if the target node is the leader.
	url := fmt.Sprintf("https://%s%s", addr, grpcEndpoint)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
	response, err := client.Head(url)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(response.Status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(config)),
	}
	return grpc.DialContext(ctx, addr, options...)
}

// Convert a raw in-memory dial function into a gRPC one.
func grpcMemoryDial(dial func() net.Conn) func() (*grpc.ClientConn, error) {
	options := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDialer(func(string, time.Duration) (net.Conn, error) {
			return dial(), nil
		}),
	}
	return func() (*grpc.ClientConn, error) {
		return grpc.Dial("", options...)
	}
}

// The LXD API endpoint path that gets routed to a gRPC server handler for
// performing SQL queries against the dqlite driver running on this node.
//
// FIXME: figure out if there's a way to configure the gRPC client to add a
//        prefix to this url, e.g. /internal/db/protocol.SQL/Conn.
const grpcEndpoint = "/protocol.SQL/Conn"

// Redirect dqlite's logs to our own logger
func dqliteLog(level, message string) {
	if level == "TRACE" {
		// Ignore TRACE level.
		//
		// TODO: lxd has no TRACE level, which is quite verbose in dqlite,
		//       we'll need to take this level into account if we need to
		//       do some deep debugging.
		return
	}

	switch level {
	case "DEBUG":
		logger.Debug(message)
	case "INFO":
		logger.Info(message)
	case "WARN":
		logger.Warn(message)
	default:
		// Ignore any other log level.
	}
}
