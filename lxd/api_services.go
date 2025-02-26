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
	"sync"

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

var servicesCmd = APIEndpoint{
	Path:        "services",
	MetricsType: entity.TypeServer,

	Get: APIEndpointAction{Handler: servicesGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementAdmin)},
}

var serviceCmd = APIEndpoint{
	Path:        "services/{name}",
	MetricsType: entity.TypeServer,

	Get:    APIEndpointAction{Handler: serviceGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementAdmin)},
	Put:    APIEndpointAction{Handler: servicePut, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementAdmin)},
	Delete: APIEndpointAction{Handler: serviceDelete, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementAdmin)},
}

var serviceJoinCmd = APIEndpoint{
	Path:        "services/add",
	MetricsType: entity.TypeServer,

	Post: APIEndpointAction{Handler: servicePost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementAdmin)},
}

// swagger:operation GET /1.0/services services
//
//	Get services
//
//	Gets services.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Services
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
//	          $ref: "#/definitions/service"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func servicesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	var resp []*api.Service

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		services, err := dbCluster.GetServices(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("failed to fetch service: %w", err)
		}

		for _, service := range services {
			serviceAPI, err := service.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			resp = append(resp, serviceAPI)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, resp)
}

// swagger:operation GET /1.0/services/{name} services
//
//	Get service
//
//	Get a service.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Service
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
//	          $ref: "#/definitions/service"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func serviceGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var resp *api.Service

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		service, err := dbCluster.GetService(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("failed to fetch service: %w", err)
		}

		resp, err = service.ToAPI(ctx, tx.Tx())
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

// swagger:operation PUT /1.0/services/{name} services
//
//	Update service
//
//	Update a service.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Update service request
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
//	          $ref: "#/definitions/service"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func servicePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ServicePut{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// TODO: If a user tries to create/update/delete a delegated service, the correct user agent must be set to prevent accidental edits of delegated services.

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing service.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := dbCluster.GetService(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("failed to fetch service: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Duplicate config for etag modification and generation.
	// TODO:
	// etagConfig := util.CopyConfig(service.Config)

	// Update DB entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get service by name.
		service, err := dbCluster.GetService(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("failed to fetch service: %w", err)
		}

		err = dbCluster.UpdateService(ctx, tx.Tx(), name, *service)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ServiceUpdated.Event(name, requestor, nil)
	s.Events.SendLifecycle(name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/services/{name} services
//
//	Delete service
//
//	Delete a service.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Delete service request
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
//	          $ref: "#/definitions/service"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func serviceDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Update DB entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := dbCluster.DeleteService(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error deleting %q from database: %w", name, err))
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ServiceDeleted.Event(name, requestor, nil)
	s.Events.SendLifecycle(name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation POST /1.0/service/join services
//
//	Post services
//
//	Join a service.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Add service request
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
//	          $ref: "#/definitions/service"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func servicePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	req := api.ServicePost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Type == api.TypeLXD.String() {
		if req.IdentityName == "" {
			return response.BadRequest(fmt.Errorf("Identity name must be provided"))
		}

		if req.TrustToken == "" {
			return response.Forbidden(errors.New("Trust token required"))
		}

		joinToken, err := shared.CertificateTokenDecode(req.TrustToken)
		if err != nil {
			return response.Forbidden(errors.New("Invalid trust token"))
		}

		// Retrieve requested join service's cluster certificate.
		cert, err := getClusterCertificate(joinToken, req.Address, version.UserAgent)
		if err != nil {
			return response.BadRequest(err)
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

			// Activate the pending identity with the certificate.
			err = dbCluster.ActivateTLSIdentity(ctx, tx.Tx(), uid, cert)
			if err != nil {
				return err
			}

			// Service addresses are stored as a string containing a comma separated list.
			serviceAddresses := strings.Join(joinToken.Addresses, ",")

			// Update DB entry for service.
			// TODO: Type and delegated are hardcoded for now.
			service := dbCluster.Service{
				IdentityID:  identity.ID,
				Name:        req.Name,
				Addresses:   serviceAddresses,
				Type:        int(api.TypeLXD),
				Description: req.Description,
			}

			_, err = dbCluster.CreateService(ctx, tx.Tx(), service)
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
	} else if req.Type == api.TypeSimpleStreams.String() {
		// TODO: Implement image server service join.
		return nil
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ServiceCreated.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle(req.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// getClusterCertificate attempts to retrieve a valid cluster certificate concurrently from multiple addresses contained in the join token.
// Once a working certificate is found, it cancels the remaining lookups.
// If an override address is included in the service join request, it will be used instead of the addresses in the join token.
func getClusterCertificate(joinToken *api.CertificateAddToken, overrideAddress string, userAgent string) (*x509.Certificate, error) {
	type result struct {
		cert    *x509.Certificate
		err     error
		address string
	}

	// Early return if an override address is provided.
	if overrideAddress != "" {
		clusterAddress := util.CanonicalNetworkAddress(overrideAddress, shared.HTTPSDefaultPort)
		u, err := url.Parse("https://" + clusterAddress)
		if err != nil || u.Host == "" {
			return nil, err
		}

		// Try to retrieve the remote certificate.
		cert, err := shared.GetRemoteCertificate(u.String(), userAgent)
		if err != nil {
			return nil, err
		}

		// Check that the certificate fingerprint matches the token.
		certDigest := shared.CertFingerprint(cert)
		if joinToken.Fingerprint != certDigest {
			return nil, fmt.Errorf("certificate fingerprint mismatch for address %q", clusterAddress)
		}

		return cert, nil
	}

	resCh := make(chan result, len(joinToken.Addresses))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	// Launch a goroutine for each address.
	for _, address := range joinToken.Addresses {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()

			clusterAddress := util.CanonicalNetworkAddress(addr, shared.HTTPSDefaultPort)
			u, err := url.Parse("https://" + clusterAddress)
			if err != nil || u.Host == "" {
				return
			}

			// Try to retrieve the remote certificate.
			cert, err := shared.GetRemoteCertificate(u.String(), userAgent)
			if err != nil {
				select {
				case resCh <- result{cert: nil, err: fmt.Errorf("failed to retrieve certificate from %q: %w", clusterAddress, err), address: clusterAddress}:
				case <-ctx.Done():
				}

				return
			}

			// Check that the certificate fingerprint matches the token.
			certDigest := shared.CertFingerprint(cert)
			if joinToken.Fingerprint != certDigest {
				select {
				case resCh <- result{cert: nil, err: fmt.Errorf("certificate fingerprint mismatch for address %q", clusterAddress), address: clusterAddress}:
				case <-ctx.Done():
				}

				return
			}

			// Return the valid certificate.
			select {
			case resCh <- result{cert: cert, err: nil, address: clusterAddress}:
				cancel()
			case <-ctx.Done():
			}
		}(address)
	}

	go func() {
		wg.Wait()
		close(resCh)
	}()

	var err error
	for res := range resCh {
		if res.cert != nil {
			return res.cert, nil
		}

		if res.err != nil {
			err = res.err
		}
	}

	return nil, fmt.Errorf("unable to connect to any of the cluster members specified in join token: %w", err)
}
