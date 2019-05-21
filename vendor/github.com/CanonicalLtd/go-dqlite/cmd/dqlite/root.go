package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Return a new root command.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "dqlite",
		Short: "Distributed SQLite for Go applications",
		Long: `Replicate a SQLite database across a cluster, using the Raft algorithm.

Complete documentation is available at https://github.com/CanonicalLtd/go-dqlite`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
	root.AddCommand(newDump())
	root.AddCommand(newDelete())
	root.AddCommand(newBench())

	return root
}
