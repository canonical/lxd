package schema

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

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

// NewFromMap creates a new schema Schema with the updates specified in the
// given map. The keys of the map are schema versions that when upgraded will
// trigger the associated Update value. It's required that the minimum key in
// the map is 1, and if key N is present then N-1 is present too, with N>1
// (i.e. there are no missing versions).
//
// NOTE: the regular New() constructor would be formally enough, but for extra
//       clarity we also support a map that indicates the version explicitely,
//       see also PR #3704.
func NewFromMap(versionsToUpdates map[int]Update) *Schema {
	// Collect all version keys.
	versions := []int{}
	for version := range versionsToUpdates {
		versions = append(versions, version)
	}

	// Sort the versions,
	sort.Sort(sort.IntSlice(versions))

	// Build the updates slice.
	updates := []Update{}
	for i, version := range versions {
		// Assert that we start from 1 and there are no gaps.
		if version != i+1 {
			panic(fmt.Sprintf("updates map misses version %d", i+1))
		}

		updates = append(updates, versionsToUpdates[version])
	}

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

// Dump returns a text of SQL commands that can be used to create this schema
// from scratch in one go, without going thorugh individual patches
// (essentially flattening them).
//
// It requires that all patches in this schema have been applied, otherwise an
// error will be returned.
func (s *Schema) Dump(db *sql.DB) (string, error) {
	var statements []string
	err := query.Transaction(db, func(tx *sql.Tx) error {
		err := checkAllUpdatesAreApplied(tx, s.updates)
		if err != nil {
			return err
		}
		statements, err = selectTablesSQL(tx)
		return err
	})
	if err != nil {
		return "", err
	}
	for i, statement := range statements {
		statements[i] = formatSQL(statement)
	}

	// Add a statement for inserting the current schema version row.
	statements = append(
		statements,
		fmt.Sprintf(`
INSERT INTO schema (version, updated_at) VALUES (%d, strftime("%%s"))
`, len(s.updates)))
	return strings.Join(statements, ";\n"), nil
}

// Ensure that the schema exists.
func ensureSchemaTableExists(tx *sql.Tx) error {
	exists, err := doesSchemaTableExist(tx)
	if err != nil {
		return fmt.Errorf("failed to check if schema table is there: %v", err)
	}
	if !exists {
		err := createSchemaTable(tx)
		if err != nil {
			return fmt.Errorf("failed to create schema table: %v", err)
		}
	}
	return nil
}

// Apply any pending update that was not yet applied.
func ensureUpdatesAreApplied(tx *sql.Tx, updates []Update, hook Hook) error {
	versions, err := selectSchemaVersions(tx)
	if err != nil {
		return fmt.Errorf("failed to fetch update versions: %v", err)
	}

	current := 0
	if len(versions) > 0 {
		err = checkSchemaVersionsHaveNoHoles(versions)
		if err != nil {
			return err
		}
		current = versions[len(versions)-1] // Highest recorded version
	}
	if current > len(updates) {
		return fmt.Errorf(
			"schema version '%d' is more recent than expected '%d'",
			current, len(updates))
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
				return fmt.Errorf("failed to execute hook (version %d): %v", current, err)
			}
		}
		err := update(tx)
		if err != nil {
			return fmt.Errorf("failed to apply update %d: %v", current, err)
		}
		current++

		err = insertSchemaVersion(tx, current)
		if err != nil {
			return fmt.Errorf("failed to insert version %d", current)
		}
	}

	return nil
}

// Check that the given list of update version numbers doesn't have "holes",
// that is each version equal the preceeding version plus 1.
func checkSchemaVersionsHaveNoHoles(versions []int) error {
	// Sanity check that there are no "holes" in the recorded
	// versions.
	for i := range versions[:len(versions)-1] {
		if versions[i+1] != versions[i]+1 {
			return fmt.Errorf(
				"missing updates: %d -> %d", versions[i], versions[i+1])
		}
	}
	return nil
}

// Check that all the given updates are applied.
func checkAllUpdatesAreApplied(tx *sql.Tx, updates []Update) error {
	versions, err := selectSchemaVersions(tx)
	if err != nil {
		return fmt.Errorf("failed to fetch update versions: %v", err)
	}

	if len(versions) == 0 {
		return fmt.Errorf("expected schema table to contain at least one row")
	}

	err = checkSchemaVersionsHaveNoHoles(versions)
	if err != nil {
		return err
	}

	current := versions[len(versions)-1]
	if current != len(updates) {
		return fmt.Errorf("update level is %d, expected %d", current, len(updates))
	}
	return nil
}

// Format the given SQL statement in a human-readable way.
//
// In particular make sure that each column definition in a CREATE TABLE clause
// is in its own row, since SQLite dumps occasionally stuff more than one
// column in the same line.
func formatSQL(statement string) string {
	lines := strings.Split(statement, "\n")
	for i, line := range lines {
		if strings.Contains(line, "UNIQUE") {
			// Let UNIQUE(x, y) constraints alone.
			continue
		}
		lines[i] = strings.Replace(line, ", ", ",\n    ", -1)
	}
	return strings.Join(lines, "\n")
}
