package cluster

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	dqlite "github.com/canonical/go-dqlite"
	dqliteclient "github.com/canonical/go-dqlite/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/eagain"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// NewGateway creates a new Gateway for managing access to the dqlite cluster.
//
// When a new gateway is created, the node-level database is queried to check
// what kind of role this node plays and if it's exposed over the network. It
// will initialize internal data structures accordingly, for example starting a
// local dqlite server if this node is a database node.
//
// After creation, the Daemon is expected to expose whatever http handlers the
// HandlerFuncs method returns and to access the dqlite cluster using the
// dialer returned by the DialFunc method.
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
		acceptCh:  make(chan net.Conn),
		store:     &dqliteServerStore{},
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
	server   *dqlite.Server
	acceptCh chan net.Conn

	// A dialer that will connect to the dqlite server using a loopback
	// net.Conn. It's non-nil when clustering is not enabled on this LXD
	// node, and so we don't expose any dqlite or raft network endpoint,
	// but still we want to use dqlite as backend for the "cluster"
	// database, to minimize the difference between code paths in
	// clustering and non-clustering modes.
	memoryDial dqlite.DialFunc

	// Used when shutting down the daemon to cancel any ongoing gRPC
	// dialing attempt.
	ctx    context.Context
	cancel context.CancelFunc

	// Used to unblock nodes that are waiting for other nodes to upgrade
	// their version.
	upgradeCh chan struct{}

	// Used to track whether we already triggered an upgrade because we
	// detected a peer with an higher version.
	upgradeTriggered bool

	// Used for the heartbeat handler
	Cluster           *db.Cluster
	HeartbeatNodeHook func(*APIHeartbeat)

	// ServerStore wrapper.
	store *dqliteServerStore

	lock sync.RWMutex

	// Abstract unix socket that the local dqlite task is listening to.
	bindAddress string
}

// Current dqlite protocol version.
const dqliteVersion = 1

// Set the dqlite version header.
func setDqliteVersionHeader(request *http.Request) {
	request.Header.Set("X-Dqlite-Version", fmt.Sprintf("%d", dqliteVersion))
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
func (g *Gateway) HandlerFuncs(nodeRefreshTask func(*APIHeartbeat)) map[string]http.HandlerFunc {
	database := func(w http.ResponseWriter, r *http.Request) {
		g.lock.RLock()
		defer g.lock.RUnlock()

		if !tlsCheckCert(r, g.cert) {
			http.Error(w, "403 invalid client certificate", http.StatusForbidden)
			return
		}

		// Compare the dqlite version of the connecting client
		// with our own one.
		versionHeader := r.Header.Get("X-Dqlite-Version")
		if versionHeader == "" {
			// No version header means an old pre dqlite 1.0 client.
			versionHeader = "0"
		}
		version, err := strconv.Atoi(versionHeader)
		if err != nil {
			http.Error(w, "400 invalid dqlite version", http.StatusBadRequest)
			return
		}
		if version != dqliteVersion {
			if version > dqliteVersion {
				if !g.upgradeTriggered {
					err = triggerUpdate()
					if err == nil {
						g.upgradeTriggered = true
					}
				}
				http.Error(w, "503 unsupported dqlite version", http.StatusServiceUnavailable)
			} else {
				http.Error(w, "426 dqlite version too old ", http.StatusUpgradeRequired)
			}
			return
		}

		// Handle heatbeats (these normally come from leader, but can come from joining nodes too).
		if r.Method == "PUT" {
			var heartbeatData APIHeartbeat
			err := json.NewDecoder(r.Body).Decode(&heartbeatData)
			if err != nil {
				logger.Errorf("Error decoding heartbeat body: %v", err)
				http.Error(w, "400 invalid heartbeat payload", http.StatusBadRequest)
				return
			}

			raftNodes := make([]db.RaftNode, 0)
			for _, node := range heartbeatData.Members {
				if node.RaftID > 0 {
					raftNodes = append(raftNodes, db.RaftNode{
						ID:      node.RaftID,
						Address: node.Address,
					})
				}
			}

			// Check we have been sent at least 1 raft node before wiping our set.
			if len(raftNodes) > 0 {
				// Accept Raft node updates from any node (joining nodes just send raft nodes heartbeat data).
				logger.Debugf("Replace current raft nodes with %+v", raftNodes)
				err = g.db.Transaction(func(tx *db.NodeTx) error {
					return tx.RaftNodesReplace(raftNodes)
				})
				if err != nil {
					logger.Errorf("Error updating raft nodes: %v", err)
					http.Error(w, "500 failed to update raft nodes", http.StatusInternalServerError)
					return
				}
			} else {
				logger.Errorf("Empty raft node set received")
			}

			// Only perform node refresh task if we have received a full state list from leader.
			if !heartbeatData.FullStateList {
				logger.Debugf("Partial node list heartbeat received, skipping full update")
				return
			}

			// If node refresh task is specified, run it async.
			if nodeRefreshTask != nil {
				go nodeRefreshTask(&heartbeatData)
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

		// Before actually establishing the connection, our dialer
		// probes the node to see if it's currently the leader
		// (otherwise it tries with another node or retry later).
		if r.Method == "HEAD" {
			leader, err := g.server.LeaderAddress(context.Background())
			if err != nil {
				http.Error(w, "500 failed to get leader address", http.StatusInternalServerError)
				return
			}
			if leader != g.raft.info.Address {
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

		if r.Header.Get("Upgrade") != "dqlite" {
			http.Error(w, "Missing or invalid upgrade header", http.StatusBadRequest)
			return
		}

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
			return
		}

		conn, _, err := hijacker.Hijack()
		if err != nil {
			message := errors.Wrap(err, "Failed to hijack connection").Error()
			http.Error(w, message, http.StatusInternalServerError)
			return
		}

		// Write the status line and upgrade header by hand since w.WriteHeader()
		// would fail after Hijack()
		data := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: dqlite\r\n\r\n")
		if n, err := conn.Write(data); err != nil || n != len(data) {
			conn.Close()
			return
		}

		g.acceptCh <- conn
	}

	return map[string]http.HandlerFunc{
		databaseEndpoint: database,
	}
}

// Snapshot can be used to manually trigger a RAFT snapshot
func (g *Gateway) Snapshot() error {
	g.lock.RLock()
	defer g.lock.RUnlock()

	// TODO: implement support for forcing a snapshot in dqlite v1
	return fmt.Errorf("Not supported")
}

// WaitUpgradeNotification waits for a notification from another node that all
// nodes in the cluster should now have been upgraded and have matching schema
// and API versions.
func (g *Gateway) WaitUpgradeNotification() {
	<-g.upgradeCh
}

// IsDatabaseNode returns true if this gateway also run acts a raft database node.
func (g *Gateway) IsDatabaseNode() bool {
	g.lock.RLock()
	defer g.lock.RUnlock()

	return g.raft != nil
}

// DialFunc returns a dial function that can be used to connect to one of the
// dqlite nodes.
func (g *Gateway) DialFunc() dqlite.DialFunc {
	return func(ctx context.Context, address string) (net.Conn, error) {
		g.lock.RLock()
		defer g.lock.RUnlock()

		// Memory connection.
		if g.memoryDial != nil {
			return g.memoryDial(ctx, address)
		}

		return dqliteNetworkDial(ctx, address, g, true)
	}
}

// Dial function for establishing raft connections.
func (g *Gateway) raftDial() dqlite.DialFunc {
	return func(ctx context.Context, address string) (net.Conn, error) {
		if address == "0" {
			provider := raftAddressProvider{db: g.db}
			addr, err := provider.ServerAddr(1)
			if err != nil {
				return nil, err
			}
			address = string(addr)
		}
		return dqliteNetworkDial(ctx, address, g, false)
	}
}

// Context returns a cancellation context to pass to dqlite.NewDriver as
// option.
//
// This context gets cancelled by Gateway.Kill() and at that point any
// connection failure won't be retried.
func (g *Gateway) Context() context.Context {
	return g.ctx
}

// ServerStore returns a dqlite server store that can be used to lookup the
// addresses of known database nodes.
func (g *Gateway) ServerStore() dqlite.ServerStore {
	return g.store
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
	logger.Debugf("Stop database gateway")

	if g.server != nil {
		g.Sync()
		g.server.Close()

		// Unset the memory dial, since Shutdown() is also called for
		// switching between in-memory and network mode.
		g.lock.Lock()
		g.memoryDial = nil
		g.lock.Unlock()
	}

	return nil
}

// Sync dumps the content of the database to disk. This is useful for
// inspection purposes, and it's also needed by the activateifneeded command so
// it can inspect the database in order to decide whether to activate the
// daemon or not.
func (g *Gateway) Sync() {
	g.lock.RLock()
	defer g.lock.RUnlock()

	if g.server == nil {
		return
	}

	client, err := g.getClient()
	if err != nil {
		logger.Warnf("Failed to get client: %v", err)
		return
	}

	files, err := client.Dump(context.Background(), "db.bin")
	if err != nil {
		// Just log a warning, since this is not fatal.
		logger.Warnf("Failed get database dump: %v", err)
		return
	}

	dir := filepath.Join(g.db.Dir(), "global")
	for _, file := range files {
		path := filepath.Join(dir, file.Name)
		err := ioutil.WriteFile(path, file.Data, 0600)
		if err != nil {
			logger.Warnf("Failed to dump database file %s: %v", file.Name, err)

		}
	}
}

func (g *Gateway) getClient() (*dqliteclient.Client, error) {
	return dqliteclient.New(context.Background(), g.bindAddress)
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
	g.lock.RLock()
	defer g.lock.RUnlock()

	// If we aren't clustered, return an error.
	if g.memoryDial != nil {
		return "", fmt.Errorf("Node is not clustered")
	}

	ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
	defer cancel()

	// If this is a raft node, return the address of the current leader, or
	// wait a bit until one is elected.
	if g.server != nil {
		for ctx.Err() == nil {
			leader, err := g.server.LeaderAddress(context.Background())
			if err != nil {
				return "", errors.Wrap(err, "Failed to get leader address")
			}
			if leader != "" {
				return leader, nil
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
		return "", errors.Wrap(err, "Failed to fetch raft nodes addresses")
	}

	if len(addresses) == 0 {
		// This should never happen because the raft_nodes table should
		// be never empty for a clustered node, but check it for good
		// measure.
		return "", fmt.Errorf("No raft node known")
	}

	for _, address := range addresses {
		url := fmt.Sprintf("https://%s%s", address, databaseEndpoint)
		request, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}
		setDqliteVersionHeader(request)
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

	return "", fmt.Errorf("RAFT cluster is unavailable")
}

// Initialize the gateway, creating a new raft factory and gRPC server (if this
// node is a database node), and a gRPC dialer.
func (g *Gateway) init() error {
	logger.Debugf("Initializing database gateway")
	raft, err := newRaft(g.db, g.cert, g.options.latency)
	if err != nil {
		return errors.Wrap(err, "Failed to create raft factory")
	}

	dir := filepath.Join(g.db.Dir(), "global")
	if shared.PathExists(filepath.Join(dir, "logs.db")) {
		err := shared.DirCopy(dir, dir+".bak")
		if err != nil {
			return errors.Wrap(err, "Failed to backup global database")
		}
		err = MigrateToDqlite10(dir)
		if err != nil {
			return errors.Wrap(err, "Failed to migrate to dqlite 1.0")
		}
		os.Remove(filepath.Join(dir, "logs.db"))
		os.RemoveAll(filepath.Join(dir, "snapshots"))
	}

	// If the resulting raft instance is not nil, it means that this node
	// should serve as database node, so create a dqlite driver possibly
	// exposing it over the network.
	if raft != nil {
		// Use the autobind feature of abstract unix sockets to get a
		// random unused address.
		listener, err := net.Listen("unix", "")
		if err != nil {
			return errors.Wrap(err, "Failed to autobind unix socket")
		}
		g.bindAddress = listener.Addr().String()
		listener.Close()

		options := []dqlite.ServerOption{
			dqlite.WithServerLogFunc(DqliteLog),
			dqlite.WithServerBindAddress(g.bindAddress),
		}

		if raft.info.Address == "1" {
			if raft.info.ID != 1 {
				panic("unexpected server ID")
			}
			g.memoryDial = dqliteMemoryDial(g.bindAddress)
			g.store.inMemory = dqlite.NewInmemServerStore()
			g.store.Set(context.Background(), []dqlite.ServerInfo{raft.info})
		} else {
			go runDqliteProxy(g.bindAddress, g.acceptCh)
			g.store.inMemory = nil
			options = append(options, dqlite.WithServerDialFunc(g.raftDial()))
		}

		server, err := dqlite.NewServer(
			raft.info,
			dir,
			options...,
		)
		if err != nil {
			return errors.Wrap(err, "Failed to create dqlite server")
		}

		err = server.Start()
		if err != nil {
			return errors.Wrap(err, "Failed to start dqlite server")
		}

		g.lock.Lock()
		g.server = server
		g.raft = raft
		g.lock.Unlock()
	} else {
		g.lock.Lock()
		g.server = nil
		g.raft = nil
		g.store.inMemory = nil
		g.lock.Unlock()
	}

	g.lock.Lock()
	g.store.onDisk = dqlite.NewServerStore(g.db.DB(), "main", "raft_nodes", "address")
	g.lock.Unlock()

	return nil
}

// Wait for the raft node to become leader. Should only be used by Bootstrap,
// since we know that we'll self elect.
func (g *Gateway) waitLeadership() error {
	n := 80
	sleep := 250 * time.Millisecond
	for i := 0; i < n; i++ {
		g.lock.RLock()
		isLeader, err := g.isLeader()
		if err != nil {
			return err
		}
		if isLeader {
			g.lock.RUnlock()
			return nil
		}
		g.lock.RUnlock()

		time.Sleep(sleep)
	}
	return fmt.Errorf("RAFT node did not self-elect within %s", time.Duration(n)*sleep)
}

func (g *Gateway) isLeader() (bool, error) {
	if g.server == nil {
		return false, nil
	}
	leader, err := g.server.LeaderAddress(context.Background())
	if err != nil {
		return false, errors.Wrap(err, "Failed to get leader address")
	}
	return leader == g.raft.info.Address, nil
}

// Internal error signalling that a node not the leader.
var errNotLeader = fmt.Errorf("Not leader")

// Return information about the LXD nodes that a currently part of the raft
// cluster, as configured in the raft log. It returns an error if this node is
// not the leader.
func (g *Gateway) currentRaftNodes() ([]db.RaftNode, error) {
	g.lock.RLock()
	defer g.lock.RUnlock()

	if g.raft == nil {
		return nil, errNotLeader
	}

	isLeader, err := g.isLeader()
	if err != nil {
		return nil, err
	}
	if !isLeader {
		return nil, errNotLeader
	}
	servers, err := g.server.Cluster(context.Background())
	if err != nil {
		return nil, err
	}
	provider := raftAddressProvider{db: g.db}
	nodes := make([]db.RaftNode, len(servers))
	for i, server := range servers {
		address, err := provider.ServerAddr(int(server.ID))
		if err != nil {
			if err != db.ErrNoSuchObject {
				return nil, errors.Wrap(err, "Failed to fetch raft server address")
			}
			// Use the initial address as fallback. This is an edge
			// case that happens when a new leader is elected and
			// its raft_nodes table is not fully up-to-date yet.
			address = server.Address
		}
		nodes[i].ID = int64(server.ID)
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
		return nil, errors.Wrap(err, "Failed to fetch raft nodes")
	}
	return addresses, nil
}

func dqliteNetworkDial(ctx context.Context, addr string, g *Gateway, checkLeader bool) (net.Conn, error) {
	config, err := tlsClientConfig(g.cert)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("https://%s%s", addr, databaseEndpoint)
	if checkLeader {
		// Make a probe HEAD request to check if the target node is the leader.
		request, err := http.NewRequest("HEAD", path, nil)
		if err != nil {
			return nil, err
		}
		setDqliteVersionHeader(request)
		request = request.WithContext(ctx)
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}

		// If the remote server has detected that we are out of date, let's
		// trigger an upgrade.
		if response.StatusCode == http.StatusUpgradeRequired {
			g.lock.Lock()
			defer g.lock.Unlock()
			if !g.upgradeTriggered {
				err = triggerUpdate()
				if err == nil {
					g.upgradeTriggered = true
				}
			}
			return nil, fmt.Errorf("Upgrade needed")
		}

		// If the endpoint does not exists, it means that the target node is
		// running version 1 of dqlite protocol. In that case we simply behave
		// as the node was at an older LXD version.
		if response.StatusCode == http.StatusNotFound {
			return nil, db.ErrSomeNodesAreBehind
		}

		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf(response.Status)
		}
	}

	// Establish the connection
	request := &http.Request{
		Method:     "POST",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       addr,
	}
	request.URL, err = url.Parse(path)
	if err != nil {
		return nil, err
	}

	request.Header.Set("Upgrade", "dqlite")
	setDqliteVersionHeader(request)
	request = request.WithContext(ctx)

	deadline, _ := ctx.Deadline()
	dialer := &net.Dialer{Timeout: time.Until(deadline)}

	conn, err := tls.DialWithDialer(dialer, "tcp", addr, config)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to HTTP endpoint")
	}

	err = request.Write(conn)
	if err != nil {
		return nil, errors.Wrap(err, "Sending HTTP request failed")
	}

	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to read response")
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("Dialing failed: expected status code 101 got %d", response.StatusCode)
	}
	if response.Header.Get("Upgrade") != "dqlite" {
		return nil, fmt.Errorf("Missing or unexpected Upgrade header in response")
	}

	listener, err := net.Listen("unix", "")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create unix listener")
	}

	goUnix, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to unix listener")
	}

	cUnix, err := listener.Accept()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to unix listener")
	}

	listener.Close()

	go func() {
		_, err := io.Copy(eagain.Writer{Writer: goUnix}, eagain.Reader{Reader: conn})
		if err != nil {
			logger.Warnf("Dqlite client proxy TLS -> Unix: %v", err)
		}
		goUnix.Close()
		conn.Close()
	}()

	go func() {
		_, err := io.Copy(eagain.Writer{Writer: conn}, eagain.Reader{Reader: goUnix})
		if err != nil {
			logger.Warnf("Dqlite client proxy Unix -> TLS: %v", err)
		}
		conn.Close()
		goUnix.Close()
	}()

	// We successfully established a connection with the leader. Maybe the
	// leader is ourselves, and we were recently elected. In that case
	// trigger a full heartbeat now: it will be a no-op if we aren't
	// actually leaders.
	if checkLeader {
		go g.heartbeat(g.ctx, true)
	}

	return cUnix, nil
}

// Create a dial function that connects to the local dqlite.
func dqliteMemoryDial(bindAddress string) dqlite.DialFunc {
	return func(ctx context.Context, address string) (net.Conn, error) {
		return net.Dial("unix", bindAddress)
	}
}

// The LXD API endpoint path that gets routed to a dqlite server handler for
// performing SQL queries against the dqlite server running on this node.
const databaseEndpoint = "/internal/database"

// DqliteLog redirects dqlite's logs to our own logger
func DqliteLog(l dqlite.LogLevel, format string, a ...interface{}) {
	format = fmt.Sprintf("Dqlite: %s", format)
	switch l {
	case dqlite.LogDebug:
		logger.Debugf(format, a...)
	case dqlite.LogInfo:
		logger.Debugf(format, a...)
	case dqlite.LogWarn:
		logger.Warnf(format, a...)
	case dqlite.LogError:
		logger.Errorf(format, a...)
	}
}

// Copy incoming TLS streams from upgraded HTTPS connections into Unix sockets
// connected to the dqlite task.
func runDqliteProxy(bindAddress string, acceptCh chan net.Conn) {
	for {
		src := <-acceptCh
		dst, err := net.Dial("unix", bindAddress)
		if err != nil {
			continue
		}

		go func() {
			_, err := io.Copy(eagain.Writer{Writer: dst}, eagain.Reader{Reader: src})
			if err != nil {
				logger.Warnf("Dqlite server proxy TLS -> Unix: %v", err)
			}
			src.Close()
			dst.Close()
		}()

		go func() {
			_, err := io.Copy(eagain.Writer{Writer: src}, eagain.Reader{Reader: dst})
			if err != nil {
				logger.Warnf("Dqlite server proxy Unix -> TLS: %v", err)
			}
			src.Close()
			dst.Close()
		}()
	}
}

// Conditionally uses the in-memory or the on-disk server store.
type dqliteServerStore struct {
	inMemory dqlite.ServerStore
	onDisk   dqlite.ServerStore
}

func (s *dqliteServerStore) Get(ctx context.Context) ([]dqlite.ServerInfo, error) {
	if s.inMemory != nil {
		return s.inMemory.Get(ctx)
	}
	return s.onDisk.Get(ctx)
}

func (s *dqliteServerStore) Set(ctx context.Context, servers []dqlite.ServerInfo) error {
	if s.inMemory != nil {
		return s.inMemory.Set(ctx, servers)
	}
	return s.onDisk.Set(ctx, servers)
}
