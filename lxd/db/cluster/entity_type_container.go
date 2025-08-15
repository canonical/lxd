package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/instance/instancetype"
)

// entityTypeContainer implements entityTypeDBInfo for a Container.
type entityTypeContainer struct {
	entityTypeCommon
}

func (e entityTypeContainer) code() int64 {
	return entityTypeCodeContainer
}

func (e entityTypeContainer) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances.id, projects.name, '', json_array(instances.name) 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE instances.type = %d
`, e.code(), instancetype.Container)
}

func (e entityTypeContainer) urlsByProjectQuery() string {
	return e.allURLsQuery() + " AND projects.name = ?"
}

func (e entityTypeContainer) urlByIDQuery() string {
	return e.allURLsQuery() + " AND instances.id = ?"
}

func (e entityTypeContainer) idFromURLQuery() string {
	return fmt.Sprintf(`
SELECT ?, instances.id 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ? 
	AND instances.type = %d
`, instancetype.Container)
}
