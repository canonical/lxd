package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/generate/db"
)

// Return a new db command.
func newDB() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db [sub-command]",
		Short: "Database-related code generation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
	}

	schema := &cobra.Command{
		Use:   "schema",
		Short: "Generate database schema by applying updates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return db.UpdateSchema()
		},
	}

	cmd.AddCommand(schema)

	return cmd
}
