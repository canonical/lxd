package recover

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/hashicorp/raft"
	"github.com/hashicorp/raft-boltdb"
	"github.com/pkg/errors"
)

// Open a raft store in the given dir.
func Open(dir string) (raft.LogStore, raft.SnapshotStore, error) {
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
