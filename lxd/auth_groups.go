package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var authGroupsCmd = APIEndpoint{
	Name:        "auth_groups",
	Path:        "auth/groups",
	MetricsType: entity.TypeIdentity,
	Get: APIEndpointAction{
		Handler:       getAuthGroups,
		AccessHandler: allowAuthenticated,
	},
	Post: APIEndpointAction{
		Handler:       createAuthGroup,
		AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanCreateGroups),
	},
}

var authGroupCmd = APIEndpoint{
	Name:        "auth_group",
	Path:        "auth/groups/{groupName}",
	MetricsType: entity.TypeIdentity,
	Get: APIEndpointAction{
		Handler:       getAuthGroup,
		AccessHandler: allowPermission(entity.TypeAuthGroup, auth.EntitlementCanView, "groupName"),
	},
	Put: APIEndpointAction{
		Handler:       updateAuthGroup,
		AccessHandler: allowPermission(entity.TypeAuthGroup, auth.EntitlementCanEdit, "groupName"),
	},
	Post: APIEndpointAction{
		Handler:       renameAuthGroup,
		AccessHandler: allowPermission(entity.TypeAuthGroup, auth.EntitlementCanEdit, "groupName"),
	},
	Delete: APIEndpointAction{
		Handler:       deleteAuthGroup,
		AccessHandler: allowPermission(entity.TypeAuthGroup, auth.EntitlementCanDelete, "groupName"),
	},
	Patch: APIEndpointAction{
		Handler:       patchAuthGroup,
		AccessHandler: allowPermission(entity.TypeAuthGroup, auth.EntitlementCanEdit, "groupName"),
	},
}

func validateGroupName(name string) error {
	if name == "" {
		return api.StatusErrorf(http.StatusBadRequest, "Group name cannot be empty")
	}

	if strings.Contains(name, "/") {
		return api.StatusErrorf(http.StatusBadRequest, "Group name cannot contain a forward slash")
	}

	if strings.Contains(name, ":") {
		return api.StatusErrorf(http.StatusBadRequest, "Group name cannot contain a colon")
	}

	return nil
}

// swagger:operation GET /1.0/auth/groups auth_groups auth_groups_get
//
//	Get the groups
//
//	Returns a list of authorization groups (URLs).
//
//	---
//	produces:
//	  - application/json
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
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/auth/groups/foo",
//	              "/1.0/auth/groups/bar"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/groups?recursion=1 auth_groups auth_groups_get_recursion1
//
//	Get the groups
//
//	Returns a list of authorization groups.
//
//	---
//	produces:
//	  - application/json
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
//	          description: List of auth groups
//	          items:
//	            $ref: "#/definitions/AuthGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getAuthGroups(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)
	s := d.State()

	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeAuthGroup, true)
	if err != nil {
		return response.SmartError(err)
	}

	var groups []dbCluster.AuthGroup
	var authGroupPermissions []dbCluster.Permission
	groupsIdentities := make(map[int][]dbCluster.Identity)
	groupsIdentityProviderGroups := make(map[int][]dbCluster.IdentityProviderGroup)
	entityURLs := make(map[entity.Type]map[int]*api.URL)
	err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		allGroups, err := dbCluster.GetAuthGroups(ctx, tx.Tx())
		if err != nil {
			return err
		}

		groups = make([]dbCluster.AuthGroup, 0, len(groups))
		for _, group := range allGroups {
			if canViewGroup(entity.AuthGroupURL(group.Name)) {
				groups = append(groups, group)
			}
		}

		if len(groups) == 0 {
			return nil
		}

		if recursion {
			// If recursing, we need all identities for all groups, all IDP groups for all groups,
			// all permissions for all groups, and finally the URLs that those permissions apply to.
			groupsIdentities, err = dbCluster.GetAllIdentitiesByAuthGroupIDs(ctx, tx.Tx())
			if err != nil {
				return err
			}

			groupsIdentityProviderGroups, err = dbCluster.GetAllIdentityProviderGroupsByGroupIDs(ctx, tx.Tx())
			if err != nil {
				return err
			}

			authGroupPermissions, err = dbCluster.GetPermissions(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// Get the EntityURLs for the permissions.
			authGroupPermissions, entityURLs, err = dbCluster.GetPermissionEntityURLs(ctx, tx.Tx(), authGroupPermissions)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion {
		authGroupPermissionsByGroupID := make(map[int][]dbCluster.Permission, len(groups))
		for _, permission := range authGroupPermissions {
			authGroupPermissionsByGroupID[permission.GroupID] = append(authGroupPermissionsByGroupID[permission.GroupID], permission)
		}
		// We need to allocate a slice of pointer to api.AuthGroup because
		// these records will be modified in place by the reportEntitlements function.
		// We'll then return a slice of api.AuthGroup as an API response.
		apiGroups := make([]*api.AuthGroup, 0, len(groups))
		urlToGroup := make(map[*api.URL]auth.EntitlementReporter, len(groups))
		for _, group := range groups {
			var apiPermissions []api.Permission

			// The group may not have any permissions.
			permissions, ok := authGroupPermissionsByGroupID[group.ID]
			if ok {
				apiPermissions = make([]api.Permission, 0, len(permissions))
				for _, permission := range permissions {
					apiPermissions = append(apiPermissions, api.Permission{
						EntityType:      string(permission.EntityType),
						EntityReference: entityURLs[entity.Type(permission.EntityType)][permission.EntityID].String(),
						Entitlement:     string(permission.Entitlement),
					})
				}
			}

			apiIdentities := make(map[string][]string)
			for _, identity := range groupsIdentities[group.ID] {
				authenticationMethod := string(identity.AuthMethod)
				if canViewIdentity(entity.IdentityURL(authenticationMethod, identity.Identifier)) {
					apiIdentities[authenticationMethod] = append(apiIdentities[authenticationMethod], identity.Identifier)
				}
			}

			idpGroups := make([]string, 0, len(groupsIdentityProviderGroups[group.ID]))
			for _, idpGroup := range groupsIdentityProviderGroups[group.ID] {
				if canViewIDPGroup(entity.IdentityProviderGroupURL(idpGroup.Name)) {
					idpGroups = append(idpGroups, idpGroup.Name)
				}
			}

			group := &api.AuthGroup{
				Name:                   group.Name,
				Description:            group.Description,
				Permissions:            apiPermissions,
				Identities:             apiIdentities,
				IdentityProviderGroups: idpGroups,
			}

			apiGroups = append(apiGroups, group)
			urlToGroup[entity.AuthGroupURL(group.Name)] = group
		}

		if len(withEntitlements) > 0 {
			err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeAuthGroup, withEntitlements, urlToGroup)
			if err != nil {
				return response.SmartError(err)
			}
		}

		return response.SyncResponse(true, apiGroups)
	}

	groupURLs := make([]string, 0, len(groups))
	for _, group := range groups {
		groupURLs = append(groupURLs, entity.AuthGroupURL(group.Name).String())
	}

	return response.SyncResponse(true, groupURLs)
}

// swagger:operation POST /1.0/auth/groups auth_groups auth_groups_post
//
//	Create a new authorization group
//
//	Creates a new authorization group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: group
//	    description: Group request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/AuthGroupsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func createAuthGroup(d *Daemon, r *http.Request) response.Response {
	var group api.AuthGroupsPost
	err := json.NewDecoder(r.Body).Decode(&group)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid request body: %w", err))
	}

	err = validateGroupName(group.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = validatePermissions(group.Permissions)
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	s := d.State()
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		groupID, err := dbCluster.CreateAuthGroup(ctx, tx.Tx(), dbCluster.AuthGroup{
			Name:        group.Name,
			Description: group.Description,
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return api.StatusErrorf(http.StatusConflict, "Authorization group %q already exists", group.Name)
			}

			return err
		}

		err = upsertPermissions(ctx, tx.Tx(), int(groupID), group.Permissions)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the group creation
	lc := lifecycle.AuthGroupCreated.Event(group.Name, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	return response.SyncResponseLocation(true, nil, entity.AuthGroupURL(group.Name).String())
}

// swagger:operation GET /1.0/auth/groups/{groupName} auth_groups auth_group_get
//
//	Get the authorization group
//
//	Gets a specific authorization group.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
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
//	          $ref: "#/definitions/AuthGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getAuthGroup(d *Daemon, r *http.Request) response.Response {
	groupName, err := url.PathUnescape(mux.Vars(r)["groupName"])
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeAuthGroup, false)
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var apiGroup *api.AuthGroup
	s := d.State()
	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetAuthGroup(ctx, tx.Tx(), groupName)
		if err != nil {
			return err
		}

		apiGroup, err = group.ToAPI(ctx, tx.Tx(), canViewIdentity, canViewIDPGroup)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeAuthGroup, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.AuthGroupURL(groupName): apiGroup})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, *apiGroup, *apiGroup)
}

// swagger:operation PUT /1.0/auth/groups/{groupName} auth_groups auth_group_put
//
//	Update the authorization group
//
//	Replaces the editable fields of an authorization group
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: group
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/AuthGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func updateAuthGroup(d *Daemon, r *http.Request) response.Response {
	groupName, err := url.PathUnescape(mux.Vars(r)["groupName"])
	if err != nil {
		return response.SmartError(err)
	}

	var groupPut api.AuthGroupPut
	err = json.NewDecoder(r.Body).Decode(&groupPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid request body: %w", err))
	}

	err = validatePermissions(groupPut.Permissions)
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	s := d.State()
	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetAuthGroup(ctx, tx.Tx(), groupName)
		if err != nil {
			return err
		}

		apiGroup, err := group.ToAPI(ctx, tx.Tx(), canViewIdentity, canViewIDPGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, *apiGroup)
		if err != nil {
			return err
		}

		err = dbCluster.UpdateAuthGroup(ctx, tx.Tx(), groupName, dbCluster.AuthGroup{
			Name:        groupName,
			Description: groupPut.Description,
		})
		if err != nil {
			return err
		}

		err = upsertPermissions(ctx, tx.Tx(), group.ID, groupPut.Permissions)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the group update
	lc := lifecycle.AuthGroupUpdated.Event(groupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/auth/groups/{groupName} auth_groups auth_group_patch
//
//	Partially update the authorization group
//
//	Updates the editable fields of an authorization group
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: group
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/AuthGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func patchAuthGroup(d *Daemon, r *http.Request) response.Response {
	groupName, err := url.PathUnescape(mux.Vars(r)["groupName"])
	if err != nil {
		return response.SmartError(err)
	}

	var groupPut api.AuthGroupPut
	err = json.NewDecoder(r.Body).Decode(&groupPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid request body: %w", err))
	}

	err = validatePermissions(groupPut.Permissions)
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	s := d.State()
	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetAuthGroup(ctx, tx.Tx(), groupName)
		if err != nil {
			return err
		}

		apiGroup, err := group.ToAPI(ctx, tx.Tx(), canViewIdentity, canViewIDPGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, *apiGroup)
		if err != nil {
			return err
		}

		if groupPut.Description != "" {
			err = dbCluster.UpdateAuthGroup(ctx, tx.Tx(), groupName, dbCluster.AuthGroup{
				Name:        groupName,
				Description: groupPut.Description,
			})
			if err != nil {
				return err
			}
		}

		newPermissions := make([]api.Permission, 0, len(groupPut.Permissions))
		for _, permission := range groupPut.Permissions {
			if !slices.Contains(apiGroup.Permissions, permission) {
				newPermissions = append(newPermissions, permission)
			}
		}

		err = upsertPermissions(ctx, tx.Tx(), group.ID, newPermissions)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the group update
	lc := lifecycle.AuthGroupUpdated.Event(groupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/auth/groups/{groupName} auth_groups auth_group_post
//
//	Rename the authorization group
//
//	Renames the authorization group
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: group
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/AuthGroupPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func renameAuthGroup(d *Daemon, r *http.Request) response.Response {
	groupName, err := url.PathUnescape(mux.Vars(r)["groupName"])
	if err != nil {
		return response.SmartError(err)
	}

	var groupPost api.AuthGroupPost
	err = json.NewDecoder(r.Body).Decode(&groupPost)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid request body: %w", err))
	}

	err = validateGroupName(groupPost.Name)
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	s := d.State()
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.RenameAuthGroup(ctx, tx.Tx(), groupName, groupPost.Name)
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			return response.Conflict(fmt.Errorf("Authorization group %q already exists", groupPost.Name))
		}

		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
	if err != nil {
		return response.SmartError(err)
	}

	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		_, _, err := client.RawQuery(http.MethodPost, "/internal/identity-cache-refresh", nil, "")
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// When a group is renamed we need to update the list of group names associated with each identity in the cache.
	// When a group is otherwise modified, the name is unchanged, so the cache doesn't need to be updated.
	// When a group is created, no identities are a member of it yet, so the cache doesn't need to be updated.
	s.UpdateIdentityCache()

	// Send a lifecycle event for the group rename
	lc := lifecycle.AuthGroupRenamed.Event(groupPost.Name, request.CreateRequestor(r.Context()), map[string]any{"old_name": groupName})
	s.Events.SendLifecycle("", lc)

	return response.SyncResponseLocation(true, nil, entity.AuthGroupURL(groupPost.Name).String())
}

// swagger:operation DELETE /1.0/auth/groups/{groupName} auth_groups auth_group_delete
//
//	Delete the authorization group
//
//	Deletes the authorization group
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deleteAuthGroup(d *Daemon, r *http.Request) response.Response {
	groupName, err := url.PathUnescape(mux.Vars(r)["groupName"])
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	s := d.State()
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteAuthGroup(ctx, tx.Tx(), groupName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
	if err != nil {
		return response.SmartError(err)
	}

	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		_, _, err := client.RawQuery(http.MethodPost, "/internal/identity-cache-refresh", nil, "")
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// When a group is deleted we need to remove it from the list of groups names associated with each identity in the cache.
	// (When a group is created, nobody is a member of it yet, so the cache doesn't need to be updated).
	s.UpdateIdentityCache()

	// Send a lifecycle event for the group deletion
	lc := lifecycle.AuthGroupDeleted.Event(groupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	return response.EmptySyncResponse
}

// validatePermissions checks that a) the entity type exists, b) the entitlement exists, c) then entity type matches the
// entity reference (URL), and d) that the entitlement is valid for the entity type.
func validatePermissions(permissions []api.Permission) error {
	for _, permission := range permissions {
		entityType := entity.Type(permission.EntityType)
		err := entityType.Validate()
		if err != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Failed to validate entity type for permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		u, err := url.Parse(permission.EntityReference)
		if err != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Failed to parse permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		referenceEntityType, _, _, _, err := entity.ParseURL(*u)
		if err != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Failed to parse permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		if entityType != referenceEntityType {
			return api.StatusErrorf(http.StatusBadRequest, "Failed to parse permission with entity reference %q and entitlement %q: Entity type does not correspond to entity reference", permission.EntityReference, permission.Entitlement)
		}

		err = auth.ValidateEntitlement(entityType, auth.Entitlement(permission.Entitlement))
		if err != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Failed to validate group permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}
	}

	return nil
}

// upsertPermissions converts the given slice of api.Permission into a slice of cluster.Permission by resolving
// the URLs of each permission to an entity ID. Then sets those permissions against the group with the given ID.
func upsertPermissions(ctx context.Context, tx *sql.Tx, groupID int, permissions []api.Permission) error {
	entityReferences := make(map[*api.URL]*dbCluster.EntityRef, len(permissions))
	permissionToURL := make(map[api.Permission]*api.URL, len(permissions))
	for _, permission := range permissions {
		u, err := url.Parse(permission.EntityReference)
		if err != nil {
			return fmt.Errorf("Failed to parse permission entity reference: %w", err)
		}

		apiURL := &api.URL{URL: *u}
		entityReferences[apiURL] = &dbCluster.EntityRef{}
		permissionToURL[permission] = apiURL
	}

	err := dbCluster.PopulateEntityReferencesFromURLs(ctx, tx, entityReferences)
	if err != nil {
		return err
	}

	authGroupPermissions := make([]dbCluster.Permission, 0, len(permissions))
	for permission, apiURL := range permissionToURL {
		entitlement := auth.Entitlement(permission.Entitlement)
		entityType := dbCluster.EntityType(permission.EntityType)
		entityRef, ok := entityReferences[apiURL]
		if !ok {
			return api.StatusErrorf(http.StatusBadRequest, "Missing entity ID for permission with URL %q", permission.EntityReference)
		}

		authGroupPermissions = append(authGroupPermissions, dbCluster.Permission{
			Entitlement: entitlement,
			EntityType:  entityType,
			EntityID:    entityRef.EntityID,
		})
	}

	err = dbCluster.SetAuthGroupPermissions(ctx, tx, groupID, authGroupPermissions)
	if err != nil {
		return fmt.Errorf("Failed to set group permissions: %w", err)
	}

	return nil
}
