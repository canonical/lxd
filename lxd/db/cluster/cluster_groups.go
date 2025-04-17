package cluster

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

//go:generate -command mapper lxd-generate db mapper -t cluster_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e cluster_group objects table=cluster_groups
//go:generate mapper stmt -e cluster_group objects-by-Name table=cluster_groups
//go:generate mapper stmt -e cluster_group id table=cluster_groups
//go:generate mapper stmt -e cluster_group create table=cluster_groups
//go:generate mapper stmt -e cluster_group rename table=cluster_groups
//go:generate mapper stmt -e cluster_group delete-by-Name table=cluster_groups
//go:generate mapper stmt -e cluster_group update table=cluster_groups
//
//go:generate mapper method -i -e cluster_group GetMany
//go:generate mapper method -i -e cluster_group GetOne
//go:generate mapper method -i -e cluster_group ID
//go:generate mapper method -i -e cluster_group Exists
//go:generate mapper method -i -e cluster_group Rename
//go:generate mapper method -i -e cluster_group Create
//go:generate mapper method -i -e cluster_group Update
//go:generate mapper method -i -e cluster_group DeleteOne-by-Name

//go:generate goimports -w cluster_groups.mapper.go
//go:generate goimports -w cluster_groups.interface.mapper.go

// ClusterGroup is a value object holding db-related details about a cluster group.
type ClusterGroup struct {
	ID          int
	Name        string
	Description string   `db:"coalesce=''"`
	Nodes       []string `db:"ignore"`
}

// ClusterGroupFilter specifies potential query parameter fields.
type ClusterGroupFilter struct {
	ID   *int
	Name *string
}

// ToAPI returns a LXD API entry.
func (c *ClusterGroup) ToAPI(ctx context.Context, tx *sql.Tx) (*api.ClusterGroup, error) {
	usedBy, err := GetClusterGroupUsedBy(ctx, tx, c.Name)
	if err != nil {
		return nil, err
	}

	result := api.ClusterGroup{
		Name:        c.Name,
		Description: c.Description,
		Members:     c.Nodes,
		UsedBy:      usedBy,
	}

	return &result, nil
}

// GetClusterGroupUsedBy collates references to the cluster group with the given name. This returns the URLs of projects
// whose `restricted.cluster.groups` configuration contains the cluster group, and URLs of placement rulesets containing
// rules whose selectors reference the cluster group.
func GetClusterGroupUsedBy(ctx context.Context, tx *sql.Tx, groupName string) ([]string, error) {
	q := `
SELECT projects.name, projects_config.value FROM projects 
JOIN projects_config ON projects.id = projects_config.project_id 
WHERE projects_config.key = 'restricted.cluster.groups'`

	var projectURLs []string
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var projectName string
		var configValue string
		err := scan(&projectName, &configValue)
		if err != nil {
			return err
		}

		if shared.ValueInSlice(groupName, shared.SplitNTrimSpace(configValue, ",", -1, false)) {
			projectURLs = append(projectURLs, entity.ProjectURL(projectName).String())
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	projectToRulesets, err := GetClusterGroupPlacementRulesetReferences(ctx, tx, groupName)
	if err != nil {
		return nil, err
	}

	var placementRulesetURLs []string
	for projectName, rulesets := range projectToRulesets {
		for _, rulesetName := range rulesets {
			u := api.NewURL().Project(projectName).Path("1.0", "placement-rulesets", rulesetName).String()
			placementRulesetURLs = append(placementRulesetURLs, u)
		}
	}

	return append(projectURLs, placementRulesetURLs...), nil
}

// GetClusterGroupPlacementRulesetReferences returns a map of project name to list of placement ruleset names where the
// ruleset contains a rule with a selector that references the given cluster group by name.
func GetClusterGroupPlacementRulesetReferences(ctx context.Context, tx *sql.Tx, groupName string) (map[string][]string, error) {
	q := `
SELECT placement_rulesets.name, projects.name, placement_rules_selectors.matchers FROM placement_rulesets
JOIN projects ON placement_rulesets.project_id = projects.id
JOIN placement_rules ON placement_rulesets.id = placement_rules.ruleset_id
JOIN placement_rules_selectors ON placement_rules.id = placement_rules_selectors.placement_rule_id
WHERE placement_rules_selectors.entity_type = ` + strconv.Itoa(int(entityTypeCodeClusterGroup)) + `
`

	placementRulesetReferences := make(map[string][]string)
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var rulesetName string
		var projectName string
		var matchers SelectorMatchers
		err := scan(&rulesetName, &projectName, &matchers)
		if err != nil {
			return err
		}

		for _, matcher := range matchers {
			if matcher.Property != "name" {
				continue
			}

			if !shared.ValueInSlice(groupName, matcher.Values) {
				continue
			}

			if shared.ValueInSlice(rulesetName, placementRulesetReferences[projectName]) {
				continue
			}

			placementRulesetReferences[projectName] = append(placementRulesetReferences[projectName], rulesetName)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return placementRulesetReferences, nil
}
