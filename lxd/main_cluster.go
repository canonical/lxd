package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/go-dqlite/client"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

func promptConfirmation(prompt string, opname string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(prompt + "Do you want to proceed? (yes/no): ")

	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !shared.ValueInSlice(strings.ToLower(input), []string{"yes"}) {
		return fmt.Errorf("%s operation aborted", opname)
	}

	return nil
}

type cmdCluster struct {
	global *cmdGlobal
}

// Command returns a subcommand for administrating a cluster.
func (c *cmdCluster) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "cluster"
	cmd.Short = "Low-level cluster administration commands"
	cmd.Long = `Description:
  Low level administration tools for inspecting and recovering LXD clusters.
`
	// List database nodes
	listDatabase := cmdClusterListDatabase{global: c.global}
	cmd.AddCommand(listDatabase.Command())

	// Recover
	clusterRecover := cmdClusterRecoverFromQuorumLoss{global: c.global}
	cmd.AddCommand(clusterRecover.Command())

	// Remove a raft node.
	removeRaftNode := cmdClusterRemoveRaftNode{global: c.global}
	cmd.AddCommand(removeRaftNode.Command())

	// Edit cluster configuration.
	clusterEdit := cmdClusterEdit{global: c.global}
	cmd.AddCommand(clusterEdit.Command())

	// Show cluster configuration.
	clusterShow := cmdClusterShow{global: c.global}
	cmd.AddCommand(clusterShow.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

const segmentComment = "# Latest dqlite segment ID: %s"

// ClusterMember is a more human-readable representation of the db.RaftNode struct.
type ClusterMember struct {
	ID      uint64 `yaml:"id"`
	Name    string `yaml:"name,omitempty"`
	Address string `yaml:"address"`
	Role    string `yaml:"role"`
}

// ClusterConfig is a representation of the current cluster configuration.
type ClusterConfig struct {
	Members []ClusterMember `yaml:"members"`
}

// ToRaftNode converts a ClusterConfig struct to a RaftNode struct.
func (c ClusterMember) ToRaftNode() (*db.RaftNode, error) {
	node := &db.RaftNode{
		NodeInfo: client.NodeInfo{
			ID:      c.ID,
			Address: c.Address,
		},
		Name: c.Name,
	}

	var role db.RaftRole
	switch c.Role {
	case "voter":
		role = db.RaftVoter
	case "stand-by":
		role = db.RaftStandBy
	case "spare":
		role = db.RaftSpare
	default:
		return nil, fmt.Errorf("unknown raft role: %q", c.Role)
	}

	node.Role = role

	return node, nil
}

const clusterEditPrompt = `You should run this command only if:
 - A quorum of cluster members is permanently lost or their addresses have changed
 - You are *absolutely* sure all LXD daemons are stopped
 - This instance has the most up to date database

See https://documentation.ubuntu.com/lxd/en/latest/howto/cluster_recover/#reconfigure-the-cluster for more info.`

const clusterEditComment = `# Member roles can be modified. Unrecoverable nodes should be given the role "spare".
#
# "voter" - Voting member of the database. A majority of voters is a quorum.
# "stand-by" - Non-voting member of the database; can be promoted to voter.
# "spare" - Not a member of the database.
#
# The edit is aborted if:
# - the number of members changes
# - the name of any member changes
# - the ID of any member changes
# - no changes are made
`

type cmdClusterEdit struct {
	global *cmdGlobal
}

// Command returns a command for reconfiguring a cluster.
func (c *cmdClusterEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "edit"
	cmd.Short = "Edit cluster configuration as YAML"
	cmd.Long = `Description:
	Edit cluster configuration as YAML.`
	cmd.RunE = c.Run

	return cmd
}

// Run executes the command for reconfiguring a cluster.
func (c *cmdClusterEdit) Run(cmd *cobra.Command, args []string) error {
	// Make sure that the daemon is not running.
	_, err := lxd.ConnectLXDUnix("", nil)
	if err == nil {
		return fmt.Errorf("The LXD daemon is running, please stop it first.")
	}

	database, err := db.OpenNode(filepath.Join(sys.DefaultOS().VarDir, "database"), nil)
	if err != nil {
		return err
	}

	var nodes []db.RaftNode
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		clusterAddress := config.ClusterAddress()
		if clusterAddress == "" {
			return fmt.Errorf(`Can't edit cluster configuration as server isn't clustered (missing "cluster.https_address" config)`)
		}

		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return err
	}

	segmentID, err := db.DqliteLatestSegment()
	if err != nil {
		return err
	}

	config := ClusterConfig{Members: []ClusterMember{}}

	for _, node := range nodes {
		member := ClusterMember{ID: node.ID, Name: node.Name, Address: node.Address, Role: node.Role.String()}
		config.Members = append(config.Members, member)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	var content []byte
	if !termios.IsTerminal(unix.Stdin) {
		content, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	} else {
		err = promptConfirmation(clusterEditPrompt, "Cluster edit")
		if err != nil {
			return err
		}

		if len(config.Members) > 0 {
			data = []byte(
				clusterEditComment + "\n\n" +
					fmt.Sprintf(segmentComment, segmentID) + "\n\n" +
					string(data))
		}

		content, err = shared.TextEditor("", data)
		if err != nil {
			return err
		}
	}

	var tarballPath string
	for {
		newConfig := ClusterConfig{}
		err = yaml.Unmarshal(content, &newConfig)
		if err == nil {
			// Convert ClusterConfig back to RaftNodes.
			newNodes := []db.RaftNode{}
			var newNode *db.RaftNode
			for _, node := range newConfig.Members {
				newNode, err = node.ToRaftNode()
				if err != nil {
					break
				}

				newNodes = append(newNodes, *newNode)
			}

			// Ensure new configuration is valid.
			if err == nil {
				err = validateNewConfig(nodes, newNodes)
				if err == nil {
					tarballPath, err = cluster.Reconfigure(database, newNodes)
				}
			}
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Config validation error: %s\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")
			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	fmt.Printf("Cluster changes applied; new database state saved to %s\n\n", tarballPath)
	fmt.Printf("*Before* starting any cluster member, copy %s to %s on all remaining cluster members.\n\n", tarballPath, tarballPath)
	fmt.Printf("LXD will load this file during startup.\n")

	return nil
}

func validateNewConfig(oldNodes []db.RaftNode, newNodes []db.RaftNode) error {
	if len(oldNodes) > len(newNodes) {
		return fmt.Errorf("Removing cluster members is not supported")
	}

	if len(oldNodes) < len(newNodes) {
		return fmt.Errorf("Adding cluster members is not supported")
	}

	numNewVoters := 0
	for i, newNode := range newNodes {
		oldNode := oldNodes[i]

		// IDs should not be reordered among cluster members.
		if oldNode.ID != newNode.ID {
			return fmt.Errorf("Changing cluster member ID is not supported")
		}

		// If the name field could not be populated, just ignore the new value.
		if oldNode.Name != "" && newNode.Name != "" && oldNode.Name != newNode.Name {
			return fmt.Errorf("Changing cluster member name is not supported")
		}

		if oldNode.Role == db.RaftSpare && newNode.Role == db.RaftVoter {
			return fmt.Errorf("A %q cluster member cannot become a %q", db.RaftSpare.String(), db.RaftVoter.String())
		}

		if newNode.Role == db.RaftVoter {
			numNewVoters++
		}
	}

	if numNewVoters < 2 && len(newNodes) > 2 {
		return fmt.Errorf("Number of %q must be 2 or more", db.RaftVoter.String())
	} else if numNewVoters < 1 {
		return fmt.Errorf("At least one member must be a %q", db.RaftVoter.String())
	}

	return nil
}

type cmdClusterShow struct {
	global *cmdGlobal
}

// Command returns a command for showing the current cluster configuration.
func (c *cmdClusterShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "show"
	cmd.Short = "Show cluster configuration as YAML"
	cmd.Long = `Description:
	Show cluster configuration as YAML.`
	cmd.RunE = c.Run

	return cmd
}

// Run executes the command for showing the current cluster configuration.
func (c *cmdClusterShow) Run(cmd *cobra.Command, args []string) error {
	database, err := db.OpenNode(filepath.Join(sys.DefaultOS().VarDir, "database"), nil)
	if err != nil {
		return err
	}

	var nodes []db.RaftNode
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return err
	}

	segmentID, err := db.DqliteLatestSegment()
	if err != nil {
		return err
	}

	config := ClusterConfig{Members: []ClusterMember{}}

	for _, node := range nodes {
		member := ClusterMember{ID: node.ID, Name: node.Name, Address: node.Address, Role: node.Role.String()}
		config.Members = append(config.Members, member)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	if len(config.Members) > 0 {
		fmt.Printf(segmentComment+"\n\n%s", segmentID, data)
	} else {
		fmt.Print(data)
	}

	return nil
}

type cmdClusterListDatabase struct {
	global *cmdGlobal
}

// Command returns a command for showing the database roles of cluster members.
func (c *cmdClusterListDatabase) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "list-database"
	cmd.Aliases = []string{"ls"}
	cmd.Short = "Print the addresses of the cluster members serving the database"

	cmd.RunE = c.Run

	return cmd
}

// Run executes the command for showing the database roles of cluster members.
func (c *cmdClusterListDatabase) Run(cmd *cobra.Command, args []string) error {
	os := sys.DefaultOS()

	db, err := db.OpenNode(filepath.Join(os.VarDir, "database"), nil)
	if err != nil {
		return fmt.Errorf("Failed to open local database: %w", err)
	}

	addresses, err := cluster.ListDatabaseNodes(db)
	if err != nil {
		return fmt.Errorf("Failed to get database nodes: %w", err)
	}

	columns := []string{"Address"}
	data := make([][]string, len(addresses))
	for i, address := range addresses {
		data[i] = []string{address}
	}

	_ = cli.RenderTable(cli.TableFormatTable, columns, data, nil)

	return nil
}

const recoverFromQuorumLossPrompt = `You should run this command only if you are *absolutely* certain that this is
the only database member left in your cluster AND that other database members will
never come back (i.e. their LXD daemon won't ever be started again).

This will make this LXD server the only member of the cluster, and it won't
be possible to perform operations on former cluster members anymore.

However all information about former cluster members will be preserved in the
database, so you can possibly inspect it for further recovery.

You'll be able to permanently delete from the database all information about
former cluster members by running "lxc cluster remove <member-name> --force".

See https://documentation.ubuntu.com/lxd/en/latest/howto/cluster_recover/#recover-from-quorum-loss for more
info.`

type cmdClusterRecoverFromQuorumLoss struct {
	global             *cmdGlobal
	flagNonInteractive bool
}

// Command returns a command for rebuilding a cluster based on the current member.
func (c *cmdClusterRecoverFromQuorumLoss) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "recover-from-quorum-loss"
	cmd.Short = "Recover a LXD instance whose cluster has lost quorum"

	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagNonInteractive, "quiet", "q", false, "Don't require user confirmation")

	return cmd
}

// Run executes the command for rebuilding a cluster based on the current member.
func (c *cmdClusterRecoverFromQuorumLoss) Run(cmd *cobra.Command, args []string) error {
	// Make sure that the daemon is not running.
	_, err := lxd.ConnectLXDUnix("", nil)
	if err == nil {
		return fmt.Errorf("The LXD daemon is running, please stop it first.")
	}

	// Prompt for confirmation unless --quiet was passed.
	if !c.flagNonInteractive {
		err := promptConfirmation(recoverFromQuorumLossPrompt, "Recover")
		if err != nil {
			return err
		}
	}

	os := sys.DefaultOS()

	db, err := db.OpenNode(filepath.Join(os.VarDir, "database"), nil)
	if err != nil {
		return fmt.Errorf("Failed to open local database: %w", err)
	}

	return cluster.Recover(db)
}

const removeRaftNodePrompt = `You should run this command only if you ended up in an
inconsistent state where a cluster member has been uncleanly removed (i.e. it
doesn't show up in "lxc cluster list" but it's still in the raft configuration).`

type cmdClusterRemoveRaftNode struct {
	global             *cmdGlobal
	flagNonInteractive bool
}

// Command returns a command for removing a raft node from the currently running database.
func (c *cmdClusterRemoveRaftNode) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "remove-raft-node <address>"
	cmd.Short = "Remove a raft node from the raft configuration"

	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagNonInteractive, "quiet", "q", false, "Don't require user confirmation")

	return cmd
}

// Run executes the command for removing a raft node from the currently running database.
func (c *cmdClusterRemoveRaftNode) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		_ = cmd.Help()
		return fmt.Errorf("Missing required arguments")
	}

	address := util.CanonicalNetworkAddress(args[0], shared.HTTPSDefaultPort)

	// Prompt for confirmation unless --quiet was passed.
	if !c.flagNonInteractive {
		err := promptConfirmation(removeRaftNodePrompt, "Remove raft node")
		if err != nil {
			return err
		}
	}

	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return fmt.Errorf("Failed to connect to LXD daemon: %w", err)
	}

	endpoint := fmt.Sprintf("/internal/cluster/raft-node/%s", address)
	_, _, err = client.RawQuery("DELETE", endpoint, nil, "")
	if err != nil {
		return err
	}

	return nil
}
