package cluster

import (
	"strconv"
)

// entityTypePlacementGroup implements [entityTypeDBInfo] for an [api.PlacementGroup].
type entityTypePlacementGroup struct {
	entityTypeCommon
}

func (e entityTypePlacementGroup) code() int64 {
	return entityTypeCodePlacementGroup
}

func (e entityTypePlacementGroup) allURLsQuery() string {
	return `
SELECT 
	` + strconv.Itoa(int(e.code())) + `,
	placement_groups.id,
	projects.name,
	'',
	json_array(placement_groups.name)
FROM placement_groups
JOIN projects ON projects.id = placement_groups.project_id
`
}

func (e entityTypePlacementGroup) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE placement_groups.id = ?"
}

func (e entityTypePlacementGroup) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypePlacementGroup) idFromURLQuery() string {
	return `
SELECT ?, placement_groups.id 
FROM placement_groups
JOIN projects ON placement_groups.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND placement_groups.name = ?`
}

func (e entityTypePlacementGroup) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_placement_group_delete"
	return name, `
CREATE TRIGGER ` + name + `
	AFTER DELETE ON placement_groups
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = ` + strconv.Itoa(int(e.code())) + ` 
		AND entity_id = OLD.id;
	END
`
}
