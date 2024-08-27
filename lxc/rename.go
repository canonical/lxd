package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdRename struct {
	global *cmdGlobal
}

func (c *cmdRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<instance>[/<snapshot>] <instance>[/<snapshot>]"))
	cmd.Short = i18n.G("Rename instances and snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename instances and snapshots`))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdRename) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Check the remotes
	sourceRemote, _, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	destRemote, _, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	if sourceRemote != destRemote {
		// We just do renames
		if strings.Contains(args[1], ":") {
			return errors.New(i18n.G("Can't specify a different remote for rename"))
		}

		// Don't require the remote to be passed as both source and target
		args[1] = fmt.Sprintf("%s:%s", sourceRemote, args[1])
	}

	// Call move
	move := cmdMove{global: c.global}
	return move.run(cmd, args)
}
