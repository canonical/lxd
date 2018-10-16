package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
	13: updateFromV12,
}

func updateFromV12(tx *sql.Tx) error {
	stmts := `
DROP VIEW profiles_used_by_ref;
CREATE VIEW profiles_used_by_ref (project,
    name,
    value) AS
  SELECT projects.name,
    profiles.name,
    printf('/1.0/containers/%s?project=%s',
    containers.name,
    containers_projects.name)
    FROM profiles
    JOIN projects ON projects.id=profiles.project_id
    JOIN containers_profiles
      ON containers_profiles.profile_id=profiles.id
    JOIN containers
      ON containers.id=containers_profiles.container_id
    JOIN projects AS containers_projects
      ON containers_projects.id=containers.project_id;
`
	_, err := tx.Exec(stmts)
	return err
}

func updateFromV11(tx *sql.Tx) error {
	// There was at least a case of dangling references to rows in the
	// containers table that don't exist anymore. So sanitize them before
	// we move forward. See #5176.
	stmts := `
DELETE FROM containers_config WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_backups WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices_config WHERE container_device_id NOT IN (SELECT id FROM containers_devices);
DELETE FROM containers_profiles WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_profiles WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM images_aliases WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_properties WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_source WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM networks_config WHERE network_id NOT IN (SELECT id FROM networks);
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
DELETE FROM storage_pools_config WHERE storage_pool_id NOT IN (SELECT id FROM storage_pools);
DELETE FROM storage_volumes WHERE storage_pool_id NOT IN (SELECT id FROM storage_pools);
DELETE FROM storage_volumes_config WHERE storage_volume_id NOT IN (SELECT id FROM storage_volumes);
`
	_, err := tx.Exec(stmts)
	if err != nil {
		return errors.Wrap(err, "Remove dangling references to containers")
	}

	// Before doing anything save the counts of all tables, so we can later
	// check that we don't accidentally delete or add anything.
	counts1, err := query.CountAll(tx)
	if err != nil {
		return errors.Wrap(err, "Failed to count rows in current tables")
	}

	// Temporarily increase the cache size and disable page spilling, to
	// avoid unnecessary writes to the WAL.
	_, err = tx.Exec("PRAGMA cache_size=100000")
	if err != nil {
		return errors.Wrap(err, "Increase cache size")
	}

	_, err = tx.Exec("PRAGMA cache_spill=0")
	if err != nil {
		return errors.Wrap(err, "Disable spilling cache pages to disk")
	}

	// Use a large timeout since the update might take a while, due to the
	// new indexes being created.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	stmts = fmt.Sprintf(`
CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    UNIQUE (name)
);

CREATE TABLE projects_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    UNIQUE (project_id, key)
);

CREATE VIEW projects_config_ref (name, key, value) AS
   SELECT projects.name, projects_config.key, projects_config.value
     FROM projects_config
     JOIN projects ON projects.id=projects_config.project_id;

-- Insert the default project, with ID 1
INSERT INTO projects (name, description) VALUES ('default', 'Default LXD project');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.images', 'true');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.profiles', 'true');

-- Add a project_id column to all tables that need to be project-scoped.
-- The column is added without the FOREIGN KEY constraint
ALTER TABLE containers ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE images ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE images_aliases ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE profiles ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE storage_volumes ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE operations ADD COLUMN project_id INTEGER;

-- Create new versions of the above tables, this time with the FOREIGN key constraint
CREATE TABLE new_containers (
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
    UNIQUE (project_id, name),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_images (
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
    UNIQUE (project_id, fingerprint),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    snapshot INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id, project_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

-- Create copy version of all the tables that have direct or indirect references
-- to the tables above, which we are going to drop. The copy just have the data,
-- without FOREIGN KEY references.
CREATE TABLE containers_backups_copy (
    id INTEGER NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    container_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    UNIQUE (container_id, name)
);
INSERT INTO containers_backups_copy SELECT * FROM containers_backups;

CREATE TABLE containers_config_copy (
    id INTEGER NOT NULL,
    container_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (container_id, key)
);
INSERT INTO containers_config_copy SELECT * FROM containers_config;

CREATE TABLE containers_devices_copy (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (container_id, name)
);
INSERT INTO containers_devices_copy SELECT * FROM containers_devices;

CREATE TABLE containers_devices_config_copy (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (container_device_id, key)
);
INSERT INTO containers_devices_config_copy SELECT * FROM containers_devices_config;

CREATE TABLE containers_profiles_copy (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id)
);
INSERT INTO containers_profiles_copy SELECT * FROM containers_profiles;

CREATE TABLE images_aliases_copy (
    id INTEGER NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (name)
);
INSERT INTO images_aliases_copy SELECT * FROM images_aliases;

CREATE TABLE images_nodes_copy (
    id INTEGER NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id)
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
INSERT INTO images_nodes_copy SELECT * FROM images_nodes;

CREATE TABLE images_properties_copy (
    id INTEGER NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT
);
INSERT INTO images_properties_copy SELECT * FROM images_properties;

CREATE TABLE images_source_copy (
    id INTEGER NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias TEXT NOT NULL
);
INSERT INTO images_source_copy SELECT * FROM images_source;

CREATE TABLE profiles_config_copy (
    id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_id, key)
);
INSERT INTO profiles_config_copy SELECT * FROM profiles_config;

CREATE TABLE profiles_devices_copy (
    id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name)
);
INSERT INTO profiles_devices_copy SELECT * FROM profiles_devices;

CREATE TABLE profiles_devices_config_copy (
    id INTEGER NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key)
);
INSERT INTO profiles_devices_config_copy SELECT * FROM profiles_devices_config;

CREATE TABLE storage_volumes_config_copy (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key)
);
INSERT INTO storage_volumes_config_copy SELECT * FROM storage_volumes_config;

-- Copy existing data into the new tables with the project_id reference
INSERT INTO new_containers SELECT * FROM containers;
INSERT INTO new_images SELECT * FROM images;
INSERT INTO new_profiles SELECT * FROM profiles;
INSERT INTO new_storage_volumes SELECT * FROM storage_volumes;
INSERT INTO new_operations SELECT * FROM operations;

-- Drop the old table and rename the new ones. This will trigger cascading
-- deletes on all tables that have direct or indirect references to the old
-- table, but we have a copy of them that we will use for restoring.
DROP TABLE containers;
ALTER TABLE new_containers RENAME TO containers;

DROP TABLE images;
ALTER TABLE new_images RENAME TO images;

DROP TABLE profiles;
ALTER TABLE new_profiles RENAME TO profiles;

DROP TABLE storage_volumes;
ALTER TABLE new_storage_volumes RENAME TO storage_volumes;

INSERT INTO new_images_aliases SELECT * FROM images_aliases_copy;

DROP TABLE images_aliases;
DROP TABLE images_aliases_copy;
ALTER TABLE new_images_aliases RENAME TO images_aliases;

DROP TABLE operations;
ALTER TABLE new_operations RENAME TO operations;

-- Restore the content of the tables with direct or indirect references.
INSERT INTO containers_backups SELECT * FROM containers_backups_copy;
INSERT INTO containers_config SELECT * FROM containers_config_copy;
INSERT INTO containers_devices SELECT * FROM containers_devices_copy;
INSERT INTO containers_devices_config SELECT * FROM containers_devices_config_copy;
INSERT INTO containers_profiles SELECT * FROM containers_profiles_copy;
INSERT INTO images_nodes SELECT * FROM images_nodes_copy;
INSERT INTO images_properties SELECT * FROM images_properties_copy;
INSERT INTO images_source SELECT * FROM images_source_copy;
INSERT INTO profiles_config SELECT * FROM profiles_config_copy;
INSERT INTO profiles_devices SELECT * FROM profiles_devices_copy;
INSERT INTO profiles_devices_config SELECT * FROM profiles_devices_config_copy;
INSERT INTO storage_volumes_config SELECT * FROM storage_volumes_config_copy;

-- Drop the copies.
DROP TABLE containers_backups_copy;
DROP TABLE containers_config_copy;
DROP TABLE containers_devices_copy;
DROP TABLE containers_devices_config_copy;
DROP TABLE containers_profiles_copy;
DROP TABLE images_nodes_copy;
DROP TABLE images_properties_copy;
DROP TABLE images_source_copy;
DROP TABLE profiles_config_copy;
DROP TABLE profiles_devices_copy;
DROP TABLE profiles_devices_config_copy;
DROP TABLE storage_volumes_config_copy;

-- Create some indexes to speed up queries filtered by project ID and node ID
CREATE INDEX containers_node_id_idx ON containers (node_id);
CREATE INDEX containers_project_id_idx ON containers (project_id);
CREATE INDEX containers_project_id_and_name_idx ON containers (project_id, name);
CREATE INDEX containers_project_id_and_node_id_idx ON containers (project_id, node_id);
CREATE INDEX containers_project_id_and_node_id_and_name_idx ON containers (project_id, node_id, name);
CREATE INDEX images_project_id_idx ON images (project_id);
CREATE INDEX images_aliases_project_id_idx ON images_aliases (project_id);
CREATE INDEX profiles_project_id_idx ON profiles (project_id);
`)
	_, err = tx.ExecContext(ctx, stmts)
	if err != nil {
		return errors.Wrap(err, "Failed to add project_id column")
	}

	// Create a view to easily query all resources using a certain project
	stmt := fmt.Sprintf(`
CREATE VIEW projects_used_by_ref (name, value) AS
  SELECT projects.name, printf('%s', containers.name, projects.name)
    FROM containers JOIN projects ON project_id=projects.id UNION
  SELECT projects.name, printf('%s', images.fingerprint)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name, printf('%s', profiles.name, projects.name)
    FROM profiles JOIN projects ON project_id=projects.id
`, EntityURIs[TypeContainer], EntityURIs[TypeImage], EntityURIs[TypeProfile])
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to create projects_used_by_ref view")
	}

	// Create a view to easily query all profiles used by a certain container
	stmt = fmt.Sprintf(`
CREATE VIEW containers_profiles_ref (project, node, name, value) AS
   SELECT projects.name, nodes.name, containers.name, profiles.name
     FROM containers_profiles
       JOIN containers ON containers.id=containers_profiles.container_id
       JOIN profiles ON profiles.id=containers_profiles.profile_id
       JOIN projects ON projects.id=containers.project_id
       JOIN nodes ON nodes.id=containers.node_id
     ORDER BY containers_profiles.apply_order
`)
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to containers_profiles_ref view")
	}

	// Create a view to easily query the config of a certain container.
	stmt = fmt.Sprintf(`
CREATE VIEW containers_config_ref (project, node, name, key, value) AS
   SELECT projects.name, nodes.name, containers.name, containers_config.key, containers_config.value
     FROM containers_config
       JOIN containers ON containers.id=containers_config.container_id
       JOIN projects ON projects.id=containers.project_id
       JOIN nodes ON nodes.id=containers.node_id
`)
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to containers_config_ref view")
	}

	// Create a view to easily query the devices of a certain container.
	stmt = fmt.Sprintf(`
CREATE VIEW containers_devices_ref (project, node, name, device, type, key, value) AS
   SELECT projects.name, nodes.name, containers.name,
          containers_devices.name, containers_devices.type,
          coalesce(containers_devices_config.key, ''), coalesce(containers_devices_config.value, '')
   FROM containers_devices
     LEFT OUTER JOIN containers_devices_config ON containers_devices_config.container_device_id=containers_devices.id
     JOIN containers ON containers.id=containers_devices.container_id
     JOIN projects ON projects.id=containers.project_id
     JOIN nodes ON nodes.id=containers.node_id
`)
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to containers_devices_ref view")
	}

	// Create a view to easily query the config of a certain profile.
	stmt = fmt.Sprintf(`
CREATE VIEW profiles_config_ref (project, name, key, value) AS
   SELECT projects.name, profiles.name, profiles_config.key, profiles_config.value
     FROM profiles_config
     JOIN profiles ON profiles.id=profiles_config.profile_id
     JOIN projects ON projects.id=profiles.project_id
`)
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to profiles_config_ref view")
	}

	// Create a view to easily query the devices of a certain profile.
	stmt = fmt.Sprintf(`
CREATE VIEW profiles_devices_ref (project, name, device, type, key, value) AS
   SELECT projects.name, profiles.name,
          profiles_devices.name, profiles_devices.type,
          coalesce(profiles_devices_config.key, ''), coalesce(profiles_devices_config.value, '')
   FROM profiles_devices
     LEFT OUTER JOIN profiles_devices_config ON profiles_devices_config.profile_device_id=profiles_devices.id
     JOIN profiles ON profiles.id=profiles_devices.profile_id
     JOIN projects ON projects.id=profiles.project_id
`)
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to profiles_devices_ref view")
	}

	// Create a view to easily query all resources using a certain profile
	stmt = fmt.Sprintf(`
CREATE VIEW profiles_used_by_ref (project, name, value) AS
  SELECT projects.name, profiles.name, printf('%s', containers.name, projects.name)
    FROM profiles
    JOIN projects ON projects.id=profiles.project_id
    JOIN containers_profiles
      ON containers_profiles.profile_id=profiles.id
    JOIN containers
      ON containers.id=containers_profiles.container_id
`, EntityURIs[TypeContainer])
	_, err = tx.Exec(stmt)
	if err != nil {
		return errors.Wrap(err, "Failed to create profiles_used_by_ref view")
	}

	// Check that the count of all rows in the database is unchanged
	// (i.e. we didn't accidentally delete or add anything).
	counts2, err := query.CountAll(tx)
	if err != nil {
		return errors.Wrap(err, "Failed to count rows in updated tables")
	}

	delete(counts2, "projects")

	for table, count1 := range counts1 {
		if table == "sqlite_sequence" {
			continue
		}
		count2 := counts2[table]
		if count1 != count2 {
			return fmt.Errorf("Row count mismatch in table '%s': %d vs %d", table, count1, count2)
		}
	}

	// Restore default cache values.
	_, err = tx.Exec("PRAGMA cache_size=2000")
	if err != nil {
		return errors.Wrap(err, "Increase cache size")
	}

	_, err = tx.Exec("PRAGMA cache_spill=1")
	if err != nil {
		return errors.Wrap(err, "Disable spilling cache pages to disk")
	}

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
	sql := `
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
	stmt, err := tx.Prepare(sql)
	if err != nil {
		return err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, func(i int) []interface{} {
		return []interface{}{
			&volumes[i].ID,
			&volumes[i].Name,
			&volumes[i].StoragePoolID,
			&volumes[i].NodeID,
			&volumes[i].Type,
			&volumes[i].Description,
		}
	})
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
