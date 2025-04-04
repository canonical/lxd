package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
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
	Addresses   IPAddresses
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
	apiClusterLink := &api.ClusterLink{
		Name:        r.Name,
		Addresses:   r.Addresses,
		Description: r.Description,
		Type:        string(r.Type),
	}

	return apiClusterLink, nil
}

// IPAddresses represents a slice of IP addresses stored as a comma-separated string in the database.
//
// This type implements the sql.Scanner and driver.Valuer interfaces to automatically handle
// conversion between an IP address slice in Go and a comma-separated string in the database.
// When reading from the database, comma-separated values are split into a slice of IP addresses.
// When writing to the database, slice elements are joined with commas.
type IPAddresses []net.IP

// Scan implements sql.Scanner for IPAddresses. This converts the string value back into the correct API slice or returns an error.
func (i *IPAddresses) Scan(value any) error {
	if value == nil {
		*i = nil
		return nil
	}

	switch v := value.(type) {
	case string:
		addresses := strings.Split(v, ",")
		ips := make([]net.IP, 0, len(addresses))
		for _, address := range addresses {
			address = strings.TrimSpace(address)

			ip := net.ParseIP(address)
			if ip == nil {
				continue
			}

			ips = append(ips, ip)
		}

		*i = ips
	default:
		return fmt.Errorf("failed to unmarshal IPAddresses: %T", v)
	}

	return nil
}

// Value implements driver.Valuer for IPAddresses. This converts the API slice into a string or throws an error.
func (i IPAddresses) Value() (driver.Value, error) {
	if len(i) == 0 {
		return "", nil
	}

	ips := make([]string, len(i))
	for idx, ip := range i {
		ips[idx] = ip.String()
	}

	return strings.Join(ips, ","), nil
}

// ClusterLinkType represents the type of a cluster link stored as a string in the database.
//
// This type implements the sql.Scanner and driver.Valuer interfaces to automatically handle
// conversion between cluster link type constants in Go and their string representation in the database.
// When reading from the database, string values are converted back to their constant type.
// When writing to the database, constants are converted to their string representation.
type ClusterLinkType string

// Scan implements sql.Scanner for ClusterLinkType. This converts the database string value back into the correct API constant or returns an error.
func (c *ClusterLinkType) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("Cluster link type cannot be null")
	}

	switch value {
	case "non-delegated":
		*c = api.ClusterLinkTypeNonDelegated
	case "delegated":
		*c = api.ClusterLinkTypeDelegated
	default:
		return fmt.Errorf("Unknown cluster link type %q", value)
	}

	return nil
}

// Value implements driver.Value for ClusterLinkType. This converts the API constant into its string database representation or throws an error.
func (c ClusterLinkType) Value() (driver.Value, error) {
	switch c {
	case api.ClusterLinkTypeNonDelegated:
		return "non-delegated", nil
	case api.ClusterLinkTypeDelegated:
		return "delegated", nil
	}

	return nil, fmt.Errorf("Invalid cluster link type %q", c)
}
