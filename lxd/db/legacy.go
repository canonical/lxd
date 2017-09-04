package db

import (
	"database/sql"

	"github.com/lxc/lxd/lxd/db/schema"
)

// LegacyPatch is a "database" update that performs non-database work. They
// are needed for historical reasons, since there was a time were db updates
// could do non-db work and depend on functionality external to the db
// package. See UpdatesApplyAll below.
type LegacyPatch struct {
	NeedsDB bool                // Whether the patch does any DB-related work
	Hook    func(*sql.DB) error // The actual patch logic
}

func legacyPatchHook(db *sql.DB, patches map[int]*LegacyPatch) schema.Hook {
	return func(version int, tx *sql.Tx) error {
		patch, ok := patches[version]
		if !ok {
			return nil
		}
		// FIXME We need to commit the transaction before the
		// hook and then open it again afterwards because this
		// legacy patch pokes with the database and would fail
		// with a lock error otherwise.
		if patch.NeedsDB {
			_, err := tx.Exec("COMMIT")
			if err != nil {
				return err
			}
		}
		err := patch.Hook(db)
		if err != nil {
			return err
		}
		if patch.NeedsDB {
			_, err = tx.Exec("BEGIN")
		}
		return err
	}
}
