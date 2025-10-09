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

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t placement_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e placement_group objects table=placement_groups
//go:generate mapper stmt -e placement_group objects-by-ID table=placement_groups
//go:generate mapper stmt -e placement_group objects-by-Project table=placement_groups
//go:generate mapper stmt -e placement_group objects-by-Name-and-Project table=placement_groups
//go:generate mapper stmt -e placement_group id table=placement_groups
//go:generate mapper stmt -e placement_group create struct=PlacementGroup table=placement_groups
//go:generate mapper stmt -e placement_group delete-by-Name-and-Project table=placement_groups
//go:generate mapper stmt -e placement_group update struct=PlacementGroup table=placement_groups
//go:generate mapper stmt -e placement_group rename struct=PlacementGroup table=placement_groups
//
//go:generate mapper method -i -e placement_group GetMany
//go:generate mapper method -i -e placement_group GetOne
//go:generate mapper method -i -e placement_group ID struct=PlacementGroup
//go:generate mapper method -i -e placement_group Exists struct=PlacementGroup
//go:generate mapper method -i -e placement_group Create struct=PlacementGroup
//go:generate mapper method -i -e placement_group DeleteOne-by-Name-and-Project
//go:generate mapper method -i -e placement_group Update struct=PlacementGroup
//go:generate mapper method -i -e placement_group Rename struct=PlacementGroup
//go:generate goimports -w placement_groups.mapper.go
//go:generate goimports -w placement_groups.interface.mapper.go

// PlacementGroup is the database representation of an [api.PlacementGroup].
type PlacementGroup struct {
	ID          int
	Name        string `db:"primary=yes"`
	Project     string `db:"primary=yes&join=projects.name"`
	Description string `db:"coalesce=''"`
}

// PlacementGroupFilter contains fields that can be used to filter results when getting placement groups.
type PlacementGroupFilter struct {
	ID              *int
	Project         *string
	Name            *string
	ExcludeMemberID *int64
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

// ToAPIBase populates base fields of the [PlacementGroup] into an [api.PlacementGroup] without querying for any additional data.
// This is so that additional fields can be populated elsewhere when performing bulk queries.
func (p PlacementGroup) ToAPIBase() api.PlacementGroup {
	return api.PlacementGroup{
		Name:        p.Name,
		Description: p.Description,
		Project:     p.Project,
	}
}

// ToAPI converts the [PlacementGroup] to an [api.PlacementGroup], querying for extra data as necessary.
func (p *PlacementGroup) ToAPI(ctx context.Context, tx *sql.Tx) (*api.PlacementGroup, error) {
	// Get config
	config, err := GetPlacementGroupConfig(ctx, tx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed getting placement group config: %w", err)
	}

	// Get used by
	usedBy, err := GetPlacementGroupUsedBy(ctx, tx, PlacementGroupFilter{Project: &p.Project, Name: &p.Name}, false)
	if err != nil {
		return nil, err
	}

	apiPlacementGroup := p.ToAPIBase()
	apiPlacementGroup.UsedBy = usedBy
	apiPlacementGroup.Config = config

	return &apiPlacementGroup, nil
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

	if filter.ExcludeMemberID != nil {
		q += " AND instances.node_id != ?"
		args = append(args, *filter.ExcludeMemberID)
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
