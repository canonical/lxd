package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
)

// entityTypeImageRegistry implements entityTypeDBInfo for an [ImageRegistriesRow].
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

func (e entityTypeImageRegistry) urlsByIDsQuery(ids ...int64) string {
	return e.allURLsQuery() + " WHERE image_registries.id IN " + query.IntParams(ids...)
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
	return standardOnDeleteTriggerSQL("on_image_registry_delete", "image_registries", e.code())
}
