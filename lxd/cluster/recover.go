package cluster

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	dqlite "github.com/canonical/go-dqlite"
	"github.com/canonical/go-dqlite/client"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// RecoveryTarballName is the filename used for recovery tarballs.
const RecoveryTarballName = "lxd_recovery_db.tar.gz"

const raftNodesFilename = "raft_nodes.yaml"

const errPatchExists = "Custom patches should not be applied during recovery"

// ListDatabaseNodes returns a list of database node names.
func ListDatabaseNodes(database *db.Node) ([]string, error) {
	nodes := []db.RaftNode{}
	err := database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to list database nodes: %w", err)
	}

	addresses := make([]string, 0)
	for _, node := range nodes {
		if node.Role != db.RaftVoter {
			continue
		}

		addresses = append(addresses, node.Address)
	}

	return addresses, nil
}

// Return the entry in the raft_nodes table that corresponds to the local
// `core.https_address`.
// Returns err if no raft_node exists for the local node.
func localRaftNode(database *db.Node) (*db.RaftNode, error) {
	var info *db.RaftNode
	err := database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		info, err = node.DetermineRaftNode(ctx, tx)

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to determine cluster member raft role: %w", err)
	}

	// If we're not a database node, return an error.
	if info == nil {
		return nil, fmt.Errorf("This cluster member has no raft role")
	}

	return info, nil
}

// Recover rebuilds the dqlite raft configuration leaving only the current
// member in the cluster. Use `Reconfigure` if more members should remain in
// the raft configuration.
func Recover(database *db.Node) error {
	info, err := localRaftNode(database)
	if err != nil {
		return err
	}

	// If this is a standalone node not exposed to the network, return an
	// error.
	if info.Address == "" {
		return fmt.Errorf("This LXD instance is not clustered")
	}

	dir := filepath.Join(database.Dir(), "global")

	cluster := []dqlite.NodeInfo{
		{
			ID:      uint64(info.ID),
			Address: info.Address,
			Role:    client.Voter,
		},
	}

	err = dqlite.ReconfigureMembershipExt(dir, cluster)
	if err != nil {
		return fmt.Errorf("Failed to recover database state: %w", err)
	}

	// Update the list of raft nodes.
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodes := []db.RaftNode{
			{
				NodeInfo: client.NodeInfo{
					ID:      info.ID,
					Address: info.Address,
				},
				Name: info.Name,
			},
		}

		return tx.ReplaceRaftNodes(nodes)
	})
	if err != nil {
		return fmt.Errorf("Failed to update database nodes: %w", err)
	}

	return nil
}

// updateLocalAddress updates the cluster.https_address for this node.
func updateLocalAddress(database *db.Node, address string) error {
	err := database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		newConfig := map[string]any{"cluster.https_address": address}
		_, err = config.Patch(newConfig)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to update node configuration: %w", err)
	}

	return nil
}

// Create a patch file for the nodes table in the global database; this updates
// the addresses of all cluster members from the list of RaftNode in case they
// were changed during cluster recovery.
func writeGlobalNodesPatch(database *db.Node, nodes []db.RaftNode) error {
	// No patch needed if there are no nodes
	if len(nodes) < 1 {
		return nil
	}

	reverter := revert.New()
	defer reverter.Fail()

	filePath := filepath.Join(database.Dir(), "patch.global.sql")

	_, err := os.Stat(filePath)
	if err == nil {
		return fmt.Errorf("Found %s: %s", filePath, errPatchExists)
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()
	reverter.Add(func() { _ = os.Remove(filePath) })

	for _, node := range nodes {
		_, err = fmt.Fprintf(file, "UPDATE nodes SET address = %q WHERE id = %d;\n", node.Address, node.ID)
		if err != nil {
			return err
		}
	}

	reverter.Success()

	return nil
}

// Reconfigure replaces the entire cluster configuration.
// Addresses and node roles may be updated. Node IDs are read-only.
// Returns the path to the new database state (recovery tarball).
func Reconfigure(database *db.Node, raftNodes []db.RaftNode) (string, error) {
	info, err := localRaftNode(database)
	if err != nil {
		return "", err
	}

	localAddress := info.Address

	nodes := make([]client.NodeInfo, 0, len(raftNodes))
	for _, raftNode := range raftNodes {
		nodes = append(nodes, raftNode.NodeInfo)

		// Get the new address for this node.
		if raftNode.ID == info.ID {
			localAddress = raftNode.Address
		}
	}

	patchPath := path.Join(database.Dir(), "patch.global.sql")
	_, err = os.Stat(patchPath)
	if err == nil {
		return "", fmt.Errorf("Found %s: %s", patchPath, errPatchExists)
	}

	// Update cluster.https_address if changed.
	if localAddress != info.Address {
		err := updateLocalAddress(database, localAddress)
		if err != nil {
			return "", err
		}
	}

	dir := filepath.Join(database.Dir(), "global")
	// Replace cluster configuration in dqlite.
	err = dqlite.ReconfigureMembershipExt(dir, nodes)
	if err != nil {
		return "", fmt.Errorf("Failed to recover database state: %w", err)
	}

	// Replace cluster configuration in local raft_nodes database.
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceRaftNodes(raftNodes)
	})
	if err != nil {
		return "", err
	}

	tarballPath, err := writeRecoveryTarball(database.Dir(), raftNodes)
	if err != nil {
		return "", fmt.Errorf("Failed to create recovery tarball: copy db manually; %w", err)
	}

	err = writeGlobalNodesPatch(database, raftNodes)
	if err != nil {
		return "", fmt.Errorf("Failed to create global db patch for cluster recover: %w", err)
	}

	return tarballPath, nil
}

// Create a tarball of the global database dir to be copied to all other
// remaining cluster members.
func writeRecoveryTarball(databaseDir string, raftNodes []db.RaftNode) (string, error) {
	reverter := revert.New()
	defer reverter.Fail()

	tarballPath := filepath.Join(databaseDir, RecoveryTarballName)

	tarball, err := os.Create(tarballPath)
	if err != nil {
		return "", err
	}

	reverter.Add(func() { _ = os.Remove(tarballPath) })

	gzWriter := gzip.NewWriter(tarball)
	tarWriter := tar.NewWriter(gzWriter)

	globalDBDirFS := os.DirFS(filepath.Join(databaseDir, "global"))

	err = tarWriter.AddFS(globalDBDirFS)
	if err != nil {
		return "", err
	}

	raftNodesYaml, err := yaml.Marshal(raftNodes)
	if err != nil {
		return "", err
	}

	raftNodesHeader := tar.Header{
		Typeflag: tar.TypeReg,
		Name:     raftNodesFilename,
		Size:     int64(len(raftNodesYaml)),
		Mode:     0o644,
		Uid:      0,
		Gid:      0,
		Format:   tar.FormatPAX,
	}

	err = tarWriter.WriteHeader(&raftNodesHeader)
	if err != nil {
		return "", err
	}

	written, err := tarWriter.Write(raftNodesYaml)
	if err != nil {
		return "", err
	}

	if written != len(raftNodesYaml) {
		return "", fmt.Errorf("Wrote %d bytes but expected to write %d", written, len(raftNodesYaml))
	}

	err = tarWriter.Close()
	if err != nil {
		return "", err
	}

	err = gzWriter.Close()
	if err != nil {
		return "", err
	}

	err = tarball.Close()
	if err != nil {
		return "", err
	}

	reverter.Success()

	return tarballPath, nil
}

// DatabaseReplaceFromTarball unpacks the tarball found at `tarballPath`, replaces
// the global database, updates the local database with any changed addresses,
// and writes a global patch file to update the global database with any changed
// addresses.
func DatabaseReplaceFromTarball(tarballPath string, database *db.Node) error {
	globalDBDir := path.Join(database.Dir(), "global")
	unpackDir := filepath.Join(database.Dir(), "global.recover")

	logger.Warn("Recovery tarball located; attempting DB recovery", logger.Ctx{"tarball": tarballPath})

	err := unpackTarball(tarballPath, unpackDir)
	if err != nil {
		return err
	}

	raftNodesYamlPath := path.Join(unpackDir, raftNodesFilename)
	raftNodesYaml, err := os.ReadFile(raftNodesYamlPath)
	if err != nil {
		return err
	}

	var incomingRaftNodes []db.RaftNode
	err = yaml.Unmarshal(raftNodesYaml, &incomingRaftNodes)
	if err != nil {
		return fmt.Errorf("Invalid %q", raftNodesYamlPath)
	}

	var localRaftNodes []db.RaftNode
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) (err error) {
		localRaftNodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return err
	}

	for _, localNode := range localRaftNodes {
		foundLocal := false
		for _, incomingNode := range incomingRaftNodes {
			foundLocal = localNode.ID == incomingNode.ID &&
				localNode.Name == incomingNode.Name

			if foundLocal {
				break
			}
		}

		// The incoming tarball should contain a node with the same dqlite ID as
		// the local LXD server; we shouldn't unpack a recovery tarball from a
		// different cluster.
		if !foundLocal {
			return fmt.Errorf("Missing cluster member %q in incoming recovery tarball", localNode.Name)
		}
	}

	// Update our core.https_address if it has changed
	localRaftNode, err := localRaftNode(database)
	if err != nil {
		return err
	}

	for _, incomingNode := range incomingRaftNodes {
		if incomingNode.ID == localRaftNode.ID {
			if incomingNode.Address != localRaftNode.Address {
				err = updateLocalAddress(database, incomingNode.Address)
				if err != nil {
					return err
				}
			}

			break
		}
	}

	// Replace cluster configuration in local raft_nodes database.
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceRaftNodes(incomingRaftNodes)
	})
	if err != nil {
		return err
	}

	err = writeGlobalNodesPatch(database, incomingRaftNodes)
	if err != nil {
		return fmt.Errorf("Failed to create global db patch for cluster recover: %w", err)
	}

	// Now that we're as sure as we can be that the recovery DB is valid, we can
	// replace the existing DB
	err = os.RemoveAll(globalDBDir)
	if err != nil {
		return err
	}

	err = os.Rename(unpackDir, globalDBDir)
	if err != nil {
		return err
	}

	// Prevent the database being restored again after subsequent restarts
	err = os.Remove(tarballPath)
	if err != nil {
		return err
	}

	return nil
}

// RemoveRaftNode removes a raft node from the raft configuration.
func RemoveRaftNode(gateway *Gateway, address string) error {
	nodes, err := gateway.currentRaftNodes()
	if err != nil {
		return fmt.Errorf("Failed to get current raft nodes: %w", err)
	}

	var id uint64
	for _, node := range nodes {
		if node.Address == address {
			id = node.ID
			break
		}
	}
	if id == 0 {
		return fmt.Errorf("No raft node with address %q", address)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := client.FindLeader(
		ctx, gateway.NodeStore(),
		client.WithDialFunc(gateway.raftDial()),
		client.WithLogFunc(DqliteLog),
	)
	if err != nil {
		return fmt.Errorf("Failed to connect to cluster leader: %w", err)
	}

	defer func() { _ = client.Close() }()
	err = client.Remove(ctx, id)
	if err != nil {
		return fmt.Errorf("Failed to remove node: %w", err)
	}

	return nil
}

func unpackTarball(tarballPath string, destRoot string) error {
	reverter := revert.New()
	defer reverter.Fail()

	tarball, err := os.Open(tarballPath)
	if err != nil {
		return err
	}

	gzReader, err := gzip.NewReader(tarball)
	if err != nil {
		return err
	}

	tarReader := tar.NewReader(gzReader)

	err = os.MkdirAll(destRoot, 0o755)
	if err != nil {
		return err
	}

	reverter.Add(func() { _ = os.RemoveAll(destRoot) })

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		// CWE-22
		if strings.Contains(header.Name, "..") {
			return fmt.Errorf("Invalid sequence `..` in recovery tarball entry %q", header.Name)
		}

		filepath := path.Join(destRoot, header.Name)

		switch header.Typeflag {
		case tar.TypeReg:
			file, err := os.Create(filepath)
			if err != nil {
				return err
			}

			countWritten, err := io.Copy(file, tarReader)
			if countWritten != header.Size {
				return fmt.Errorf("Mismatched written (%d) and size (%d) for entry %q in %q", countWritten, header.Size, header.Name, tarballPath)
			} else if err != nil {
				return err
			}

		case tar.TypeDir:
			err = os.MkdirAll(filepath, fs.FileMode(header.Mode&int64(fs.ModePerm)))
			if err != nil {
				return err
			}
		}
	}

	reverter.Success()

	return nil
}
