package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/grant-he/lxd/shared"
	cli "github.com/grant-he/lxd/shared/cmd"
	"github.com/grant-he/lxd/shared/i18n"
)

type cmdManpage struct {
	global *cmdGlobal
}

func (c *cmdManpage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("manpage", i18n.G("<target>"))
	cmd.Short = i18n.G("Generate manpages for all commands")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Generate manpages for all commands`))
	cmd.Hidden = true

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdManpage) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Generate the manpages
	header := &doc.GenManHeader{
		Title:   i18n.G("LXD - Command line client"),
		Section: "1",
	}

	opts := doc.GenManTreeOptions{
		Header:           header,
		Path:             shared.HostPathFollow(args[0]),
		CommandSeparator: ".",
	}

	doc.GenManTreeFromOpts(c.global.cmd, opts)

	return nil
}
