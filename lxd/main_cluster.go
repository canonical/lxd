package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type cmdCluster struct {
	global *cmdGlobal
}

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
	recover := cmdClusterRecoverFromQuorumLoss{global: c.global}
	cmd.AddCommand(recover.Command())

	return cmd
}

type cmdClusterListDatabase struct {
	global *cmdGlobal
}

func (c *cmdClusterListDatabase) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "list-database"
	cmd.Aliases = []string{"ls"}
	cmd.Short = "Print the addresses of the cluster members serving the database"

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterListDatabase) Run(cmd *cobra.Command, args []string) error {
	os := sys.DefaultOS()

	db, _, err := db.OpenNode(filepath.Join(os.VarDir, "database"), nil, nil)
	if err != nil {
		return errors.Wrapf(err, "Failed to open local database")
	}

	addresses, err := cluster.ListDatabaseNodes(db)
	if err != nil {
		return errors.Wrapf(err, "Failed to get database nodes")
	}

	columns := []string{"Address"}
	data := make([][]string, len(addresses))
	for i, address := range addresses {
		data[i] = []string{address}
	}
	utils.RenderTable(utils.TableFormatTable, columns, data, nil)

	return nil
}

type cmdClusterRecoverFromQuorumLoss struct {
	global             *cmdGlobal
	flagNonInteractive bool
}

func (c *cmdClusterRecoverFromQuorumLoss) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "recover-from-quorum-loss"
	cmd.Aliases = []string{"ls"}
	cmd.Short = "Recover a LXD instance whose cluster has lost quorum"

	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagNonInteractive, "quiet", "q", false, "Don't require user confirmation")

	return cmd
}

func (c *cmdClusterRecoverFromQuorumLoss) Run(cmd *cobra.Command, args []string) error {
	// Make sure that the daemon is not running.
	_, err := lxd.ConnectLXDUnix("", nil)
	if err == nil {
		return fmt.Errorf("The LXD daemon is running, please stop it first.")
	}

	// Prompt for confiromation unless --quiet was passed.
	if !c.flagNonInteractive {
		err := c.promptConfirmation()
		if err != nil {
			return err
		}
	}

	os := sys.DefaultOS()

	db, _, err := db.OpenNode(filepath.Join(os.VarDir, "database"), nil, nil)
	if err != nil {
		return errors.Wrapf(err, "Failed to open local database")
	}

	return cluster.Recover(db)
}

func (c *cmdClusterRecoverFromQuorumLoss) promptConfirmation() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(`You should run this command only if you are *absolutely* certain that this is
the only database node left in your cluster AND that other database nodes will
never come back (i.e. their LXD daemon won't ever be started again).

This will make this LXD instance the only member of the cluster, and it won't
be possible to perform operations on former cluster members anymore.

However all information about former cluster members will be preserved in the
database, so you can possibly inspect it for further recovery.

You'll be able to permanently delete from the database all information about
former cluster members by running "lxc cluster remove <member-name> --force".

See https://lxd.readthedocs.io/en/latest/clustering/#disaster-recovery for more
info.

Do you want to proceed? (yes/no): `)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !shared.StringInSlice(strings.ToLower(input), []string{"yes"}) {
		return fmt.Errorf("Recover operation aborted")
	}
	return nil
}
