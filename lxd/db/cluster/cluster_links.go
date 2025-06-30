package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t cluster_links.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e cluster_link objects table=cluster_links
//go:generate mapper stmt -e cluster_link objects-by-ID table=cluster_links
//go:generate mapper stmt -e cluster_link objects-by-Name table=cluster_links
//go:generate mapper stmt -e cluster_link id table=cluster_links
//go:generate mapper stmt -e cluster_link create table=cluster_links
//go:generate mapper stmt -e cluster_link update table=cluster_links
//go:generate mapper stmt -e cluster_link delete-by-Name table=cluster_links
//go:generate mapper stmt -e cluster_link rename table=cluster_links
//
//go:generate mapper method -i -e cluster_link GetMany table=cluster_links
//go:generate mapper method -i -e cluster_link GetOne table=cluster_links
//go:generate mapper method -i -e cluster_link ID table=cluster_links
//go:generate mapper method -i -e cluster_link Exists table=cluster_links
//go:generate mapper method -i -e cluster_link Create table=cluster_links
//go:generate mapper method -i -e cluster_link Update table=cluster_links
//go:generate mapper method -i -e cluster_link DeleteOne-by-Name table=cluster_links
//go:generate mapper method -i -e cluster_link Rename table=cluster_links
//go:generate goimports -w cluster_links.mapper.go
//go:generate goimports -w cluster_links.interface.mapper.go

// ClusterLink is the database representation of an [api.ClusterLink].
type ClusterLink struct {
	ID          int
	IdentityID  int
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
	Type        ClusterLinkType
}

// ClusterLinkFilter contains fields upon which a cluster link can be filtered.
type ClusterLinkFilter struct {
	ID   *int
	Name *string
}

// ToAPI converts the database [ClusterLink] struct to API type [api.ClusterLink].
func (r *ClusterLink) ToAPI(ctx context.Context, tx *sql.Tx) (*api.ClusterLink, error) {
	clusterLink := &api.ClusterLink{
		Name:        r.Name,
		Description: r.Description,
		Type:        string(r.Type),
	}

	err := getClusterLinkConfig(ctx, tx, int64(r.ID), clusterLink)
	if err != nil {
		return nil, err
	}

	return clusterLink, nil
}

// ClusterLinkType represents the type of a cluster link stored as a string in the database.
//
// This type implements the [sql.Scanner] and [driver.Value] interfaces to automatically handle
// conversion between API constants and their int64 representation in the database.
// When reading from the database, int64 values are converted back to their constant type.
// When writing to the database, API constants are converted to their int64 representation.
type ClusterLinkType string

const (
	clusterLinkTypeUser      int64 = 0
	clusterLinkTypeDelegated int64 = 1
)

// Scan implements [sql.Scanner] for [ClusterLinkType]. This converts the database integer value back into the correct API constant or returns an error.
func (c *ClusterLinkType) Scan(value any) error {
	if value == nil {
		return errors.New("Cluster link type cannot be empty")
	}

	intValue, err := driver.Int32.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid cluster link type: %w", err)
	}

	clusterLinkTypeInt, ok := intValue.(int64)
	if !ok {
		return fmt.Errorf("Cluster link type should be an integer, got `%v` (%T)", intValue, intValue)
	}

	switch clusterLinkTypeInt {
	case clusterLinkTypeUser:
		*c = api.ClusterLinkTypeUser
	case clusterLinkTypeDelegated:
		*c = api.ClusterLinkTypeDelegated
	default:
		return fmt.Errorf("Unknown cluster link type `%d`", clusterLinkTypeInt)
	}

	return nil
}

// Value implements [driver.Value] for [ClusterLinkType]. This converts the API constant into its integer database representation or throws an error.
func (c ClusterLinkType) Value() (driver.Value, error) {
	switch c {
	case api.ClusterLinkTypeUser:
		return clusterLinkTypeUser, nil
	case api.ClusterLinkTypeDelegated:
		return clusterLinkTypeDelegated, nil
	}

	return nil, fmt.Errorf("Invalid cluster link type %q", c)
}

// CreateClusterLinkConfig creates config for a new cluster link with the given name.
func CreateClusterLinkConfig(ctx context.Context, tx *sql.Tx, name string, config map[string]string) error {
	id, err := GetClusterLinkID(ctx, tx, name)
	if err != nil {
		return err
	}

	err = clusterLinkConfigAdd(tx, id, config)
	if err != nil {
		return err
	}

	return nil
}

// UpdateClusterLinkConfig updates the cluster link with the given name, setting its config.
func UpdateClusterLinkConfig(ctx context.Context, tx *sql.Tx, name string, config map[string]string) error {
	id, err := GetClusterLinkID(ctx, tx, name)
	if err != nil {
		return err
	}

	err = clearClusterLinkConfig(tx, id)
	if err != nil {
		return err
	}

	err = clusterLinkConfigAdd(tx, id, config)
	if err != nil {
		return err
	}

	return nil
}

// getClusterLinkConfig populates the config map of the [api.ClusterLink] with the given ID.
func getClusterLinkConfig(ctx context.Context, tx *sql.Tx, clusterLinkID int64, clusterLink *api.ClusterLink) error {
	q := `
        SELECT key, value
        FROM cluster_links_config
		WHERE cluster_link_id=?
	`

	clusterLink.Config = map[string]string{}

	return query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := clusterLink.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for cluster link ID %d", key, clusterLinkID)
		}

		clusterLink.Config[key] = value

		return nil
	}, clusterLinkID)
}

// clusterLinkConfigAdd adds config to the cluster link with the given ID.
func clusterLinkConfigAdd(tx *sql.Tx, clusterLinkID int64, config map[string]string) error {
	str := "INSERT INTO cluster_links_config (cluster_link_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(clusterLinkID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// clearClusterLinkConfig removes any config from the cluster link with the given ID.
func clearClusterLinkConfig(tx *sql.Tx, clusterLinkID int64) error {
	_, err := tx.Exec(
		"DELETE FROM cluster_links_config WHERE cluster_link_id=?", clusterLinkID)
	if err != nil {
		return err
	}

	return nil
}
