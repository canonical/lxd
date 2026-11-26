package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/request/security"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var authGroupsCmd = APIEndpoint{
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

// isServerAdminPermission returns true if p grants the "admin" entitlement on the server entity.
func isServerAdminPermission(p api.Permission) bool {
	return p.EntityType == string(entity.TypeServer) && p.Entitlement == string(auth.EntitlementAdmin)
}

// hasServerAdminPermission returns true if permissions contains the server admin permission.
func hasServerAdminPermission(permissions []api.Permission) bool {
	return slices.ContainsFunc(permissions, isServerAdminPermission)
}

// validateAdminsGroupPermissions ensures that the desired permissions for the admins group consist of
// exactly the server admin permission (and nothing else). This is used when creating the admins group or
// when granting the server admin permission to an admins group that does not yet have it.
func validateAdminsGroupPermissions(permissions []api.Permission) error {
	if len(permissions) != 1 || !isServerAdminPermission(permissions[0]) {
		return api.StatusErrorf(http.StatusBadRequest, "The admins group can only be granted the server admin permission")
	}

	return nil
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
//	parameters:
//	  - in: query
//	    name: image_alias_types
//	    type: integer
//	    enum: [0, 1]
//	    description: 'Return typed image alias permission URLs (API extension: image_alias_types).'
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
	recursion, _ := util.IsRecursionRequest(r)
	s := d.State()

	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeAuthGroup, true)
	if err != nil {
		return response.SmartError(err)
	}

	var groups []dbCluster.AuthGroupsRow
	var groupURLs []string
	var authGroupPermissions []dbCluster.Permission
	groupsIdentities := make(map[int64][]dbCluster.IdentitiesRow)
	groupsIdentityProviderGroups := make(map[int64][]dbCluster.IdentityProviderGroupsRow)
	entityURLs := make(map[entity.Type]map[int]*api.URL)
	err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		groups, groupURLs, err = dbCluster.GetAuthGroupsAndURLs(ctx, tx.Tx(), func(group dbCluster.AuthGroupsRow) bool {
			return canViewGroup(entity.AuthGroupURL(group.Name))
		})
		if err != nil {
			return err
		}

		if recursion == 0 || len(groups) == 0 {
			return nil
		}

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
		if r.URL.Query().Get("image_alias_types") == "1" {
			authGroupPermissions, entityURLs, err = dbCluster.GetPermissionEntityURLs(ctx, tx.Tx(), authGroupPermissions)
		} else {
			authGroupPermissions, entityURLs, err = dbCluster.GetAuthGroupPermissionEntityURLs(ctx, tx.Tx(), authGroupPermissions)
		}

		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, groupURLs)
	}

	authGroupPermissionsByGroupID := make(map[int64][]dbCluster.Permission, len(groups))
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

	// The admins group is a built-in group. It may be created without permissions (e.g. by users
	// upgrading from a version of LXD that predates the built-in admins group), but if permissions
	// are provided at creation time they must consist of exactly the server admin permission.
	if group.Name == api.AuthGroupAdminsName && len(group.Permissions) > 0 {
		err = validateAdminsGroupPermissions(group.Permissions)
		if err != nil {
			return response.SmartError(err)
		}
	}

	s := d.State()
	validatedPermissions, err := validatePermissions(r.Context(), s, group.Permissions)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		groupID, err := query.Create(ctx, tx.Tx(), dbCluster.AuthGroupsRow{
			Name:        group.Name,
			Description: group.Description,
		})
		if err != nil {
			return err
		}

		err = dbCluster.SetAuthGroupPermissions(ctx, tx.Tx(), groupID, validatedPermissions)
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

	secEvt := security.AuthzAdmin.WithSuffix("group_create", group.Name).UserEvent(r.Context(), security.LevelInfo, "Authorization group created")
	s.Events.SendSecurity(secEvt)

	return response.SyncResponseLocation(true, nil, entity.AuthGroupURL(group.Name).String())
}

// swagger:operation GET /1.0/auth/groups/{groupName} auth_groups auth_group_get
//
//	Get the authorization group
//
//	Gets a specific authorization group.
//
//	---
//	parameters:
//	  - in: query
//	    name: image_alias_types
//	    type: integer
//	    enum: [0, 1]
//	    description: 'Return typed image alias permission URLs (API extension: image_alias_types).'
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
	groupName := r.PathValue("groupName")
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeAuthGroup, false)
	if err != nil {
		return response.SmartError(err)
	}

	var apiGroup *api.AuthGroup
	var etagPermissions []api.Permission
	s := d.State()
	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetAuthGroup(ctx, tx.Tx(), groupName)
		if err != nil {
			return err
		}

		apiGroup, err = group.ToAPI(ctx, tx.Tx(), canViewIdentity, canViewIDPGroup)
		if err != nil {
			return err
		}

		etagPermissions = apiGroup.Permissions
		if r.URL.Query().Get("image_alias_types") == "1" {
			apiGroup.Permissions, err = typedImageAliasPermissions(ctx, tx, apiGroup.Permissions)
			if err != nil {
				return err
			}
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

	// Keep ETags independent of the requested permission URL representation.
	etagGroup := *apiGroup
	etagGroup.Permissions = etagPermissions
	return response.SyncResponseETag(true, *apiGroup, etagGroup)
}

// typedImageAliasPermissions renders legacy group references using canonical typed entity URLs.
func typedImageAliasPermissions(ctx context.Context, tx *db.ClusterTx, permissions []api.Permission) ([]api.Permission, error) {
	var references map[*api.URL]*dbCluster.EntityRef
	var indices map[*api.URL]int
	for i, permission := range permissions {
		if permission.EntityType != string(entity.TypeImageAlias) {
			continue
		}

		u, err := url.Parse(permission.EntityReference)
		if err != nil {
			return nil, err
		}

		_, _, _, args, err := entity.ParseURL(*u)
		if err != nil {
			return nil, err
		}

		if args[0] != "" {
			continue
		}

		if references == nil {
			references = make(map[*api.URL]*dbCluster.EntityRef)
			indices = make(map[*api.URL]int)
		}

		apiURL := &api.URL{URL: *u}
		references[apiURL] = &dbCluster.EntityRef{}
		indices[apiURL] = i
	}

	if len(references) == 0 {
		return permissions, nil
	}

	err := dbCluster.PopulateEntityReferencesFromURLs(ctx, tx.Tx(), references)
	if err != nil {
		return nil, err
	}

	typedPermissions := slices.Clone(permissions)
	for u, reference := range references {
		typedPermissions[indices[u]].EntityReference = reference.URL().String()
	}

	return typedPermissions, nil
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
	groupName := r.PathValue("groupName")
	var err error

	var groupPut api.AuthGroupPut
	err = json.NewDecoder(r.Body).Decode(&groupPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid request body: %w", err))
	}

	s := d.State()
	validatedPermissions, err := validatePermissions(r.Context(), s, groupPut.Permissions)
	if err != nil {
		return response.SmartError(err)
	}

	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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

		// The admins group is a built-in group with special semantics. Once it has been granted the
		// server admin permission it becomes immutable. It may still be updated to grant the server
		// admin permission if it does not yet have it (for example when the group has been created
		// manually on an installation that was upgraded from a version of LXD that did not seed the
		// admins group automatically).
		if groupName == api.AuthGroupAdminsName {
			if hasServerAdminPermission(apiGroup.Permissions) {
				return api.StatusErrorf(http.StatusBadRequest, "The admins group cannot be modified")
			}

			err = validateAdminsGroupPermissions(groupPut.Permissions)
			if err != nil {
				return err
			}
		}

		group.Description = groupPut.Description

		err = query.UpdateByPrimaryKey(ctx, tx.Tx(), group)
		if err != nil {
			return err
		}

		err = dbCluster.SetAuthGroupPermissions(ctx, tx.Tx(), group.ID, validatedPermissions)
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

	secEvt := security.AuthzAdmin.WithSuffix("group_edit", groupName).UserEvent(r.Context(), security.LevelInfo, "Authorization group updated")
	s.Events.SendSecurity(secEvt)

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
	groupName := r.PathValue("groupName")
	var err error

	var groupPut api.AuthGroupPut
	err = json.NewDecoder(r.Body).Decode(&groupPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid request body: %w", err))
	}

	s := d.State()
	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting a permission checker: %w", err))
	}

	var permissions []api.Permission
	var group *dbCluster.AuthGroupsRow
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		group, err = dbCluster.GetAuthGroup(ctx, tx.Tx(), groupName)
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

		// The admins group is a built-in group with special semantics. Once it has been granted the
		// server admin permission it becomes immutable. It may still be patched to grant the server
		// admin permission if it does not yet have it (for example when the group has been created
		// manually on an installation that was upgraded from a version of LXD that did not seed the
		// admins group automatically).
		if groupName == api.AuthGroupAdminsName {
			if hasServerAdminPermission(apiGroup.Permissions) {
				return api.StatusErrorf(http.StatusBadRequest, "The admins group cannot be modified")
			}

			err = validateAdminsGroupPermissions(groupPut.Permissions)
			if err != nil {
				return err
			}
		}

		// Validate the full PATCH union before deduplicating canonical permissions.
		permissions = append(apiGroup.Permissions, groupPut.Permissions...)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	validatedPermissions, err := validatePermissions(r.Context(), s, permissions)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if groupPut.Description != "" {
			group.Description = groupPut.Description
			err = query.UpdateByPrimaryKey(ctx, tx.Tx(), group)
			if err != nil {
				return err
			}
		}

		return dbCluster.SetAuthGroupPermissions(ctx, tx.Tx(), group.ID, validatedPermissions)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the group update
	lc := lifecycle.AuthGroupUpdated.Event(groupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	secEvt := security.AuthzAdmin.WithSuffix("group_edit", groupName).UserEvent(r.Context(), security.LevelInfo, "Authorization group updated")
	s.Events.SendSecurity(secEvt)

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
	groupName := r.PathValue("groupName")
	var err error
	if groupName == api.AuthGroupAdminsName {
		return response.BadRequest(errors.New("The admins group cannot be renamed"))
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

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetAuthGroup(ctx, tx.Tx(), groupName)
		if err != nil {
			return err
		}

		group.Name = groupPost.Name
		return query.UpdateByPrimaryKey(ctx, tx.Tx(), group)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the group rename
	lc := lifecycle.AuthGroupRenamed.Event(groupPost.Name, request.CreateRequestor(r.Context()), map[string]any{"old_name": groupName})
	s.Events.SendLifecycle("", lc)

	// Rename is treated as an edit per spec; no separate group_rename action.
	secEvt := security.AuthzAdmin.WithSuffix("group_edit", groupPost.Name).UserEvent(r.Context(), security.LevelInfo, "Authorization group renamed from "+groupName)
	s.Events.SendSecurity(secEvt)

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
	groupName := r.PathValue("groupName")
	var err error
	if groupName == api.AuthGroupAdminsName {
		return response.BadRequest(errors.New("The admins group cannot be deleted"))
	}

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return query.DeleteOne[dbCluster.AuthGroupsRow](ctx, tx.Tx(), "WHERE name = ?", groupName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the group deletion
	lc := lifecycle.AuthGroupDeleted.Event(groupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	secEvt := security.AuthzAdmin.WithSuffix("group_delete", groupName).UserEvent(r.Context(), security.LevelInfo, "Authorization group deleted")
	s.Events.SendSecurity(secEvt)

	return response.EmptySyncResponse
}

// validatePermissions checks that a) the entity type exists, b) the entitlement exists, c) then entity type matches the
// entity reference (URL), and d) that the entitlement is valid for the entity type.
func validatePermissions(ctx context.Context, s *state.State, permissions []api.Permission) ([]dbCluster.Permission, error) {
	// Compose a map of entity URLs where view permission is required. For example, if a permission is for an entity that is project specific,
	// then the URL of the project is added to the map and the permission appended to the list. Later, we will check that a group with the input list of
	// permissions can view the project. The list of permissions associated with the URL (map values) are only used for informative error messages.
	// URLs are added to the map as string keys to prevent duplicates.
	entityURLsWithViewPermissionRequired := make(map[string][]api.Permission, len(permissions))

	// Populate database references for the input URLs. Ensuring we can find the entities in the database.
	entityReferences := make(map[*api.URL]*dbCluster.EntityRef, len(permissions))
	permissionToURL := make(map[api.Permission]*api.URL, len(permissions))
	contextualPermissions := make([]api.Permission, 0, len(permissions))
	for _, permission := range permissions {
		entityType := entity.Type(permission.EntityType)
		err := entityType.Validate()
		if err != nil {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Failed validating entity type for permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		u, err := url.Parse(permission.EntityReference)
		if err != nil {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Failed parsing permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		referenceEntityType, projectName, location, args, err := entity.ParseURL(*u)
		if err != nil {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Failed parsing permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		if entityType != referenceEntityType {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Failed parsing permission with entity reference %q and entitlement %q: Entity type does not correspond to entity reference", permission.EntityReference, permission.Entitlement)
		}

		// Accept the legacy image alias URL without an image type when the name is unique.
		if referenceEntityType == entity.TypeImageAlias && len(args) == 2 && args[0] == "" {
			var imageTypes []string
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				imageTypes, err = tx.GetImageAliasTypes(ctx, projectName, args[1])
				return err
			})
			if err != nil {
				return nil, api.StatusErrorf(http.StatusBadRequest, "Failed resolving image alias %q: %w", args[1], err)
			}

			switch len(imageTypes) {
			case 0:
				return nil, api.StatusErrorf(http.StatusBadRequest, "Image alias %q not found", args[1])
			case 1:
				args[0] = imageTypes[0]
			default:
				return nil, api.StatusErrorf(http.StatusBadRequest, "Image alias %q exists for image types %q and %q", args[1], imageTypes[0], imageTypes[1])
			}
		}

		entityURL, err := referenceEntityType.URL(projectName, location, args...)
		if err != nil {
			return nil, err
		}

		permissionEntitlement := auth.Entitlement(permission.Entitlement)
		err = auth.ValidateEntitlement(entityType, permissionEntitlement)
		if err != nil {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Failed validating group permission with entity reference %q and entitlement %q: %w", permission.EntityReference, permission.Entitlement, err)
		}

		// If the entitlement is not `can_view`, check that a group with these permissions can view the entity.
		if permissionEntitlement != auth.EntitlementCanView {
			// Only perform the check if `can_view` is valid for the entity type.
			// E.g. If granting `can_edit` on a storage pool, we don't check that a group with these permissions can view
			// the storage pool because there is no such entitlement.
			err = auth.ValidateEntitlement(entityType, auth.EntitlementCanView)
			if err == nil {
				entityURLString := entityURL.String()
				entityURLsWithViewPermissionRequired[entityURLString] = append(entityURLsWithViewPermissionRequired[entityURLString], permission)
			}
		}

		// If the entity type requires a project, check that a group with these permissions can view the project.
		requiresProject, _ := entityType.RequiresProject()
		if requiresProject {
			projectURLString := entity.ProjectURL(projectName).String()
			entityURLsWithViewPermissionRequired[projectURLString] = append(entityURLsWithViewPermissionRequired[projectURLString], permission)
		}

		// Canonicalize alias URLs for consistent contextual checks and deduplication.
		permission.EntityReference = entityURL.String()
		_, seen := permissionToURL[permission]
		if !seen {
			entityReferences[entityURL] = &dbCluster.EntityRef{}
			permissionToURL[permission] = entityURL
			contextualPermissions = append(contextualPermissions, permission)
		}
	}

	// Check the group for consistency. A group permission that references a project specific entity is only allowed if the group can view the parent project.
	// A group permission that references any entity (and whose entitlement is not "can_view") is only allowed if the group can view the entity.
	for urlStr, perms := range entityURLsWithViewPermissionRequired {
		u, err := url.Parse(urlStr)
		if err != nil {
			// We should never hit this error because we've already parsed the URL above.
			return nil, fmt.Errorf("Failed re-parsing entity URL during group consistency checks: %w", err)
		}

		// Check view permission on the entity contextually, so that the permission check is not conflated with permissions of the caller.
		isAllowed, err := s.Authorizer.CheckContextualPermission(ctx, &api.URL{URL: *u}, auth.EntitlementCanView, contextualPermissions)
		if err != nil {
			return nil, err
		}

		// If members of the group can view the entity, we're good.
		if isAllowed {
			continue
		}

		// Otherwise, return an informative error message based on the offending permissions.
		if len(perms) == 1 {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Entitlement %q on entity type %q references %q, but the entity cannot be viewed by the group", perms[0].Entitlement, perms[0].EntityType, urlStr)
		}

		return nil, api.StatusErrorf(http.StatusBadRequest, "Members of the group cannot view %q, but %d permissions reference this entity", urlStr, len(perms))
	}

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.PopulateEntityReferencesFromURLs(ctx, tx.Tx(), entityReferences)
	})
	if err != nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Could not resolve permission URLs: %w", err)
	}

	authGroupPermissions := make([]dbCluster.Permission, 0, len(permissions))
	for permission, apiURL := range permissionToURL {
		entitlement := auth.Entitlement(permission.Entitlement)
		entityType := dbCluster.EntityType(permission.EntityType)
		entityRef, ok := entityReferences[apiURL]
		if !ok {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Missing entity ID for permission with URL %q", permission.EntityReference)
		}

		authGroupPermissions = append(authGroupPermissions, dbCluster.Permission{
			Entitlement: entitlement,
			EntityType:  entityType,
			EntityID:    entityRef.EntityID,
		})
	}

	// Typed and legacy alias URLs can resolve to the same entity; deduplicate before insertion.
	type permissionKey struct {
		entityType  dbCluster.EntityType
		entityID    int
		entitlement auth.Entitlement
	}

	uniquePermissions := make([]dbCluster.Permission, 0, len(authGroupPermissions))
	seenPermissions := make(map[permissionKey]struct{}, len(authGroupPermissions))
	for _, permission := range authGroupPermissions {
		key := permissionKey{
			entityType:  permission.EntityType,
			entityID:    permission.EntityID,
			entitlement: permission.Entitlement,
		}

		_, seen := seenPermissions[key]
		if seen {
			continue
		}

		seenPermissions[key] = struct{}{}
		uniquePermissions = append(uniquePermissions, permission)
	}

	return uniquePermissions, nil
}
