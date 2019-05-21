package store

import (
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// DefaultRange returns a Range spanning all available indexes.
func DefaultRange(logs raft.LogStore) (*Range, error) {
	first, err := logs.FirstIndex()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get first index")
	}
	last, err := logs.LastIndex()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get last index")
	}

	return &Range{First: first, Last: last}, nil
}

// HeadRange returns a range that includes only the first n entries.
func HeadRange(logs raft.LogStore, n int) (*Range, error) {
	first, err := logs.FirstIndex()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get first index")
	}
	last, err := logs.LastIndex()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get last index")
	}

	if first+uint64(n) < last {
		last = first + uint64(n)
	}

	return &Range{First: first, Last: last}, nil
}

// TailRange returns a range that includes only the last n entries.
func TailRange(logs raft.LogStore, n int) (*Range, error) {
	last, err := logs.LastIndex()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get last index")
	}

	first := uint64(0)
	if last > uint64(n) {
		first = last - uint64(n)
	}

	return &Range{First: first, Last: last}, nil
}

// Range contains the first and last index of a dump.
type Range struct {
	First uint64
	Last  uint64
}
