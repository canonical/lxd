package schema_test

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/db/schema"
)

// Create a new Schema by specifying an explicit map from versions to Update
// functions.
func TestNewFromMap(t *testing.T) {
	db := newDB(t)
	schema := schema.NewFromMap(map[int]schema.Update{
		1: updateCreateTable,
		2: updateInsertValue,
	})
	assert.NoError(t, schema.Ensure(db))
}

// Panic if there are missing versions in the map.
func TestNewFromMap_MissingVersions(t *testing.T) {
	assert.Panics(t, func() {
		schema.NewFromMap(map[int]schema.Update{
			1: updateCreateTable,
			3: updateInsertValue,
		})
	}, "updates map misses version 2")
}

// If the database schema version is more recent than our update series, an
// error is returned.
func TestSchemaEnsure_VersionMoreRecentThanExpected(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateNoop)
	assert.NoError(t, schema.Ensure(db))

	schema, _ = newSchemaAndDB(t)
	err := schema.Ensure(db)
	assert.NotNil(t, err)
	assert.EqualError(t, err, "schema version '1' is more recent than expected '0'")
}

// If the database schema contains "holes" in the applied versions, an error is
// returned.
func TestSchemaEnsure_MissingVersion(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateNoop)
	assert.NoError(t, schema.Ensure(db))

	_, err := db.Exec(`INSERT INTO schema (version, updated_at) VALUES (3, strftime("%s"))`)

	schema.Add(updateNoop)
	schema.Add(updateNoop)

	err = schema.Ensure(db)
	assert.NotNil(t, err)
	assert.EqualError(t, err, "missing updates: 1 -> 3")
}

// If the schema has no update, the schema table gets created and has no version.
func TestSchemaEnsure_ZeroUpdates(t *testing.T) {
	schema, db := newSchemaAndDB(t)

	err := schema.Ensure(db)
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	versions, err := query.SelectIntegers(tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{}, versions)
}

// If the schema has updates and no one was applied yet, all of them get
// applied.
func TestSchemaEnsure_ApplyAllUpdates(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateInsertValue)

	err := schema.Ensure(db)
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	// THe update version is recorded.
	versions, err := query.SelectIntegers(tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 2}, versions)

	// The two updates have been applied in order.
	ids, err := query.SelectIntegers(tx, "SELECT id FROM test")
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, ids)
}

// If the schema schema has been created using a dump, the schema table will
// contain just one row with the update level associated with the dump. It's
// possible to apply further updates from there, and only these new ones will
// be inserted in the schema table.
func TestSchemaEnsure_ApplyAfterInitialDumpCreation(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateAddColumn)
	assert.NoError(t, schema.Ensure(db))

	dump, err := schema.Dump(db)
	assert.NoError(t, err)

	_, db = newSchemaAndDB(t)
	_, err = db.Exec(dump)
	assert.NoError(t, err)

	schema.Add(updateNoop)
	assert.NoError(t, schema.Ensure(db))

	tx, err := db.Begin()
	assert.NoError(t, err)

	// Only updates starting from the initial dump are recorded.
	versions, err := query.SelectIntegers(tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{2, 3}, versions)
}

// If the schema has updates and part of them were already applied, only the
// missing ones are applied.
func TestSchemaEnsure_OnlyApplyMissing(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	assert.NoError(t, schema.Ensure(db))

	schema.Add(updateInsertValue)
	assert.NoError(t, schema.Ensure(db))

	tx, err := db.Begin()
	assert.NoError(t, err)

	// All update versions are recorded.
	versions, err := query.SelectIntegers(tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 2}, versions)

	// The two updates have been applied in order.
	ids, err := query.SelectIntegers(tx, "SELECT id FROM test")
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, ids)
}

// If a update fails, an error is returned, and all previous changes are rolled
// back.
func TestSchemaEnsure_FailingUpdate(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateBoom)
	err := schema.Ensure(db)
	assert.EqualError(t, err, "failed to apply update 1: boom")

	tx, err := db.Begin()
	assert.NoError(t, err)

	// Not update was applied.
	tables, err := query.SelectStrings(tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	assert.NoError(t, err)
	assert.NotContains(t, tables, "schema")
	assert.NotContains(t, tables, "test")
}

// If a hook fails, an error is returned, and all previous changes are rolled
// back.
func TestSchemaEnsure_FailingHook(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Hook(func(int, *sql.Tx) error { return fmt.Errorf("boom") })
	err := schema.Ensure(db)
	assert.EqualError(t, err, "failed to execute hook (version 0): boom")

	tx, err := db.Begin()
	assert.NoError(t, err)

	// Not update was applied.
	tables, err := query.SelectStrings(tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	assert.NoError(t, err)
	assert.NotContains(t, tables, "schema")
	assert.NotContains(t, tables, "test")
}

// The SQL text returns by Dump() can be used to create the schema from
// scratch, without applying each individual update.
func TestSchemaDump(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateAddColumn)
	assert.NoError(t, schema.Ensure(db))

	dump, err := schema.Dump(db)
	assert.NoError(t, err)

	_, db = newSchemaAndDB(t)
	_, err = db.Exec(dump)
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	// All update versions are in place.
	versions, err := query.SelectIntegers(tx, "SELECT version FROM schema")
	assert.NoError(t, err)
	assert.Equal(t, []int{2}, versions)

	// Both the table added by the first update and the extra column added
	// by the second update are there.
	_, err = tx.Exec("SELECT id, name FROM test")
	assert.NoError(t, err)
}

// If not all updates are applied, Dump() returns an error.
func TestSchemaDump_MissingUpdatees(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	assert.NoError(t, schema.Ensure(db))
	schema.Add(updateAddColumn)

	_, err := schema.Dump(db)
	assert.EqualError(t, err, "update level is 1, expected 2")
}

// Return a new in-memory SQLite database.
func newDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)
	return db
}

// Return both an empty schema and a test database.
func newSchemaAndDB(t *testing.T) (*schema.Schema, *sql.DB) {
	return schema.Empty(), newDB(t)
}

// An update that does nothing.
func updateNoop(*sql.Tx) error {
	return nil
}

// An update that creates a test table.
func updateCreateTable(tx *sql.Tx) error {
	_, err := tx.Exec("CREATE TABLE test (id INTEGER)")
	return err
}

// An update that inserts a value into the test table.
func updateInsertValue(tx *sql.Tx) error {
	_, err := tx.Exec("INSERT INTO test VALUES (1)")
	return err
}

// An update that adds a column to the test tabble.
func updateAddColumn(tx *sql.Tx) error {
	_, err := tx.Exec("ALTER TABLE test ADD COLUMN name TEXT")
	return err
}

// An update that unconditionally fails with an error.
func updateBoom(tx *sql.Tx) error {
	return fmt.Errorf("boom")
}
