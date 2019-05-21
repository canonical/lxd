package delete

import (
	"fmt"

	"github.com/CanonicalLtd/go-dqlite/internal/store"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// Delete the given log entries from the given store.
func Delete(logs raft.LogStore, index uint64) error {
	r, err := store.DefaultRange(logs)
	if err != nil {
		return errors.Wrap(err, "failed to get current log store range")
	}

	found := false

	store.Iterate(logs, r, func(i uint64, log *raft.Log) error {
		if i == index {
			found = true
		}
		return nil
	})

	if !found {
		return fmt.Errorf("log %d not found", index)
	}

	if err := logs.DeleteRange(index, r.Last); err != nil {
		return errors.Wrapf(err, "failed to delete range %d -> %d", index, r.Last)
	}

	return nil
}
