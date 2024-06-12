package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

var identitiesCmd = APIEndpoint{
	Name: "identities",
	Path: "auth/identities",
	Get: APIEndpointAction{
		Handler:       getIdentities,
		AccessHandler: allowAuthenticated,
	},
}

var identitiesByAuthenticationMethodCmd = APIEndpoint{
	Name: "identities",
	Path: "auth/identities/{authenticationMethod}",
	Get: APIEndpointAction{
		Handler:       getIdentities,
		AccessHandler: allowAuthenticated,
	},
}

var identityCmd = APIEndpoint{
	Name: "identity",
	Path: "auth/identities/{authenticationMethod}/{nameOrIdentifier}",
	Get: APIEndpointAction{
		Handler:       getIdentity,
		AccessHandler: identityAccessHandler(auth.EntitlementCanView),
	},
	Put: APIEndpointAction{
		Handler:       updateIdentity,
		AccessHandler: identityAccessHandler(auth.EntitlementCanEdit),
	},
	Patch: APIEndpointAction{
		Handler:       patchIdentity,
		AccessHandler: identityAccessHandler(auth.EntitlementCanEdit),
	},
}

const (
	// ctxClusterDBIdentity is used in the identityAccessHandler to set a cluster.Identity into the request context.
	// The database call is required for authorization and this avoids performing the same query twice.
	ctxClusterDBIdentity request.CtxKey = "cluster-db-identity"
)

// identityAccessHandler performs some initial validation of the request and gets the identity by its name or
// identifier. If one is found, the identifier is used in the URL that is passed to (auth.Authorizer).CheckPermission.
// The cluster.Identity is set in the request context.
func identityAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		muxVars := mux.Vars(r)
		authenticationMethod := muxVars["authenticationMethod"]
		err := auth.ValidateAuthenticationMethod(authenticationMethod)
		if err != nil {
			return response.SmartError(err)
		}

		nameOrID, err := url.PathUnescape(muxVars["nameOrIdentifier"])
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to unescape path argument: %w", err))
		}

		s := d.State()
		var id *dbCluster.Identity
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			id, err = dbCluster.GetIdentityByNameOrIdentifier(ctx, tx.Tx(), authenticationMethod, nameOrID)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		err = s.Authorizer.CheckPermission(r.Context(), r, entity.IdentityURL(authenticationMethod, id.Identifier), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		request.SetCtxValue(r, ctxClusterDBIdentity, id)
		return response.EmptySyncResponse
	}
}

// swagger:operation GET /1.0/auth/identities identities identities_get
//
//	Get the identities
//
//	Returns a list of identities (URLs).
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
//	              "/1.0/auth/identities/tls/e1e06266e36f67431c996d5678e66d732dfd12fe5073c161e62e6360619fc226",
//	              "/1.0/auth/identities/oidc/auth0|4daf5e37ce230e455b64b65b"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities?recursion=1 identities identities_get_recursion1
//
//	Get the identities
//
//	Returns a list of identities.
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
//	          description: List of identities
//	          items:
//	            $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/{authenticationMethod} identities identities_get_by_auth_method
//
//	Get the identities
//
//	Returns a list of identities (URLs).
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
//	              "/1.0/auth/identities/tls/e1e06266e36f67431c996d5678e66d732dfd12fe5073c161e62e6360619fc226",
//	              "/1.0/auth/identities/oidc/auth0|4daf5e37ce230e455b64b65b"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/{authenticationMethod}?recursion=1 identities identities_get_by_auth_method_recursion1
//
//	Get the identities
//
//	Returns a list of identities.
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
//	          description: List of identities
//	          items:
//	            $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getIdentities(d *Daemon, r *http.Request) response.Response {
	authenticationMethod, err := url.PathUnescape(mux.Vars(r)["authenticationMethod"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to unescape path argument: %w", err))
	}

	if authenticationMethod == "current" {
		return getCurrentIdentityInfo(d, r)
	} else if authenticationMethod != "" {
		err = auth.ValidateAuthenticationMethod(authenticationMethod)
		if err != nil {
			return response.SmartError(err)
		}
	}

	recursion := r.URL.Query().Get("recursion")
	s := d.State()
	canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	var identities []dbCluster.Identity
	var groupsByIdentityID map[int][]dbCluster.AuthGroup
	var apiIdentity *api.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get all identities, filter by authentication method if present.
		var filters []dbCluster.IdentityFilter
		if authenticationMethod != "" {
			clusterAuthMethod := dbCluster.AuthMethod(authenticationMethod)
			filters = append(filters, dbCluster.IdentityFilter{AuthMethod: &clusterAuthMethod})
		}

		allIdentities, err := dbCluster.GetIdentitys(ctx, tx.Tx(), filters...)
		if err != nil {
			return err
		}

		// Filter results by what the user is allowed to view.
		for _, id := range allIdentities {
			if canViewIdentity(entity.IdentityURL(string(id.AuthMethod), id.Identifier)) {
				identities = append(identities, id)
			}
		}

		if len(identities) == 0 {
			return nil
		}

		if recursion == "1" && len(identities) == 1 {
			// It's likely that the user can only view themselves. If so we can optimise here by only getting the
			// groups for that user.
			apiIdentity, err = identities[0].ToAPI(ctx, tx.Tx(), canViewGroup)
			if err != nil {
				return err
			}
		} else if recursion == "1" {
			// Otherwise, get all groups and populate the identities outside of the transaction.
			groupsByIdentityID, err = dbCluster.GetAllAuthGroupsByIdentityIDs(ctx, tx.Tx())
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Optimisation for user that can only view themselves.
	if apiIdentity != nil {
		return response.SyncResponse(true, []api.Identity{*apiIdentity})
	}

	if recursion == "1" {
		// Convert the []cluster.Group in the groupsByIdentityID map to string slices of the group names.
		groupNamesByIdentityID := make(map[int][]string, len(groupsByIdentityID))
		for identityID, groups := range groupsByIdentityID {
			for _, group := range groups {
				if canViewGroup(entity.AuthGroupURL(group.Name)) {
					groupNamesByIdentityID[identityID] = append(groupNamesByIdentityID[identityID], group.Name)
				}
			}
		}

		apiIdentities := make([]api.Identity, 0, len(identities))
		for _, id := range identities {
			apiIdentities = append(apiIdentities, api.Identity{
				AuthenticationMethod: string(id.AuthMethod),
				Type:                 string(id.Type),
				Identifier:           id.Identifier,
				Name:                 id.Name,
				Groups:               groupNamesByIdentityID[id.ID],
			})
		}

		return response.SyncResponse(true, apiIdentities)
	}

	urls := make([]string, 0, len(identities))
	for _, id := range identities {
		urls = append(urls, entity.IdentityURL(string(id.AuthMethod), id.Identifier).String())
	}

	return response.SyncResponse(true, urls)
}

// swagger:operation GET /1.0/auth/identities/{authenticationMethod}/{nameOrIdentifier} identities identity_get
//
//	Get the identity
//
//	Gets a specific identity.
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
//	          $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getIdentity(d *Daemon, r *http.Request) response.Response {
	id, err := request.GetCtxValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIdentity *api.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, apiIdentity, apiIdentity)
}

// swagger:operation GET /1.0/auth/identities/current identities identity_get_current
//
//	Get the current identity
//
//	Gets the identity of the requestor, including contextual authorization information.
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
//	          $ref: "#/definitions/IdentityInfo"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func getCurrentIdentityInfo(d *Daemon, r *http.Request) response.Response {
	identifier, err := request.GetCtxValue[string](r.Context(), request.CtxUsername)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get identity identifier: %w", err))
	}

	protocol, err := request.GetCtxValue[string](r.Context(), request.CtxProtocol)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get authentication method: %w", err))
	}

	// Must be a remote API request.
	err = auth.ValidateAuthenticationMethod(protocol)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Current identity information must be requested via the HTTPS API"))
	}

	// Identity provider groups may not be present.
	identityProviderGroupNames, _ := request.GetCtxValue[[]string](r.Context(), request.CtxIdentityProviderGroups)

	s := d.State()
	var apiIdentity *api.Identity
	var effectiveGroups []string
	var effectivePermissions []api.Permission
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := dbCluster.GetIdentity(ctx, tx.Tx(), dbCluster.AuthMethod(protocol), identifier)
		if err != nil {
			return fmt.Errorf("Failed to get current identity from database: %w", err)
		}

		// Using a permission checker here is redundant, we know who the user is, and we know that they are allowed
		// to view the groups that they are a member of.
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), func(entityURL *api.URL) bool { return true })
		if err != nil {
			return fmt.Errorf("Failed to populate LXD groups: %w", err)
		}

		effectiveGroups = apiIdentity.Groups
		mappedGroups, err := dbCluster.GetDistinctAuthGroupNamesFromIDPGroupNames(ctx, tx.Tx(), identityProviderGroupNames)
		if err != nil {
			return fmt.Errorf("Failed to get effective groups: %w", err)
		}

		for _, mappedGroup := range mappedGroups {
			if !shared.ValueInSlice(mappedGroup, effectiveGroups) {
				effectiveGroups = append(effectiveGroups, mappedGroup)
			}
		}

		permissions, err := dbCluster.GetDistinctPermissionsByGroupNames(ctx, tx.Tx(), effectiveGroups)
		if err != nil {
			return fmt.Errorf("Failed to get effective permissions: %w", err)
		}

		permissions, entityURLs, err := dbCluster.GetPermissionEntityURLs(ctx, tx.Tx(), permissions)
		if err != nil {
			return fmt.Errorf("Failed to get entity URLs for effective permissions: %w", err)
		}

		effectivePermissions = make([]api.Permission, 0, len(permissions))
		for _, permission := range permissions {
			effectivePermissions = append(effectivePermissions, api.Permission{
				EntityType:      string(permission.EntityType),
				EntityReference: entityURLs[entity.Type(permission.EntityType)][permission.EntityID].String(),
				Entitlement:     string(permission.Entitlement),
			})
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, api.IdentityInfo{
		Identity:             *apiIdentity,
		EffectiveGroups:      effectiveGroups,
		EffectivePermissions: effectivePermissions,
	})
}

// swagger:operation PUT /1.0/auth/identities/{authenticationMethod}/{nameOrIdentifier} identities identity_put
//
//	Update the identity
//
//	Replaces the editable fields of an identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func updateIdentity(d *Daemon, r *http.Request) response.Response {
	id, err := request.GetCtxValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	var identityPut api.IdentityPut
	err = json.NewDecoder(r.Body).Decode(&identityPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
	}

	if id.AuthMethod == api.AuthenticationMethodTLS && len(identityPut.Groups) > 0 {
		return response.NotImplemented(fmt.Errorf("Adding TLS identities to groups is currently not supported"))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		apiIdentity, err := id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIdentity)
		if err != nil {
			return err
		}

		err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), id.ID, identityPut.Groups)
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

	err = notifier(func(client lxd.InstanceServer) error {
		_, _, err := client.RawQuery(http.MethodPost, "/internal/identity-cache-refresh", nil, "")
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the identity update.
	lc := lifecycle.IdentityUpdated.Event(string(id.AuthMethod), id.Identifier, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	s.UpdateIdentityCache()

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/auth/identities/{authenticationMethod}/{nameOrIdentifier} identities identity_patch
//
//	Partially update the identity
//
//	Updates the editable fields of an identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func patchIdentity(d *Daemon, r *http.Request) response.Response {
	id, err := request.GetCtxValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	var identityPut api.IdentityPut
	err = json.NewDecoder(r.Body).Decode(&identityPut)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
	}

	authenticationMethod := mux.Vars(r)["authenticationMethod"]
	if authenticationMethod == api.AuthenticationMethodTLS && len(identityPut.Groups) > 0 {
		return response.NotImplemented(fmt.Errorf("Adding TLS identities to groups is currently not supported"))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIdentity *api.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIdentity)
		if err != nil {
			return err
		}

		for _, groupName := range identityPut.Groups {
			if !shared.ValueInSlice(groupName, apiIdentity.Groups) {
				apiIdentity.Groups = append(apiIdentity.Groups, groupName)
			}
		}

		err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), id.ID, identityPut.Groups)
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

	err = notifier(func(client lxd.InstanceServer) error {
		_, _, err := client.RawQuery(http.MethodPost, "/internal/identity-cache-refresh", nil, "")
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send a lifecycle event for the identity update.
	lc := lifecycle.IdentityUpdated.Event(string(id.AuthMethod), id.Identifier, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	s.UpdateIdentityCache()

	return response.EmptySyncResponse
}

// updateIdentityCache reads all identities from the database and sets them in the identity.Cache.
// The certificates in the local database are replaced with identities in the cluster database that
// are of type api.IdentityTypeCertificateServer. This ensures that this cluster member is able to
// trust other cluster members on restart.
func updateIdentityCache(d *Daemon) {
	s := d.State()

	logger.Debug("Refreshing identity cache")

	var identities []dbCluster.Identity
	projects := make(map[int][]string)
	groups := make(map[int][]string)
	idpGroupMapping := make(map[string][]string)
	var err error
	err = s.DB.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		identities, err = dbCluster.GetIdentitys(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, identity := range identities {
			identityProjects, err := dbCluster.GetIdentityProjects(ctx, tx.Tx(), identity.ID)
			if err != nil {
				return err
			}

			for _, p := range identityProjects {
				projects[identity.ID] = append(projects[identity.ID], p.Name)
			}

			identityGroups, err := dbCluster.GetAuthGroupsByIdentityID(ctx, tx.Tx(), identity.ID)
			if err != nil {
				return err
			}

			for _, g := range identityGroups {
				groups[identity.ID] = append(groups[identity.ID], g.Name)
			}
		}

		idpGroups, err := dbCluster.GetIdentityProviderGroups(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, idpGroup := range idpGroups {
			// Internal method does not need a permission checker.
			apiIDPGroup, err := idpGroup.ToAPI(ctx, tx.Tx(), func(_ *api.URL) bool { return true })
			if err != nil {
				return err
			}

			idpGroupMapping[apiIDPGroup.Name] = apiIDPGroup.Groups
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed reading certificates from global database", logger.Ctx{"err": err})
		return
	}

	identityCacheEntries := make([]identity.CacheEntry, 0, len(identities))
	var localServerCerts []dbCluster.Certificate
	for _, id := range identities {
		cacheEntry := identity.CacheEntry{
			Identifier:           id.Identifier,
			Name:                 id.Name,
			AuthenticationMethod: string(id.AuthMethod),
			IdentityType:         string(id.Type),
			Projects:             projects[id.ID],
			Groups:               groups[id.ID],
		}

		if cacheEntry.AuthenticationMethod == api.AuthenticationMethodTLS {
			cert, err := id.X509()
			if err != nil {
				logger.Warn("Failed to extract x509 certificate from TLS identity metadata", logger.Ctx{"error": err})
				continue
			}

			cacheEntry.Certificate = cert
		} else if cacheEntry.AuthenticationMethod == api.AuthenticationMethodOIDC {
			subject, err := id.Subject()
			if err != nil {
				logger.Warn("Failed to extract OIDC subject from OIDC identity metadata", logger.Ctx{"error": err})
				continue
			}

			cacheEntry.Subject = subject
		}

		identityCacheEntries = append(identityCacheEntries, cacheEntry)

		// Add server certs to list of certificates to store in local database to allow cluster restart.
		if id.Type == api.IdentityTypeCertificateServer {
			cert, err := id.ToCertificate()
			if err != nil {
				logger.Warn("Failed to convert TLS identity to server certificate", logger.Ctx{"error": err})
			}

			localServerCerts = append(localServerCerts, *cert)
		}
	}

	// Write out the server certs to the local database to allow the cluster to restart.
	err = s.DB.Node.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceCertificates(localServerCerts)
	})
	if err != nil {
		logger.Warn("Failed writing certificates to local database", logger.Ctx{"err": err})
		// Don't return here, as we still should update the in-memory cache to allow the cluster to
		// continue functioning, and hopefully the write will succeed on next update.
	}

	err = d.identityCache.ReplaceAll(identityCacheEntries, idpGroupMapping)
	if err != nil {
		logger.Warn("Failed to update identity cache", logger.Ctx{"error": err})
	}
}

// updateIdentityCacheFromLocal loads trusted server certificates from local database into the identity cache.
func updateIdentityCacheFromLocal(d *Daemon) error {
	logger.Debug("Refreshing identity cache with local trusted certificates")

	var localServerCerts []dbCluster.Certificate
	var err error

	err = d.State().DB.Node.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.NodeTx) error {
		localServerCerts, err = tx.GetCertificates(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed reading certificates from local database: %w", err)
	}

	var identityCacheEntries []identity.CacheEntry
	for _, dbCert := range localServerCerts {
		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		id, err := dbCert.ToIdentity()
		if err != nil {
			logger.Warn("Failed to convert node certificate into identity entry", logger.Ctx{"error": err})
			continue
		}

		identityCacheEntries = append(identityCacheEntries, identity.CacheEntry{
			Identifier:           id.Identifier,
			AuthenticationMethod: string(id.AuthMethod),
			IdentityType:         string(id.Type),
			Certificate:          cert,
		})
	}

	err = d.identityCache.ReplaceAll(identityCacheEntries, nil)
	if err != nil {
		return fmt.Errorf("Failed to update identity cache from local trust store: %w", err)
	}

	return nil
}
