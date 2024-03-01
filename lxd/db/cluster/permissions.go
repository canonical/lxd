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

// Permission is the database representation of an api.Permission.
type Permission struct {
	ID          int
	GroupID     int
	Entitlement auth.Entitlement
	EntityType  EntityType
	EntityID    int
}

// GetPermissionEntityURLs accepts a slice of Permission and returns a map of entity.Type, to entity ID, to api.URL.
// The returned map contains the URL of the entity of each given permission. It is used for populating api.Permission.
func GetPermissionEntityURLs(ctx context.Context, tx *sql.Tx, permissions []Permission) (map[entity.Type]map[int]*api.URL, error) {
	// To make as few calls as possible, categorize the permissions by entity type.
	permissionsByEntityType := map[EntityType][]Permission{}
	for _, permission := range permissions {
		permissionsByEntityType[permission.EntityType] = append(permissionsByEntityType[permission.EntityType], permission)
	}

	// For each entity type, if there is only on permission for the entity type, we'll get the URL by its entity type and ID.
	// If there are multiple permissions for the entity type, append the entity type to a list for later use.
	entityURLs := make(map[entity.Type]map[int]*api.URL)
	var entityTypes []entity.Type
	for entityType, permissions := range permissionsByEntityType {
		if len(permissions) > 1 {
			entityTypes = append(entityTypes, entity.Type(entityType))
			continue
		}

		u, err := GetEntityURL(ctx, tx, entity.Type(entityType), permissions[0].EntityID)
		if err != nil {
			return nil, err
		}

		entityURLs[entity.Type(entityType)] = make(map[int]*api.URL)
		entityURLs[entity.Type(entityType)][permissions[0].EntityID] = u
	}

	// If there are any entity types with multiple permissions, get all URLs for those entities.
	if len(entityTypes) > 0 {
		entityURLsAll, err := GetEntityURLs(ctx, tx, "", entityTypes...)
		if err != nil {
			return nil, err
		}

		for k, v := range entityURLsAll {
			entityURLs[k] = v
		}
	}

	return entityURLs, nil
}

// GetAllAuthGroupsByPermissionID returns a map of all permission IDs to a slice of groups that have that permission.
func GetAllAuthGroupsByPermissionID(ctx context.Context, tx *sql.Tx) (map[int][]AuthGroup, error) {
	stmt := `
SELECT auth_groups_permissions.permission_id, auth_groups.id, auth_groups.name, auth_groups.description 
FROM auth_groups 
JOIN auth_groups_permissions ON auth_groups.id = auth_groups_permissions.auth_group_id`

	result := make(map[int][]AuthGroup)
	dest := func(scan func(dest ...any) error) error {
		var permissionID int
		p := AuthGroup{}
		err := scan(&permissionID, &p.ID, &p.Name, &p.Description)
		if err != nil {
			return err
		}

		result[permissionID] = append(result[permissionID], p)
		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed to get permissions for all groups: %w", err)
	}

	return result, nil
}
