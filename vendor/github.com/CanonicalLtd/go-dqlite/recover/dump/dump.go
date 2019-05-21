package dump

import (
	"fmt"
	"io"
	"log"

	"github.com/CanonicalLtd/go-dqlite/internal/protocol"
	"github.com/CanonicalLtd/go-dqlite/internal/store"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// Dump the content of a dqlite store.
func Dump(logs raft.LogStore, snaps raft.SnapshotStore, out io.Writer, options ...Option) error {
	o := defaultOptions()
	for _, option := range options {
		err := option(logs, o)
		if err != nil {
			return err
		}
	}

	if o.r == nil {
		r, err := store.DefaultRange(logs)
		if err != nil {
			return err
		}
		o.r = r
	}

	if o.dir != "" {
		// Replay the logs.
		if err := store.Replay(logs, snaps, o.r, o.dir); err != nil {
			return errors.Wrap(err, "failed to replay logs")
		}
		return nil
	}

	logger := log.New(out, "", 0)

	h := func(index uint64, log *raft.Log) error {
		cmd, err := protocol.UnmarshalCommand(log.Data)
		if err != nil {
			return errors.Wrapf(err, "index %d: failed to unmarshal command", index)
		}

		logger.Printf(dumpCommand(index, cmd))
		return nil
	}

	return store.Iterate(logs, o.r, h)
}

func dumpCommand(index uint64, cmd *protocol.Command) string {
	var name string
	var dump string
	switch payload := cmd.Payload.(type) {
	case *protocol.Command_Open:
		name = "open"
		dump = dumpOpen(payload.Open)
	case *protocol.Command_Begin:
		name = "begin"
		dump = dumpBegin(payload.Begin)
	case *protocol.Command_Frames:
		name = "frames"
		dump = dumpFrames(payload.Frames)
	case *protocol.Command_Undo:
		name = "undo"
		dump = dumpUndo(payload.Undo)
	case *protocol.Command_End:
		name = "end"
		dump = dumpEnd(payload.End)
	case *protocol.Command_Checkpoint:
		name = "checkpoint"
		dump = dumpCheckpoint(payload.Checkpoint)
	}

	return fmt.Sprintf("index %6d: %-8s: %s", index, name, dump)
}

func dumpOpen(params *protocol.Open) string {
	return fmt.Sprintf("name: %8s", params.Name)
}

func dumpBegin(params *protocol.Begin) string {
	return fmt.Sprintf("name: %8s txn: %6d", params.Name, params.Txid)
}

func dumpFrames(params *protocol.Frames) string {
	return fmt.Sprintf("name: %8s txn: %6d commit: %d pages: %2d",
		params.Filename, params.Txid, params.IsCommit, len(params.PageNumbers))
}

func dumpUndo(params *protocol.Undo) string {
	return fmt.Sprintf("txn: %6d", params.Txid)
}

func dumpEnd(params *protocol.End) string {
	return fmt.Sprintf("txn: %6d", params.Txid)
}

func dumpCheckpoint(params *protocol.Checkpoint) string {
	return fmt.Sprintf("file: %-8s", params.Name)
}
