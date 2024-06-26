package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeStorageVolume implements entityType for a StorageVolume.
type entityTypeStorageVolume struct {
	entity.StorageVolume
}

// Code returns entityTypeCodeStorageVolume.
func (e entityTypeStorageVolume) Code() int64 {
	return entityTypeCodeStorageVolume
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeStorageVolume, the ID of the StorageVolume,
// the project name of the StorageVolume, the location of the StorageVolume, and the path arguments of the
// StorageVolume in the order that they are found in its URL.
func (e entityTypeStorageVolume) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT 
	%d, 
	storage_volumes.id, 
	projects.name, 
	replace(coalesce(nodes.name, ''), 'none', ''), 
	json_array(
		storage_pools.name,
		CASE storage_volumes.type
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
			WHEN %d THEN '%s'
		END,
		storage_volumes.name
	)
FROM storage_volumes
	JOIN projects ON storage_volumes.project_id = projects.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
`,
		e.Code(),
		StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
		StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
		StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
		StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM,
	)
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeStorageVolume) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeStorageVolume) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE storage_volumes.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeStorageVolume) IDFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, storage_volumes.id 
FROM storage_volumes
JOIN projects ON storage_volumes.project_id = projects.id
JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
WHERE projects.name = ? 
	AND replace(coalesce(nodes.name, ''), 'none', '') = ? 
	AND storage_pools.name = ? 
	AND CASE storage_volumes.type 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
		WHEN %d THEN '%s' 
	END = ? 
	AND storage_volumes.name = ?
`, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
		StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
		StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
		StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM)
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type StorageVolume are deleted.
func (e entityTypeStorageVolume) OnDeleteTriggerName() string {
	return "on_storage_volume_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type StorageVolume are deleted.
func (e entityTypeStorageVolume) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON storage_volumes
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
