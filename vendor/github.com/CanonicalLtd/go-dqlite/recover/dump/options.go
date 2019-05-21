package dump

import (
	"github.com/CanonicalLtd/go-dqlite/internal/store"
	"github.com/hashicorp/raft"
)

// Option to tweak the output of dump
type Option func(logs raft.LogStore, o *options) error

// Tail limits the output to the last N entries.
func Tail(n int) Option {
	return func(logs raft.LogStore, o *options) error {
		r, err := store.TailRange(logs, n)
		if err != nil {
			return err
		}

		o.r = r

		return nil
	}
}

// Head limits the output to the first N entries.
func Head(n int) Option {
	return func(logs raft.LogStore, o *options) error {
		r, err := store.HeadRange(logs, n)
		if err != nil {
			return err
		}

		o.r = r

		return nil
	}
}

// Replay the commands generating SQLite databases in the given dir.
func Replay(dir string) Option {
	return func(logs raft.LogStore, o *options) error {
		o.dir = dir
		return nil
	}
}

// Hold options for the Dump function.
type options struct {
	r   *store.Range
	dir string
}

// Return the default Dump options.
func defaultOptions() *options {
	return &options{}
}
