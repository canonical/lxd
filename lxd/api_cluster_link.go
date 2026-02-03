package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
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

	recursion := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeClusterLink, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeClusterLink)
	if err != nil {
		return response.InternalError(err)
	}

	var apiClusterLinks []*api.ClusterLink
	var clusterLinkURLs []string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		allClusterLinks, err := dbCluster.GetClusterLinks(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed fetching cluster links: %w", err)
		}

		clusterLinks := make([]dbCluster.ClusterLink, 0, len(allClusterLinks))
		for _, clusterLink := range allClusterLinks {
			if userHasPermission(entity.ClusterLinkURL(clusterLink.Name)) {
				clusterLinks = append(clusterLinks, clusterLink)
			}
		}

		if recursion {
			apiClusterLinks = make([]*api.ClusterLink, 0, len(clusterLinks))
			for _, clusterLink := range clusterLinks {
				apiClusterLink, err := clusterLink.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				apiClusterLinks = append(apiClusterLinks, apiClusterLink)
			}
		} else {
			clusterLinkURLs = make([]string, 0, len(clusterLinks))
			for _, clusterLink := range clusterLinks {
				clusterLinkURLs = append(clusterLinkURLs, entity.ClusterLinkURL(clusterLink.Name).String())
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		return response.SyncResponse(true, clusterLinkURLs)
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
			return fmt.Errorf("Failed fetching cluster link: %w", err)
		}

		apiClusterLink, err = dbClusterLink.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

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

	// TODO: Once delegated cluster links are supported, if a request tries to create/update/delete a delegated cluster link, the correct user agent must be set to prevent accidental edits of delegated cluster links.

	var dbClusterLink *dbCluster.ClusterLink
	var apiClusterLink *api.ClusterLink
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get cluster link by name.
		dbClusterLink, err = dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed fetching cluster link: %w", err)
		}

		apiClusterLink, err = dbClusterLink.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

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

		err = dbCluster.UpdateClusterLink(ctx, tx.Tx(), name, *dbClusterLink)
		if err != nil {
			return err
		}

		err = dbCluster.UpdateClusterLinkConfig(ctx, tx.Tx(), apiClusterLink.Name, req.Config)
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
//	      $ref: "#/definitions/ClusterLinkRename"
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

	req := api.ClusterLinkRename{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// TODO: Once delegated cluster links are supported, if a request tries to create/update/delete a delegated cluster link, the correct user agent must be set to prevent accidental edits of delegated cluster links.

	// Get the existing cluster link.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed fetching cluster link: %w", err)
		}

		// Get identity for notification and lifecycle event.
		identities, err := dbCluster.GetIdentitys(ctx, tx.Tx(), dbCluster.IdentityFilter{
			ID: &clusterLink.IdentityID,
		})
		if err != nil {
			return err
		}

		identity := &identities[0]

		// Rename identity.
		err = dbCluster.UpdateIdentity(ctx, tx.Tx(), identity.AuthMethod, identity.Identifier, dbCluster.Identity{
			AuthMethod: identity.AuthMethod,
			Type:       identity.Type,
			Identifier: identity.Identifier,
			Name:       req.Name,
			Metadata:   identity.Metadata,
		})
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

	// TODO: Once delegated cluster links are supported, if a request tries to create/update/delete a delegated cluster link, the correct user agent must be set to prevent accidental edits of delegated cluster links.

	// Update DB entry.
	var identity *dbCluster.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get cluster link.
		clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		// Get identity for notification and lifecycle event.
		identities, err := dbCluster.GetIdentitys(ctx, tx.Tx(), dbCluster.IdentityFilter{
			ID: &clusterLink.IdentityID,
		})
		if err != nil {
			return err
		}

		identity = &identities[0]

		// Deleting the identity also deletes the cluster link.
		err = dbCluster.DeleteIdentity(ctx, tx.Tx(), api.AuthenticationMethodTLS, identity.Identifier)
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
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkRemoved.Event(name, requestor, nil))

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
//	      $ref: "#/definitions/ClusterLinkPost"
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

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	// Parse the request.
	req := api.ClusterLinkPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)
	requestor := request.CreateRequestor(r.Context())

	// TODO: Once delegated cluster links are supported, if a request tries to create/update/delete a delegated cluster link, the correct user agent must be set to prevent accidental edits of delegated cluster links.
	clusterLinkType := api.ClusterLinkTypeUser

	if req.Name != "" && req.TrustToken == "" {
		// This is a request to create a pending cluster link (when a trust token is not provided).
		logger.Info("Creating pending cluster link", logger.Ctx{"name": req.Name})

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
			id, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
				AuthMethod: api.AuthenticationMethodTLS,
				Type:       api.IdentityTypeCertificateClusterLinkPending,
				Identifier: identifier.String(),
				Name:       req.Name,
				Metadata:   string(metadataJSON),
			})
			if err != nil {
				return err
			}

			// Set auth groups.
			if len(req.AuthGroups) > 0 {
				err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), int(id), req.AuthGroups)
				if err != nil {
					return err
				}
			}

			err = clusterLinkValidateConfig(req.Config)
			if err != nil {
				return err
			}

			_, err = dbCluster.CreateClusterLink(ctx, tx.Tx(), dbCluster.ClusterLink{
				IdentityID:  id,
				Name:        req.Name,
				Description: req.Description,
				Type:        dbCluster.ClusterLinkType(clusterLinkType),
			})
			if err != nil {
				return fmt.Errorf("Error inserting %q into database: %w", req.Name, err)
			}

			err = dbCluster.CreateClusterLinkConfig(ctx, tx.Tx(), req.Name, req.Config)
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

	// This is a request to create an active cluster link and identity, or activate a pending cluster link using a token provided by another cluster.
	if req.TrustToken == "" {
		return response.Forbidden(errors.New("Trust token required"))
	}

	// Decode the token.
	trustToken, err := shared.CertificateTokenDecode(req.TrustToken)
	if err != nil {
		return response.Forbidden(fmt.Errorf("Invalid trust token: %w", err))
	}

	if req.Name != "" {
		// This is a request to create an active cluster link and identity.
		logger.Info("Creating cluster link using trust token", logger.Ctx{"name": req.Name})

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

		fingerprint, metadata, err := validateIdentityCert(networkCert, clusterCert)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check if we already have the certificate.
			_, err := dbCluster.GetIdentityID(ctx, tx.Tx(), api.AuthenticationMethodTLS, fingerprint)
			if err == nil {
				return api.StatusErrorf(http.StatusConflict, "Identity already exists")
			}

			// Create the identity.
			id, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
				AuthMethod: api.AuthenticationMethodTLS,
				Type:       api.IdentityTypeCertificateClusterLink,
				Identifier: fingerprint,
				Name:       req.Name,
				Metadata:   metadata,
			})
			if err != nil {
				return err
			}

			// Set auth groups.
			if len(req.AuthGroups) > 0 {
				err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), int(id), req.AuthGroups)
				if err != nil {
					return err
				}
			}

			// Create cluster link DB entry.
			_, err = dbCluster.CreateClusterLink(ctx, tx.Tx(), dbCluster.ClusterLink{
				IdentityID:  id,
				Name:        req.Name,
				Description: req.Description,
				Type:        dbCluster.ClusterLinkType(clusterLinkType),
			})
			if err != nil {
				return fmt.Errorf("Error inserting %q into database: %w", req.Name, err)
			}

			req.Config = map[string]string{"volatile.addresses": strings.Join(trustToken.Addresses, ",")}
			err = dbCluster.CreateClusterLinkConfig(ctx, tx.Tx(), req.Name, req.Config)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Send cluster link lifecycle event.
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterLinkCreated.Event(req.Name, requestor, nil))

		// Notify other members, update the cache, and send an identity lifecycle event.
		lc, err := notify(lifecycle.IdentityCreated, api.AuthenticationMethodTLS, fingerprint, true)
		if err != nil {
			return response.SmartError(err)
		}

		// Send POST to remote /1.0/cluster/links to activate pending cluster link using token.
		for _, address := range trustToken.Addresses {
			localHTTPSAddress := s.LocalConfig.HTTPSAddress()

			args := &lxd.ConnectionArgs{
				InsecureSkipVerify: true,
				UserAgent:          version.UserAgent,
			}

			clusterAddress := util.CanonicalNetworkAddress(address, shared.HTTPSDefaultPort)
			client, err := lxd.ConnectLXD("https://"+clusterAddress, args)
			if err != nil {
				continue
			}

			listenAddresses, err := util.ListenAddresses(localHTTPSAddress)
			if err != nil {
				return response.InternalError(err)
			}

			clusterLinkPut := api.ClusterLinkPut{
				Config: map[string]string{"volatile.addresses": strings.Join(listenAddresses, ",")},
			}

			_, _, err = client.RawQuery(http.MethodPost, "/1.0/cluster/links", api.ClusterLinkPost{TrustToken: trustToken.String(), ClusterLinkPut: clusterLinkPut, ClusterCertificate: string(networkCert.PublicKey())}, "")
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed activating cluster link %q: %w", req.Name, err))
			}

			err = refreshClusterLinkVolatileAddressesNow(r.Context(), s, req.Name)
			if err != nil {
				logger.Warn("Failed refreshing cluster link addresses after link creation", logger.Ctx{"err": err, "clusterLinkName": req.Name})
			}

			return response.SyncResponseLocation(true, nil, lc.Source)
		}
	}

	addresses := shared.SplitNTrimSpace(req.Config["volatile.addresses"], ",", -1, false)
	if req.Name == "" && len(addresses) > 0 {
		// This is a request to activate a pending cluster link using a token provided by another cluster.
		logger.Info("Activating cluster link using trust token")

		// Check there is a matching pending cluster link identity.
		identifier, err := tlsIdentityTokenValidate(r.Context(), s, *trustToken)
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed during search for pending identity: %w", err))
		}

		if trustToken.Type != api.IdentityTypeCertificateClusterLink {
			return response.Forbidden(fmt.Errorf("Invalid trust token type %q", trustToken.Type))
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
				return fmt.Errorf("Failed fetching identity: %w", err)
			}

			// Get cluster link by name.
			clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), identity.Name)
			if err != nil {
				return fmt.Errorf("Failed fetching cluster link: %w", err)
			}

			clusterLinkName = clusterLink.Name

			// Activate the pending cluster link (update its addresses).
			err = dbCluster.UpdateClusterLinkConfig(ctx, tx.Tx(), clusterLink.Name, req.Config)
			if err != nil {
				return fmt.Errorf("Failed activating cluster link %q: %w", clusterLink.Name, err)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		err = refreshClusterLinkVolatileAddressesNow(r.Context(), s, clusterLinkName)
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

	return response.SyncResponse(false, nil)
}

// refreshClusterLinkVolatileAddressesNow performs an immediate best-effort refresh of volatile addresses for a cluster link.
// We do this right after link establishment because trust token addresses are only bootstrap endpoints; waiting for the daily refresh task can leave the link with a partial address set and reduce failover/reachability.
func refreshClusterLinkVolatileAddressesNow(ctx context.Context, s *state.State, clusterLinkName string) error {
	clusterLinks, err := getClusterLinks(ctx, s, clusterLinkName)
	if err != nil {
		return err
	}

	if len(clusterLinks) == 0 {
		return fmt.Errorf("Cluster link %q not found", clusterLinkName)
	}

	return cluster.RefreshClusterLinkVolatileAddresses(ctx, s, *clusterLinks[0])
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
		"volatile.addresses": validate.Optional(func(value string) error {
			for _, addr := range shared.SplitNTrimSpace(value, ",", -1, false) {
				host, portStr, err := net.SplitHostPort(addr)
				if err != nil {
					return fmt.Errorf("Invalid address format: %w", err)
				}

				// Allow IP addresses and hostnames.
				host = strings.Trim(host, "[]")
				if host == "" {
					return fmt.Errorf("Invalid host: %q", host)
				}

				ip := net.ParseIP(host)
				if ip == nil {
					for _, label := range strings.Split(host, ".") {
						err := validate.IsHostname(label)
						if err != nil {
							return fmt.Errorf("Invalid hostname: %s", host)
						}
					}
				}

				port, err := strconv.Atoi(portStr)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("Invalid port: %s", portStr)
				}
			}

			return nil
		}),
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
			return fmt.Errorf("Invalid cluster link configuration key %q value", k)
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

		if !leaderInfo.Clustered {
			return
		}

		if !leaderInfo.Leader {
			logger.Debug("Skipping refresh cluster link address task since we're not leader")
			return
		}

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return autoRefreshClusterLinkVolatileAddresses(ctx, s)
		}

		op, err := operations.CreateServerOperation(s, operations.OperationArgs{
			ProjectName: "",
			Type:        operationtype.RefreshClusterLinkVolatileAddresses,
			Class:       operations.OperationClassTask,
			RunHook:     opRun,
		})
		if err != nil {
			logger.Error("Failed creating refresh cluster link addresses operation", logger.Ctx{"err": err})
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting refresh cluster link addresses operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed refreshing cluster link addresses", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Daily()
}

// autoRefreshClusterLinkVolatileAddresses refreshes the volatile addresses of all cluster links.
func autoRefreshClusterLinkVolatileAddresses(ctx context.Context, s *state.State) error {
	clusterLinks, err := getClusterLinks(ctx, s, "")
	if err != nil {
		return err
	}

	for _, clusterLink := range clusterLinks {
		err := cluster.RefreshClusterLinkVolatileAddresses(ctx, s, *clusterLink)
		if err != nil {
			logger.Warn("Failed refreshing cluster link addresses", logger.Ctx{"err": err, "clusterLinkName": clusterLink.Name})
		}
	}

	return nil
}

// getClusterLinks returns cluster links converted to API type. If clusterLinkName is empty, all links are returned.
func getClusterLinks(ctx context.Context, s *state.State, clusterLinkName string) ([]*api.ClusterLink, error) {
	clusterLinks := make([]*api.ClusterLink, 0)

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		if clusterLinkName != "" {
			dbClusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), clusterLinkName)
			if err != nil {
				return fmt.Errorf("Failed fetching cluster link %q: %w", clusterLinkName, err)
			}

			clusterLink, err := dbClusterLink.ToAPI(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed converting cluster link to API type: %w", err)
			}

			clusterLinks = append(clusterLinks, clusterLink)
			return nil
		}

		dbClusterLinks, err := dbCluster.GetClusterLinks(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed fetching cluster links: %w", err)
		}

		clusterLinks = make([]*api.ClusterLink, 0, len(dbClusterLinks))
		for _, dbClusterLink := range dbClusterLinks {
			clusterLink, err := dbClusterLink.ToAPI(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed converting cluster link to API type: %w", err)
			}

			clusterLinks = append(clusterLinks, clusterLink)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return clusterLinks, nil
}
