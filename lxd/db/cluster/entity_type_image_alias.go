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
	return projectEntityIDFromURLQuery("images_aliases")
}

func (e entityTypeImageAlias) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_image_alias_delete", "images_aliases", e.code())
}
