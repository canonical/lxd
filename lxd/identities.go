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
		// Empty authentication method will return all identities.
		Handler:       getIdentities(""),
		AccessHandler: allowAuthenticated,
	},
}

var currentIdentityCmd = APIEndpoint{
	Name: "identities",
	Path: "auth/identities/current",
	Get: APIEndpointAction{
		Handler:       getCurrentIdentityInfo,
		AccessHandler: allowAuthenticated,
	},
}

var tlsIdentitiesCmd = APIEndpoint{
	Name: "identities",
	Path: "auth/identities/tls",
	Get: APIEndpointAction{
		Handler:       getIdentities(api.AuthenticationMethodTLS),
		AccessHandler: allowAuthenticated,
	},
}

var oidcIdentitiesCmd = APIEndpoint{
	Name: "identities",
	Path: "auth/identities/oidc",
	Get: APIEndpointAction{
		Handler:       getIdentities(api.AuthenticationMethodOIDC),
		AccessHandler: allowAuthenticated,
	},
}

var tlsIdentityCmd = APIEndpoint{
	Name: "identity",
	Path: "auth/identities/tls/{nameOrIdentifier}",
	Get: APIEndpointAction{
		Handler:       getIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodTLS, auth.EntitlementCanView),
	},
	Put: APIEndpointAction{
		Handler:       updateIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodTLS, auth.EntitlementCanEdit),
	},
	Patch: APIEndpointAction{
		Handler:       patchIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodTLS, auth.EntitlementCanEdit),
	},
	Delete: APIEndpointAction{
		Handler:       deleteIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodTLS, auth.EntitlementCanDelete),
	},
}

var oidcIdentityCmd = APIEndpoint{
	Name: "identity",
	Path: "auth/identities/oidc/{nameOrIdentifier}",
	Get: APIEndpointAction{
		Handler:       getIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanView),
	},
	Put: APIEndpointAction{
		Handler:       updateIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanEdit),
	},
	Patch: APIEndpointAction{
		Handler:       patchIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanEdit),
	},
	Delete: APIEndpointAction{
		Handler:       deleteIdentity,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanDelete),
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
func identityAccessHandler(authenticationMethod string, entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		muxVars := mux.Vars(r)
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

		if identity.IsFineGrainedIdentityType(string(id.Type)) {
			err = s.Authorizer.CheckPermission(r.Context(), entity.IdentityURL(authenticationMethod, id.Identifier), entitlement)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			err = s.Authorizer.CheckPermission(r.Context(), entity.CertificateURL(id.Identifier), entitlement)
			if err != nil {
				return response.SmartError(err)
			}
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

// swagger:operation GET /1.0/auth/identities/tls identities identities_get_tls
//
//	Get the TLS identities
//
//	Returns a list of TLS identities (URLs).
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
//	              "/1.0/auth/identities/tls/6d5678e66d732dfd12fe5073c161eec9962e6360619fc2261e06266e36f67431"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/oidc identities identities_get_oidc
//
//	Get the OIDC identities
//
//	Returns a list of OIDC identities (URLs).
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
//	              "/1.0/auth/identities/oidc/jane.doe@example.com",
//	              "/1.0/auth/identities/oidc/joe.bloggs@example.com"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/tls?recursion=1 identities identities_get_tls_recursion1
//
//	Get the TLS identities
//
//	Returns a list of TLS identities.
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

// swagger:operation GET /1.0/auth/identities/oidc?recursion=1 identities identities_get_oidc_recursion1
//
//	Get the OIDC identities
//
//	Returns a list of OIDC identities.
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
func getIdentities(authenticationMethod string) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		recursion := r.URL.Query().Get("recursion")
		s := d.State()
		canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
		if err != nil {
			return response.SmartError(err)
		}

		canViewCertificate, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeCertificate)
		if err != nil {
			return response.SmartError(err)
		}

		canView := func(id dbCluster.Identity) bool {
			if identity.IsFineGrainedIdentityType(string(id.Type)) {
				return canViewIdentity(entity.IdentityURL(string(id.AuthMethod), id.Identifier))
			}

			return canViewCertificate(entity.CertificateURL(id.Identifier))
		}

		canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
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
				if canView(id) {
					identities = append(identities, id)
				}
			}

			if len(identities) == 0 {
				return nil
			}

			if recursion == "1" && len(identities) == 1 {
				// If there is only one identity to return (either the caller can only view themselves, or there is only one identity in database)
				// we can optimise here by only getting the groups for that user. This sets the value of `apiIdentity`
				// which is to be returned if non-nil.
				apiIdentity, err = identities[0].ToAPI(ctx, tx.Tx(), canViewGroup)
				if err != nil {
					return err
				}
			} else if recursion == "1" {
				// Otherwise, get all groups and populate the identities outside of the transaction.
				// This optimisation prevents us from iterating through each identity and querying the database for the
				// groups of each identity in turn.
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

		// Optimisation for when only one identity is present on the system.
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
}

// swagger:operation GET /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_get_tls
//
//	Get the TLS identity
//
//	Gets a specific TLS identity.
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

// swagger:operation GET /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_get_oidc
//
//	Get the OIDC identity
//
//	Gets a specific OIDC identity.
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
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
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
	err = identity.ValidateAuthenticationMethod(protocol)
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

// swagger:operation PUT /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_put_tls
//
//	Update the TLS identity
//
//	Replaces the editable fields of a TLS identity
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
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation PUT /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_put_oidc
//
//	Update the OIDC identity
//
//	Replaces the editable fields of an OIDC identity
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
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"
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

	if !identity.IsFineGrainedIdentityType(string(id.Type)) {
		return response.NotImplemented(fmt.Errorf("Identities of type %q cannot be modified via this API", id.Type))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
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

// swagger:operation PATCH /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_patch_tls
//
//	Partially update the TLS identity
//
//	Updates the editable fields of a TLS identity
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
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation PATCH /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_patch_oidc
//
//	Partially update the OIDC identity
//
//	Updates the editable fields of an OIDC identity
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
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"
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

	if !identity.IsFineGrainedIdentityType(string(id.Type)) {
		return response.NotImplemented(fmt.Errorf("Identities of type %q cannot be modified via this API", id.Type))
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
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

// swagger:operation DELETE /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_delete_tls
//
//	Delete the TLS identity
//
//	Removes the TLS identity and revokes trust.
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
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation DELETE /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_delete_oidc
//
//	Delete the OIDC identity
//
//	Removes the OIDC identity.
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
//	  "501":
//	    $ref: "#/responses/NotImplemented"
func deleteIdentity(d *Daemon, r *http.Request) response.Response {
	id, err := request.GetCtxValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	if !identity.IsFineGrainedIdentityType(string(id.Type)) {
		return response.NotImplemented(fmt.Errorf("Identities of type %q cannot be modified via this API", id.Type))
	}

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier)
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
	lc := lifecycle.IdentityDeleted.Event(string(id.AuthMethod), id.Identifier, request.CreateRequestor(r), nil)
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
				logger.Warn("Failed to extract x509 certificate from TLS identity metadata", logger.Ctx{"err": err})
				continue
			}

			cacheEntry.Certificate = cert
		} else if cacheEntry.AuthenticationMethod == api.AuthenticationMethodOIDC {
			subject, err := id.Subject()
			if err != nil {
				logger.Warn("Failed to extract OIDC subject from OIDC identity metadata", logger.Ctx{"err": err})
				continue
			}

			cacheEntry.Subject = subject
		}

		identityCacheEntries = append(identityCacheEntries, cacheEntry)

		// Add server certs to list of certificates to store in local database to allow cluster restart.
		if id.Type == api.IdentityTypeCertificateServer {
			cert, err := id.ToCertificate()
			if err != nil {
				logger.Warn("Failed to convert TLS identity to server certificate", logger.Ctx{"err": err})
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
		logger.Warn("Failed to update identity cache", logger.Ctx{"err": err})
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
			logger.Warn("Failed to convert node certificate into identity entry", logger.Ctx{"err": err})
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
