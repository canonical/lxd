package cluster

import (
	"strconv"
)

// entityTypeAuthGroup implements entityTypeDBInfo for an AuthGroup.
type entityTypePlacementRuleset struct {
	entityTypeCommon
}

func (e entityTypePlacementRuleset) code() int64 {
	return entityTypeCodePlacementRuleset
}

func (e entityTypePlacementRuleset) allURLsQuery() string {
	return `
SELECT 
	` + strconv.Itoa(int(e.code())) + `,
	placement_rulesets.id,
	projects.name,
	'',
	json_array(placement_rulesets.name)
FROM placement_rulesets
JOIN projects ON projects.id = placement_rulesets.project_id
`
}

func (e entityTypePlacementRuleset) urlByIDQuery() string {
	return e.allURLsQuery() + " WHERE placement_rulesets.id = ?"
}

func (e entityTypePlacementRuleset) urlsByProjectQuery() string {
	return e.allURLsQuery() + " WHERE projects.name = ?"
}

func (e entityTypePlacementRuleset) idFromURLQuery() string {
	return `
SELECT ?, placement_rulesets.id 
FROM placement_rulesets
JOIN projects ON placement_rulesets.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND placement_rulesets.name = ?`
}

func (e entityTypePlacementRuleset) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_placement_ruleset_delete"
	return name, `
CREATE TRIGGER ` + name + `
	AFTER DELETE ON placement_rulesets
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = ` + strconv.Itoa(int(e.code())) + ` 
		AND entity_id = OLD.id;
	END
`
}
