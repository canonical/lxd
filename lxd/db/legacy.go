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
	Hook func(*sql.Tx) error // The actual patch logic
}

func legacyPatchHook(patches map[int]*LegacyPatch) schema.Hook {
	return func(version int, tx *sql.Tx) error {
		patch, ok := patches[version]
		if !ok {
			return nil
		}

		err := patch.Hook(tx)
		if err != nil {
			return err
		}

		return err
	}
}
