package store

import (
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// Handler handles a single command log yielded by Iterate.
type Handler func(uint64, *raft.Log) error

// Iterate through all command logs in the given store within the given range.
func Iterate(logs raft.LogStore, r *Range, handler Handler) error {
	for index := r.First; index <= r.Last; index++ {
		log := &raft.Log{}
		if err := logs.GetLog(index, log); err != nil {
			return errors.Wrapf(err, "failed to get log %d", index)
		}

		if log.Type != raft.LogCommand {
			continue
		}

		if err := handler(index, log); err != nil {
			return err
		}
	}

	return nil
}
