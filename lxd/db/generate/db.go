package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd/db/generate/db"
	"github.com/lxc/lxd/lxd/db/generate/file"
	"github.com/lxc/lxd/lxd/db/generate/lex"
)

// Return a new db command.
func newDb() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db [sub-command]",
		Short: "Database-related code generation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
	}

	cmd.AddCommand(newDbSchema())
	cmd.AddCommand(newDbMapper())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { cmd.Usage() }
	return cmd
}

func newDbSchema() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Generate database schema by applying updates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return db.UpdateSchema()
		},
	}

	return cmd
}

func newDbMapper() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mapper [sub-command]",
		Short: "Generate code mapping database rows to Go structs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
	}

	cmd.AddCommand(newDbMapperReset())
	cmd.AddCommand(newDbMapperStmt())
	cmd.AddCommand(newDbMapperMethod())

	return cmd
}

func newDbMapperReset() *cobra.Command {
	var target string

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset target source file and its interface file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return file.Reset(target, db.Imports)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&target, "target", "t", "-", "target source file to generate")

	return cmd
}

func newDbMapperStmt() *cobra.Command {
	var target string
	var database string
	var pkg string
	var entity string

	cmd := &cobra.Command{
		Use:   "stmt [kind]",
		Args:  cobra.MinimumNArgs(1),
		Short: "Generate a particular database statement.",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]

			if entity == "" {
				return fmt.Errorf("No database entity given")
			}

			config, err := parseParams(args[1:])
			if err != nil {
				return err
			}

			stmt, err := db.NewStmt(database, pkg, entity, kind, config)
			if err != nil {
				return err
			}

			return file.Append(target, stmt)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&target, "target", "t", "-", "target source file to generate")
	flags.StringVarP(&database, "database", "d", "cluster", "target database")
	flags.StringVarP(&pkg, "package", "p", "api", "Go package where the entity struct is declared")
	flags.StringVarP(&entity, "entity", "e", "", "database entity to generate the statement for")

	return cmd
}

func newDbMapperMethod() *cobra.Command {
	var target string
	var database string
	var pkg string
	var entity string
	var transaction string

	cmd := &cobra.Command{
		Use:   "method [kind] [param1=value1 ... paramN=valueN]",
		Args:  cobra.MinimumNArgs(1),
		Short: "Generate a particular transaction method and interface signature.",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]

			if entity == "" {
				return fmt.Errorf("No database entity given")
			}

			config, err := parseParams(args[1:])
			if err != nil {
				return err
			}

			// FIXME: Transition every entity to using V2, and then replace V1 with it.
			var method file.Snippet
			if config["version"] != "" {
				method, err = db.NewMethodV2(database, pkg, entity, kind, config, transaction)
				if err != nil {
					return err
				}

			} else {
				method, err = db.NewMethod(database, pkg, entity, kind, config)
				if err != nil {
					return err
				}
			}

			return file.Append(target, method)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&target, "target", "t", "-", "target source file to generate")
	flags.StringVarP(&database, "database", "d", "cluster", "target database")
	flags.StringVarP(&pkg, "package", "p", "api", "Go package where the entity struct is declared")
	flags.StringVarP(&entity, "entity", "e", "", "database entity to generate the method for")
	flags.StringVarP(&transaction, "tx", "", "", "custom transaction argument in lieu of receiver")

	return cmd
}

func parseParams(args []string) (map[string]string, error) {
	config := map[string]string{}
	for _, arg := range args {
		key, value, err := lex.KeyValue(arg)
		if err != nil {
			return nil, fmt.Errorf("Invalid config parameter: %w", err)
		}
		config[key] = value
	}

	return config, nil
}
