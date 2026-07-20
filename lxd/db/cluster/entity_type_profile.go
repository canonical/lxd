package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
)

// entityTypeProfile implements entityTypeDBInfo for a Profile.
type entityTypeProfile struct {
	entityTypeCommon
}

func (e entityTypeProfile) code() int64 {
	return entityTypeCodeProfile
}

func (e entityTypeProfile) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, profiles.id, projects.name, '', json_array(profiles.name) 
FROM profiles 
JOIN projects ON profiles.project_id = projects.id`, e.code())
}

func (e entityTypeProfile) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypeProfile) urlsByIDsQuery(ids ...int64) string {
	return e.allURLsQuery() + " WHERE profiles.id IN " + query.IntParams(ids...)
}

func (e entityTypeProfile) idFromURLQuery() string {
	return projectEntityIDFromURLQuery("profiles")
}

func (e entityTypeProfile) onDeleteTriggerSQL() (name string, sql string) {
	return standardOnDeleteTriggerSQL("on_profile_delete", "profiles", e.code())
}
