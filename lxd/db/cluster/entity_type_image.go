package cluster

import (
	"fmt"
)

// entityTypeImage implements entityTypeDBInfo for an Image.
type entityTypeImage struct {
	entityTypeCommon
}

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
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeImage) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE images.id = ?"
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
	return standardOnDeleteTriggerSQL("on_image_delete", "images", e.code())
}
