package cluster

import (
	"fmt"
)

// entityTypeStorageVolumeSnapshot implements entityTypeDBInfo for a StorageVolumeSnapshot.
type entityTypeStorageVolumeSnapshot struct{}

func (e entityTypeStorageVolumeSnapshot) code() int64 {
	return entityTypeCodeStorageVolumeSnapshot
}

func (e entityTypeStorageVolumeSnapshot) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT 
	%d, 
	storage_volumes_snapshots.id, 
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
		storage_volumes.name,
		storage_volumes_snapshots.name
	)
FROM storage_volumes_snapshots
	JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
	JOIN projects ON storage_volumes.project_id = projects.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	LEFT JOIN nodes ON storage_volumes.node_id = nodes.id
`,
		e.code(),
		StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
		StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
		StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
		StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM,
	)
}

func (e entityTypeStorageVolumeSnapshot) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeStorageVolumeSnapshot) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE storage_volumes_snapshots.id = ?`, e.allURLsQuery())
}

func (e entityTypeStorageVolumeSnapshot) idFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, storage_volumes_snapshots.id 
FROM storage_volumes_snapshots
JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
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
	AND storage_volumes_snapshots.name = ?
`, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeNameContainer,
		StoragePoolVolumeTypeImage, StoragePoolVolumeTypeNameImage,
		StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeNameCustom,
		StoragePoolVolumeTypeVM, StoragePoolVolumeTypeNameVM)
}

func (e entityTypeStorageVolumeSnapshot) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_storage_volume_snapshot_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON storage_volumes_snapshots
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
