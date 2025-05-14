package cluster

import (
	"context"
	"database/sql"
	"slices"

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

// GetClusterGroupUsedBy collates references to the cluster group with the given name.
// This currently only returns the URLs of projects whose `restricted.cluster.groups` configuration
// contains the cluster group.
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

		if slices.Contains(shared.SplitNTrimSpace(configValue, ",", -1, false), groupName) {
			projectURLs = append(projectURLs, entity.ProjectURL(projectName).String())
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return projectURLs, nil
}
