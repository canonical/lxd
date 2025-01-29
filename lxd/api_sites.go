package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var sitesCmd = APIEndpoint{
	Path:        "sites",
	MetricsType: entity.TypeClusterMember,

	Get: APIEndpointAction{Handler: sitesGet, AccessHandler: allowAuthenticated},
}

var siteCmd = APIEndpoint{
	Path:        "sites/{name}",
	MetricsType: entity.TypeClusterMember,

	Get:    APIEndpointAction{Handler: siteGet, AccessHandler: allowAuthenticated},
	Put:    APIEndpointAction{Handler: sitePut, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: siteDelete, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var siteJoinCmd = APIEndpoint{
	Path:        "site/join",
	MetricsType: entity.TypeClusterMember,

	Post: APIEndpointAction{Handler: sitePost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation GET /1.0/sites sites
//
//	Get sites
//
//	Gets sites.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Sites
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
//	          $ref: "#/definitions/site"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func sitesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	var resp []*api.Site

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		sites, err := dbCluster.GetSites(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Fetch site: %w", err)
		}

		for _, site := range sites {
			siteAPI, err := site.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			resp = append(resp, siteAPI)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, resp)
}

// swagger:operation GET /1.0/sites/{name} sites
//
//	Get site
//
//	Get a site.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Site
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
//	          $ref: "#/definitions/site"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func siteGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var resp *api.Site

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		site, err := dbCluster.GetSite(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Fetch site: %w", err)
		}

		resp, err = site.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{resp.Name, resp.Addresses, resp.Type, resp.Description}
	return response.SyncResponseETag(true, resp, etag)
}

// swagger:operation PUT /1.0/sites/{name} sites
//
//	Update site
//
//	Update a site.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Update site request
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
//	          $ref: "#/definitions/site"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func sitePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.SitePut{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// TODO: Update addresses if provided.

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name or addresses provided"))
	}

	// Update DB entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get site by name.
		site, err := dbCluster.GetSite(ctx, tx.Tx(), req.Name)
		if err != nil {
			return fmt.Errorf("Fetch site: %w", err)
		}

		err = dbCluster.UpdateSite(ctx, tx.Tx(), req.Name, *site)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error updating %q in database: %w", req.Name, err))
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.SiteUpdated.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle(req.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/sites/{name} sites
//
//	Delete site
//
//	Delete a site.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Delete site request
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
//	          $ref: "#/definitions/site"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func siteDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.SitePut{}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Update DB entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := dbCluster.DeleteSite(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error deleting %q from database: %w", req.Name, err))
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.SiteDeleted.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle(req.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation POST /1.0/site/join sites
//
//	Post sites
//
//	Add a site.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Site cluster add request
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
//	          $ref: "#/definitions/site"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func sitePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request
	req := api.SitePost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.TrustToken == "" {
		return response.Forbidden(errors.New("Trust token required"))
	}

	joinToken, err := shared.JoinTokenDecode(req.TrustToken)
	if err != nil {
		return response.BadRequest(errors.New("Invalid trust token"))
	}

	var cert *x509.Certificate

	// Attempt to find a working site member to use for joining by retrieving the cluster certificate from each address in the join token until we succeed.
	for _, address := range joinToken.Addresses {
		clusterAddress := util.CanonicalNetworkAddress(address, shared.HTTPSDefaultPort)
		u, err := url.Parse("https://" + clusterAddress)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			continue
		}

		cert, err = shared.GetRemoteCertificate(u.String(), version.UserAgent)
		if err != nil {
			continue
		}

		certDigest := shared.CertFingerprint(cert)
		if joinToken.Fingerprint != certDigest {
			return response.Forbidden(fmt.Errorf("Certificate fingerprint mismatch between join token and cluster member %q", clusterAddress))
		}

		break
	}

	if cert == nil {
		return response.BadRequest(fmt.Errorf("Unable to connect to any of the cluster members specified in join token"))
	}

	if req.IdentityName == "" {
		return response.BadRequest(fmt.Errorf("Identity name must be provided"))
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var identity *dbCluster.Identity
		// Retrieve pending TLS identity.
		identity, err := dbCluster.GetPendingTLSIdentityByName(ctx, tx.Tx(), req.IdentityName)
		if err != nil {
			return err
		}

		uid, err := uuid.Parse(identity.Identifier)
		if err != nil {
			return fmt.Errorf("Unexpected identifier format for pending TLS identity: %w", err)
		}

		// Check requested authentication group is associated with the requested identity.
		validAuthGroup := false
		authGroups, _ := dbCluster.GetAuthGroupsByIdentityID(ctx, tx.Tx(), identity.ID)
		for _, authGroup := range authGroups {
			if req.Group != authGroup.Name {
				continue
			}

			validAuthGroup = true
		}

		if !validAuthGroup {
			return fmt.Errorf("Requested authentication group %q not associated with identity %q", req.Group, req.IdentityName)
		}

		// Activate the pending identity with the certificate.
		err = dbCluster.ActivateTLSIdentity(ctx, tx.Tx(), uid, cert)
		if err != nil {
			return err
		}

		// Site addresses are stored as a string containing a comma separated list.
		siteAddresses := strings.Join(joinToken.Addresses, ",")

		// Update DB entry for site.
		// TODO: Type and description are hardcoded for now.
		site := dbCluster.Site{
			IdentityID:  identity.ID,
			Name:        req.Name,
			Addresses:   siteAddresses,
			Type:        0,
			Delegated:   0,
			Description: "",
		}

		_, err = dbCluster.CreateSite(ctx, tx.Tx(), site)
		if err != nil {
			return fmt.Errorf("Error inserting %q into database: %w", req.Name, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Update cached trusted certificates.
	s.UpdateIdentityCache()

	requestor := request.CreateRequestor(r)
	lc := lifecycle.SiteCreated.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle(req.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}
