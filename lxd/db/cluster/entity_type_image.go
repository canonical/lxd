package cluster

import (
	"fmt"
)

// entityTypeImage implements entityTypeDBInfo for an Image.
type entityTypeImage struct{}

func (e entityTypeImage) code() int64 {
	return entityTypeCodeImage
}

func (e entityTypeImage) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, images.id, projects.name, '', json_array(images.fingerprint) 
FROM images 
JOIN projects ON images.project_id = projects.id`, e.code())
}

func (e entityTypeImage) urlsByProjectQuery() string {
	return fmt.Sprintf("%s WHERE projects.name = ?", e.allURLsQuery())
}

func (e entityTypeImage) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE images.id = ?`, e.allURLsQuery())
}

func (e entityTypeImage) idFromURLQuery() string {
	return `
SELECT ?, images.id 
FROM images 
JOIN projects ON images.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND images.fingerprint = ?`
}

func (e entityTypeImage) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_image_delete"
	return name, fmt.Sprintf(`CREATE TRIGGER %s
	AFTER DELETE ON images
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
