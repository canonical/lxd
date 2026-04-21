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
	return standardOnDeleteTriggerSQL("on_storage_pool_delete", "storage_pools", e.code())
}
