package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
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

	err := membershipCheckNoLeftoverClusterCert(state.OS.VarDir)
	if err != nil {
		return err
	}

	var address string
	err = state.Node.Transaction(func(tx *db.NodeTx) error {
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

// Accept a new node and add it to the cluster.
//
// This instance must already be clustered.
//
// Return an updated list raft database nodes (possibly including the newly
// accepted node).
func Accept(state *state.State, name, address string, schema, api int) ([]db.RaftNode, error) {
	// Check parameters
	if name == "" {
		return nil, fmt.Errorf("node name must not be empty")
	}
	if address == "" {
		return nil, fmt.Errorf("node address must not be empty")
	}

	// Insert the new node into the nodes table.
	err := state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		// Check that the node can be accepted with these parameters.
		err := membershipCheckClusterStateForAccept(tx, name, address, schema, api)
		if err != nil {
			return err
		}
		// Add the new node
		_, err = tx.NodeAdd(name, address)
		if err != nil {
			return errors.Wrap(err, "failed to insert first raft node")
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Possibly insert the new node into the raft_nodes table (if we have
	// less than 3 database nodes).
	var nodes []db.RaftNode
	err = state.Node.Transaction(func(tx *db.NodeTx) error {
		var err error
		nodes, err = tx.RaftNodes()
		if err != nil {
			return errors.Wrap(err, "failed to fetch current raft nodes")
		}
		if len(nodes) >= membershipMaxRaftNodes {
			return nil
		}
		id, err := tx.RaftNodeAdd(address)
		if err != nil {
			return err
		}
		nodes = append(nodes, db.RaftNode{ID: id, Address: address})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodes, nil
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

// Check that cluster-related preconditions are met for accepting a new node.
func membershipCheckClusterStateForAccept(tx *db.ClusterTx, name string, address string, schema int, api int) error {
	nodes, err := tx.Nodes()
	if err != nil {
		return errors.Wrap(err, "failed to fetch current cluster nodes")
	}
	if len(nodes) == 0 {
		return fmt.Errorf("clustering not enabled")
	}

	for _, node := range nodes {
		if node.Name == name {
			return fmt.Errorf("cluster already has node with name %s", name)
		}
		if node.Address == address {
			return fmt.Errorf("cluster already has node with address %s", address)
		}
		if node.Schema != schema {
			return fmt.Errorf("schema version mismatch: cluster has %d", node.Schema)
		}
		if node.APIExtensions != api {
			return fmt.Errorf("API version mismatch: cluster has %d", node.APIExtensions)
		}
	}

	return nil
}

// Check that there is no left-over cluster certificate in the LXD var dir of
// this node.
func membershipCheckNoLeftoverClusterCert(dir string) error {
	// Sanity check that there's no leftover cluster certificate
	for _, basename := range []string{"cluster.crt", "cluster.key", "cluster.ca"} {
		if shared.PathExists(filepath.Join(dir, basename)) {
			return fmt.Errorf("inconsistent state: found leftover cluster certificate")
		}
	}
	return nil
}

// SchemaVersion holds the version of the cluster database schema.
var SchemaVersion = cluster.SchemaVersion

// We currently aim at having 3 nodes part of the raft dqlite cluster.
//
// TODO: this number should probably be configurable.
const membershipMaxRaftNodes = 3
