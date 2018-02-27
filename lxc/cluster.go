package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
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

lxc cluster show [<remote>:]<node>
    Show details of a node.

lxc cluster rename [<remote>:]<node> <new-name>
    Rename a cluster node.

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

	switch args[0] {
	case "list":
		return c.doClusterList(conf, args)
	case "show":
		return c.doClusterNodeShow(conf, args)
	case "rename":
		return c.doClusterNodeRename(conf, args)
	case "delete":
		return c.doClusterNodeDelete(conf, args)
	default:
		return errArgs
	}
}

func (c *clusterCmd) doClusterNodeShow(conf *config.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	node, _, err := client.GetClusterMember(name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *clusterCmd) doClusterNodeRename(conf *config.Config, args []string) error {
	if len(args) < 3 {
		return errArgs
	}
	newName := args[2]

	remote, name, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	err = client.RenameClusterMember(name, api.ClusterMemberPost{ServerName: newName})
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Node %s renamed to %s")+"\n", name, newName)
	return nil
}

func (c *clusterCmd) doClusterNodeDelete(conf *config.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	err = client.DeleteClusterMember(name, c.force)
	if err != nil {
		return err
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

	nodes, err := client.GetClusterMembers()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, node := range nodes {
		database := "NO"
		if node.Database {
			database = "YES"
		}
		line := []string{node.ServerName, node.URL, database, strings.ToUpper(node.Status), node.Message}
		data = append(data, line)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("URL"),
		i18n.G("DATABASE"),
		i18n.G("STATE"),
		i18n.G("MESSAGE"),
	})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}
