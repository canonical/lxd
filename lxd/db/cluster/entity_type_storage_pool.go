package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeStoragePool implements entityType for a StoragePool.
type entityTypeStoragePool struct {
	entity.StoragePool
}

// Code returns entityTypeCodeStoragePool.
func (e entityTypeStoragePool) Code() int64 {
	return entityTypeCodeStoragePool
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeStoragePool, the ID of the StoragePool,
// the project name of the StoragePool, the location of the StoragePool, and the path arguments of the
// StoragePool in the order that they are found in its URL.
func (e entityTypeStoragePool) AllURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, storage_pools.id, '', '', json_array(storage_pools.name) FROM storage_pools`, e.Code())
}

// URLsByProjectQuery returns an empty string because StoragePool entities are not project specific.
func (e entityTypeStoragePool) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeStoragePool) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE storage_pools.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeStoragePool) IDFromURLQuery() string {
	return `
SELECT ?, storage_pools.id 
FROM storage_pools 
WHERE '' = ? 
	AND '' = ? 
	AND storage_pools.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type StoragePool are deleted.
func (e entityTypeStoragePool) OnDeleteTriggerName() string {
	return "on_storage_pool_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type StoragePool are deleted.
func (e entityTypeStoragePool) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
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
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
