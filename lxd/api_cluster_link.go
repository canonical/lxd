package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

var clusterLinksCmd = APIEndpoint{
	Path:        "cluster/links",
	MetricsType: entity.TypeClusterLink,

	Get:  APIEndpointAction{Handler: clusterLinksGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: clusterLinksPost, AllowUntrusted: true},
}

var clusterLinkCmd = APIEndpoint{
	Path:        "cluster/links/{name}",
	MetricsType: entity.TypeClusterLink,

	Get:    APIEndpointAction{Handler: clusterLinkGet, AccessHandler: allowPermission(entity.TypeClusterLink, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: clusterLinkPost, AccessHandler: allowPermission(entity.TypeClusterLink, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: clusterLinkPatch, AccessHandler: allowPermission(entity.TypeClusterLink, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: clusterLinkPut, AccessHandler: allowPermission(entity.TypeClusterLink, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: clusterLinkDelete, AccessHandler: allowPermission(entity.TypeClusterLink, auth.EntitlementCanDelete, "name")},
}

// swagger:operation GET /1.0/cluster/links cluster-links cluster_links_get
//
//		Get the cluster links
//
//		Returns a list of cluster links (URLs).
//
//		---
//		produces:
//		  - application/json
//		responses:
//		  "200":
//		    description: API endpoints
//		    schema:
//		      type: object
//		      description: Sync response
//		      properties:
//		        type:
//		          type: string
//		          description: Response type
//		          example: sync
//		        status:
//		          type: string
//		          description: Status description
//		          example: Success
//		        status_code:
//		          type: integer
//		          description: Status code
//		          example: 200
//		        metadata:
//		          type: array
//		          description: List of endpoints
//		          items:
//		            type: string
//		          example: |-
//		            [
//		              "/1.0/cluster/links/primary",
//		              "/1.0/cluster/links/backup"
//		            ]
//		  "400":
//		    $ref: "#/responses/BadRequest"
//		  "403":
//		    $ref: "#/responses/Forbidden"
//		  "500":
//		    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/links?recursion=1 cluster-links cluster_links_get_recursion1
//
//	Get the cluster links
//
//	Returns a list of cluster links (structs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster links
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
//	          description: List of cluster links
//	          items:
//	            $ref: "#/definitions/ClusterLink"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterLinksGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	recursion, _ := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeClusterLink, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeClusterLink)
	if err != nil {
		return response.InternalError(err)
	}

	var clusterLinks []dbCluster.ClusterLinkRow
	var clusterLinkURLs []string
	var allConfigs map[int64]map[string]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		clusterLinks, clusterLinkURLs, err = dbCluster.GetClusterLinksAndURLs(ctx, tx.Tx(), func(link dbCluster.ClusterLinkRow) bool {
			return userHasPermission(entity.ClusterLinkURL(link.Name))
		})
		if err != nil {
			return err
		}

		if recursion != 0 && len(clusterLinks) > 0 {
			allConfigs, err = dbCluster.GetClusterLinkConfig(ctx, tx.Tx(), nil)
			if err != nil {
				return fmt.Errorf("Failed loading cluster link configs: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, clusterLinkURLs)
	}

	apiClusterLinks := make([]*api.ClusterLink, 0, len(clusterLinks))
	for _, link := range clusterLinks {
		apiClusterLinks = append(apiClusterLinks, link.ToAPI(allConfigs))
	}

	if len(withEntitlements) > 0 {
		urlToClusterLink := make(map[*api.URL]auth.EntitlementReporter, len(apiClusterLinks))
		for _, c := range apiClusterLinks {
			u := entity.ClusterLinkURL(c.Name)
			urlToClusterLink[u] = c
		}

		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeClusterLink, withEntitlements, urlToClusterLink)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiClusterLinks)
}

// swagger:operation GET /1.0/cluster/links/{name} cluster-links cluster_link_get
//
//	Get the cluster link
//
//	Gets a specific cluster link.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster link
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
//	          $ref: "#/definitions/ClusterLink"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterLinkGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeClusterLink, false)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var apiClusterLink *api.ClusterLink
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbClusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link: %w", err)
		}

		config, err := dbCluster.GetClusterLinkConfig(ctx, tx.Tx(), &dbClusterLink.ID)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link config: %w", err)
		}

		apiClusterLink = dbClusterLink.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeClusterLink, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ClusterLinkURL(name): apiClusterLink})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, apiClusterLink, apiClusterLink.Writable())
}

// updateClusterLink is shared between [clusterLinkPut] and [clusterLinkPatch].
func updateClusterLink(s *state.State, r *http.Request, isPatch bool) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbClusterLink *dbCluster.ClusterLinkRow
	var apiClusterLink *api.ClusterLink
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get cluster link by name.
		dbClusterLink, err = dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link: %w", err)
		}

		config, err := dbCluster.GetClusterLinkConfig(ctx, tx.Tx(), &dbClusterLink.ID)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link config: %w", err)
		}

		apiClusterLink = dbClusterLink.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag.
	err = util.EtagCheck(r, apiClusterLink.Writable())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request.
	req := api.ClusterLinkPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Reject attempts to add, remove, or change volatile.* keys — these are managed internally only.
	// For PUT (strict), the volatile key set must be identical to the current config.
	// For PATCH (non-strict), omitted volatile keys are allowed; they are restored below.
	err = checkVolatileConfig(apiClusterLink.Config, req.Config, !isPatch)
	if err != nil {
		return response.BadRequest(err)
	}

	// Re-insert existing volatile values so they are preserved across PUT/PATCH.
	for k, currentVal := range apiClusterLink.Config {
		if !strings.HasPrefix(k, "volatile.") {
			continue
		}

		if req.Config == nil {
			req.Config = map[string]string{}
		}

		req.Config[k] = currentVal
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Update the fields from the request.
		err = clusterLinkValidateConfig(req.Config)
		if err != nil {
			return err
		}

		if isPatch {
			// Populate request config with current values.
			if req.Config == nil {
				req.Config = apiClusterLink.Config
			} else {
				for k, v := range apiClusterLink.Config {
					_, ok := req.Config[k]
					if !ok {
						req.Config[k] = v
					}
				}
			}
		}

		if !isPatch || req.Description != "" {
			dbClusterLink.Description = req.Description
		}

		err = dbCluster.UpdateClusterLink(ctx, tx.Tx(), *dbClusterLink)
		if err != nil {
			return err
		}

		err = dbCluster.UpdateClusterLinkConfig(ctx, tx.Tx(), dbClusterLink.ID, req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send cluster link lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkUpdated.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/cluster/links/{name} cluster-links cluster_link_patch
//
//	Update the cluster link
//
//	Updates a subset of the cluster link configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster_link
//	    description: Update cluster link request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterLinkPut"
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
func clusterLinkPatch(d *Daemon, r *http.Request) response.Response {
	return updateClusterLink(d.State(), r, true)
}

// swagger:operation PUT /1.0/cluster/links/{name} cluster-links cluster_link_put
//
//	Update the cluster link
//
//	Updates the cluster link configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster_link
//	    description: Update cluster link request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterLinkPut"
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
func clusterLinkPut(d *Daemon, r *http.Request) response.Response {
	return updateClusterLink(d.State(), r, false)
}

// swagger:operation POST /1.0/cluster/links/{name} cluster-links cluster_link_post
//
//	Rename the cluster link
//
//	Renames the cluster link.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster_link
//	    description: Rename cluster link request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterLinkPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterLinkPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ClusterLinkPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = validateClusterLinkName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)

	var identityIdentifier string

	// Get the existing cluster link.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link: %w", err)
		}

		// Get identity for notification and lifecycle event.
		identity, err := dbCluster.GetIdentityByID(ctx, tx.Tx(), clusterLink.IdentityID)
		if err != nil {
			return fmt.Errorf("Failed getting identity with ID %d: %w", clusterLink.IdentityID, err)
		}

		identityIdentifier = identity.Identifier

		// Rename identity.
		identity.Name = req.Name
		err = query.UpdateByPrimaryKey(ctx, tx.Tx(), *identity)
		if err != nil {
			return err
		}

		// Rename cluster link.
		err = dbCluster.RenameClusterLink(ctx, tx.Tx(), name, req.Name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Send cluster link lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": name}))

	// Notify other members, update the cache, and send an identity lifecycle event.
	_, err = notify(lifecycle.IdentityUpdated, api.AuthenticationMethodTLS, identityIdentifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/links/{name} cluster-links cluster_link_delete
//
//	Delete the cluster link
//
//	Deletes the cluster link.
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
func clusterLinkDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)

	// Update DB entry.
	var identity *dbCluster.IdentitiesRow
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get cluster link.
		clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		// Get identity for notification and lifecycle event.
		identity, err = dbCluster.GetIdentityByID(ctx, tx.Tx(), clusterLink.IdentityID)
		if err != nil {
			return fmt.Errorf("Failed getting identity with ID %d: %w", clusterLink.IdentityID, err)
		}

		// Deleting the identity also deletes the cluster link.
		err = dbCluster.DeleteIdentityByAuthenticationMethodAndIdentifier(ctx, tx.Tx(), api.AuthenticationMethodTLS, identity.Identifier)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error deleting %q from database: %w", name, err))
	}

	// Send cluster link lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkDeleted.Event(name, requestor, nil))

	// Notify other members, update the cache, and send an identity lifecycle event.
	_, err = notify(lifecycle.IdentityDeleted, api.AuthenticationMethodTLS, identity.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/cluster/links cluster-links cluster_links_post
//
//	Add a cluster link
//
//	Creates a new cluster link.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: clusterLink
//	    description: Cluster link
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterLinksPost"
//	responses:
//	  "200":
//	    oneOf:
//	      - $ref: "#/responses/CertificateAddToken"
//	      - $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterLinksPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	req := api.ClusterLinksPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	clusterLinkType, err := validateClusterLinkType(req.Type)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name != "" {
		err = validateClusterLinkName(req.Name)
		if err != nil {
			return response.BadRequest(err)
		}
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)
	requestor := request.CreateRequestor(r.Context())

	if req.Name != "" && req.TrustToken == "" {
		return clusterLinkCreatePending(s, r, req, clusterLinkType, notify, requestor)
	}

	// All remaining modes require a trust token.
	if req.TrustToken == "" {
		return response.Forbidden(errors.New("Trust token required"))
	}

	trustToken, err := shared.CertificateTokenDecode(req.TrustToken)
	if err != nil {
		return response.Forbidden(fmt.Errorf("Invalid trust token: %w", err))
	}

	if req.Name != "" {
		return clusterLinkCreateActive(s, r, req, clusterLinkType, networkCert, notify, requestor, trustToken)
	}

	addresses := shared.SplitNTrimSpace(req.Config["volatile.addresses"], ",", -1, true)
	if len(addresses) > 0 {
		return clusterLinkActivate(s, r, req, notify, requestor, trustToken, addresses)
	}

	return response.BadRequest(errors.New(`Invalid cluster link request: expected one of pending creation (name without trust_token), active creation (name with trust_token), or activation (trust_token with non-empty "volatile.addresses")`))
}

// clusterLinkCreatePending handles a request to create a pending cluster link (name provided, no trust token).
// It creates a pending identity and cluster link in the database and returns a trust token for the remote cluster to use.
func clusterLinkCreatePending(s *state.State, r *http.Request, req api.ClusterLinksPost, clusterLinkType dbCluster.ClusterLinkType, notify identityNotificationFunc, requestor *api.EventLifecycleRequestor) response.Response {
	// Check if the caller has permission to create identities.
	err := s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanCreateIdentities)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if the caller has permission to create cluster links.
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanCreateClusterLinks)
	if err != nil {
		return response.SmartError(err)
	}

	// Create certificate add token.
	token, err := createCertificateAddToken(s, req.Name, api.IdentityTypeCertificateClusterLink)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed generating trust token: %w", err))
	}

	// Generate an identifier for the identity and calculate its metadata.
	identifier := uuid.New()
	metadata := dbCluster.PendingTLSMetadata{
		Secret: token.Secret,
		Expiry: token.ExpiresAt,
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed encoding pending TLS identity metadata: %w", err))
	}

	// Create the pending identity and cluster link.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := query.Create(ctx, tx.Tx(), dbCluster.IdentitiesRow{
			AuthMethod: dbCluster.AuthMethod(api.AuthenticationMethodTLS),
			Type:       dbCluster.IdentityType(api.IdentityTypeCertificateClusterLinkPending),
			Identifier: identifier.String(),
			Name:       req.Name,
			Metadata:   string(metadataJSON),
		})
		if err != nil {
			return err
		}

		// Set auth groups.
		if len(req.AuthGroups) > 0 {
			err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), id, req.AuthGroups)
			if err != nil {
				return err
			}
		}

		err = clusterLinkValidateConfig(req.Config)
		if err != nil {
			return err
		}

		clusterLinkID, err := dbCluster.CreateClusterLink(ctx, tx.Tx(), dbCluster.ClusterLinkRow{
			IdentityID:  id,
			Name:        req.Name,
			Description: req.Description,
			Type:        clusterLinkType,
		})
		if err != nil {
			return fmt.Errorf("Error inserting %q into database: %w", req.Name, err)
		}

		err = dbCluster.CreateClusterLinkConfig(ctx, tx.Tx(), clusterLinkID, req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed creating pending TLS identity: %w", err))
	}

	// Send cluster link lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkCreated.Event(req.Name, requestor, nil))

	// Notify other members, update the cache, and send a lifecycle event.
	lc, err := notify(lifecycle.IdentityCreated, api.AuthenticationMethodTLS, identifier.String(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, token, lc.Source)
}

// clusterLinkCreateActive handles a request to create an active cluster link (name and trust token provided).
// It validates the remote cluster certificate, creates the identity and cluster link locally, then activates the pending cluster link on the remote cluster.
func clusterLinkCreateActive(s *state.State, r *http.Request, req api.ClusterLinksPost, clusterLinkType dbCluster.ClusterLinkType, networkCert *shared.CertInfo, notify identityNotificationFunc, requestor *api.EventLifecycleRequestor, trustToken *api.CertificateAddToken) response.Response {
	// Check if the caller has permission to create identities.
	err := s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanCreateIdentities)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if the caller has permission to create cluster links.
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanCreateClusterLinks)
	if err != nil {
		return response.SmartError(err)
	}

	if len(trustToken.Addresses) == 0 {
		return response.BadRequest(errors.New("No cluster addresses provided in trust token"))
	}

	cert, _, err := cluster.CheckClusterLinkCertificate(r.Context(), trustToken.Addresses, trustToken.Fingerprint, version.UserAgent)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed validating cluster certificate: %w", err))
	}

	clusterCert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

	fingerprint, err := validateIdentityCert(networkCert, clusterCert)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the identity and its certificate.
		id, err := dbCluster.CreateTLSIdentity(ctx, tx.Tx(), req.Name, api.IdentityTypeCertificateClusterLink, fingerprint, clusterCert)
		if err != nil {
			return err
		}

		// Set auth groups.
		if len(req.AuthGroups) > 0 {
			err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), id, req.AuthGroups)
			if err != nil {
				return err
			}
		}

		// Create cluster link DB entry.
		clusterLinkID, err := dbCluster.CreateClusterLink(ctx, tx.Tx(), dbCluster.ClusterLinkRow{
			IdentityID:  id,
			Name:        req.Name,
			Description: req.Description,
			Type:        clusterLinkType,
		})
		if err != nil {
			return fmt.Errorf("Error inserting %q into database: %w", req.Name, err)
		}

		if req.Config == nil {
			req.Config = map[string]string{}
		}

		err = clusterLinkValidateConfig(req.Config)
		if err != nil {
			return err
		}

		req.Config["volatile.addresses"] = strings.Join(trustToken.Addresses, ",")
		err = dbCluster.CreateClusterLinkConfig(ctx, tx.Tx(), clusterLinkID, req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() {
		err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.DeleteIdentityByAuthenticationMethodAndIdentifier(ctx, tx.Tx(), api.AuthenticationMethodTLS, fingerprint)
		})
		if err != nil {
			logger.Warn("Failed cleaning up cluster link after activation failure", logger.Ctx{"err": err, "clusterLinkName": req.Name, "fingerprint": fingerprint})
		}
	})

	localHTTPSAddress := s.LocalConfig.HTTPSAddress()
	listenAddresses, err := util.ListenAddresses(localHTTPSAddress)
	if err != nil {
		return response.InternalError(err)
	}

	clusterLinkPut := api.ClusterLinkPut{
		Config: map[string]string{"volatile.addresses": strings.Join(listenAddresses, ",")},
	}

	activationErrs := make([]error, 0, len(trustToken.Addresses))

	// Send POST to remote /1.0/cluster/links to activate pending cluster link using token.
	for _, address := range trustToken.Addresses {
		args := &lxd.ConnectionArgs{
			TLSServerCert: clusterCert,
			UserAgent:     version.UserAgent,
		}

		clusterAddress := util.CanonicalNetworkAddress(address, shared.HTTPSDefaultPort)
		client, err := lxd.ConnectLXD("https://"+clusterAddress, args)
		if err != nil {
			activationErrs = append(activationErrs, fmt.Errorf("Failed connecting to remote cluster address %q: %w", clusterAddress, err))
			continue
		}

		_, _, err = client.RawQuery(http.MethodPost, "/1.0/cluster/links", api.ClusterLinksPost{TrustToken: trustToken.String(), Type: req.Type, ClusterLinkPut: clusterLinkPut, ClusterCertificate: string(networkCert.PublicKey())}, "")
		if err != nil {
			activationErrs = append(activationErrs, fmt.Errorf("Remote cluster address %q: %w", clusterAddress, err))
			continue
		}

		reverter.Success()

		// Send cluster link lifecycle event.
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkCreated.Event(req.Name, requestor, nil))

		// Notify other members, update the cache, and send an identity lifecycle event.
		lc, err := notify(lifecycle.IdentityCreated, api.AuthenticationMethodTLS, fingerprint, true)
		if err != nil {
			return response.SmartError(err)
		}

		err = cluster.RefreshClusterLinkVolatileAddresses(r.Context(), s, req.Name)
		if err != nil {
			logger.Warn("Failed refreshing cluster link addresses after link creation", logger.Ctx{"err": err, "clusterLinkName": req.Name})
		}

		return response.SyncResponseLocation(true, nil, lc.Source)
	}

	var statusErr error
	errStrings := make([]string, 0, len(activationErrs))
	for _, err := range activationErrs {
		errStrings = append(errStrings, err.Error())

		if statusErr == nil {
			_, found := api.StatusErrorMatch(err)
			if found {
				statusErr = err
			}
		}
	}

	if statusErr != nil {
		return response.SmartError(fmt.Errorf("Failed activating cluster link %q after trying %d address(es): %w", req.Name, len(activationErrs), statusErr))
	}

	return response.SmartError(api.StatusErrorf(http.StatusBadGateway, "Failed activating cluster link %q: %s", req.Name, strings.Join(errStrings, "; ")))
}

// clusterLinkActivate handles a server-side request to activate a pending cluster link using a trust token provided by another cluster. The caller is the remote cluster, not a human operator.
func clusterLinkActivate(s *state.State, r *http.Request, req api.ClusterLinksPost, notify identityNotificationFunc, requestor *api.EventLifecycleRequestor, trustToken *api.CertificateAddToken, addresses []string) response.Response {
	if trustToken.Type != api.IdentityTypeCertificateClusterLink {
		return response.Forbidden(fmt.Errorf("Invalid trust token type %q", trustToken.Type))
	}

	// Check there is a matching pending cluster link identity.
	identifier, err := tlsIdentityTokenValidate(r.Context(), s, *trustToken)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed during search for pending identity: %w", err))
	}

	if req.ClusterCertificate == "" {
		return response.BadRequest(errors.New("Cluster certificate required"))
	}

	block, _ := pem.Decode([]byte(req.ClusterCertificate))
	if block == nil {
		return response.BadRequest(errors.New("Failed decoding certificate"))
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		// This should not happen.
		return response.InternalError(err)
	}

	// Calculate cluster certificate fingerprint.
	fingerprint, err := shared.CertFingerprintStr(req.ClusterCertificate)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed calculating fingerprint: %w", err))
	}

	// Validate that the addresses are reachable and return consistent certificates with fingerprints matching that of the requestor's cluster certificate.
	cert, _, err = cluster.CheckClusterLinkCertificate(r.Context(), addresses, fingerprint, version.UserAgent)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed validating cluster certificate: %w", err))
	}

	var clusterLinkName string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Activate the pending identity with the certificate.
		err = dbCluster.ActivateTLSIdentity(ctx, tx.Tx(), identifier, cert)
		if err != nil {
			return fmt.Errorf("Failed activating identity %q: %w", identifier.String(), err)
		}

		identity, err := dbCluster.GetIdentityByNameOrIdentifier(ctx, tx.Tx(), api.AuthenticationMethodTLS, fingerprint)
		if err != nil {
			return fmt.Errorf("Failed loading identity: %w", err)
		}

		// Get cluster link by name.
		clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), identity.Name)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link: %w", err)
		}

		clusterLinkName = clusterLink.Name

		config, err := dbCluster.GetClusterLinkConfig(ctx, tx.Tx(), &clusterLink.ID)
		if err != nil {
			return fmt.Errorf("Failed loading cluster link config: %w", err)
		}

		currentConfig := config[clusterLink.ID]
		if currentConfig == nil {
			currentConfig = map[string]string{}
		}

		// Activate the pending cluster link by updating only volatile keys from the request.
		// This preserves any user.* configuration set while the link was pending.
		err = dbCluster.UpdateClusterLinkConfig(ctx, tx.Tx(), clusterLink.ID, mergeClusterLinkActivationConfig(currentConfig, req.Config))
		if err != nil {
			return fmt.Errorf("Failed activating cluster link %q: %w", clusterLink.Name, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = cluster.RefreshClusterLinkVolatileAddresses(r.Context(), s, clusterLinkName)
	if err != nil {
		logger.Warn("Failed refreshing cluster link addresses after link activation", logger.Ctx{"err": err, "clusterLinkName": clusterLinkName})
	}

	// Send cluster link lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkCreated.Event(clusterLinkName, requestor, nil))

	// Notify other members, update the cache, and send an identity lifecycle event.
	lc, err := notify(lifecycle.IdentityUpdated, api.AuthenticationMethodTLS, identifier.String(), true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// validateClusterLinkName validates cluster link names used in API paths and entity records.
func validateClusterLinkName(name string) error {
	if name == "" {
		return errors.New("Cluster link name cannot be empty")
	}

	err := validate.IsURLSegmentSafe(name)
	if err != nil {
		return err
	}

	// Defend against path traversal attacks.
	if !shared.IsFileName(name) {
		return fmt.Errorf("Invalid name %q, may not contain slashes or consecutive dots", name)
	}

	err = validate.IsEntityName(name)
	if err != nil {
		return err
	}

	return nil
}

// validateClusterLinkType returns the requested cluster link type if it is explicitly set
// to a supported value.
func validateClusterLinkType(reqType string) (dbCluster.ClusterLinkType, error) {
	clusterLinkType := dbCluster.ClusterLinkType(reqType)
	_, err := clusterLinkType.Value()
	if err != nil {
		return "", err
	}

	return clusterLinkType, nil
}

// checkVolatileConfig returns an error if any volatile.* keys were added, removed, or changed in updated relative to current.
// When strict is true (PUT semantics), the volatile key sets must be identical; when false (PATCH semantics), omitted volatile keys are allowed but changed or new volatile keys are still rejected.
func checkVolatileConfig(current, updated map[string]string, strict bool) error {
	currentVolatile := make(map[string]string)
	for k, v := range current {
		if strings.HasPrefix(k, "volatile.") {
			currentVolatile[k] = v
		}
	}

	updatedVolatile := make(map[string]string)
	for k, v := range updated {
		if strings.HasPrefix(k, "volatile.") {
			updatedVolatile[k] = v
		}
	}

	if strict {
		if !maps.Equal(currentVolatile, updatedVolatile) {
			return errors.New("Volatile configuration keys cannot be modified")
		}

		return nil
	}

	// Non-strict (PATCH): volatile keys may be omitted, but present keys must be unchanged.
	for k, uv := range updatedVolatile {
		cv, ok := currentVolatile[k]
		if !ok || cv != uv {
			return errors.New("Volatile configuration keys cannot be modified")
		}
	}

	return nil
}

// mergeClusterLinkActivationConfig preserves existing cluster link config and only applies
// volatile updates supplied by the activation request.
func mergeClusterLinkActivationConfig(current, updated map[string]string) map[string]string {
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]string{}
	}

	for k, v := range updated {
		if strings.HasPrefix(k, "volatile.") {
			merged[k] = v
		}
	}

	return merged
}

// clusterLinkValidateConfig validates the configuration keys/values for cluster links.
func clusterLinkValidateConfig(config map[string]string) error {
	clusterLinkConfigKeys := map[string]func(value string) error{
		// lxdmeta:generate(entities=cluster; group=link-volatile-conf; key=volatile.addresses)
		// A comma-separated list of cluster link member addresses.
		// ---
		//  type: string
		//  shortdesc: Cluster link member addresses.
		//  scope: global
		"volatile.addresses": func(value string) error {
			for _, addr := range shared.SplitNTrimSpace(value, ",", -1, true) {
				host, portStr, err := net.SplitHostPort(addr)
				if err != nil {
					return fmt.Errorf("Invalid address format: %w", err)
				}

				// Allow IP addresses and hostnames.
				if host == "" {
					return fmt.Errorf("Invalid host: %q", host)
				}

				ip := net.ParseIP(host)
				if ip == nil {
					err = validate.IsDomainName(host)
					if err != nil {
						return err
					}
				}

				err = validate.IsNetworkPort(portStr)
				if err != nil {
					return fmt.Errorf("Invalid port: %w", err)
				}
			}

			return nil
		},
	}

	for k, v := range config {
		// User keys are free for all.

		// lxdmeta:generate(entities=cluster; group=link-conf; key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := clusterLinkConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid cluster link configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid cluster link configuration key %q value: %w", k, err)
		}
	}

	return nil
}

// autoRefreshClusterLinkVolatileAddressesTask returns a task function and schedule for refreshing cluster link volatile addresses.
// Volatile addresses are refreshed daily and the task only runs on the cluster leader.
func autoRefreshClusterLinkVolatileAddressesTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		leaderInfo, err := s.LeaderInfo()
		if err != nil {
			logger.Error("Failed getting leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if !leaderInfo.Leader {
			logger.Debug("Skipping refresh cluster link address task since we're not leader")
			return
		}

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return autoRefreshClusterLinkVolatileAddresses(ctx, s)
		}

		logger.Info("Refreshing cluster link addresses")
		op, err := operations.ScheduleServerOperation(s, operations.OperationArgs{
			Type:    operationtype.RefreshClusterLinkVolatileAddresses,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		})
		if err != nil {
			logger.Error("Failed creating refresh cluster link addresses operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed refreshing cluster link addresses", logger.Ctx{"err": err})
			return
		}

		logger.Info("Cluster link addresses refreshed")
	}

	return f, task.Daily()
}

// autoRefreshClusterLinkVolatileAddresses refreshes the volatile addresses of all cluster links.
func autoRefreshClusterLinkVolatileAddresses(ctx context.Context, s *state.State) error {
	var names []string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		links, err := dbCluster.GetClusterLinks(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading cluster links: %w", err)
		}

		names = make([]string, len(links))
		for i, l := range links {
			names[i] = l.Name
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, name := range names {
		err := cluster.RefreshClusterLinkVolatileAddresses(ctx, s, name)
		if err != nil {
			logger.Warn("Failed refreshing cluster link addresses", logger.Ctx{"err": err, "clusterLinkName": name})
		}
	}

	return nil
}
