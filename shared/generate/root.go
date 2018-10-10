package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Return a new root command.
func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lxd-generate",
		Short: "Code generation tool for LXD development",
		Long: `This is the entry point for all "go:generate" directives
used in LXD's source code.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
	}
	cmd.AddCommand(newDb())

	return cmd
}
