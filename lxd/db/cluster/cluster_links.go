package cluster

import (
	"database/sql/driver"
	"errors"
	"fmt"

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
