package query_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db/query"
)

func TestSelectConfig(t *testing.T) {
	tx := newTxForConfig(t)
	values, err := query.SelectConfig(context.Background(), tx, "config", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"foo": "x", "bar": "zz"}, values)
}

func TestSelectConfig_WithFilters(t *testing.T) {
	tx := newTxForConfig(t)
	values, err := query.SelectConfig(context.Background(), tx, "config", "key=?", "bar")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"bar": "zz"}, values)
}

// New keys are added to the table.
func TestUpdateConfig_NewKeys(t *testing.T) {
	tx := newTxForConfig(t)

	values := map[string]string{"foo": "y"}
	err := query.UpdateServerConfig(tx, values)
	require.NoError(t, err)

	values, err = query.SelectConfig(context.Background(), tx, "config", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"foo": "y", "bar": "zz"}, values)
}

// Unset keys are deleted from the table.
func TestDeleteConfig_Delete(t *testing.T) {
	tx := newTxForConfig(t)
	values := map[string]string{"foo": ""}

	err := query.UpdateServerConfig(tx, values)

	require.NoError(t, err)
	values, err = query.SelectConfig(context.Background(), tx, "config", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"bar": "zz"}, values)
}

// Return a new transaction against an in-memory SQLite database with a single
// test table populated with a few rows.
func newTxForConfig(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE config (key TEXT NOT NULL, value TEXT)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO config VALUES ('foo', 'x'), ('bar', 'zz')")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}

type entityConfigStoreSuite struct {
	suite.Suite
	runTx            func(func(ctx context.Context, tx *sql.Tx) error)
	projectConfig    func() *query.EntityConfigStore
	replicatorConfig func() *query.EntityConfigStore
}

func TestEntityConfigStore(t *testing.T) {
	suite.Run(t, new(entityConfigStoreSuite))
}

func (s *entityConfigStoreSuite) SetupSuite() {
	db, err := sql.Open("sqlite3", ":memory:")
	s.Require().NoError(err)

	_, err = db.ExecContext(s.T().Context(), "PRAGMA foreign_keys = ON;")
	s.Require().NoError(err)

	s.runTx = func(f func(ctx context.Context, tx *sql.Tx) error) {
		tx, err := db.Begin()
		s.Require().NoError(err)

		err = f(s.T().Context(), tx)
		s.Require().NoError(err)
		err = tx.Commit()
		s.Require().NoError(err)
	}

	s.projectConfig = func() *query.EntityConfigStore {
		return &query.EntityConfigStore{
			EntityTable:               "projects",
			ConfigTable:               "projects_config",
			ConfigTableEntityIDColumn: "project_id",
		}
	}

	s.replicatorConfig = func() *query.EntityConfigStore {
		return &query.EntityConfigStore{
			EntityTable:               "replicators",
			ConfigTable:               "replicators_config",
			ConfigTableEntityIDColumn: "replicator_id",
		}
	}
}

func (s *entityConfigStoreSuite) SetupTest() {
	s.runTx(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
CREATE TABLE "projects" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    UNIQUE (name)
);
CREATE TABLE "projects_config" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES "projects" (id) ON DELETE CASCADE,
    UNIQUE (project_id, key)
);
CREATE TABLE replicators (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	name TEXT NOT NULL,
	project_id INTEGER NOT NULL,
	UNIQUE(project_id, name),
	FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE TABLE replicators_config (
	replicator_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	FOREIGN KEY (replicator_id) REFERENCES replicators (id) ON DELETE CASCADE,
	PRIMARY KEY (replicator_id,
    key)
) WITHOUT ROWID;

INSERT INTO projects (name) VALUES ('p1'), ('p2'), ('p3');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'user.foo', 'bar'), (1, 'user.fizz', 'buzz'), (1, 'user.ibble', 'dibble');
INSERT INTO projects_config (project_id, key, value) VALUES (2, 'user.foo', 'bar');
INSERT INTO replicators (name, project_id) VALUES ('r1', 1), ('r2', 1), ('r1', 2);
INSERT INTO replicators_config (replicator_id, key, value) VALUES (1, 'user.foo', 'bar'), (1, 'user.fizz', 'buzz'), (1, 'user.ibble', 'dibble');
INSERT INTO replicators_config (replicator_id, key, value) VALUES (3, 'user.foo', 'bar');
`)
		if err != nil {
			return err
		}

		return nil
	})
}

func (s *entityConfigStoreSuite) TearDownTest() {
	s.runTx(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
DROP TABLE replicators_config;
DROP TABLE replicators;
DROP TABLE projects_config;
DROP TABLE projects;
`)
		return err
	})
}

func (s *entityConfigStoreSuite) TestGet() {
	s.runTx(func(ctx context.Context, tx *sql.Tx) error {
		projectConfig := s.projectConfig()

		// Expect to get project1 config and no error.
		project1Config, err := projectConfig.GetByEntityID(ctx, tx, 1)
		s.Require().NoError(err)
		s.Require().Equal(map[string]string{
			"user.foo":   "bar",
			"user.fizz":  "buzz",
			"user.ibble": "dibble",
		}, project1Config)

		// Expect to get project2 config and no error.
		project2Config, err := projectConfig.GetByEntityID(ctx, tx, 2)
		s.Require().NoError(err)
		s.Require().Equal(map[string]string{
			"user.foo": "bar",
		}, project2Config)

		// Expect an empty map because project3 has no config but does exist.
		project3Config, err := projectConfig.GetByEntityID(ctx, tx, 3)
		s.Require().NoError(err)
		s.Require().Equal(map[string]string{}, project3Config)

		// Expect an error because there is no fourth project
		_, err1 := projectConfig.GetByEntityID(ctx, tx, 4)
		s.Require().Error(err1)

		// Expect the same error because there is no fourth project
		_, err2 := projectConfig.GetByEntityIDs(ctx, tx, 1, 2, 3, 4)
		s.Require().Error(err2)
		s.Require().Equal(err1, err2)

		// Expect GetAll to return the same results and no map entry for fourth project (which doesn't exist).
		projectConfigs, err := projectConfig.GetAll(ctx, tx)
		s.Require().NoError(err)
		s.Require().Equal(project1Config, projectConfigs[1])
		s.Require().Equal(project2Config, projectConfigs[2])
		s.Require().Equal(project3Config, projectConfigs[3])
		_, ok := projectConfigs[4]
		s.Require().False(ok)

		// Get config for p1 and p2. Expect no map entry for p3 because we didn't select for it.
		projectConfigs, err = projectConfig.Select(ctx, tx, "WHERE projects.name IN (?, ?)", "p1", "p2")
		s.Require().NoError(err)
		s.Require().Equal(projectConfigs[1], project1Config)
		s.Require().Equal(projectConfigs[2], project2Config)
		_, ok = projectConfigs[3]
		s.Require().False(ok)

		// Test that joining replicator config to projects works.
		replicatorConfig := s.replicatorConfig()
		replicatorConfigs, err := replicatorConfig.Select(ctx, tx, "JOIN projects ON replicators.project_id = projects.id WHERE projects.name = ?", "p1")
		s.Require().NoError(err)
		s.Require().Equal(map[string]string{
			"user.foo":   "bar",
			"user.fizz":  "buzz",
			"user.ibble": "dibble",
		}, replicatorConfigs[1])

		// The second replicator is in p1 but has no config, so expect an empty map.
		s.Require().Equal(map[string]string{}, replicatorConfigs[2])

		// The third replicator is not present because it is not in project p1.
		_, ok = replicatorConfigs[3]
		s.Require().False(ok)

		return nil
	})
}

func (s *entityConfigStoreSuite) TestSet() {
	projectConfig := s.projectConfig()
	newConfig := map[string]string{
		"user.foo":   "bar",
		"user.fizz":  "buzz",
		"user.ibble": "dibble",
		"user.bacon": "eggs",
	}

	s.runTx(func(ctx context.Context, tx *sql.Tx) error {
		err := projectConfig.Set(ctx, tx, 1, newConfig)
		s.Require().NoError(err)
		config, err := s.projectConfig().GetByEntityID(ctx, tx, 1)
		s.Require().NoError(err)
		s.Require().Equal(newConfig, config)
		return nil
	})

	s.runTx(func(ctx context.Context, tx *sql.Tx) error {
		err := projectConfig.Set(ctx, tx, 4, newConfig)
		s.Require().Error(err)
		return nil
	})
}
