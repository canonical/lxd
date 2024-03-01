package cluster

import (
	"context"
	"database/sql"

	"github.com/canonical/lxd/lxd/auth"
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
