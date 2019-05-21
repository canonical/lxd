package main

import (
	"strconv"

	"github.com/CanonicalLtd/go-dqlite/recover"
	"github.com/CanonicalLtd/go-dqlite/recover/delete"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Return a new delete command.
func newDelete() *cobra.Command {
	delete := &cobra.Command{
		Use:   "delete [path to raft data dir] [index]",
		Short: "Delete all raft logs after the given index (included).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]

			index, err := strconv.Atoi(args[1])
			if err != nil {
				return errors.Wrapf(err, "invalid index: %s", args[1])
			}

			logs, _, err := recover.Open(dir)
			if err != nil {
				return err
			}

			if err := delete.Delete(logs, uint64(index)); err != nil {
				return err
			}

			return nil
		},
	}

	return delete
}
