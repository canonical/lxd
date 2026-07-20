package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
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

func (e entityTypeReplicator) urlsByIDsQuery(ids ...int64) string {
	return e.allURLsQuery() + " WHERE replicators.id IN " + query.IntParams(ids...)
}

func (e entityTypeReplicator) idFromURLQuery() string {
	return projectEntityIDFromURLQuery("replicators")
}

func (e entityTypeReplicator) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_replicator_delete", "replicators", e.code())
}
