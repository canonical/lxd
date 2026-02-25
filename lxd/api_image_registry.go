package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

var imageRegistriesCmd = APIEndpoint{
	Path:        "image-registries",
	MetricsType: entity.TypeImageRegistry,

	Get:  APIEndpointAction{Handler: imageRegistriesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: imageRegistriesPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanCreateImageRegistries)},
}

var imageRegistryCmd = APIEndpoint{
	Path:        "image-registries/{name}",
	MetricsType: entity.TypeImageRegistry,

	Get:    APIEndpointAction{Handler: imageRegistryGet, AccessHandler: allowPermission(entity.TypeImageRegistry, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: imageRegistryPost, AccessHandler: allowPermission(entity.TypeImageRegistry, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: imageRegistryPatch, AccessHandler: allowPermission(entity.TypeImageRegistry, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: imageRegistryPut, AccessHandler: allowPermission(entity.TypeImageRegistry, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: imageRegistryDelete, AccessHandler: allowPermission(entity.TypeImageRegistry, auth.EntitlementCanDelete, "name")},
}

var imageRegistryImagesCmd = APIEndpoint{
	Path:        "image-registries/{name}/images",
	MetricsType: entity.TypeImageRegistry,

	Get: APIEndpointAction{Handler: imageRegistryImagesGet, AccessHandler: allowPermission(entity.TypeImageRegistry, auth.EntitlementCanView, "name")},
}

// swagger:operation GET /1.0/image-registries image-registries image_registries_get
//
//   Get the image registries
//
//   Returns a list of image registries (URLs).
//
//   ---
//   produces:
//     - application/json
//   responses:
//     "200":
//       description: API endpoints
//       schema:
//         type: object
//         description: Sync response
//         properties:
//           type:
//             type: string
//             description: Response type
//             example: sync
//           status:
//             type: string
//             description: Status description
//             example: Success
//           status_code:
//             type: integer
//             description: Status code
//             example: 200
//           metadata:
//             type: array
//             description: List of endpoints
//             items:
//               type: string
//             example:
//               - "/1.0/image-registries/ubuntu"
//               - "/1.0/image-registries/lxd01"
//     "400":
//       $ref: "#/responses/BadRequest"
//     "403":
//       $ref: "#/responses/Forbidden"
//     "500":
//       $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/image-registries?recursion=1 image-registries image_registries_get_recursion1
//
//	Get the image registries
//
//	Returns a list of image registries (structs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Image registries
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
//	          description: List of image registries
//	          items:
//	            $ref: "#/definitions/ImageRegistry"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistriesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	recursion := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageRegistry, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeImageRegistry)
	if err != nil {
		return response.InternalError(err)
	}

	var apiImageRegistries []*api.ImageRegistry
	var imageRegistryURLs []string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		allImageRegistries, err := dbCluster.GetImageRegistrys(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed fetching image registries: %w", err)
		}

		imageRegistries := make([]dbCluster.ImageRegistry, 0, len(allImageRegistries))
		for _, registry := range allImageRegistries {
			if userHasPermission(entity.ImageRegistryURL(registry.Name)) {
				imageRegistries = append(imageRegistries, registry)
			}
		}

		if recursion {
			apiImageRegistries = make([]*api.ImageRegistry, 0, len(imageRegistries))
			for _, registry := range imageRegistries {
				apiImageRegistry := registry.ToAPI()
				apiImageRegistries = append(apiImageRegistries, apiImageRegistry)
			}
		} else {
			imageRegistryURLs = make([]string, 0, len(imageRegistries))
			for _, registry := range imageRegistries {
				imageRegistryURLs = append(imageRegistryURLs, entity.ImageRegistryURL(registry.Name).String())
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		return response.SyncResponse(true, imageRegistryURLs)
	}

	if len(withEntitlements) > 0 {
		urlToImageRegistry := make(map[*api.URL]auth.EntitlementReporter, len(apiImageRegistries))
		for _, registry := range apiImageRegistries {
			u := entity.ImageRegistryURL(registry.Name)
			urlToImageRegistry[u] = registry
		}

		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImageRegistry, withEntitlements, urlToImageRegistry)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiImageRegistries)
}

// swagger:operation GET /1.0/image-registries/{name} image-registries image_registry_get
//
//	Get the image registry
//
//	Returns a specific image registry.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Image registry
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
//	          $ref: "#/definitions/ImageRegistry"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistryGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageRegistry, false)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var apiImageRegistry *api.ImageRegistry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbImageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed fetching image registry %q: %w", name, err)
		}

		apiImageRegistry = dbImageRegistry.ToAPI()
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImageRegistry, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ImageRegistryURL(name): apiImageRegistry})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, apiImageRegistry, apiImageRegistry.Etag())
}

// swagger:operation GET /1.0/image-registries/{name}/images image-registries image_registry_images_get
//
//  Get the available images from image registry
//
//  Returns a list of available images (URLs) from the image registry.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb",
//                "/1.0/images/084dd79dd1360fd25a2479eb46674c2a5ef3022a40fe03c91ab3603e3402b8e1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/image-registries/{name}/images?recursion=1 image-registries image_registry_images_get
//
//	Get the available images from image registry
//
//	Returns a list of available images (structs) from the image registry.
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
//	    name: filter
//	    description: Collection filter
//	    type: string
//	    example: default
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
//	          description: List of images
//	          items:
//	            $ref: "#/definitions/Image"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistryImagesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageRegistry, false)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var apiImageRegistry *api.ImageRegistry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbImageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed fetching image registry %q: %w", name, err)
		}

		apiImageRegistry = dbImageRegistry.ToAPI()
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// getRemoteCert retrieves the remote certificate from the image server.
	getRemoteCert := func() ([]byte, error) {
		remoteCert, err := shared.GetRemoteCertificate(context.Background(), apiImageRegistry.URL, version.UserAgent)
		if err != nil {
			return nil, err
		}

		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: remoteCert.Raw}), nil
	}

	var imgServer lxd.ImageServer
	var images []api.Image
	var remoteCert []byte

	switch apiImageRegistry.Protocol {
	case api.ImageRegistryProtocolSimpleStreams:
		// Retrieve the remote certificate to connect to the image server.
		remoteCert, err = getRemoteCert()
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed getting remote certificate for image registry %q: %w", name, err))
		}

		// Connect to the SimpleStreams image server.
		imgServer, err = lxd.ConnectSimpleStreams(apiImageRegistry.URL, &lxd.ConnectionArgs{
			TLSServerCert: string(remoteCert),
			UserAgent:     version.UserAgent,
			Proxy:         s.Proxy,
			CachePath:     s.OS.CacheDir,
			CacheExpiry:   time.Hour,
		})

	case api.ImageRegistryProtocolLXD:
		if apiImageRegistry.Public {
			// Retrieve the remote certificate to connect to the image server.
			remoteCert, err = getRemoteCert()
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed getting remote certificate for image registry %q: %w", name, err))
			}

			// Connect to the public LXD image server.
			imgServer, err = lxd.ConnectPublicLXD(apiImageRegistry.URL, &lxd.ConnectionArgs{
				TLSServerCert: string(remoteCert),
				UserAgent:     version.UserAgent,
				Proxy:         s.Proxy,
				CachePath:     s.OS.CacheDir,
				CacheExpiry:   time.Hour,
			})
		} else {
			var clusterLink *api.ClusterLink

			// Get the cluster link information.
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				dbClusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), apiImageRegistry.Cluster)
				if err != nil {
					return err
				}

				clusterLink, err = dbClusterLink.ToAPI(ctx, tx.Tx())
				return err
			})
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed fetching cluster link %q for image registry %q: %w", apiImageRegistry.Cluster, name, err))
			}

			// Connect to the private LXD image server using the cluster link.
			imgServer, err = cluster.ConnectClusterLink(r.Context(), s, *clusterLink)
		}

	default:
		return response.InternalError(fmt.Errorf("Unknown image registry protocol %q for image registry %q", apiImageRegistry.Protocol, name))
	}

	// Check the error from the connection attempt.
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed connecting to image registry %q: %w", name, err))
	}

	images, err = imgServer.GetImages()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed fetching images from image registry %q: %w", name, err))
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImageRegistry, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ImageRegistryURL(name): apiImageRegistry})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, images)
}

// swagger:operation POST /1.0/image-registries image-registries image_registries_post
//
//	Add an image registry
//
//	Creates a new image registry.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: image_registry
//	    description: Image registry
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageRegistriesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistriesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ImageRegistriesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validateImageRegistryName(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = validateImageRegistryPut(req.ImageRegistryPut)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the associated cluster link exists for a private LXD image registry.
		if req.Protocol == api.ImageRegistryProtocolLXD && !req.Public {
			exists, err := dbCluster.ClusterLinkExists(ctx, tx.Tx(), req.Cluster)
			if err != nil {
				return fmt.Errorf("Failed fetching cluster link %q for image registry: %w", req.Cluster, err)
			} else if !exists {
				return fmt.Errorf("Cluster link %q does not exist", req.Cluster)
			}
		}

		_, err := dbCluster.CreateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistry{
			Name:          req.Name,
			Cluster:       req.Cluster,
			URL:           req.URL,
			SourceProject: req.SourceProject,
			Public:        req.Public,
			Protocol:      dbCluster.ImageRegistryProtocol(req.Protocol),
		})
		if err != nil {
			return fmt.Errorf("Failed adding image registry database record: %w", err)
		}

		return nil
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			return response.Conflict(fmt.Errorf("Image registry %q already exists", req.Name))
		}

		return response.SmartError(fmt.Errorf("Failed creating image registry %q: %w", req.Name, err))
	}

	// Send image registry lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ImageRegistryCreated.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation POST /1.0/image-registries/{name} image-registries image_registry_post
//
//	Rename the image registry
//
//	Renames an existing image registry.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: image_registry
//	    description: Image registry rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageRegistryPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistryPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ImageRegistryPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validateImageRegistryName(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	oldName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.RenameImageRegistry(ctx, tx.Tx(), oldName, req.Name)
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			return response.Conflict(fmt.Errorf("Image registry %q already exists", req.Name))
		}

		return response.SmartError(fmt.Errorf("Failed renaming image registry %q: %w", oldName, err))
	}

	// Send image registry lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": oldName}))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/image-registries/{name} image-registries image_registry_patch
//
//	Update the image registry
//
//	Updates a subset of the image registry configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: image_registry
//	    description: Update image registry request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageRegistryPut"
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
func imageRegistryPatch(d *Daemon, r *http.Request) response.Response {
	return updateImageRegistry(d.State(), r, http.MethodPatch)
}

// swagger:operation PUT /1.0/image-registries/{name} image-registries image_registry_put
//
//	Update the image registry
//
//	Updates the image registry configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: image_registry
//	    description: Update image registry request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageRegistryPut"
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
func imageRegistryPut(d *Daemon, r *http.Request) response.Response {
	return updateImageRegistry(d.State(), r, http.MethodPut)
}

// updateImageRegistry is shared between [imageRegistryPatch] and [imageRegistryPut].
func updateImageRegistry(s *state.State, r *http.Request, httpMethod string) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbImageRegistry *dbCluster.ImageRegistry
	var apiImageRegistry *api.ImageRegistry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the image registry by name.
		dbImageRegistry, err = dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed fetching image registry %q: %w", name, err))
	}

	apiImageRegistry = dbImageRegistry.ToAPI()

	// Validate ETag.
	err = util.EtagCheck(r, apiImageRegistry.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ImageRegistryPut{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Override the fields according to the http method.
	switch httpMethod {
	case http.MethodPatch:
		if dbImageRegistry.Cluster != req.Cluster {
			dbImageRegistry.Cluster = req.Cluster
		}

		if dbImageRegistry.URL != req.URL {
			dbImageRegistry.URL = req.URL
		}

		if dbImageRegistry.Public != req.Public {
			dbImageRegistry.Public = req.Public
		}

		if dbImageRegistry.Protocol != dbCluster.ImageRegistryProtocol(req.Protocol) {
			dbImageRegistry.Protocol = dbCluster.ImageRegistryProtocol(req.Protocol)
		}

		if dbImageRegistry.SourceProject != req.SourceProject {
			dbImageRegistry.SourceProject = req.SourceProject
		}

	case http.MethodPut:
		dbImageRegistry.Cluster = req.Cluster
		dbImageRegistry.URL = req.URL
		dbImageRegistry.Public = req.Public
		dbImageRegistry.Protocol = dbCluster.ImageRegistryProtocol(req.Protocol)
		dbImageRegistry.SourceProject = req.SourceProject

	default:
		return response.BadRequest(fmt.Errorf("Unsupported HTTP Method %q", httpMethod))
	}

	// Check that the updated fields constitute a valid image registry.
	err = validateImageRegistryPut(dbImageRegistry.ToAPI().Writable())
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the associated cluster link exists for a private LXD image registry.
		if dbImageRegistry.Protocol == api.ImageRegistryProtocolLXD && !dbImageRegistry.Public {
			exists, err := dbCluster.ClusterLinkExists(ctx, tx.Tx(), dbImageRegistry.Cluster)
			if err != nil {
				return fmt.Errorf("Failed fetching cluster link %q for image registry %q: %w", dbImageRegistry.Cluster, name, err)
			} else if !exists {
				return fmt.Errorf("Cluster link %q does not exist", dbImageRegistry.Cluster)
			}
		}

		return dbCluster.UpdateImageRegistry(ctx, tx.Tx(), apiImageRegistry.Name, *dbImageRegistry)
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating image registry %q: %w", name, err))
	}

	// Send image registry lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryUpdated.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/image-registries/{name} image-registries image_registry_delete
//
//	Delete the image registry
//
//	Deletes the image registry.
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
func imageRegistryDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteImageRegistry(ctx, tx.Tx(), name)
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error deleting image registry %q from database: %w", name, err))
	}

	// Send image registry lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// validateImageRegistryName checks that the image registry name contains only allowed characters.
func validateImageRegistryName(name string) error {
	if name == "" {
		return api.StatusErrorf(http.StatusBadRequest, "Image registry name cannot be empty")
	}

	if strings.Contains(name, "/") {
		return api.StatusErrorf(http.StatusBadRequest, "Image registry name cannot contain a forward slash")
	}

	if strings.Contains(name, ":") {
		return api.StatusErrorf(http.StatusBadRequest, "Image registry name cannot contain a colon")
	}

	// Validate ASCII-only.
	err := validate.IsEntityName(name)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Image registry name cannot contain non-ASCII characters")
	}

	return nil
}

// validateImageRegistryPut checks that the image registry writable fields are valid.
func validateImageRegistryPut(registry api.ImageRegistryPut) error {
	if registry.Protocol == "" {
		return api.StatusErrorf(http.StatusBadRequest, "No image registry protocol provided")
	}

	if registry.URL == "" {
		return api.StatusErrorf(http.StatusBadRequest, "No image registry source URL provided")
	}

	parsedURL, err := url.ParseRequestURI(registry.URL)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Invalid image registry source URL: %w", err)
	}

	// Check that the URL does not contain Basic authentication credentials.
	if parsedURL.User != nil {
		return api.StatusErrorf(http.StatusBadRequest, "URL containing Basic authentication credentials not allowed")
	}

	// Validate the image registry based on its protocol.
	switch registry.Protocol {
	case api.ImageRegistryProtocolSimpleStreams:
		if !registry.Public {
			return api.StatusErrorf(http.StatusBadRequest, "SimpleStreams image registry cannot be private")
		}

		if registry.Cluster != "" {
			return api.StatusErrorf(http.StatusBadRequest, "SimpleStreams image registry cannot have a cluster link")
		}

		if registry.SourceProject != "" {
			return api.StatusErrorf(http.StatusBadRequest, "SimpleStreams image registry cannot have a source project")
		}

	case api.ImageRegistryProtocolLXD:
		if registry.SourceProject == "" {
			return api.StatusErrorf(http.StatusBadRequest, "No source project provided for a LXD image registry")
		}

		if !registry.Public && registry.Cluster == "" {
			return api.StatusErrorf(http.StatusBadRequest, "No cluster link provided for a private LXD image registry")
		}

		if registry.Public && registry.Cluster != "" {
			return api.StatusErrorf(http.StatusBadRequest, "Public LXD image registry cannot have a cluster link")
		}

	default:
		return api.StatusErrorf(http.StatusBadRequest, "Unknown image registry protocol %q", registry.Protocol)
	}

	return nil
}
