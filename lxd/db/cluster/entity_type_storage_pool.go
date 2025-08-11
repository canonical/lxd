package cluster

import (
	"fmt"
)

// entityTypeStoragePool implements entityTypeDBInfo for a StoragePool.
type entityTypeStoragePool struct {
	entityTypeCommon
}

func (e entityTypeStoragePool) code() int64 {
	return entityTypeCodeStoragePool
}

func (e entityTypeStoragePool) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, storage_pools.id, '', '', json_array(storage_pools.name) FROM storage_pools`, e.code())
}

func (e entityTypeStoragePool) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE storage_pools.id = ?"
}

func (e entityTypeStoragePool) idFromURLQuery() string {
	return `
SELECT ?, storage_pools.id 
FROM storage_pools 
WHERE '' = ? 
	AND '' = ? 
	AND storage_pools.name = ?`
}

func (e entityTypeStoragePool) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_storage_pool_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON storage_pools
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
