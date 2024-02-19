package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var permissionsCmd = APIEndpoint{
	Name: "permissions",
	Path: "auth/permissions",
	Get: APIEndpointAction{
		Handler:       getPermissions,
		AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanViewPermissions),
	},
}

// swagger:operation GET /1.0/auth/permissions?recursion=1 permissions permissions_get_recursion1
//
//	Get the permissions
//
//	Returns a list of available permissions (including groups that have those permissions).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: entity-type
//	    description: Type of entity
//	    type: string
//	    example: instance
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of permissions
//	          items:
//	            $ref: "#/definitions/PermissionInfo"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/permissions permissions permissions_get
//
//	Get the permissions
//
//	Returns a list of available permissions.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: entityType
//	    description: Type of entity
//	    type: string
//	    example: instance
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of permissions
//	          items:
//	            $ref: "#/definitions/Permission"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getPermissions(d *Daemon, r *http.Request) response.Response {
	projectNameFilter := r.URL.Query().Get("project")
	entityTypeFilter := r.URL.Query().Get("entity-type")
	recursion := r.URL.Query().Get("recursion")
	var entityTypes []entity.Type
	if entityTypeFilter != "" {
		entityType := entity.Type(entityTypeFilter)
		err := entityType.Validate()
		if err != nil {
			return response.BadRequest(fmt.Errorf("Invalid `entity-type` query parameter %q: %w", entityTypeFilter, err))
		}

		entityTypes = append(entityTypes, entityType)
	}

	var entityURLs map[entity.Type]map[int]*api.URL
	var permissions []cluster.Permission
	var groupsByPermissionID map[int][]cluster.AuthGroup
	err := d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		if projectNameFilter != "" {
			// Validate that the project exists first.
			_, err = cluster.GetProject(ctx, tx.Tx(), projectNameFilter)
			if err != nil {
				return err
			}
		}

		if recursion == "1" {
			permissions, err = cluster.GetPermissions(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed to get currently assigned permissions: %w", err)
			}

			groupsByPermissionID, err = cluster.GetAllAuthGroupsByPermissionID(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed to get groups by permission mapping: %w", err)
			}
		}

		entityURLs, err = cluster.GetEntityURLs(ctx, tx.Tx(), projectNameFilter, entityTypes...)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// If we're recursing, convert the groupsByPermissionID map into a map of cluster.Permission to list of group names.
	assignedPermissions := make(map[cluster.Permission][]string, len(groupsByPermissionID))
	if recursion == "1" {
		for permissionID, groups := range groupsByPermissionID {
			var perm cluster.Permission
			for _, p := range permissions {
				if permissionID == p.ID {
					perm = p

					// A permission is unique via its entity ID, entity type, and entitlement. Set the ID to zero
					// so we can create a map key from the entityURL map below.
					perm.ID = 0
					break
				}
			}

			groupNames := make([]string, 0, len(groups))
			for _, g := range groups {
				groupNames = append(groupNames, g.Name)
			}

			assignedPermissions[perm] = groupNames
		}
	}

	var apiPermissions []api.Permission
	var apiPermissionInfos []api.PermissionInfo
	for entityType, entities := range entityURLs {
		for entityID, entityURL := range entities {
			entitlements, err := auth.EntitlementsByEntityType(entityType)
			if err != nil {
				return response.InternalError(fmt.Errorf("Failed to list available entitlements for entity type %q: %w", entityType, err))
			}

			for _, entitlement := range entitlements {
				if recursion == "1" {
					permissionInfo := api.PermissionInfo{
						Permission: api.Permission{
							EntityType:      string(entityType),
							EntityReference: entityURL.String(),
							Entitlement:     string(entitlement),
						},
						// Get the groups from the assigned permissions map. We don't have the permission ID in scope
						// here. Thats why we set it to zero above.
						Groups: assignedPermissions[cluster.Permission{
							Entitlement: entitlement,
							EntityType:  cluster.EntityType(entityType),
							EntityID:    entityID,
						}],
					}

					apiPermissionInfos = append(apiPermissionInfos, permissionInfo)
				} else {
					apiPermissions = append(apiPermissions, api.Permission{
						EntityType:      string(entityType),
						EntityReference: entityURL.String(),
						Entitlement:     string(entitlement),
					})
				}
			}
		}
	}

	if recursion == "1" {
		return response.SyncResponse(true, apiPermissionInfos)
	}

	return response.SyncResponse(true, apiPermissions)
}
