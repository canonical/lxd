package cluster

import (
	"fmt"
)

// entityTypeStorageBucket implements entityTypeDBInfo for a StorageBucket.
type entityTypeStorageBucket struct{}

func (e entityTypeStorageBucket) code() int64 {
	return entityTypeCodeStorageBucket
}

func (e entityTypeStorageBucket) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, storage_buckets.id, projects.name, replace(coalesce(nodes.name, ''), 'none', ''), json_array(storage_pools.name, storage_buckets.name)
FROM storage_buckets
	JOIN projects ON storage_buckets.project_id = projects.id
	JOIN storage_pools ON storage_buckets.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_buckets.node_id = nodes.id
`, e.code(),
	)
}

func (e entityTypeStorageBucket) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeStorageBucket) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE storage_buckets.id = ?`, e.allURLsQuery())
}

func (e entityTypeStorageBucket) idFromURLQuery() string {
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

func (e entityTypeStorageBucket) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_storage_bucket_delete"
	return name, fmt.Sprintf(`
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
`, name, e.code(), e.code())
}
