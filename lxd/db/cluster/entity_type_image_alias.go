package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
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
SELECT %d, images_aliases.id, projects.name, '', json_array(CASE images_aliases.image_type WHEN 1 THEN 'virtual-machine' ELSE 'container' END, images_aliases.name)
FROM images_aliases 
JOIN projects ON images_aliases.project_id = projects.id`, e.code())
}

func (e entityTypeImageAlias) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeImageAlias) urlsByIDsQuery(ids ...int64) string {
	return e.allURLsQuery() + " WHERE images_aliases.id IN " + query.IntParams(ids...)
}

func (e entityTypeImageAlias) idFromURLQuery() string {
	return `
SELECT ?, images_aliases.id
FROM images_aliases
JOIN projects ON images_aliases.project_id = projects.id
WHERE projects.name = ?
	AND '' = ?
	AND images_aliases.image_type = CASE ? WHEN 'container' THEN 0 WHEN 'virtual-machine' THEN 1 ELSE -1 END
	AND images_aliases.name = ?`
}

func (e entityTypeImageAlias) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_image_alias_delete", "images_aliases", e.code())
}
