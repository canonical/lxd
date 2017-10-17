package db_test

import (
	"database/sql"
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPreClusteringData(t *testing.T) {
	tx := newPreClusteringTx(t)

	dump, err := db.LoadPreClusteringData(tx)
	require.NoError(t, err)

	assert.Equal(t, []string{"id", "key", "value"}, dump.Schema["config"])
	assert.Len(t, dump.Data["config"], 1)
	rows := []interface{}{int64(1), []byte("core.https_address"), []byte("1.2.3.4:666")}
	assert.Equal(t, rows, dump.Data["config"][0])
}

func TestImportPreClusteringData(t *testing.T) {
	tx := newPreClusteringTx(t)

	dump, err := db.LoadPreClusteringData(tx)
	require.NoError(t, err)

	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err = cluster.ImportPreClusteringData(dump)
	require.NoError(t, err)

	cluster.Transaction(func(tx *db.ClusterTx) error {
		config, err := tx.Config()
		require.NoError(t, err)
		values := map[string]string{"core.https_address": "1.2.3.4:666"}
		assert.Equal(t, values, config)
		return nil
	})
}

// Return a sql.Tx against a memory database populated with pre-clustering
// data.
func newPreClusteringTx(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	stmts := []string{
		preClusteringNodeSchema,
		"INSERT INTO config VALUES(1, 'core.https_address', '1.2.3.4:666')",
	}
	for _, stmt := range stmts {
		_, err := tx.Exec(stmt)
		require.NoError(t, err)
	}
	return tx
}

const preClusteringNodeSchema = `
CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE "containers" (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
CREATE TABLE containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
CREATE TABLE containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id) ON DELETE CASCADE,
    UNIQUE (container_device_id, key)
);
CREATE TABLE containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE images (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    cached INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    auto_update INTEGER NOT NULL DEFAULT 0,
    UNIQUE (fingerprint)
);
CREATE TABLE "images_aliases" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE images_source (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias VARCHAR(255) NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (network_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE
);
CREATE TABLE patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);
CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255),
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id) ON DELETE CASCADE
);
CREATE TABLE raft_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    address TEXT NOT NULL,
    UNIQUE (address)
);
CREATE TABLE storage_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    driver VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE storage_pools_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    UNIQUE (storage_pool_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);

INSERT INTO schema (version, updated_at) VALUES (37, strftime("%s"))
`
