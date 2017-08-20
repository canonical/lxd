package schema

import (
	"database/sql"
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/query"
)

// Schema captures the schema of a database in terms of a series of ordered
// updates.
type Schema struct {
	updates []Update // Ordered series of updates making up the schema
	hook    Hook     // Optional hook to execute whenever a update gets applied
}

// Update applies a specific schema change to a database, and returns an error
// if anything goes wrong.
type Update func(*sql.Tx) error

// Hook is a callback that gets fired when a update gets applied.
type Hook func(int, *sql.Tx) error

// New creates a new schema Schema with the given updates.
func New(updates []Update) *Schema {
	return &Schema{
		updates: updates,
	}
}

// Empty creates a new schema with no updates.
func Empty() *Schema {
	return New([]Update{})
}

// Add a new update to the schema. It will be appended at the end of the
// existing series.
func (s *Schema) Add(update Update) {
	s.updates = append(s.updates, update)
}

// Hook instructs the schema to invoke the given function whenever a update is
// about to be applied. The function gets passed the update version number and
// the running transaction, and if it returns an error it will cause the schema
// transaction to be rolled back. Any previously installed hook will be
// replaced.
func (s *Schema) Hook(hook Hook) {
	s.hook = hook
}

// Ensure makes sure that the actual schema in the given database matches the
// one defined by our updates.
//
// All updates are applied transactionally. In case any error occurs the
// transaction will be rolled back and the database will remain unchanged.
//
// A update will be applied only if it hasn't been before (currently applied
// updates are tracked in the a 'shema' table, which gets automatically
// created).
func (s *Schema) Ensure(db *sql.DB) error {
	return query.Transaction(db, func(tx *sql.Tx) error {
		err := ensureSchemaTableExists(tx)
		if err != nil {
			return err
		}

		err = ensureUpdatesAreApplied(tx, s.updates, s.hook)
		if err != nil {
			return err
		}

		return nil
	})
}

// Ensure that the schema exists.
func ensureSchemaTableExists(tx *sql.Tx) error {
	exists, err := doesSchemaTableExist(tx)
	if err != nil {
		return errors.Wrap(err, "failed to check if schema table is there")
	}
	if !exists {
		err := createSchemaTable(tx)
		if err != nil {
			return errors.Wrap(err, "failed to create schema table")
		}
	}
	return nil
}

// Apply any pending update that was not yet applied.
func ensureUpdatesAreApplied(tx *sql.Tx, updates []Update, hook Hook) error {
	current := 0 // Current update level in the database

	versions, err := selectSchemaVersions(tx)
	if err != nil {
		return errors.Wrap(err, "failed to fetch update versions")
	}
	if len(versions) > 1 {
		return fmt.Errorf(
			"schema table contains %d rows, expected at most one", len(versions))
	}

	// If this is a fresh database insert a row with this schema's update
	// level, otherwise update the existing row (it's okay to do this
	// before actually running the updates since the transaction will be
	// rolled back in case of errors).
	if len(versions) == 0 {
		err := insertSchemaVersion(tx, len(updates))
		if err != nil {
			return errors.Wrap(
				err,
				fmt.Sprintf("failed to insert version %d", len(updates)))
		}
	} else {
		current = versions[0]
		if current > len(updates) {
			return fmt.Errorf(
				"schema version '%d' is more recent than expected '%d'",
				current, len(updates))
		}
		err := updateSchemaVersion(tx, current, len(updates))
		if err != nil {
			return errors.Wrap(
				err,
				fmt.Sprintf("failed to update version %d", current))
		}
	}

	// If there are no updates, there's nothing to do.
	if len(updates) == 0 {
		return nil
	}

	// Apply missing updates.
	for _, update := range updates[current:] {
		if hook != nil {
			err := hook(current, tx)
			if err != nil {
				return errors.Wrap(
					err,
					fmt.Sprintf("failed to execute hook (version %d)", current))
			}
		}
		err := update(tx)
		if err != nil {
			return errors.Wrap(
				err,
				fmt.Sprintf("failed to apply update %d", current))
		}
		current++
	}

	return nil
}
