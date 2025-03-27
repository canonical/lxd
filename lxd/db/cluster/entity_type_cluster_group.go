package cluster

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/entity"
)

// entityTypeClusterGroup implements entityTypeDBInfo for a ClusterGroup.
type entityTypeClusterGroup struct {
	entityTypeCommon
}

func (e entityTypeClusterGroup) code() int64 {
	return entityTypeCodeClusterGroup
}

func (e entityTypeClusterGroup) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, cluster_groups.id, '', '', json_array(cluster_groups.name) FROM cluster_groups`, e.code())
}

func (e entityTypeClusterGroup) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE cluster_groups.id = ?`, e.allURLsQuery())
}

func (e entityTypeClusterGroup) idFromURLQuery() string {
	return `
SELECT ?, cluster_groups.id 
FROM cluster_groups 
WHERE '' = ? 
	AND '' = ? 
	AND cluster_groups.name = ?`
}

func (e entityTypeClusterGroup) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_cluster_group_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON cluster_groups
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

// runSelector for entityTypeClusterGroup only accepts the "name" matcher key.
func (e entityTypeClusterGroup) runSelector(ctx context.Context, tx *sql.Tx, selector Selector) ([]int, error) {
	q, args, err := e.selectorQuery(selector)
	if err != nil {
		return nil, err
	}

	return query.SelectIntegers(ctx, tx, q, args...)
}

func (e entityTypeClusterGroup) selectorQuery(selector Selector) (string, []any, error) {
	if entity.Type(selector.EntityType) != entity.TypeClusterGroup {
		return "", nil, fmt.Errorf("Invalid selector entity type %q (expected %q)", selector.EntityType, entity.TypeClusterGroup)
	}

	if len(selector.Matchers) == 0 {
		return "", nil, fmt.Errorf("Selector for entity type %q has no matchers", entity.TypeClusterGroup)
	}

	if len(selector.Matchers) > 1 {
		return "", nil, fmt.Errorf("Selectors for entity type %q may only have one matcher", entity.TypeClusterGroup)
	}

	if selector.Matchers[0].Property != "name" {
		return "", nil, fmt.Errorf("Selectors for entity type %q may have only one matcher with property %q", entity.TypeClusterGroup, "name")
	}

	if len(selector.Matchers[0].Values) == 0 {
		return "", nil, fmt.Errorf("Selector matcher for entity type %q requires at least one value", entity.TypeClusterGroup)
	}

	q := `SELECT id FROM cluster_groups WHERE name IN ` + query.Params(len(selector.Matchers[0].Values))
	args := make([]any, 0, len(selector.Matchers[0].Values))
	for _, v := range selector.Matchers[0].Values {
		args = append(args, v)
	}

	return q, args, nil
}
