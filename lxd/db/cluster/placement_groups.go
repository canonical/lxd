package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// PlacementGroupsRow represents a single row of the placement_groups table.
// db:model placement_groups
type PlacementGroupsRow struct {
	ID          int64  `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	ProjectID   int64  `db:"project_id"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (PlacementGroupsRow) APIName() string {
	return "Placement group"
}

// PlacementGroup contains [PlacementGroupsRow] with additional joins.
// db:model placement_groups
type PlacementGroup struct {
	Row PlacementGroupsRow

	// db:join JOIN projects ON placement_groups.project_id = projects.id
	ProjectName string `db:"projects.name"`
}

// PlacementGroupFilter contains fields that can be used to filter results when getting placement groups.
type PlacementGroupFilter struct {
	Project *string
	Name    *string
}

// CreatePlacementGroupConfig creates config for a new placement group with the given ID.
func CreatePlacementGroupConfig(ctx context.Context, tx *sql.Tx, placementGroupID int64, config map[string]string) error {
	q := `INSERT INTO placement_groups_config (placement_group_id, key, value) VALUES(?, ?, ?)`

	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}

	defer func() {
		err := stmt.Close()
		if err != nil {
			logger.Warn("Failed closing statement", logger.Ctx{"query": q, "err": err})
		}
	}()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(placementGroupID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdatePlacementGroupConfig updates the placement group config with the given ID.
func UpdatePlacementGroupConfig(ctx context.Context, tx *sql.Tx, placementGroupID int64, config map[string]string) error {
	// Delete current entries.
	_, err := tx.Exec("DELETE FROM placement_groups_config WHERE placement_group_id=?", placementGroupID)
	if err != nil {
		return err
	}

	// Insert new entries.
	return CreatePlacementGroupConfig(ctx, tx, placementGroupID, config)
}

// GetPlacementGroupConfig returns the config for the placement group with the given ID.
func GetPlacementGroupConfig(ctx context.Context, tx *sql.Tx, placementGroupID int) (map[string]string, error) {
	q := `SELECT key, value FROM placement_groups_config WHERE placement_group_id=?`

	config := map[string]string{}
	return config, query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for placement group ID %d", key, placementGroupID)
		}

		config[key] = value
		return nil
	}, placementGroupID)
}

// ToAPI converts the [PlacementGroup] to an [api.PlacementGroup], querying for extra data as necessary.
func (p *PlacementGroup) ToAPI(ctx context.Context, tx *sql.Tx) (*api.PlacementGroup, error) {
	// Get config
	config, err := GetPlacementGroupConfig(ctx, tx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed getting placement group config: %w", err)
	}

	return &api.PlacementGroup{
		Name:        p.Name,
		Description: p.Description,
		Project:     p.Project,
		Config:      config,
	}, nil
}

// GetPlacementGroupUsedBy returns a list of URLs of all instances and profiles that reference placement groups matching the provided [PlacementGroupFilter].
func GetPlacementGroupUsedBy(ctx context.Context, tx *sql.Tx, filter PlacementGroupFilter, firstOnly bool) ([]string, error) {
	var b strings.Builder
	var args []any

	b.WriteString(`SELECT ` + strconv.Itoa(int(entityTypeCodeInstance)) + `, instances.name, projects.name, instances_config.value FROM instances
JOIN instances_config ON instances.id = instances_config.instance_id
JOIN projects ON instances.project_id = projects.id
WHERE instances_config.key = 'placement.group'`)

	if filter.Name != nil {
		b.WriteString(" AND instances_config.value = ?")
		args = append(args, *filter.Name)
	}

	if filter.Project != nil {
		b.WriteString(" AND projects.name = ?")
		args = append(args, *filter.Project)
	}

	b.WriteString(`
UNION SELECT ` + strconv.Itoa(int(entityTypeCodeProfile)) + `, profiles.name, projects.name, profiles_config.value FROM profiles
JOIN profiles_config ON profiles.id = profiles_config.profile_id
JOIN projects ON profiles.project_id = projects.id
WHERE profiles_config.key = 'placement.group'`)

	if filter.Name != nil {
		b.WriteString(" AND profiles_config.value = ?")
		args = append(args, *filter.Name)
	}

	if filter.Project != nil {
		b.WriteString(" AND projects.name = ?")
		args = append(args, *filter.Project)
	}

	if firstOnly {
		b.WriteString("LIMIT 1")
	}

	var urls []string
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		var eType EntityType
		var eName string
		var pName string
		var placementGroupName string
		err := scan(&eType, &eName, &pName, &placementGroupName)
		if err != nil {
			return err
		}

		switch entity.Type(eType) {
		case entity.TypeInstance:
			urls = append(urls, api.NewURL().Project(pName).Path("1.0", "instances", eName).String())
		case entity.TypeProfile:
			urls = append(urls, api.NewURL().Project(pName).Path("1.0", "profiles", eName).String())
		default:
			return errors.New("Unexpected entity type in placement group usage query")
		}

		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed finding references to placement group: %w", err)
	}

	return urls, nil
}

// GetInstancesInPlacementGroup returns a map of member (node) ID to a slice of instance IDs for all instances that reference the given placement group either directly or indirectly via a profile.
// The target placement group is specified using a [PlacementGroupFilter] which must contain both [PlacementGroupFilter.Project] and [PlacementGroupFilter.Name].
func GetInstancesInPlacementGroup(ctx context.Context, tx *sql.Tx, filter PlacementGroupFilter) (map[int][]int, error) {
	if filter.Project == nil || filter.Name == nil {
		return nil, errors.New("Project and placement group name must be provided")
	}

	args := []any{*filter.Project, *filter.Name}

	// Compute the "placement.group" for each instance using COALESCE(instance-level-config, last-applied-profile-config) so that instance-level config overrides profile-level config.
	q := `SELECT instances.id, instances.node_id
FROM instances
JOIN projects ON instances.project_id = projects.id
WHERE projects.name = ?
AND COALESCE(
  (SELECT value FROM instances_config WHERE instance_id = instances.id AND key = 'placement.group' LIMIT 1),
  (SELECT profiles_config.value FROM instances_profiles JOIN profiles ON instances_profiles.profile_id = profiles.id JOIN profiles_config ON profiles.id = profiles_config.profile_id WHERE instances_profiles.instance_id = instances.id AND profiles_config.key = 'placement.group' ORDER BY instances_profiles.apply_order DESC LIMIT 1)
) = ?`

	// Exclude member ID if specified.
	if filter.ID != nil {
		q += " AND instances.node_id != ?"
		args = append(args, *filter.ID)
	}

	result := make(map[int][]int)
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var instID int
		var nodeID int
		err := scan(&instID, &nodeID)
		if err != nil {
			return err
		}

		result[nodeID] = append(result[nodeID], instID)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return result, nil
}
