package cluster

import (
	"database/sql"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/pkg/errors"
)

// Schema for the cluster database.
func Schema() *schema.Schema {
	schema := schema.NewFromMap(updates)
	schema.Fresh(freshSchema)
	return schema
}

// FreshSchema returns the fresh schema definition of the global database.
func FreshSchema() string {
	return freshSchema
}

// SchemaDotGo refreshes the schema.go file in this package, using the updates
// defined here.
func SchemaDotGo() error {
	return schema.DotGo(updates, "schema")
}

// SchemaVersion is the current version of the cluster database schema.
var SchemaVersion = len(updates)

var updates = map[int]schema.Update{
	1:  updateFromV0,
	2:  updateFromV1,
	3:  updateFromV2,
	4:  updateFromV3,
	5:  updateFromV4,
	6:  updateFromV5,
	7:  updateFromV6,
	8:  updateFromV7,
	9:  updateFromV8,
	10: updateFromV9,
	11: updateFromV10,
	12: updateFromV11,
}

func updateFromV11(tx *sql.Tx) error {
	stmts := `
CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    has_images INTEGER NOT NULL default 0,
    has_profiles INTEGER NOT NULL default 0,
    description TEXT,
    UNIQUE (name)
);
INSERT INTO projects (name, description, has_images, has_profiles) VALUES ('default', 'Default LXD project', 1, 1);

ALTER TABLE containers ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE images ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE images_aliases ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE profiles ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;

CREATE TABLE tmp_containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    node_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (name),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE tmp_images (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint TEXT NOT NULL,
    filename TEXT NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    cached INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    auto_update INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER NOT NULL,
    UNIQUE (fingerprint),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE tmp_images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (name),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE tmp_profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (name),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

INSERT INTO tmp_containers SELECT * FROM containers;
INSERT INTO tmp_images SELECT * FROM images;
INSERT INTO tmp_images_aliases SELECT * FROM images_aliases;
INSERT INTO tmp_profiles SELECT * FROM profiles;

DROP TABLE containers;
ALTER TABLE tmp_containers RENAME TO containers;

DROP TABLE images;
ALTER TABLE tmp_images RENAME TO images;

DROP TABLE images_aliases;
ALTER TABLE tmp_images_aliases RENAME TO images_aliases;

DROP TABLE profiles;
ALTER TABLE tmp_profiles RENAME TO profiles;
`
	_, err := tx.Exec(stmts)
	return err
}

func updateFromV10(tx *sql.Tx) error {
	stmt := `
ALTER TABLE storage_volumes ADD COLUMN snapshot INTEGER NOT NULL DEFAULT 0;
UPDATE storage_volumes SET snapshot = 0;
`
	_, err := tx.Exec(stmt)
	return err
}

// Add a new 'type' column to the operations table.
func updateFromV9(tx *sql.Tx) error {
	stmts := `
	ALTER TABLE operations ADD COLUMN type INTEGER NOT NULL DEFAULT 0;
	UPDATE operations SET type = 0;
	`
	_, err := tx.Exec(stmts)
	return err
}

// The lvm.thinpool_name and lvm.vg_name config keys are node-specific and need
// to be linked to nodes.
func updateFromV8(tx *sql.Tx) error {
	// Moved to patchLvmNodeSpecificConfigKeys, since there's no schema
	// change. That makes it easier to backport.
	return nil
}

func updateFromV7(tx *sql.Tx) error {
	stmts := `
CREATE TABLE containers_backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    container_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
`
	_, err := tx.Exec(stmts)
	return err
}

// The zfs.pool_name config key is node-specific, and needs to be linked to
// nodes.
func updateFromV6(tx *sql.Tx) error {
	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(tx, "SELECT id FROM nodes")
	if err != nil {
		return errors.Wrap(err, "failed to get IDs of current nodes")
	}

	// Fetch the IDs of all existing zfs pools.
	poolIDs, err := query.SelectIntegers(tx, `
SELECT id FROM storage_pools WHERE driver='zfs'
`)
	if err != nil {
		return errors.Wrap(err, "failed to get IDs of current zfs pools")
	}

	for _, poolID := range poolIDs {
		// Fetch the config for this zfs pool and check if it has the zfs.pool_name key
		config, err := query.SelectConfig(
			tx, "storage_pools_config", "storage_pool_id=? AND node_id IS NULL", poolID)
		if err != nil {
			return errors.Wrap(err, "failed to fetch of zfs pool config")
		}
		poolName, ok := config["zfs.pool_name"]
		if !ok {
			continue // This zfs storage pool does not have a zfs.pool_name config
		}

		// Delete the current zfs.pool_name key
		_, err = tx.Exec(`
DELETE FROM storage_pools_config WHERE key='zfs.pool_name' AND storage_pool_id=? AND node_id IS NULL
`, poolID)
		if err != nil {
			return errors.Wrap(err, "failed to delete zfs.pool_name config")
		}

		// Add zfs.pool_name config entry for each node
		for _, nodeID := range nodeIDs {
			_, err := tx.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(?, ?, 'zfs.pool_name', ?)
`, poolID, nodeID, poolName)
			if err != nil {
				return errors.Wrap(err, "failed to create zfs.pool_name node config")
			}
		}
	}

	return nil
}

// For ceph volumes, add node-specific rows for all existing nodes, since any
// node is able to access those volumes.
func updateFromV5(tx *sql.Tx) error {
	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(tx, "SELECT id FROM nodes")
	if err != nil {
		return errors.Wrap(err, "failed to get IDs of current nodes")
	}

	// Fetch the IDs of all existing ceph volumes.
	volumeIDs, err := query.SelectIntegers(tx, `
SELECT storage_volumes.id FROM storage_volumes
    JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
    WHERE storage_pools.driver='ceph'
`)
	if err != nil {
		return errors.Wrap(err, "failed to get IDs of current ceph volumes")
	}

	// Fetch all existing ceph volumes.
	volumes := make([]struct {
		ID            int
		Name          string
		StoragePoolID int
		NodeID        int
		Type          int
		Description   string
	}, len(volumeIDs))
	stmt := `
SELECT
    storage_volumes.id,
    storage_volumes.name,
    storage_volumes.storage_pool_id,
    storage_volumes.node_id,
    storage_volumes.type,
    storage_volumes.description
FROM storage_volumes
    JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
    WHERE storage_pools.driver='ceph'
`
	err = query.SelectObjects(tx, func(i int) []interface{} {
		return []interface{}{
			&volumes[i].ID,
			&volumes[i].Name,
			&volumes[i].StoragePoolID,
			&volumes[i].NodeID,
			&volumes[i].Type,
			&volumes[i].Description,
		}
	}, stmt)
	if err != nil {
		return errors.Wrap(err, "failed to fetch current volumes")
	}

	// Duplicate each volume row across all nodes, and keep track of the
	// new volume IDs that we've inserted.
	created := make(map[int][]int64, 0) // Existing volume ID to new volumes IDs.
	columns := []string{"name", "storage_pool_id", "node_id", "type", "description"}
	for _, volume := range volumes {
		for _, nodeID := range nodeIDs {
			if volume.NodeID == nodeID {
				// This node already has the volume row
				continue
			}
			values := []interface{}{
				volume.Name,
				volume.StoragePoolID,
				nodeID,
				volume.Type,
				volume.Description,
			}
			id, err := query.UpsertObject(tx, "storage_volumes", columns, values)
			if err != nil {
				return errors.Wrap(err, "failed to insert new volume")
			}
			_, ok := created[volume.ID]
			if !ok {
				created[volume.ID] = make([]int64, 0)
			}
			created[volume.ID] = append(created[volume.ID], id)
		}
	}

	// Duplicate each volume config row across all nodes.
	for id, newIDs := range created {
		config, err := query.SelectConfig(tx, "storage_volumes_config", "storage_volume_id=?", id)
		if err != nil {
			errors.Wrap(err, "failed to fetch volume config")
		}
		for _, newID := range newIDs {
			for key, value := range config {
				_, err := tx.Exec(`
INSERT INTO storage_volumes_config(storage_volume_id, key, value) VALUES(?, ?, ?)
`, newID, key, value)
				if err != nil {
					return errors.Wrap(err, "failed to insert new volume config")
				}
			}
		}
	}

	return nil
}

func updateFromV4(tx *sql.Tx) error {
	stmt := "UPDATE networks SET state = 1"
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV3(tx *sql.Tx) error {
	stmt := `
CREATE TABLE storage_pools_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
ALTER TABLE storage_pools ADD COLUMN state INTEGER NOT NULL DEFAULT 0;
UPDATE storage_pools SET state = 1;
`
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV2(tx *sql.Tx) error {
	stmt := `
CREATE TABLE operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
`
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV1(tx *sql.Tx) error {
	stmt := `
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint TEXT NOT NULL,
    type INTEGER NOT NULL,
    name TEXT NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    node_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    description TEXT,
    UNIQUE (name),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
CREATE TABLE containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
CREATE TABLE containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
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
    fingerprint TEXT NOT NULL,
    filename TEXT NOT NULL,
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
CREATE TABLE images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE images_source (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias TEXT NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE images_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);
CREATE TABLE networks_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (network_id, node_id),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_id, node_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id) ON DELETE CASCADE
);
CREATE TABLE storage_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    driver TEXT NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE storage_pools_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, node_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    UNIQUE (storage_pool_id, node_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);
`
	_, err := tx.Exec(stmt)
	return err
}
func updateFromV0(tx *sql.Tx) error {
	// v0..v1 the dawn of clustering
	stmt := `
CREATE TABLE nodes (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    address TEXT NOT NULL,
    schema INTEGER NOT NULL,
    api_extensions INTEGER NOT NULL,
    heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
    pending INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name),
    UNIQUE (address)
);
`
	_, err := tx.Exec(stmt)
	return err
}
