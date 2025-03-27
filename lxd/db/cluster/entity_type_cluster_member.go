package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// entityTypeClusterMember implements entityTypeDBInfo for a ClusterMember.
type entityTypeClusterMember struct {
	entityTypeCommon
}

func (e entityTypeClusterMember) code() int64 {
	return entityTypeCodeClusterMember
}

func (e entityTypeClusterMember) allURLsQuery() string {
	return fmt.Sprintf(`SELECT %d, nodes.id, '', '', json_array(nodes.name) FROM nodes`, e.code())
}

func (e entityTypeClusterMember) urlByIDQuery() string {
	return fmt.Sprintf(`%s WHERE nodes.id = ?`, e.allURLsQuery())
}

func (e entityTypeClusterMember) idFromURLQuery() string {
	return `
SELECT ?, nodes.id 
FROM nodes 
WHERE '' = ? 
	AND '' = ? 
	AND nodes.name = ?`
}

func (e entityTypeClusterMember) onDeleteTriggerSQL() (name string, sql string) {
	name = "on_node_delete"
	return name, fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON nodes
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

// runSelector for entityTypeClusterMember accepts the "name" and "config.*" matcher keys.
func (e entityTypeClusterMember) runSelector(ctx context.Context, tx *sql.Tx, selector Selector) ([]int, error) {
	var b strings.Builder
	b.WriteString(`SELECT nodes.id FROM nodes `)
	for _, m := range selector.Matchers {
		switch {
		case m.Property == "name":
		case strings.HasPrefix(m.Property, "config."):
			b.WriteString(`LEFT JOIN nodes_config ON nodes.id = nodes_config.node_id `)
		default:
			return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid selector matcher key %q for entity type %q", m.Property, entity.TypeClusterMember)
		}
	}

	args := make([]any, 0, len(selector.Matchers))
	for i, m := range selector.Matchers {
		matcherArgs := make([]any, 0, len(m.Values))
		for _, v := range m.Values {
			matcherArgs = append(matcherArgs, v)
		}

		if i == 0 {
			b.WriteString("WHERE ")
		} else {
			b.WriteString("AND ")
		}

		switch {
		case m.Property == "name":
			b.WriteString("nodes.name IN " + query.Params(len(matcherArgs)))
			args = append(args, matcherArgs...)
		case strings.HasPrefix(m.Property, "config."):
			key := strings.TrimPrefix(m.Property, "config.")
			b.WriteString("nodes_config.key = ? AND nodes_config.value IN " + query.Params(len(matcherArgs)))
			matcherArgs = append([]any{key}, matcherArgs...)
			args = append(args, matcherArgs)
		default:
			return nil, api.StatusErrorf(http.StatusBadRequest, "Selector matcher key %q not supported for entity type %q", m.Property, entity.TypeClusterGroup)
		}
	}

	q := b.String()
	logger.Warn(q, logger.Ctx{"args": args})
	return query.SelectIntegers(ctx, tx, q, args...)
}
