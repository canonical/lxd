package cluster

import (
	"fmt"
)

// entityTypeImageAlias implements entityTypeDBInfo for an ImageAlias.
type entityTypeImageAlias struct {
	entityTypeCommon
}

func (e entityTypeImageAlias) code() int64 {
	return entityTypeCodeImageAlias
}

func (e entityTypeImageAlias) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, images_aliases.id, projects.name, '', json_array(images_aliases.name) 
FROM images_aliases 
JOIN projects ON images_aliases.project_id = projects.id`, e.code())
}

func (e entityTypeImageAlias) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeImageAlias) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE images_aliases.id = ?"
}

func (e entityTypeImageAlias) idFromURLQuery() string {
	return `
SELECT ?, images_aliases.id 
FROM images_aliases 
JOIN projects ON images_aliases.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND images_aliases.name = ? `
}

func (e entityTypeImageAlias) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_image_alias_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON images_aliases
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
