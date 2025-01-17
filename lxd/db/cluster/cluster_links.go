package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"

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
//go:generate mapper stmt -e cluster_link delete-by-Name table=cluster_links
//go:generate mapper stmt -e cluster_link update table=cluster_links
//go:generate mapper stmt -e cluster_link rename table=cluster_links
//
//go:generate mapper method -i -e cluster_link GetMany
//go:generate mapper method -i -e cluster_link GetOne
//go:generate mapper method -i -e cluster_link ID
//go:generate mapper method -i -e cluster_link Exists
//go:generate mapper method -i -e cluster_link Create
//go:generate mapper method -i -e cluster_link DeleteOne-by-Name
//go:generate mapper method -i -e cluster_link Update
//go:generate mapper method -i -e cluster_link Rename
//go:generate goimports -w cluster_links.mapper.go
//go:generate goimports -w cluster_links.interface.mapper.go

// ClusterLink is the database representation of an api.ClusterLink.
type ClusterLink struct {
	ID          int
	IdentityID  int
	Name        string `db:"primary=true"`
	Addresses   Addresses
	Description string `db:"coalesce=''"`
	Type        ClusterLinkType
}

// ClusterLinkFilter contains fields upon which a cluster link can be filtered.
type ClusterLinkFilter struct {
	ID   *int
	Name *string
}

// ToAPI converts the database ClusterLink struct to API type.
func (r *ClusterLink) ToAPI(ctx context.Context, tx *sql.Tx) (*api.ClusterLink, error) {
	return &api.ClusterLink{
		Name:        r.Name,
		Addresses:   r.Addresses,
		Description: r.Description,
		Type:        string(r.Type),
	}, nil
}

// Addresses represents a slice of addresses stored as a comma-separated string in the database.
//
// This type implements the sql.Scanner and driver.Valuer interfaces to automatically handle
// conversion between a string slice in Go and a comma-separated string in the database.
// When reading from the database, comma-separated values are split into a slice of strings.
// When writing to the database, slice elements are joined with commas.
type Addresses []string

// Scan implements sql.Scanner for Addresses. This converts the string value back into the correct API slice or returns an error.
func (a *Addresses) Scan(value any) error {
	if value == nil {
		*a = nil
		return nil
	}

	switch v := value.(type) {
	case string:
		if v == "" {
			*a = nil
			return nil
		}

		addresses := strings.Split(v, ",")

		*a = addresses
	default:
		return fmt.Errorf("failed to unmarshal Addresses: %T", v)
	}

	return nil
}

// Value implements driver.Valuer for Addresses. This converts the API slice into a string.
func (a Addresses) Value() (driver.Value, error) {
	addresses := make([]string, 0, len(a))
	for _, address := range a {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}

		addresses = append(addresses, address)
	}

	return strings.Join(addresses, ","), nil
}

// ClusterLinkType represents the type of a cluster link stored as a string in the database.
//
// This type implements the sql.Scanner and driver.Value interfaces to automatically handle
// conversion between API constants and their int64 representation in the database.
// When reading from the database, int64 values are converted back to their constant type.
// When writing to the database, API constants are converted to their int64 representation.
type ClusterLinkType string

const (
	clusterLinkTypeNonDelegated int64 = 0
	clusterLinkTypeDelegated    int64 = 1
)

// Scan implements sql.Scanner for ClusterLinkType. This converts the database integer value value back into the correct API constant or returns an error.
func (c *ClusterLinkType) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("Cluster link type cannot be null")
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
	case clusterLinkTypeNonDelegated:
		*c = api.ClusterLinkTypeNonDelegated
	case clusterLinkTypeDelegated:
		*c = api.ClusterLinkTypeDelegated
	default:
		return fmt.Errorf("Unknown cluster link type `%d`", clusterLinkTypeInt)
	}

	return nil
}

// Value implements driver.Value for ClusterLinkType. This converts the API constant into its integer database representation or throws an error.
func (c ClusterLinkType) Value() (driver.Value, error) {
	switch c {
	case api.ClusterLinkTypeNonDelegated:
		return clusterLinkTypeNonDelegated, nil
	case api.ClusterLinkTypeDelegated:
		return clusterLinkTypeDelegated, nil
	}

	return nil, fmt.Errorf("Invalid cluster link type %q", c)
}
