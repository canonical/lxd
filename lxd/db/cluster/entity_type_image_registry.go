package cluster

import (
	"fmt"
)

// entityTypeImageRegistry implements entityTypeDBInfo for an [ImageRegistry].
type entityTypeImageRegistry struct {
	entityTypeCommon
}

func (e entityTypeImageRegistry) code() int64 {
	return entityTypeCodeImageRegistry
}

func (e entityTypeImageRegistry) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, image_registries.id, '', '', json_array(image_registries.name) FROM image_registries`, e.code())
}

func (e entityTypeImageRegistry) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeImageRegistry) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE image_registries.id = ?"
}

func (e entityTypeImageRegistry) idFromURLQuery() string {
	return `
SELECT ?, image_registries.id 
FROM image_registries 
WHERE '' = ? 
	AND '' = ? 
	AND image_registries.name = ?`
}

func (e entityTypeImageRegistry) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_image_registry_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON image_registries
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
