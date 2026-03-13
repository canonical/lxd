package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// Permission is the database representation of an api.Permission.
type Permission struct {
	ID          int
	GroupID     int64
	Entitlement auth.Entitlement
	EntityType  EntityType
	EntityID    int
}

// GetPermissionEntityURLs accepts a slice of Permission as input. The input Permission slice may include permissions
// that are no longer valid because the entity against which they are defined no longer exists. This method determines
// which permissions are valid and which are not valid by attempting to retrieve their entity URL. It uses as few
// queries as possible to do this. It returns a slice of valid permissions and a map of entity.Type, to entity ID, to
// api.URL. The returned map contains the URL of the entity of each returned valid permission. It is used for populating
// api.Permission. A warning is logged if any invalid permissions are found. And error is returned if any query returns
// unexpected error.
func GetPermissionEntityURLs(ctx context.Context, tx *sql.Tx, permissions []Permission) ([]Permission, map[entity.Type]map[int]*api.URL, error) {
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

		// Skip any permissions we have already evaluated. We've already checked that there is only one permission
		// for this entity type, so checking if the entity type is already a key in the map is enough.
		_, ok := entityURLs[entity.Type(permissions[0].EntityType)]
		if ok {
			continue
		}

		u, err := GetEntityURL(ctx, tx, entity.Type(entityType), permissions[0].EntityID)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, nil, err
		} else if err != nil {
			continue
		}

		entityURLs[entity.Type(entityType)] = make(map[int]*api.URL)
		entityURLs[entity.Type(entityType)][permissions[0].EntityID] = u
	}

	// If there are any entity types with multiple permissions, get all URLs for those entities.
	if len(entityTypes) > 0 {
		entityURLsAll, err := GetEntityURLs(ctx, tx, "", entityTypes...)
		if err != nil {
			return nil, nil, err
		}

		maps.Copy(entityURLs, entityURLsAll)
	}

	// Iterate over the input permissions and check which ones are present in the entityURLs map.
	// If they are not present, the entity against which they are defined is no longer present in the DB.
	validPermissions := make([]Permission, 0, len(permissions))
	danglingPermissions := make([]Permission, 0, len(permissions))
	for _, permission := range permissions {
		entityIDToURL, ok := entityURLs[entity.Type(permission.EntityType)]
		if !ok {
			danglingPermissions = append(danglingPermissions, permission)
			continue
		}

		_, ok = entityIDToURL[permission.EntityID]
		if !ok {
			danglingPermissions = append(danglingPermissions, permission)
			continue
		}

		validPermissions = append(validPermissions, permission)
	}

	// If there are any dangling permissions, log an appropriate warning message.
	if len(danglingPermissions) > 0 {
		permissionIDs := make([]int, 0, len(danglingPermissions))
		entityTypes := make([]EntityType, 0, len(danglingPermissions))
		for _, perm := range danglingPermissions {
			permissionIDs = append(permissionIDs, perm.ID)
			if !slices.Contains(entityTypes, perm.EntityType) {
				entityTypes = append(entityTypes, perm.EntityType)
			}
		}

		logger.Warn("Encountered dangling permissions", logger.Ctx{"permission_ids": permissionIDs, "entity_types": entityTypes})
	}

	return validPermissions, entityURLs, nil
}

// GetDistinctPermissionsByGroupNames gets all distinct permissions that the groups with the given names have been granted.
func GetDistinctPermissionsByGroupNames(ctx context.Context, tx *sql.Tx, groupNames []string) ([]Permission, error) {
	if len(groupNames) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(groupNames))
	for _, effectiveGroup := range groupNames {
		args = append(args, effectiveGroup)
	}

	q := `
SELECT DISTINCT auth_groups_permissions.entitlement, auth_groups_permissions.entity_type, auth_groups_permissions.entity_id
FROM auth_groups_permissions
JOIN auth_groups ON auth_groups_permissions.auth_group_id = auth_groups.id
WHERE auth_groups.name IN ` + query.Params(len(groupNames))

	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to query distinct permissions by group names: %w", err)
	}

	var permissions []Permission
	for rows.Next() {
		var permission Permission
		err := rows.Scan(&permission.Entitlement, &permission.EntityType, &permission.EntityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan effective permissions: %w", err)
		}

		permissions = append(permissions, permission)
	}

	return permissions, nil
}

// GetGroupPermissions returns a map of group name to slice of permissions. This is used by the OpenFGADatastore
// implementation. It is pre-loaded into an openfga.RequestCache to reduce the total number of queries.
func GetGroupPermissions(ctx context.Context, tx *sql.Tx) (map[string][]Permission, error) {
	q := `
SELECT auth_groups.name, auth_groups_permissions.entity_id, auth_groups_permissions.entity_type, auth_groups_permissions.entitlement
FROM auth_groups
JOIN auth_groups_permissions ON auth_groups_permissions.auth_group_id = auth_groups.id
`
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("Failed to query group permissions: %w", err)
	}

	groupPermissions := make(map[string][]Permission)
	for rows.Next() {
		var permission Permission
		var groupName string
		err := rows.Scan(&groupName, &permission.EntityID, &permission.EntityType, &permission.Entitlement)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan effective permissions: %w", err)
		}

		groupPermissions[groupName] = append(groupPermissions[groupName], permission)
	}

	return groupPermissions, nil
}
