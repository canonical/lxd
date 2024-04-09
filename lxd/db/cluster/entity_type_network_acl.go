package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeNetworkACL implements entityType for a NetworkACL.
type entityTypeNetworkACL struct {
	entity.NetworkACL
}

// Code returns entityTypeCodeNetworkACL.
func (e entityTypeNetworkACL) Code() int64 {
	return entityTypeCodeNetworkACL
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeNetworkACL, the ID of the NetworkACL,
// the project name of the NetworkACL, the location of the NetworkACL, and the path arguments of the
// NetworkACL in the order that they are found in its URL.
func (e entityTypeNetworkACL) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, networks_acls.id, projects.name, '', json_array(networks_acls.name) 
FROM networks_acls 
JOIN projects ON networks_acls.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeNetworkACL) URLsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeNetworkACL) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE networks_acls.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeNetworkACL) IDFromURLQuery() string {
	return `
SELECT ?, networks_acls.id 
FROM networks_acls 
JOIN projects ON networks_acls.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND networks_acls.name = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type NetworkACL are deleted.
func (e entityTypeNetworkACL) OnDeleteTriggerName() string {
	return "on_network_acl_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type NetworkACL are deleted.
func (e entityTypeNetworkACL) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON networks_acls
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
