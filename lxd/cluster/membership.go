package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"
)

// Bootstrap turns a non-clustered LXD instance into the first (and leader)
// node of a new LXD cluster.
//
// This instance must already have its core.https_address set and be listening
// on the associated network address.
func Bootstrap(state *state.State, gateway *Gateway, name string) error {
	// Check parameters
	if name == "" {
		return fmt.Errorf("node name must not be empty")
	}

	// Sanity check that there's no leftover cluster certificate
	for _, basename := range []string{"cluster.crt", "cluster.key", "cluster.ca"} {
		if shared.PathExists(filepath.Join(state.OS.VarDir, basename)) {
			return fmt.Errorf("inconsistent state: found leftover cluster certificate")
		}
	}

	var address string
	err := state.Node.Transaction(func(tx *db.NodeTx) error {
		// Fetch current network address and raft nodes
		config, err := node.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch node configuration")
		}
		address = config.HTTPSAddress()

		// Make sure node-local database state is in order.
		err = membershipCheckNodeStateForBootstrapOrJoin(tx, address)
		if err != nil {
			return err
		}

		// Add ourselves as first raft node
		err = tx.RaftNodeFirst(address)
		if err != nil {
			return errors.Wrap(err, "failed to insert first raft node")
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Insert ourselves into the nodes table.
	err = state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Make sure cluster database state is in order.
		err := membershipCheckClusterStateForBootstrapOrJoin(tx)
		if err != nil {
			return err
		}

		// Add ourselves to the nodes table.
		_, err = tx.NodeAdd(name, address)
		if err != nil {
			return errors.Wrap(err, "failed to insert cluster node")
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Shutdown the gateway. This will trash any gRPC SQL connection
	// against our in-memory dqlite driver and shutdown the associated raft
	// instance.
	err = gateway.Shutdown()
	if err != nil {
		return errors.Wrap(err, "failed to shutdown gRPC SQL gateway")
	}

	// Re-initialize the gateway. This will create a new raft factory an
	// dqlite driver instance, which will be exposed over gRPC by the
	// gateway handlers.
	err = gateway.init()
	if err != nil {
		return errors.Wrap(err, "failed to re-initialize gRPC SQL gateway")
	}
	err = gateway.waitLeadership()
	if err != nil {
		return err
	}

	// The cluster certificates are symlinks against the regular node
	// certificate.
	for _, ext := range []string{".crt", ".key", ".ca"} {
		if ext == ".ca" && !shared.PathExists(filepath.Join(state.OS.VarDir, "server.ca")) {
			continue
		}
		err := os.Symlink("server"+ext, filepath.Join(state.OS.VarDir, "cluster"+ext))
		if err != nil {
			return errors.Wrap(err, "failed to create cluster cert symlink")
		}
	}

	// Make sure we can actually connect to the cluster database through
	// the network endpoint. This also makes the Go SQL pooling system
	// invalidate the old connection, so new queries will be executed over
	// the new gRPC network connection.
	err = state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.Nodes()
		return err
	})
	if err != nil {
		return errors.Wrap(err, "cluster database initialization failed")
	}

	return nil
}

// Check that node-related preconditions are met for bootstrapping or joining a
// cluster.
func membershipCheckNodeStateForBootstrapOrJoin(tx *db.NodeTx, address string) error {
	nodes, err := tx.RaftNodes()
	if err != nil {
		return errors.Wrap(err, "failed to fetch current raft nodes")
	}

	hasNetworkAddress := address != ""
	hasRaftNodes := len(nodes) > 0

	// Sanity check that we're not in an inconsistent situation, where no
	// network address is set, but still there are entries in the
	// raft_nodes table.
	if !hasNetworkAddress && hasRaftNodes {
		return fmt.Errorf("inconsistent state: found leftover entries in raft_nodes")
	}

	if !hasNetworkAddress {
		return fmt.Errorf("no core.https_address config is set on this node")
	}
	if hasRaftNodes {
		return fmt.Errorf("the node is already part of a cluster")
	}

	return nil
}

// Check that cluster-related preconditions are met for bootstrapping or
// joining a cluster.
func membershipCheckClusterStateForBootstrapOrJoin(tx *db.ClusterTx) error {
	nodes, err := tx.Nodes()
	if err != nil {
		return errors.Wrap(err, "failed to fetch current cluster nodes")
	}
	if len(nodes) > 0 {
		return fmt.Errorf("inconsistent state: found leftover entries in nodes")
	}
	return nil
}
