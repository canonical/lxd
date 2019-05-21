package main

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/CanonicalLtd/go-dqlite/recover"
	"github.com/CanonicalLtd/go-dqlite/recover/dump"
	"github.com/hashicorp/raft"
	"github.com/hashicorp/raft-boltdb"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Return a new dump command.
func newDump() *cobra.Command {
	var head int
	var tail int
	var replay string

	dump := &cobra.Command{
		Use:   "dump [path to raft data dir]",
		Short: "Dump or replay the content of a dqlite raft store.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]

			logs, snaps, err := recover.Open(dir)
			if err != nil {
				return err
			}

			options := make([]dump.Option, 0)

			if head != 0 {
				options = append(options, dump.Head(head))
			}
			if tail != 0 {
				options = append(options, dump.Tail(tail))
			}
			if replay != "" {
				options = append(options, dump.Replay(replay))
			}

			if err := dump.Dump(logs, snaps, os.Stdout, options...); err != nil {
				return err
			}

			return nil
		},
	}

	flags := dump.Flags()
	flags.IntVarP(&head, "head", "H", 0, "limit the dump to the first N log commands")
	flags.IntVarP(&tail, "tail", "t", 0, "limit the dump to the last N log commands")
	flags.StringVarP(&replay, "replay", "r", "", "replay the logs to the given database dir")

	return dump
}

func dumpOpen(dir string) (raft.LogStore, raft.SnapshotStore, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, nil, errors.Wrap(err, "invalid raft data dir")
	}

	logs, err := raftboltdb.NewBoltStore(filepath.Join(dir, "logs.db"))
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to open boltdb file")
	}

	snaps, err := raft.NewFileSnapshotStore(dir, 1, ioutil.Discard)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to open snapshot store")
	}

	return logs, snaps, nil
}
