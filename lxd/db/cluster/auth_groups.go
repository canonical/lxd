package cluster

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t auth_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e auth_group objects table=auth_groups
//go:generate mapper stmt -e auth_group objects-by-ID table=auth_groups
//go:generate mapper stmt -e auth_group objects-by-Name table=auth_groups
//go:generate mapper stmt -e auth_group id table=auth_groups
//go:generate mapper stmt -e auth_group create table=auth_groups
//go:generate mapper stmt -e auth_group delete-by-Name table=auth_groups
//go:generate mapper stmt -e auth_group update table=auth_groups
//go:generate mapper stmt -e auth_group rename table=auth_groups
//
//go:generate mapper method -i -e auth_group GetMany
//go:generate mapper method -i -e auth_group GetOne
//go:generate mapper method -i -e auth_group ID
//go:generate mapper method -i -e auth_group Exists
//go:generate mapper method -i -e auth_group Create
//go:generate mapper method -i -e auth_group DeleteOne-by-Name
//go:generate mapper method -i -e auth_group Update
//go:generate mapper method -i -e auth_group Rename

// AuthGroup is the database representation of an api.AuthGroup.
type AuthGroup struct {
	ID          int
	Name        string `db:"primary=true"`
	Description string
}

// AuthGroupFilter contains fields upon which an AuthGroup can be filtered.
type AuthGroupFilter struct {
	ID   *int
	Name *string
}

// ToAPI converts the Group to an api.AuthGroup, making extra database queries as necessary.
func (g *AuthGroup) ToAPI(ctx context.Context, tx *sql.Tx, canViewIdentity auth.PermissionChecker, canViewIDPGroup auth.PermissionChecker) (*api.AuthGroup, error) {
	group := &api.AuthGroup{
		Name:        g.Name,
		Description: g.Description,
	}

	permissions, err := GetPermissionsByAuthGroupID(ctx, tx, g.ID)
	if err != nil {
		return nil, err
	}

	permissions, entityURLs, err := GetPermissionEntityURLs(ctx, tx, permissions)
	if err != nil {
		return nil, err
	}

	apiPermissions := make([]api.Permission, 0, len(permissions))
	for _, p := range permissions {
		entityURLs, ok := entityURLs[entity.Type(p.EntityType)]
		if !ok {
			return nil, fmt.Errorf("Entity URLs missing for permissions with entity type %q", p.EntityType)
		}

		u, ok := entityURLs[p.EntityID]
		if !ok {
			return nil, fmt.Errorf("Entity URL missing for permission with entity type %q and entity ID `%d`", p.EntityType, p.EntityID)
		}

		apiPermissions = append(apiPermissions, api.Permission{
			EntityType:      string(p.EntityType),
			EntityReference: u.String(),
			Entitlement:     string(p.Entitlement),
		})
	}

	group.Permissions = apiPermissions

	identities, err := GetIdentitiesByAuthGroupID(ctx, tx, g.ID)
	if err != nil {
		return nil, err
	}

	group.Identities = make(map[string][]string)
	for _, identity := range identities {
		authenticationMethod := string(identity.AuthMethod)
		if canViewIdentity(entity.IdentityURL(authenticationMethod, identity.Identifier)) {
			group.Identities[authenticationMethod] = append(group.Identities[authenticationMethod], identity.Identifier)
		}
	}

	identityProviderGroups, err := GetIdentityProviderGroupsByGroupID(ctx, tx, g.ID)
	if err != nil {
		return nil, err
	}

	for _, idpGroup := range identityProviderGroups {
		if canViewIDPGroup(entity.IdentityProviderGroupURL(idpGroup.Name)) {
			group.IdentityProviderGroups = append(group.IdentityProviderGroups, idpGroup.Name)
		}
	}

	return group, nil
}

// GetIdentitiesByAuthGroupID returns the identities that are members of the group with the given ID.
func GetIdentitiesByAuthGroupID(ctx context.Context, tx *sql.Tx, groupID int) ([]Identity, error) {
	stmt := `
SELECT identities.id, identities.auth_method, identities.type, identities.identifier, identities.name, identities.metadata
FROM identities
JOIN identities_auth_groups ON identities.id = identities_auth_groups.identity_id
WHERE identities_auth_groups.auth_group_id = ?`

	var result []Identity
	dest := func(scan func(dest ...any) error) error {
		i := Identity{}
		err := scan(&i.ID, &i.AuthMethod, &i.Type, &i.Identifier, &i.Name, &i.Metadata)
		if err != nil {
			return err
		}

		result = append(result, i)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest, groupID)
	if err != nil {
		return nil, fmt.Errorf("Failed to get identities for the group with ID `%d`: %w", groupID, err)
	}

	return result, nil
}

// GetAllIdentitiesByAuthGroupIDs returns a map of group IDs to the identities that are members of the group with that ID.
func GetAllIdentitiesByAuthGroupIDs(ctx context.Context, tx *sql.Tx) (map[int][]Identity, error) {
	stmt := `
SELECT identities_auth_groups.auth_group_id, identities.id, identities.auth_method, identities.type, identities.identifier, identities.name, identities.metadata
FROM identities
JOIN identities_auth_groups ON identities.id = identities_auth_groups.identity_id`

	result := make(map[int][]Identity)
	dest := func(scan func(dest ...any) error) error {
		var groupID int
		i := Identity{}
		err := scan(&groupID, &i.ID, &i.AuthMethod, &i.Type, &i.Identifier, &i.Name, &i.Metadata)
		if err != nil {
			return err
		}

		result[groupID] = append(result[groupID], i)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed to get identities for all groups: %w", err)
	}

	return result, nil
}

// GetIdentityProviderGroupsByGroupID returns the identity provider groups that map to the group with the given ID.
func GetIdentityProviderGroupsByGroupID(ctx context.Context, tx *sql.Tx, groupID int) ([]IdentityProviderGroup, error) {
	stmt := `
SELECT identity_provider_groups.id, identity_provider_groups.name
FROM identity_provider_groups
JOIN auth_groups_identity_provider_groups ON identity_provider_groups.id = auth_groups_identity_provider_groups.identity_provider_group_id
WHERE auth_groups_identity_provider_groups.auth_group_id = ?`

	var result []IdentityProviderGroup
	dest := func(scan func(dest ...any) error) error {
		i := IdentityProviderGroup{}
		err := scan(&i.ID, &i.Name)
		if err != nil {
			return err
		}

		result = append(result, i)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest, groupID)
	if err != nil {
		return nil, fmt.Errorf("Failed to get identity provider groups for the group with ID `%d`: %w", groupID, err)
	}

	return result, nil
}

// GetAllIdentityProviderGroupsByGroupIDs returns a map of group IDs to the IdentityProviderGroups that map to the group with that ID.
func GetAllIdentityProviderGroupsByGroupIDs(ctx context.Context, tx *sql.Tx) (map[int][]IdentityProviderGroup, error) {
	stmt := `
SELECT auth_groups_identity_provider_groups.auth_group_id, identity_provider_groups.id, identity_provider_groups.name
FROM identity_provider_groups
JOIN auth_groups_identity_provider_groups ON identity_provider_groups.id = auth_groups_identity_provider_groups.identity_provider_group_id`

	result := make(map[int][]IdentityProviderGroup)
	dest := func(scan func(dest ...any) error) error {
		var groupID int
		i := IdentityProviderGroup{}
		err := scan(&groupID, &i.ID, &i.Name)
		if err != nil {
			return err
		}

		result[groupID] = append(result[groupID], i)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed to get identity provider groups for all groups: %w", err)
	}

	return result, nil
}

// GetPermissionsByAuthGroupID returns the permissions that belong to the group with the given ID.
func GetPermissionsByAuthGroupID(ctx context.Context, tx *sql.Tx, groupID int) ([]Permission, error) {
	var result []Permission
	dest := func(scan func(dest ...any) error) error {
		p := Permission{}
		err := scan(&p.ID, &p.GroupID, &p.Entitlement, &p.EntityType, &p.EntityID)
		if err != nil {
			return err
		}

		result = append(result, p)
		return nil
	}

	err := query.Scan(ctx, tx, `SELECT id, auth_group_id, entitlement, entity_type, entity_id FROM auth_groups_permissions WHERE auth_group_id = ?`, dest, groupID)
	if err != nil {
		return nil, fmt.Errorf("Failed to get permissions for the group with ID `%d`: %w", groupID, err)
	}

	return result, nil
}

// GetPermissions returns a map of group ID to the permissions that belong to the auth group with that ID.
func GetPermissions(ctx context.Context, tx *sql.Tx) ([]Permission, error) {
	stmt := `SELECT id, auth_group_id, entitlement, entity_type, entity_id FROM auth_groups_permissions`

	var result []Permission
	dest := func(scan func(dest ...any) error) error {
		p := Permission{}
		err := scan(&p.ID, &p.GroupID, &p.Entitlement, &p.EntityType, &p.EntityID)
		if err != nil {
			return err
		}

		result = append(result, p)
		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed to get permissions for all groups: %w", err)
	}

	return result, nil
}

// SetAuthGroupPermissions deletes all auth_group -> permission mappings from the `auth_group_permissions` table
// where the group ID is equal to the given value. Then it inserts a new row for each given permission ID.
func SetAuthGroupPermissions(ctx context.Context, tx *sql.Tx, groupID int, authGroupPermissions []Permission) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM auth_groups_permissions WHERE auth_group_id = ?`, groupID)
	if err != nil {
		return fmt.Errorf("Failed to delete existing permissions for group with ID `%d`: %w", groupID, err)
	}

	if len(authGroupPermissions) == 0 {
		return nil
	}

	for _, permission := range authGroupPermissions {
		_, err := tx.ExecContext(ctx, `INSERT INTO auth_groups_permissions (auth_group_id, entity_type, entity_id, entitlement) VALUES (?, ?, ?, ?);`, permission.GroupID, permission.EntityType, permission.EntityID, permission.Entitlement)
		if err != nil {
			return fmt.Errorf("Failed to write group permissions: %w", err)
		}
	}

	return nil
}
