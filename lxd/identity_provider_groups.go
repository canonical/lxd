package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"

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

var identityProviderGroupsCmd = APIEndpoint{
	Name:        "identity_provider_groups",
	Path:        "auth/identity-provider-groups",
	MetricsType: entity.TypeIdentity,
	Get: APIEndpointAction{
		Handler:       getIdentityProviderGroups,
		AccessHandler: allowAuthenticated,
	},
	Post: APIEndpointAction{
		Handler:       createIdentityProviderGroup,
		AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanCreateIdentityProviderGroups),
	},
}

var identityProviderGroupCmd = APIEndpoint{
	Name:        "identity_provider_group",
	Path:        "auth/identity-provider-groups/{idpGroupName}",
	MetricsType: entity.TypeIdentity,
	Get: APIEndpointAction{
		Handler:       getIdentityProviderGroup,
		AccessHandler: allowPermission(entity.TypeIdentityProviderGroup, auth.EntitlementCanView, "idpGroupName"),
	},
	Put: APIEndpointAction{
		Handler:       updateIdentityProviderGroup,
		AccessHandler: allowPermission(entity.TypeIdentityProviderGroup, auth.EntitlementCanEdit, "idpGroupName"),
	},
	Post: APIEndpointAction{
		Handler:       renameIdentityProviderGroup,
		AccessHandler: allowPermission(entity.TypeIdentityProviderGroup, auth.EntitlementCanEdit, "idpGroupName"),
	},
	Delete: APIEndpointAction{
		Handler:       deleteIdentityProviderGroup,
		AccessHandler: allowPermission(entity.TypeIdentityProviderGroup, auth.EntitlementCanDelete, "idpGroupName"),
	},
	Patch: APIEndpointAction{
		Handler:       patchIdentityProviderGroup,
		AccessHandler: allowPermission(entity.TypeIdentityProviderGroup, auth.EntitlementCanEdit, "idpGroupName"),
	},
}

// swagger:operation GET /1.0/auth/identity-provider-groups identity_provider_groups identity_provider_groups_get
//
//	Get the identity provider groups
//
//	Returns a list of identity provider groups (URLs).
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
//	              "/1.0/auth/identity-provider-groups/sales",
//	              "/1.0/auth/identity-provider-groups/operations"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identity-provider-groups?recursion=1 identity_provider_groups identity_provider_groups_get_recursion1
//
//	Get the groups
//
//	Returns a list of identity provider groups.
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
//	          description: List of identity provider groups
//	          items:
//	            $ref: "#/definitions/IdentityProviderGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getIdentityProviderGroups(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)
	s := d.State()

	canViewIDPGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentityProviderGroup)
	if err != nil {
		return response.SmartError(err)
	}

	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeIdentityProviderGroup, true)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIDPGroups []*api.IdentityProviderGroup
	var idpGroups []dbCluster.IdentityProviderGroup
	urlToIDPGroup := make(map[*api.URL]auth.EntitlementReporter)
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		allIDPGroups, err := dbCluster.GetIdentityProviderGroups(ctx, tx.Tx())
		if err != nil {
			return err
		}

		idpGroups = make([]dbCluster.IdentityProviderGroup, 0, len(allIDPGroups))
		for _, idpGroup := range allIDPGroups {
			if canViewIDPGroup(entity.IdentityProviderGroupURL(idpGroup.Name)) {
				idpGroups = append(idpGroups, idpGroup)
			}
		}

		if recursion {
			apiIDPGroups = make([]*api.IdentityProviderGroup, 0, len(idpGroups))
			for _, idpGroup := range idpGroups {
				apiIDPGroup, err := idpGroup.ToAPI(ctx, tx.Tx(), canViewGroup)
				if err != nil {
					return err
				}

				apiIDPGroups = append(apiIDPGroups, apiIDPGroup)
				urlToIDPGroup[entity.IdentityProviderGroupURL(idpGroup.Name)] = apiIDPGroup
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion {
		if len(withEntitlements) > 0 {
			err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeIdentityProviderGroup, withEntitlements, urlToIDPGroup)
			if err != nil {
				return response.SmartError(err)
			}
		}

		return response.SyncResponse(true, apiIDPGroups)
	}

	idpGroupURLs := make([]string, 0, len(idpGroups))
	for _, idpGroup := range idpGroups {
		idpGroupURLs = append(idpGroupURLs, entity.IdentityProviderGroupURL(idpGroup.Name).String())
	}

	return response.SyncResponse(true, idpGroupURLs)
}

// swagger:operation GET /1.0/auth/identity-provider-groups/{idpGroupName} identity_provider_groups identity_provider_group_get
//
//	Get the identity provider group
//
//	Gets a specific identity provider group.
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
//	          $ref: "#/definitions/IdentityProviderGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getIdentityProviderGroup(d *Daemon, r *http.Request) response.Response {
	idpGroupName, err := url.PathUnescape(mux.Vars(r)["idpGroupName"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to unescape identity provider group name path parameter: %w", err))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeIdentityProviderGroup, false)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIDPGroup *api.IdentityProviderGroup
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		idpGroup, err := dbCluster.GetIdentityProviderGroup(ctx, tx.Tx(), idpGroupName)
		if err != nil {
			return err
		}

		apiIDPGroup, err = idpGroup.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeIdentityProviderGroup, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.IdentityProviderGroupURL(idpGroupName): apiIDPGroup})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, apiIDPGroup, apiIDPGroup)
}

// swagger:operation POST /1.0/auth/identity-provider-groups identity_provider_groups identity_provider_groups_post
//
//	Create a new identity provider group
//
//	Creates a new identity provider group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: group
//	    description: Identity provider request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/IdentityProviderGroup"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func createIdentityProviderGroup(d *Daemon, r *http.Request) response.Response {
	var idpGroup api.IdentityProviderGroup
	err := json.NewDecoder(r.Body).Decode(&idpGroup)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
	}

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := dbCluster.CreateIdentityProviderGroup(ctx, tx.Tx(), dbCluster.IdentityProviderGroup{Name: idpGroup.Name})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return api.StatusErrorf(http.StatusConflict, "Identity provider group %q already exists", idpGroup.Name)
			}

			return err
		}

		err = dbCluster.SetIdentityProviderGroupMapping(ctx, tx.Tx(), int(id), idpGroup.Groups)
		if err != nil {
			return err
		}

		return nil
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

	s.UpdateIdentityCache()

	// Send a lifecycle event for the IDP group creation.
	lc := lifecycle.IdentityProviderGroupCreated.Event(idpGroup.Name, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	return response.SyncResponseLocation(true, nil, entity.IdentityProviderGroupURL(idpGroup.Name).String())
}

// swagger:operation POST /1.0/auth/identity-provider-groups/{idpGroupName} identity_provider_groups identity_provider_group_post
//
//	Rename the identity provider group
//
//	Renames the identity provider group
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
//	      $ref: "#/definitions/IdentityProviderGroupPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func renameIdentityProviderGroup(d *Daemon, r *http.Request) response.Response {
	idpGroupName, err := url.PathUnescape(mux.Vars(r)["idpGroupName"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to unescape path argument: %w", err))
	}

	var idpGroupPost api.IdentityProviderGroupPost
	err = json.NewDecoder(r.Body).Decode(&idpGroupPost)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
	}

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.RenameIdentityProviderGroup(ctx, tx.Tx(), idpGroupName, idpGroupPost.Name)
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			return response.Conflict(fmt.Errorf("Identity provider group %q already exists", idpGroupPost.Name))
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

	// Send a lifecycle event for the IDP group rename.
	lc := lifecycle.IdentityProviderGroupRenamed.Event(idpGroupPost.Name, request.CreateRequestor(r.Context()), map[string]any{"old_name": idpGroupName})
	s.Events.SendLifecycle("", lc)

	s.UpdateIdentityCache()

	return response.SyncResponseLocation(true, nil, entity.IdentityProviderGroupURL(idpGroupPost.Name).String())
}

// swagger:operation PUT /1.0/auth/identity-provider-groups/{idpGroupName} identity_provider_groups identity_provider_group_put
//
//	Update the identity provider group
//
//	Replaces the editable fields of an identity provider group
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
//	      $ref: "#/definitions/IdentityProviderGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func updateIdentityProviderGroup(d *Daemon, r *http.Request) response.Response {
	idpGroupName, err := url.PathUnescape(mux.Vars(r)["idpGroupName"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to unescape path argument: %w", err))
	}

	var idpGroupPut api.IdentityProviderGroupPut
	err = json.NewDecoder(r.Body).Decode(&idpGroupPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		idpGroup, err := dbCluster.GetIdentityProviderGroup(ctx, tx.Tx(), idpGroupName)
		if err != nil {
			return err
		}

		apiIDPGroup, err := idpGroup.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIDPGroup)
		if err != nil {
			return err
		}

		return dbCluster.SetIdentityProviderGroupMapping(ctx, tx.Tx(), idpGroup.ID, idpGroupPut.Groups)
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

	// Send a lifecycle event for the IDP group update.
	lc := lifecycle.IdentityProviderGroupUpdated.Event(idpGroupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	s.UpdateIdentityCache()

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/auth/identity-provider-groups/{idpGroupName} identity_provider_groups identity_provider_group_patch
//
//	Partially update the identity provider group
//
//	Updates the editable fields of an identity provider group
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
//	      $ref: "#/definitions/IdentityProviderGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func patchIdentityProviderGroup(d *Daemon, r *http.Request) response.Response {
	idpGroupName, err := url.PathUnescape(mux.Vars(r)["idpGroupName"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to unescape path argument: %w", err))
	}

	var idpGroupPut api.IdentityProviderGroupPut
	err = json.NewDecoder(r.Body).Decode(&idpGroupPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIDPGroup *api.IdentityProviderGroup
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		idpGroup, err := dbCluster.GetIdentityProviderGroup(ctx, tx.Tx(), idpGroupName)
		if err != nil {
			return err
		}

		apiIDPGroup, err = idpGroup.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIDPGroup)
		if err != nil {
			return err
		}

		for _, newGroup := range idpGroupPut.Groups {
			if !slices.Contains(apiIDPGroup.Groups, newGroup) {
				apiIDPGroup.Groups = append(apiIDPGroup.Groups, newGroup)
			}
		}

		return dbCluster.SetIdentityProviderGroupMapping(ctx, tx.Tx(), idpGroup.ID, apiIDPGroup.Groups)
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

	// Send a lifecycle event for the IDP group update.
	lc := lifecycle.IdentityProviderGroupUpdated.Event(idpGroupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	s.UpdateIdentityCache()

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/auth/identity-provider-groups/{idpGroupName} identity_provider_groups identity_provider_group_delete
//
//	Delete the identity provider group
//
//	Deletes the identity provider group
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
func deleteIdentityProviderGroup(d *Daemon, r *http.Request) response.Response {
	idpGroupName, err := url.PathUnescape(mux.Vars(r)["idpGroupName"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to unescape path argument: %w", err))
	}

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteIdentityProviderGroup(ctx, tx.Tx(), idpGroupName)
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

	// Send a lifecycle event for the IDP group deletion.
	lc := lifecycle.IdentityProviderGroupDeleted.Event(idpGroupName, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle("", lc)

	s.UpdateIdentityCache()

	return response.EmptySyncResponse
}
