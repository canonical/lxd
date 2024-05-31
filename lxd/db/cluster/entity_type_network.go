package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeNetwork implements entityType for a Network.
type entityTypeNetwork struct {
	entity.Network
}

// Code returns entityTypeCodeNetwork.
func (e entityTypeNetwork) Code() int64 {
	return entityTypeCodeNetwork
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeNetwork, the ID of the Network,
// the project name of the Network, the location of the Network, and the path arguments of the
// Network in the order that they are found in its URL.
func (e entityTypeNetwork) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, networks.id, projects.name, '', json_array(networks.name) 
FROM networks 
JOIN projects ON networks.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeNetwork) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeNetwork) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE networks.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeNetwork) IDFromURLQuery() string {
	return `
SELECT ?, networks.id 
FROM networks 
JOIN projects ON networks.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Network are deleted.
func (e entityTypeNetwork) OnDeleteTriggerName() string {
	return "on_network_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Network are deleted.
func (e entityTypeNetwork) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON networks
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
