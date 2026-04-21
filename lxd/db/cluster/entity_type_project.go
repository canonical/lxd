package cluster

import (
	"fmt"
)

// entityTypeProject implements entityTypeDBInfo for a Project.
type entityTypeProject struct {
	entityTypeCommon
}

func (e entityTypeProject) code() int64 {
	return entityTypeCodeProject
}

func (e entityTypeProject) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, projects.id, projects.name, '', json_array(projects.name) FROM projects`, e.code())
}

func (e entityTypeProject) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeProject) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE id = ?"
}

func (e entityTypeProject) idFromURLQuery() string {
	return `
SELECT ?, projects.id 
FROM projects 
WHERE projects.name = ? 
	AND '' = ? 
	AND projects.name = ?`
}

func (e entityTypeProject) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_project_delete", "projects", e.code())
}
