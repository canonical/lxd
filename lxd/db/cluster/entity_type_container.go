package cluster

import (
	"fmt"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/entity"
)

// entityTypeContainer implements entityType for a Container.
type entityTypeContainer struct {
	entity.Container
}

// Code returns entityTypeCodeContainer.
func (e entityTypeContainer) Code() int64 {
	return entityTypeCodeContainer
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeContainer, the ID of the Container,
// the project name of the Container, the location of the Container, and the path arguments of the
// Container in the order that they are found in its URL.
func (e entityTypeContainer) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances.id, projects.name, '', json_array(instances.name) 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE instances.type = %d
`, e.Code(), instancetype.Container)
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeContainer) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s AND projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeContainer) URLByIDQuery() string {
	return fmt.Sprintf(`%s AND instances.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeContainer) IDFromURLQuery() string {
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

// OnDeleteTriggerName returns an empty string because there is no `containers` table (we use the `instances` table).
func (e entityTypeContainer) OnDeleteTriggerName() string {
	return ""
}

// OnDeleteTriggerSQL  returns an empty string because there is no `containers` table (we use the `instances` table).
func (e entityTypeContainer) OnDeleteTriggerSQL() string {
	return ""
}
