package cluster

import (
	"github.com/canonical/lxd/lxd/auth"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t permissions.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e permission objects
//go:generate mapper stmt -e permission objects-by-ID
//go:generate mapper stmt -e permission objects-by-EntityType
//go:generate mapper stmt -e permission objects-by-EntityType-and-EntityID
//go:generate mapper stmt -e permission objects-by-EntityType-and-EntityID-and-Entitlement
//
//go:generate mapper method -i -e permission GetMany
//go:generate mapper method -i -e permission GetOne

// Permission is the database representation of an api.Permission.
type Permission struct {
	ID          int
	Entitlement auth.Entitlement `db:"primary=true"`
	EntityType  EntityType       `db:"primary=true"`
	EntityID    int              `db:"primary=true"`
}

// PermissionFilter contains the fields upon which a Permission may be filtered.
type PermissionFilter struct {
	ID          *int
	Entitlement *auth.Entitlement
	EntityType  *EntityType
	EntityID    *int
}
