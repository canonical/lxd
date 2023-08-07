package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdManpage struct {
	global *cmdGlobal
}

// Command returns a Cobra command for generating manpages for all commands.
func (c *cmdManpage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "manpage <target>"
	cmd.Short = "Generate manpages for all commands"
	cmd.Long = cli.FormatSection("Description",
		`Generate manpages for all commands`)
	cmd.Hidden = true

	cmd.RunE = c.Run

	return cmd
}

// Run generates manpages for all commands based on the specified arguments.
func (c *cmdManpage) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) != 1 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Generate the manpages
	header := &doc.GenManHeader{
		Title:   "LXD - Container server",
		Section: "1",
	}

	opts := doc.GenManTreeOptions{
		Header:           header,
		Path:             args[0],
		CommandSeparator: ".",
	}

	_ = doc.GenManTreeFromOpts(c.global.cmd, opts)

	return nil
}
