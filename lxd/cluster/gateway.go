package cluster

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
func NewGateway(db *db.Node, cert *shared.CertInfo, options ...Option) (*Gateway, error) {
	ctx, cancel := context.WithCancel(context.Background())

	o := newOptions()
	for _, option := range options {
		option(o)

	}

	gateway := &Gateway{
		db:        db,
		cert:      cert,
		options:   o,
		ctx:       ctx,
		cancel:    cancel,
		upgradeCh: make(chan struct{}, 16),
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
	options *options

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

	// Used when shutting down the daemon to cancel any ongoing gRPC
	// dialing attempt.
	ctx    context.Context
	cancel context.CancelFunc

	// Used to unblock nodes that are waiting for other nodes to upgrade
	// their version.
	upgradeCh chan struct{}
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
		if !tlsCheckCert(r, g.cert) {
			http.Error(w, "403 invalid client certificate", http.StatusForbidden)
			return
		}

		// Handle heatbeats.
		if r.Method == "PUT" {
			var nodes []db.RaftNode
			err := shared.ReadToJSON(r.Body, &nodes)
			if err != nil {
				http.Error(w, "400 invalid raft nodes payload", http.StatusBadRequest)
				return
			}
			logger.Debugf("Replace current raft nodes with %+v", nodes)
			err = g.db.Transaction(func(tx *db.NodeTx) error {
				return tx.RaftNodesReplace(nodes)
			})
			if err != nil {
				http.Error(w, "500 failed to update raft nodes", http.StatusInternalServerError)
				return
			}
			return
		}

		// From here on we require that this node is part of the raft cluster.
		if g.server == nil || g.memoryDial != nil {
			http.NotFound(w, r)
			return
		}

		// Handle database upgrade notifications.
		if r.Method == "PATCH" {
			select {
			case g.upgradeCh <- struct{}{}:
			default:
			}
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

		// Handle leader address requests.
		if r.Method == "GET" {
			leader, err := g.LeaderAddress()
			if err != nil {
				http.Error(w, "500 no elected leader", http.StatusInternalServerError)
				return
			}
			util.WriteJSON(w, map[string]string{"leader": leader}, false)
			return
		}

		g.server.ServeHTTP(w, r)
	}
	raft := func(w http.ResponseWriter, r *http.Request) {
		// If we are not part of the raft cluster, reply with a
		// redirect to one of the raft nodes that we know about.
		if g.raft == nil {
			var address string
			err := g.db.Transaction(func(tx *db.NodeTx) error {
				nodes, err := tx.RaftNodes()
				if err != nil {
					return err
				}
				address = nodes[0].Address
				return nil
			})
			if err != nil {
				http.Error(w, "500 failed to fetch raft nodes", http.StatusInternalServerError)
				return
			}
			url := &url.URL{
				Scheme:   "http",
				Path:     r.URL.Path,
				RawQuery: r.URL.RawQuery,
				Host:     address,
			}
			http.Redirect(w, r, url.String(), http.StatusPermanentRedirect)
			return
		}

		// If this node is not clustered return a 404.
		if g.raft.HandlerFunc() == nil {
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

// WaitUpgradeNotification waits for a notification from another node that all
// nodes in the cluster should now have been upgraded and have matching schema
// and API versions.
func (g *Gateway) WaitUpgradeNotification() {
	<-g.upgradeCh
}

// IsDatabaseNode returns true if this gateway also run acts a raft database node.
func (g *Gateway) IsDatabaseNode() bool {
	return g.raft != nil
}

// Dialer returns a gRPC dial function that can be used to connect to one of
// the dqlite nodes via gRPC.
func (g *Gateway) Dialer() grpcsql.Dialer {
	return func() (*grpc.ClientConn, error) {
		// Memory connection.
		if g.memoryDial != nil {
			return g.memoryDial()
		}

		// TODO: should the timeout be configurable?
		ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
		defer cancel()
		var err error
		for {
			// Network connection.
			addresses, dbErr := g.cachedRaftNodes()
			if dbErr != nil {
				return nil, dbErr
			}

			for _, address := range addresses {
				var conn *grpc.ClientConn
				conn, err = grpcNetworkDial(g.ctx, address, g.cert)
				if err == nil {
					return conn, nil
				}
				logger.Debugf("Failed to establish gRPC connection with %s: %v", address, err)
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			select {
			case <-time.After(250 * time.Millisecond):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
}

// Kill is an API that the daemon calls before it actually shuts down and calls
// Shutdown(). It will abort any ongoing or new attempt to establish a SQL gRPC
// connection with the dialer (typically for running some pre-shutdown
// queries).
func (g *Gateway) Kill() {
	logger.Debug("Cancel ongoing or future gRPC connection attempts")
	g.cancel()
}

// Shutdown this gateway, stopping the gRPC server and possibly the raft factory.
func (g *Gateway) Shutdown() error {
	logger.Info("Stop database gateway")
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

// Reset the gateway, shutting it down and starting against from scratch using
// the given certificate.
//
// This is used when disabling clustering on a node.
func (g *Gateway) Reset(cert *shared.CertInfo) error {
	err := g.Shutdown()
	if err != nil {
		return err
	}
	err = os.RemoveAll(filepath.Join(g.db.Dir(), "global"))
	if err != nil {
		return err
	}
	err = g.db.Transaction(func(tx *db.NodeTx) error {
		return tx.RaftNodesReplace(nil)
	})
	if err != nil {
		return err
	}
	g.cert = cert
	return g.init()
}

// LeaderAddress returns the address of the current raft leader.
func (g *Gateway) LeaderAddress() (string, error) {
	// If we aren't clustered, return an error.
	if g.memoryDial != nil {
		return "", fmt.Errorf("node is not clustered")
	}

	ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
	defer cancel()

	// If this is a raft node, return the address of the current leader, or
	// wait a bit until one is elected.
	if g.raft != nil {
		for ctx.Err() == nil {
			address := string(g.raft.Raft().Leader())
			if address != "" {
				return address, nil
			}
			time.Sleep(time.Second)
		}
		return "", ctx.Err()

	}

	// If this isn't a raft node, contact a raft node and ask for the
	// address of the current leader.
	config, err := tlsClientConfig(g.cert)
	if err != nil {
		return "", err
	}
	addresses := []string{}
	err = g.db.Transaction(func(tx *db.NodeTx) error {
		nodes, err := tx.RaftNodes()
		if err != nil {
			return err
		}
		for _, node := range nodes {
			addresses = append(addresses, node.Address)
		}
		return nil
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to fetch raft nodes addresses")
	}

	if len(addresses) == 0 {
		// This should never happen because the raft_nodes table should
		// be never empty for a clustered node, but check it for good
		// measure.
		return "", fmt.Errorf("no raft node known")
	}

	for _, address := range addresses {
		url := fmt.Sprintf("https://%s%s", address, grpcEndpoint)
		request, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}
		request = request.WithContext(ctx)
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
		response, err := client.Do(request)
		if err != nil {
			logger.Debugf("Failed to fetch leader address from %s", address)
			continue
		}
		if response.StatusCode != http.StatusOK {
			logger.Debugf("Request for leader address from %s failed", address)
			continue
		}
		info := map[string]string{}
		err = shared.ReadToJSON(response.Body, &info)
		if err != nil {
			logger.Debugf("Failed to parse leader address from %s", address)
			continue
		}
		leader := info["leader"]
		if leader == "" {
			logger.Debugf("Raft node %s returned no leader address", address)
			continue
		}
		return leader, nil
	}

	return "", fmt.Errorf("raft cluster is unavailable")
}

// Initialize the gateway, creating a new raft factory and gRPC server (if this
// node is a database node), and a gRPC dialer.
func (g *Gateway) init() error {
	logger.Info("Initializing database gateway")
	raft, err := newRaft(g.db, g.cert, g.options.latency)
	if err != nil {
		return errors.Wrap(err, "failed to create raft factory")
	}

	// If the resulting raft instance is not nil, it means that this node
	// should serve as database node, so create a dqlite driver to be
	// exposed it over gRPC.
	if raft != nil {
		config := dqlite.DriverConfig{}
		driver, err := dqlite.NewDriver(raft.Registry(), raft.Raft(), config)
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
	} else {
		g.server = nil
		g.raft = nil
	}
	return nil
}

// Wait for the raft node to become leader. Should only be used by Bootstrap,
// since we know that we'll self elect.
func (g *Gateway) waitLeadership() error {
	n := 80
	sleep := 250 * time.Millisecond
	for i := 0; i < n; i++ {
		if g.raft.raft.State() == raft.Leader {
			return nil
		}
		time.Sleep(sleep)
	}
	return fmt.Errorf("raft node did not self-elect within %s", time.Duration(n)*sleep)
}

// Return information about the LXD nodes that a currently part of the raft
// cluster, as configured in the raft log. It returns an error if this node is
// not the leader.
func (g *Gateway) currentRaftNodes() ([]db.RaftNode, error) {
	if g.raft == nil {
		return nil, raft.ErrNotLeader
	}
	servers, err := g.raft.Servers()
	if err != nil {
		return nil, err
	}
	provider := raftAddressProvider{db: g.db}
	nodes := make([]db.RaftNode, len(servers))
	for i, server := range servers {
		address, err := provider.ServerAddr(server.ID)
		if err != nil {
			if err != db.ErrNoSuchObject {
				return nil, errors.Wrap(err, "failed to fetch raft server address")
			}
			// Use the initial address as fallback. This is an edge
			// case that happens when a new leader is elected and
			// its raft_nodes table is not fully up-to-date yet.
			address = server.Address
		}
		id, err := strconv.Atoi(string(server.ID))
		if err != nil {
			return nil, errors.Wrap(err, "non-numeric server ID")
		}
		nodes[i].ID = int64(id)
		nodes[i].Address = string(address)
	}
	return nodes, nil
}

// Return the addresses of the raft nodes as stored in the node-level
// database.
//
// These values might leg behind the actual values, and are refreshed
// periodically during heartbeats.
func (g *Gateway) cachedRaftNodes() ([]string, error) {
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

func grpcNetworkDial(ctx context.Context, addr string, cert *shared.CertInfo) (*grpc.ClientConn, error) {
	config, err := tlsClientConfig(cert)
	if err != nil {
		return nil, err
	}

	// The whole attempt should not take more than a few seconds. If the
	// context gets cancelled, calling code will typically try against
	// another database node, in round robin.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Make a probe HEAD request to check if the target node is the leader.
	url := fmt.Sprintf("https://%s%s", addr, grpcEndpoint)
	request, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return nil, err
	}
	request = request.WithContext(ctx)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(response.Status)
	}

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
func dqliteLog(configuredLevel string) func(level, message string) {
	return func(level, message string) {
		if level == "TRACE" {
			// TODO: lxd has no TRACE level, so let's map it to
			// DEBUG. However, ignore it altogether if the
			// configured level is not TRACE, to save some CPU
			// (since TRACE is quite verbose in dqlite).
			if configuredLevel != "TRACE" {
				return
			}
			level = "DEBUG"
		}

		message = fmt.Sprintf("DQLite: %s", message)
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
}
