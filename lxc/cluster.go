package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/olekukonko/tablewriter"
)

type clusterCmd struct {
	force bool
}

func (c *clusterCmd) usage() string {
	return i18n.G(
		`Usage: lxc cluster <subcommand> [options]

Manage cluster nodes.

lxc cluster list [<remote>:]
    List all nodes in the cluster.

lxc cluster delete [<remote>:]<node> [--force]
    Delete a node from the cluster.`)
}

func (c *clusterCmd) flags() {
	gnuflag.BoolVar(&c.force, "force", false, i18n.G("Force removing a node, even if degraded"))
}

func (c *clusterCmd) showByDefault() bool {
	return true
}

func (c *clusterCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	if args[0] == "list" {
		return c.doClusterList(conf, args)
	}

	if args[0] == "delete" {
		return c.doClusterNodeDelete(conf, args)
	}

	return nil
}

func (c *clusterCmd) doClusterNodeDelete(conf *config.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	// [[lxc cluster]] remove production:bionic-1
	remote, name, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	op, err := client.LeaveCluster(name, c.force)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return nil
	}

	fmt.Printf(i18n.G("Node %s removed")+"\n", name)
	return nil
}

func (c *clusterCmd) doClusterList(conf *config.Config, args []string) error {
	remote := conf.DefaultRemote

	if len(args) > 1 {
		var err error
		remote, _, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	nodes, err := client.GetNodes()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, node := range nodes {
		database := "NO"
		if node.Database {
			database = "YES"
		}
		data = append(data, []string{node.Name, node.URL, database, node.State})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("URL"),
		i18n.G("DATABASE"),
		i18n.G("STATE")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}
