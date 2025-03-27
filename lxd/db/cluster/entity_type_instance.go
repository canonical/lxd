package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// entityTypeInstance implements entityTypeDBInfo for an Instance.
type entityTypeInstance struct{}

func (e entityTypeInstance) code() int64 {
	return entityTypeCodeInstance
}

func (e entityTypeInstance) allURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, instances.id, projects.name, '', json_array(instances.name) 
FROM instances 
JOIN projects ON instances.project_id = projects.id`, e.code())
}

func (e entityTypeInstance) urlsByProjectQuery() string {
	return fmt.Sprintf(`%s WHERE projects.name = ?`, e.allURLsQuery())
}

func (e entityTypeInstance) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE instances.id = ?`, e.allURLsQuery())
}

func (e entityTypeInstance) idFromURLQuery() string {
	return `
SELECT ?, instances.id 
FROM instances 
JOIN projects ON instances.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND instances.name = ?`
}

func (e entityTypeInstance) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_instance_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON instances
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

func (e entityTypeInstance) runSelector(ctx context.Context, tx *sql.Tx, selector Selector) ([]int, error) {
	propertyQuery, propertyArgs, hasConfigMatcher, err := e.propertyQuery(selector)
	if err != nil {
		return nil, err
	}

	candidates, err := query.SelectIntegers(ctx, tx, propertyQuery, propertyArgs...)
	if err != nil {
		return nil, err
	}

	if !hasConfigMatcher || len(candidates) == 0 {
		return candidates, nil
	}

	type precedenceMapValue struct {
		precedence int
		value      string
	}

	configQuery, matcher, configQueryArgs, err := e.configQuery(selector, candidates)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, configQuery, configQueryArgs...)
	if err != nil {
		return nil, err
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	instances := make(map[int]precedenceMapValue)
	for rows.Next() {
		var instanceID int
		var precedence int
		var value string
		err := rows.Scan(&instanceID, &precedence, &value)
		if err != nil {
			return nil, err
		}

		current, ok := instances[instanceID]
		if ok && current.precedence > precedence {
			continue
		}

		instances[instanceID] = precedenceMapValue{precedence: precedence, value: value}
	}

	candidates = make([]int, 0, len(candidates))
	for instanceID, v := range instances {
		if shared.ValueInSlice(v.value, matcher.Values) {
			candidates = append(candidates, instanceID)
		}
	}

	return candidates, nil
}

func (e entityTypeInstance) propertyQuery(selector Selector) (string, []any, bool, error) {
	if entity.Type(selector.EntityType) != entity.TypeInstance {
		return "", nil, false, fmt.Errorf("Invalid selector entity type %q (expected %q)", selector.EntityType, entity.TypeInstance)
	}

	if len(selector.Matchers) == 0 {
		return "", nil, false, fmt.Errorf("Selector for entity type %q has no matchers", entity.TypeInstance)
	}

	var hasConfigMatcher bool
	existingProperties := make([]string, 0, len(selector.Matchers))
	for _, m := range selector.Matchers {
		if shared.ValueInSlice(m.Property, existingProperties) {
			return "", nil, false, fmt.Errorf("Repeated selector matcher property %q", m.Property)
		}

		existingProperties = append(existingProperties, m.Property)

		isConfigMatcher := strings.HasPrefix(m.Property, "config.")
		if isConfigMatcher {
			if hasConfigMatcher {
				return "", nil, false, fmt.Errorf("Multiple configuration matchers not supported")
			}

			hasConfigMatcher = true
		}

		if !shared.ValueInSlice(m.Property, []string{"name", "project"}) && !isConfigMatcher {
			return "", nil, false, fmt.Errorf("Invalid selector property %q for entity type %q", m.Property, entity.TypeInstance)
		}

		if len(m.Values) == 0 {
			return "", nil, false, fmt.Errorf("Selector matcher with property %q for entity type %q requires at least one value", m.Property, entity.TypeClusterGroup)
		}
	}

	var b strings.Builder
	b.WriteString(`SELECT instances.id FROM instances `)
	for _, m := range selector.Matchers {
		switch m.Property {
		case "name":
		case "project":
			b.WriteString(`JOIN projects ON instances.project_id = projects.id `)
		}
	}

	args := make([]any, 0, len(selector.Matchers))
	for i, m := range selector.Matchers {
		if !shared.ValueInSlice(m.Property, []string{"name", "project"}) {
			continue
		}

		matcherArgs := make([]any, 0, len(m.Values))
		for _, v := range m.Values {
			matcherArgs = append(matcherArgs, v)
		}

		if i == 0 {
			b.WriteString("WHERE ")
		} else {
			b.WriteString("AND ")
		}

		switch m.Property {
		case "name":
			b.WriteString("instances.name IN " + query.Params(len(matcherArgs)))
		case "project":
			b.WriteString("projects.name IN " + query.Params(len(matcherArgs)))
		}

		args = append(args, matcherArgs...)
	}

	return b.String(), args, hasConfigMatcher, nil
}

func (e entityTypeInstance) configQuery(selector Selector, instanceIDs []int) (string, api.SelectorMatcher, []any, error) {
	var key string
	var matcher api.SelectorMatcher
	for _, m := range selector.Matchers {
		var ok bool
		key, ok = strings.CutPrefix(m.Property, "config.")
		if !ok {
			continue
		}

		matcher = m
		break
	}

	args := make([]any, 0, 2*(len(instanceIDs)+1))
	for i := 0; i < 2; i++ {
		args = append(args, key)
		for _, c := range instanceIDs {
			args = append(args, c)
		}
	}

	q := `
SELECT 
	instances.id AS instance_id, 
	1000000 AS apply_order, 
	instances_config.value 
FROM instances 
JOIN instances_config ON instances.id = instances_config.instance_id 
WHERE instances_config.key = ? 
	AND instances.id IN ` + query.Params(len(instanceIDs)) + `
UNION 
SELECT 
	instances.id AS instance_id, 
	instances_profiles.apply_order, 
	profiles_config.value 
FROM instances 
JOIN instances_profiles ON instances.id = instances_profiles.instance_id 
JOIN profiles ON instances_profiles.profile_id = profiles.id 
JOIN profiles_config ON profiles.id = profiles_config.profile_id 
WHERE profiles_config.key = ? 
	AND instances.id IN ` + query.Params(len(instanceIDs))

	return q, matcher, args, nil
}
