package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeStorageBucket implements entityType for a StorageBucket.
type entityTypeStorageBucket struct {
	entity.StorageBucket
}

// Code returns entityTypeCodeStorageBucket.
func (e entityTypeStorageBucket) Code() int64 {
	return entityTypeCodeStorageBucket
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeStorageBucket, the ID of the StorageBucket,
// the project name of the StorageBucket, the location of the StorageBucket, and the path arguments of
// the StorageBucket in the order that they are found in its URL.
func (e entityTypeStorageBucket) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, storage_buckets.id, projects.name, replace(coalesce(nodes.name, ''), 'none', ''), json_array(storage_pools.name, storage_buckets.name)
FROM storage_buckets
	JOIN projects ON storage_buckets.project_id = projects.id
	JOIN storage_pools ON storage_buckets.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_buckets.node_id = nodes.id
`, e.Code(),
	)
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeStorageBucket) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeStorageBucket) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE storage_buckets.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeStorageBucket) IDFromURLQuery() string {
	return `
SELECT ?, storage_buckets.id 
FROM storage_buckets
JOIN projects ON storage_buckets.project_id = projects.id
JOIN storage_pools ON storage_buckets.storage_pool_id = storage_pools.id
LEFT JOIN nodes ON storage_buckets.node_id = nodes.id
WHERE projects.name = ? 
	AND replace(coalesce(nodes.name, ''), 'none', '') = ? 
	AND storage_pools.name = ? 
	AND storage_buckets.name = ?
`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type StorageBucket are deleted.
func (e entityTypeStorageBucket) OnDeleteTriggerName() string {
	return "on_storage_bucket_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type StorageBucket are deleted.
func (e entityTypeStorageBucket) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON storage_buckets
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
