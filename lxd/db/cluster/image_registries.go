package cluster

import (
	"database/sql/driver"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t image_registries.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e image_registry objects table=image_registries
//go:generate mapper stmt -e image_registry objects-by-ID table=image_registries
//go:generate mapper stmt -e image_registry objects-by-Name table=image_registries
//go:generate mapper stmt -e image_registry id table=image_registries
//go:generate mapper stmt -e image_registry create table=image_registries
//go:generate mapper stmt -e image_registry update table=image_registries
//go:generate mapper stmt -e image_registry delete-by-Name table=image_registries
//go:generate mapper stmt -e image_registry rename table=image_registries
//
//go:generate mapper method -i -e image_registry GetMany table=image_registries
//go:generate mapper method -i -e image_registry GetOne table=image_registries
//go:generate mapper method -i -e image_registry ID table=image_registries
//go:generate mapper method -i -e image_registry Exists table=image_registries
//go:generate mapper method -i -e image_registry Create table=image_registries
//go:generate mapper method -i -e image_registry Update table=image_registries
//go:generate mapper method -i -e image_registry DeleteOne-by-Name table=image_registries
//go:generate mapper method -i -e image_registry Rename talbe=image_registries
//go:generate goimports -w image_registries.mapper.go
//go:generate goimports -w image_registries.interface.mapper.go

// ImageRegistry is the database representation of an [api.ImageRegistry].
type ImageRegistry struct {
	ID            int64
	Name          string `db:"primary=yes"`
	Cluster       string `db:"coalesce=''&leftjoin=cluster_links.name"`
	URL           string
	SourceProject string `db:"coalesce=''"`
	Public        bool
	Protocol      ImageRegistryProtocol
}

// ImageRegistryFilter contains fields upon which an image registry can be filtered.
type ImageRegistryFilter struct {
	ID   *int64
	Name *string
}

// ToAPI converts the database [ImageRegistry] struct to API type [api.ImageRegistry].
func (r *ImageRegistry) ToAPI() *api.ImageRegistry {
	return &api.ImageRegistry{
		Name:          r.Name,
		Cluster:       r.Cluster,
		URL:           r.URL,
		Public:        r.Public,
		Protocol:      string(r.Protocol),
		SourceProject: r.SourceProject,
	}
}

// ImageRegistryProtocol represents the types of supported image registry protocols.
//
// This type implements the [sql.Scanner] and [driver.Value] interfaces to automatically handle
// conversion between API constants and their int64 representation in the database.
// When reading from the database, int64 values are converted back to their constant type.pick
// When writing to the database, API constants are converted to their int64 representation.
type ImageRegistryProtocol string

const (
	protocolSimpleStreams int64 = iota // Image registry protocol "SimpleStreams".
	protocolLXD                        // Image registry protocol "LXD".
)

// ScanInteger implements [query.IntegerScanner] for [ImageRegistryProtocol].
func (p *ImageRegistryProtocol) ScanInteger(protocolCode int64) error {
	switch protocolCode {
	case protocolSimpleStreams:
		*p = api.ImageRegistryProtocolSimpleStreams
	case protocolLXD:
		*p = api.ImageRegistryProtocolLXD
	default:
		return fmt.Errorf("Unknown image registry protocol `%d`", protocolCode)
	}

	return nil
}

// Scan implements [sql.Scanner] for [ImageRegistryProtocol]. This converts the database integer value back into the correct API constant or returns an error.
func (p *ImageRegistryProtocol) Scan(value any) error {
	return query.ScanValue(value, p, false)
}

// Value implements [driver.Value] for [ImageRegistryProtocol]. This converts the API constant into its integer database representation or throws an error.
func (p ImageRegistryProtocol) Value() (driver.Value, error) {
	switch p {
	case api.ImageRegistryProtocolSimpleStreams:
		return protocolSimpleStreams, nil
	case api.ImageRegistryProtocolLXD:
		return protocolLXD, nil
	}

	return nil, fmt.Errorf("Invalid image registry protocol %q", p)
}
