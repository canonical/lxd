package connection

import (
	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/pkg/errors"
)

// Snapshot returns a snapshot of the SQLite database with the given path.
//
// The snapshot is comprised of two byte slices, one with the content of the
// database and one is the content of the WAL file.
func Snapshot(vfs *bindings.Vfs, path string) ([]byte, []byte, error) {
	database, err := vfs.ReadFile(path)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get database file content")
	}

	wal, err := vfs.ReadFile(path + "-wal")
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get WAL file content")
	}

	return database, wal, nil
}

// Restore the given database and WAL backups, writing them at the given
// database path.
func Restore(vfs *bindings.Vfs, path string, database, wal []byte) error {
	if err := vfs.WriteFile(path, database); err != nil {
		return errors.Wrap(err, "failed to restore database file")
	}

	if err := vfs.WriteFile(path+"-wal", wal); err != nil {
		return errors.Wrap(err, "failed to restore WAL file")
	}

	return nil
}
