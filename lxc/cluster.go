package main

import (
	"fmt"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type clusterCmd struct {
	force bool
}

func (c *clusterCmd) usage() string {
	return i18n.G(
		`Usage: lxc cluster <subcommand> [options]

Manage cluster nodes.

*Cluster nodes*
lxc cluster remove <node> [--force]
    Remove a node from the cluster.`)
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

	if args[0] == "remove" {
		return c.doClusterNodeRemove(conf, args)
	}

	return nil
}

func (c *clusterCmd) doClusterNodeRemove(conf *config.Config, args []string) error {
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
