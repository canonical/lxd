package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/registry"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
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

	recursion, _ := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageRegistry, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeImageRegistry)
	if err != nil {
		return response.InternalError(err)
	}

	var imageRegistries []dbCluster.ImageRegistryRow
	var imageRegistryURLs []string
	var allConfigs map[int64]map[string]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		imageRegistries, imageRegistryURLs, err = dbCluster.GetImageRegistriesAndURLs(ctx, tx.Tx(), func(registry dbCluster.ImageRegistryRow) bool {
			return userHasPermission(entity.ImageRegistryURL(registry.Name))
		})
		if err != nil {
			return err
		}

		if recursion != 0 && len(imageRegistries) > 0 {
			allConfigs, err = dbCluster.GetImageRegistryConfig(ctx, tx.Tx(), nil)
			if err != nil {
				return fmt.Errorf("Failed loading image registry configs: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, imageRegistryURLs)
	}

	apiImageRegistries := make([]*api.ImageRegistry, 0, len(imageRegistries))
	for _, registry := range imageRegistries {
		apiImageRegistries = append(apiImageRegistries, registry.ToAPI(allConfigs))
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
			return fmt.Errorf("Failed loading image registry %q: %w", name, err)
		}

		config, err := dbCluster.GetImageRegistryConfig(ctx, tx.Tx(), &dbImageRegistry.ID)
		if err != nil {
			return fmt.Errorf("Failed loading image registry config: %w", err)
		}

		apiImageRegistry = dbImageRegistry.ToAPI(config)
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
//	Get the available images from image registry
//
//	Returns a list of available images (structs) from the image registry.
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
//	          description: List of images
//	          items:
//	            $ref: "#/definitions/Image"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistryImagesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Determine if the response compression is requested.
	compress := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")

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
			return fmt.Errorf("Failed loading image registry %q: %w", name, err)
		}

		config, err := dbCluster.GetImageRegistryConfig(ctx, tx.Tx(), &dbImageRegistry.ID)
		if err != nil {
			return fmt.Errorf("Failed loading image registry config: %w", err)
		}

		apiImageRegistry = dbImageRegistry.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Connect to the image registry.
	imageServer, err := registry.ConnectImageRegistry(r.Context(), s, *apiImageRegistry)
	if err != nil {
		return response.SmartError(err)
	}

	// Fetch the available images from the image registry.
	images, err := imageServer.GetImages()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading images from image registry %q: %w", name, err))
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImageRegistry, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ImageRegistryURL(name): apiImageRegistry})
		if err != nil {
			return response.SmartError(err)
		}
	}

	if compress {
		return response.SyncResponseCompressed(true, images)
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

	// Check that the request fields constitute a valid image registry.
	err = imageRegistryValidate(api.ImageRegistry{
		Name:        req.Name,
		Description: req.Description,
		Protocol:    req.Protocol,
		Config:      req.Config,
	})
	if err != nil {
		return response.SmartError(err)
	}

	registryPublic := req.Config["url"] != ""
	registryCluster := req.Config["cluster"]

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the associated cluster link exists for a private LXD image registry.
		if req.Protocol == api.ImageRegistryProtocolLXD && !registryPublic {
			_, err := dbCluster.GetClusterLink(ctx, tx.Tx(), registryCluster)
			if err != nil {
				if response.IsNotFoundError(err) {
					return fmt.Errorf("Cluster link %q does not exist", registryCluster)
				}

				return fmt.Errorf("Failed loading cluster link %q for image registry: %w", registryCluster, err)
			}
		}

		registryID, err := dbCluster.CreateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistryRow{
			Name:        req.Name,
			Description: req.Description,
			Protocol:    dbCluster.ImageRegistryProtocol(req.Protocol),
			Public:      registryPublic,
			Builtin:     false,
		})
		if err != nil {
			return fmt.Errorf("Failed adding image registry database record: %w", err)
		}

		err = dbCluster.CreateImageRegistryConfig(ctx, tx.Tx(), registryID, req.Config)
		if err != nil {
			return fmt.Errorf("Failed adding image registry config database record: %w", err)
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
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
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

	err = imageRegistryValidateName(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	oldName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the image registry by old name to check if it is built-in.
		imageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), oldName)
		if err != nil {
			return fmt.Errorf("Failed loading image registry %q: %w", oldName, err)
		}

		// Ensure that built-in image registries cannot be renamed.
		if imageRegistry.Builtin {
			return api.NewStatusError(http.StatusBadRequest, "Built-in image registry cannot be renamed")
		}

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
	return updateImageRegistry(d.State(), r)
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
	return updateImageRegistry(d.State(), r)
}

// updateImageRegistry is shared between [imageRegistryPatch] and [imageRegistryPut].
func updateImageRegistry(s *state.State, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var existingRegistry *api.ImageRegistry
	var registryID int64
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the image registry by name.
		dbImageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading image registry %q: %w", name, err)
		}

		// Save the ID, this is needed to update the config.
		registryID = dbImageRegistry.ID

		// Fetch the config.
		config, err := dbCluster.GetImageRegistryConfig(ctx, tx.Tx(), &registryID)
		if err != nil {
			return fmt.Errorf("Failed loading image registry config: %w", err)
		}

		existingRegistry = dbImageRegistry.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Ensure that built-in image registries cannot be modified.
	if existingRegistry.Builtin {
		return response.BadRequest(errors.New("Built-in image registry cannot be updated"))
	}

	// Validate ETag.
	err = util.EtagCheck(r, existingRegistry.Etag())
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
	switch r.Method {
	case http.MethodPatch:
		if req.Description != "" {
			existingRegistry.Description = req.Description
		}

		// Merge config.
		if req.Config == nil {
			req.Config = existingRegistry.Config
		} else {
			for k, v := range existingRegistry.Config {
				_, ok := req.Config[k]
				if !ok {
					req.Config[k] = v
				}
			}
		}

		existingRegistry.Config = req.Config

	case http.MethodPut:
		existingRegistry.Description = req.Description
		existingRegistry.Config = req.Config

	default:
		return response.BadRequest(fmt.Errorf("Unsupported HTTP Method %q", r.Method))
	}

	// Check that the updated fields constitute a valid image registry.
	err = imageRegistryValidate(*existingRegistry)
	if err != nil {
		return response.SmartError(err)
	}

	registryPublic := existingRegistry.Config["url"] != ""
	registryCluster := existingRegistry.Config["cluster"]

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the associated cluster link exists for a private LXD image registry.
		if existingRegistry.Protocol == api.ImageRegistryProtocolLXD && !registryPublic {
			_, err := dbCluster.GetClusterLink(ctx, tx.Tx(), registryCluster)
			if err != nil {
				if response.IsNotFoundError(err) {
					return fmt.Errorf("Cluster link %q does not exist", registryCluster)
				}

				return fmt.Errorf("Failed loading cluster link %q for image registry: %w", registryCluster, err)
			}
		}

		// Update the image registry record.
		err = dbCluster.UpdateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistryRow{
			ID:          registryID,
			Name:        existingRegistry.Name,
			Description: existingRegistry.Description,
			Protocol:    dbCluster.ImageRegistryProtocol(existingRegistry.Protocol),
			Public:      registryPublic,
			Builtin:     false,
		})
		if err != nil {
			return fmt.Errorf("Failed updating image registry %q: %w", name, err)
		}

		// Update the configuration.
		err = dbCluster.UpdateImageRegistryConfig(ctx, tx.Tx(), registryID, existingRegistry.Config)
		if err != nil {
			return fmt.Errorf("Failed updating image registry config %q: %w", name, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
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
		// Get the image registry by name to check if it is built-in.
		imageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading image registry %q: %w", name, err)
		}

		// Ensure that built-in image registries cannot be deleted.
		if imageRegistry.Builtin {
			return api.NewStatusError(http.StatusBadRequest, "Built-in image registry cannot be deleted")
		}

		return dbCluster.DeleteImageRegistry(ctx, tx.Tx(), name)
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting image registry %q from database: %w", name, err))
	}

	// Send image registry lifecycle event.
	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// imageRegistryValidate checks that the image registry configuration is valid as a whole.
func imageRegistryValidate(registry api.ImageRegistry) error {
	// Validate image registry name.
	err := imageRegistryValidateName(registry.Name)
	if err != nil {
		return err
	}

	// Validate image registry config key/value pairs.
	err = imageRegistryValidateConfig(registry.Config)
	if err != nil {
		return err
	}

	// Validate image registry protocol.
	if registry.Protocol == "" {
		return api.NewStatusError(http.StatusBadRequest, "No image registry protocol provided")
	}

	registryURL := registry.Config["url"]
	registryCluster := registry.Config["cluster"]
	registrySourceProject := registry.Config["source_project"]

	if registryURL != "" {
		parsedURL, err := url.ParseRequestURI(registryURL)
		if err != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Invalid image registry source URL: %w", err)
		}

		// Check that the URL does not contain Basic authentication credentials.
		if parsedURL.User != nil {
			return api.NewStatusError(http.StatusBadRequest, "URL containing Basic authentication credentials not allowed")
		}
	}

	// Validate the image registry based on its protocol.
	switch registry.Protocol {
	case api.ImageRegistryProtocolSimpleStreams:
		if registryURL == "" {
			return api.NewStatusError(http.StatusBadRequest, "No source URL provided for a SimpleStreams image registry")
		}

		if registryCluster != "" {
			return api.NewStatusError(http.StatusBadRequest, "SimpleStreams image registry cannot have a cluster link")
		}

		if registrySourceProject != "" {
			return api.NewStatusError(http.StatusBadRequest, "SimpleStreams image registry cannot have a source project")
		}

	case api.ImageRegistryProtocolLXD:
		if registrySourceProject == "" {
			return api.NewStatusError(http.StatusBadRequest, "No source project provided for a LXD image registry")
		}

		if registryCluster == "" && registryURL == "" {
			return api.NewStatusError(http.StatusBadRequest, "No source URL or cluster link provided for a LXD image registry")
		}

		if registryCluster != "" && registryURL != "" {
			return api.NewStatusError(http.StatusBadRequest, "LXD image registry cannot have both URL and cluster link set")
		}

	default:
		return api.StatusErrorf(http.StatusBadRequest, "Unknown image registry protocol %q", registry.Protocol)
	}

	return nil
}

// imageRegistryValidateName checks that the image registry name contains only allowed characters.
func imageRegistryValidateName(name string) error {
	if name == "" {
		return api.NewStatusError(http.StatusBadRequest, "Image registry name cannot be empty")
	}

	if strings.Contains(name, "/") {
		return api.NewStatusError(http.StatusBadRequest, "Image registry name cannot contain a forward slash")
	}

	if strings.Contains(name, ":") {
		return api.NewStatusError(http.StatusBadRequest, "Image registry name cannot contain a colon")
	}

	// Validate ASCII-only.
	err := validate.IsEntityName(name)
	if err != nil {
		return api.NewStatusError(http.StatusBadRequest, "Image registry name cannot contain non-ASCII characters")
	}

	return nil
}

// imageRegistryValidateConfig validates the configuration key-value pairs for image registries.
func imageRegistryValidateConfig(config map[string]string) error {
	imageRegistryConfigKeys := map[string]func(value string) error{
		// lxdmeta:generate(entities=image-registry; group=image-registry-conf; key=url)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: Source URL for image registry using "SimpleStreams" protocol
		"url": validate.Optional(validate.IsHTTPSURL),
		// lxdmeta:generate(entities=image-registry; group=image-registry-conf; key=cluster)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: Cluster link name for image registry using "LXD" protocol
		"cluster": validate.Optional(validate.IsEntityName),
		// lxdmeta:generate(entities=image-registry; group=image-registry-conf; key=source_project)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: Source project for image registry using "LXD" protocol
		"source_project": validate.Optional(validate.IsEntityName),
	}

	for k, v := range config {
		// User keys are free for all.

		// lxdmeta:generate(entities=image-registry; group=image-registry-conf; key=user.*)
		//
		// ---
		//  type: string
		//  shortdesc: User-provided free-form user key/value pairs
		if strings.HasPrefix(k, "user.") {
			continue
		}

		// Validate all other keys.
		validator, ok := imageRegistryConfigKeys[k]
		if !ok {
			return api.StatusErrorf(http.StatusBadRequest, "Invalid image registry configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Invalid image registry configuration key %q value: %w", k, err)
		}
	}

	return nil
}
