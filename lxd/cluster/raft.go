package cluster

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dqlite "github.com/CanonicalLtd/go-dqlite"
	rafthttp "github.com/CanonicalLtd/raft-http"
	raftmembership "github.com/CanonicalLtd/raft-membership"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Create a raft instance and all its dependencies, to be used as backend for
// the dqlite driver running on this LXD node.
//
// If this node should not serve as dqlite node, nil is returned.
//
// The raft instance will use an in-memory transport if clustering is not
// enabled on this node.
//
// The certInfo parameter should contain the cluster TLS keypair and optional
// CA certificate.
//
// The latency parameter is a coarse grain measure of how fast/reliable network
// links are. This is used to tweak the various timeouts parameters of the raft
// algorithm. See the raft.Config structure for more details. A value of 1.0
// means use the default values from hashicorp's raft package. Values closer to
// 0 reduce the values of the various timeouts (useful when running unit tests
// in-memory).
func newRaft(database *db.Node, cert *shared.CertInfo, latency float64) (*raftInstance, error) {
	if latency <= 0 {
		return nil, fmt.Errorf("latency should be positive")
	}

	// Figure out if we actually need to act as dqlite node.
	var info *db.RaftNode
	err := database.Transaction(func(tx *db.NodeTx) error {
		var err error
		info, err = node.DetermineRaftNode(tx)
		return err
	})
	if err != nil {
		return nil, err
	}

	// If we're not part of the dqlite cluster, there's nothing to do.
	if info == nil {
		return nil, nil
	}
	logger.Debug("Start database node", log15.Ctx{"id": info.ID, "address": info.Address})

	// Initialize a raft instance along with all needed dependencies.
	instance, err := raftInstanceInit(database, info, cert, latency)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// A LXD-specific wrapper around raft.Raft, which also holds a reference to its
// network transport and dqlite FSM.
type raftInstance struct {
	info              dqlite.ServerInfo
	layer             *rafthttp.Layer       // HTTP-based raft transport layer
	handler           http.HandlerFunc      // Handles join/leave/connect requests
	membershipChanger func(*raft.Raft)      // Forwards to raft membership requests from handler
	logs              *raftboltdb.BoltStore // Raft logs store, needs to be closed upon shutdown
	fsm               raft.FSM              // The dqlite FSM linked to the raft instance
	raft              *raft.Raft            // The actual raft instance
}

// Create a new raftFactory, instantiating all needed raft dependencies.
func raftInstanceInit(
	db *db.Node, node *db.RaftNode, cert *shared.CertInfo, latency float64) (*raftInstance, error) {

	addr := node.Address
	if addr == "" {
		// This is a standalone node not exposed to the network.
		addr = "1"
	}

	// Rename legacy data directory if needed.
	dir := filepath.Join(db.Dir(), "global")
	legacyDir := filepath.Join(db.Dir(), "..", "raft")
	if shared.PathExists(legacyDir) {
		if shared.PathExists(dir) {
			return nil, fmt.Errorf("both legacy and new global database directories exist")
		}
		logger.Info("Renaming global database directory from raft/ to database/global/")
		err := os.Rename(legacyDir, dir)
		if err != nil {
			return nil, errors.Wrap(err, "failed to rename legacy global database directory")
		}
	}

	// Data directory
	if !shared.PathExists(dir) {
		err := os.Mkdir(dir, 0750)
		if err != nil {
			return nil, err
		}
	}

	instance := &raftInstance{}
	instance.info.ID = uint64(node.ID)
	instance.info.Address = addr

	return instance, nil
}

var serial = 99

// FSM returns the dqlite FSM associated with the raft instance.
func (i *raftInstance) FSM() raft.FSM {
	return i.fsm
}

// Raft returns the actual underlying raft instance.
func (i *raftInstance) Raft() *raft.Raft {
	return i.raft
}

// Servers returns the servers that are currently part of the cluster.
//
// If this raft instance is not the leader, an error is returned.
func (i *raftInstance) Servers() ([]raft.Server, error) {
	if i.raft.State() != raft.Leader {
		return nil, raft.ErrNotLeader
	}
	future := i.raft.GetConfiguration()
	err := future.Error()
	if err != nil {
		return nil, err
	}
	configuration := future.Configuration()
	return configuration.Servers, nil
}

// HandlerFunc can be used to handle HTTP requests performed against the LXD
// API RaftEndpoint ("/internal/raft"), in order to join/leave/form the raft
// cluster.
//
// If it returns nil, it means that this node is not supposed to expose a raft
// endpoint over the network, because it's running as a non-clustered single
// node.
func (i *raftInstance) HandlerFunc() http.HandlerFunc {
	if i.handler == nil {
		return nil
	}
	return i.handler.ServeHTTP
}

// MembershipChanger returns the underlying rafthttp.Layer, which can be used
// to change the membership of this node in the cluster.
func (i *raftInstance) MembershipChanger() raftmembership.Changer {
	return i.layer
}

// Shutdown raft and any raft-related resource we have instantiated.
func (i *raftInstance) Shutdown() error {
	logger.Debug("Stop raft instance")

	// Invoke raft APIs asynchronously to allow for a timeout.
	timeout := 10 * time.Second

	errCh := make(chan error)
	timer := time.After(timeout)
	go func() {
		errCh <- i.raft.Shutdown().Error()
	}()
	select {
	case err := <-errCh:
		if err != nil {
			return errors.Wrap(err, "failed to shutdown raft")
		}
	case <-timer:
		logger.Debug("Timeout waiting for raft to shutdown")
		return fmt.Errorf("raft did not shutdown within %s", timeout)

	}
	err := i.logs.Close()
	if err != nil {
		return errors.Wrap(err, "failed to close boltdb logs store")
	}
	return nil
}

// Snapshot can be used to manually trigger a RAFT snapshot
func (i *raftInstance) Snapshot() error {
	return i.raft.Snapshot().Error()
}

// Create an in-memory raft transport.
func raftMemoryTransport() raft.Transport {
	_, transport := raft.NewInmemTransport("0")
	return transport
}

// Create a rafthttp.Dial function that connects over TLS using the given
// cluster (and optionally CA) certificate both as client and remote
// certificate.
func raftDial(cert *shared.CertInfo) (rafthttp.Dial, error) {
	config, err := tlsClientConfig(cert)
	if err != nil {
		return nil, err
	}
	dial := rafthttp.NewDialTLS(config)
	return dial, nil
}

// An address provider that looks up server addresses in the raft_nodes table.
type raftAddressProvider struct {
	db *db.Node
}

func (p *raftAddressProvider) ServerAddr(id raft.ServerID) (raft.ServerAddress, error) {
	databaseID, err := strconv.Atoi(string(id))
	if err != nil {
		return "", errors.Wrap(err, "non-numeric server ID")
	}
	var address string
	err = p.db.Transaction(func(tx *db.NodeTx) error {
		var err error
		address, err = tx.RaftNodeAddress(int64(databaseID))
		return err
	})
	if err != nil {
		return "", err
	}
	return raft.ServerAddress(address), nil
}

// Create a base raft configuration tweaked for a network with the given latency measure.
func raftConfig(latency float64) *raft.Config {
	config := raft.DefaultConfig()
	scale := func(duration *time.Duration) {
		*duration = time.Duration((math.Ceil(float64(*duration) * latency)))
	}
	durations := []*time.Duration{
		&config.HeartbeatTimeout,
		&config.ElectionTimeout,
		&config.CommitTimeout,
		&config.LeaderLeaseTimeout,
	}
	for _, duration := range durations {
		scale(duration)
	}

	config.SnapshotThreshold = 1024
	config.TrailingLogs = 512

	return config
}

func raftHandler(info *shared.CertInfo, handler *rafthttp.Handler) http.HandlerFunc {
	if handler == nil {
		return nil
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !tlsCheckCert(r, info) {
			http.Error(w, "403 invalid client certificate", http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r)
	}
}

func raftLogger() hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{
		Name:   "raft",
		Output: &raftLogWriter{},
	})
}

// Implement io.Writer on top of LXD's logging system.
type raftLogWriter struct {
}

func (o *raftLogWriter) Write(line []byte) (n int, err error) {
	// Parse the log level according to hashicorp's raft pkg convetions.
	level := ""
	msg := ""
	x := bytes.IndexByte(line, '[')
	if x >= 0 {
		y := bytes.IndexByte(line[x:], ']')
		if y >= 0 {
			level = string(line[x+1 : x+y])

			// Capitalize the string, to match LXD logging conventions
			first := strings.ToUpper(string(line[x+y+2]))
			rest := string(line[x+y+3 : len(line)-1])
			msg = first + rest
		}
	}

	if level == "" {
		// Ignore log entries that don't stick to the convetion.
		return len(line), nil
	}

	switch level {
	case "DEBUG":
		logger.Debug(msg)
	case "INFO":
		logger.Debug(msg)
	case "WARN":
		logger.Warn(msg)
	default:
		// Ignore any other log level.
	}
	return len(line), nil
}
