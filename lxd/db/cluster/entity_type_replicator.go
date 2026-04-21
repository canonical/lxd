package cluster

import (
	"fmt"
)

// entityTypeReplicator implements entityTypeDBInfo for a Replicator.
type entityTypeReplicator struct {
	entityTypeCommon
}

func (e entityTypeReplicator) code() int64 {
	return entityTypeCodeReplicator
}

func (e entityTypeReplicator) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, replicators.id, projects.name, '', json_array(replicators.name)
FROM replicators
JOIN projects ON projects.id = replicators.project_id`, e.code())
}

func (e entityTypeReplicator) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeReplicator) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE replicators.id = ?"
}

func (e entityTypeReplicator) idFromURLQuery() string {
	return `
SELECT ?, replicators.id
FROM replicators
JOIN projects ON replicators.project_id = projects.id
WHERE projects.name = ?
	AND '' = ?
	AND replicators.name = ?`
}

func (e entityTypeReplicator) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_replicator_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON replicators
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
