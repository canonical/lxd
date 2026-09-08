package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
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

	var imageRegistries []dbCluster.ImageRegistriesRow
	var imageRegistryURLs []string
	var allConfigs map[int64]map[string]string
	var clusterLinkTypes map[string]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		imageRegistries, imageRegistryURLs, err = dbCluster.GetImageRegistriesAndURLs(ctx, tx.Tx(), func(registry dbCluster.ImageRegistriesRow) bool {
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

			// Load all cluster link types once so the public flag can be derived without a query per registry.
			clusterLinks, err := dbCluster.GetClusterLinks(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading cluster links: %w", err)
			}

			clusterLinkTypes = make(map[string]string, len(clusterLinks))
			for _, clusterLink := range clusterLinks {
				clusterLinkTypes[clusterLink.Name] = string(clusterLink.Type)
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
		apiImageRegistry := registry.ToAPI(allConfigs)

		// Derive the public flag. SimpleStreams registries are always public; LXD registries are
		// public when their associated cluster link is a public link.
		if apiImageRegistry.Protocol == api.ImageRegistryProtocolLXD {
			apiImageRegistry.Public = clusterLinkTypes[apiImageRegistry.Config["cluster"]] == api.ClusterLinkTypePublic
		} else {
			apiImageRegistry.Public = true
		}

		apiImageRegistries = append(apiImageRegistries, apiImageRegistry)
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
	name := r.PathValue("name")

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageRegistry, false)
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

		// Derive the public flag from the registry protocol and its associated cluster link.
		apiImageRegistry.Public, err = imageRegistryIsPublic(ctx, tx, apiImageRegistry)
		if err != nil {
			return err
		}

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
//	    description: Images
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
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistryImagesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	name := r.PathValue("name")

	// Determine if the response compression is requested.
	compress := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageRegistry, false)
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
//	  "202":
//	    $ref: "#/responses/Operation"
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

	// Infer the registry protocol from its configuration. The "url" and "cluster" keys are
	// mutually exclusive and select the "simplestreams" and "lxd" protocols respectively.
	protocol, err := imageRegistryInferProtocol(req.Config)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the request fields constitute a valid image registry.
	err = imageRegistryValidate(api.ImageRegistry{
		Name:        req.Name,
		Description: req.Description,
		Protocol:    protocol,
		Config:      req.Config,
	})
	if err != nil {
		return response.SmartError(err)
	}

	registryCluster := req.Config["cluster"]

	requestor := request.CreateRequestor(r.Context())

	run := func(ctx context.Context, op *operations.Operation) error {
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Check that the associated cluster link exists for a LXD image registry.
			if protocol == api.ImageRegistryProtocolLXD {
				_, err := dbCluster.GetClusterLink(ctx, tx.Tx(), registryCluster)
				if err != nil {
					if response.IsNotFoundError(err) {
						return api.StatusErrorf(http.StatusBadRequest, "Cluster link %q does not exist", registryCluster)
					}

					return fmt.Errorf("Failed loading cluster link %q for image registry: %w", registryCluster, err)
				}
			}

			registryID, err := dbCluster.CreateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistriesRow{
				Name:        req.Name,
				Description: req.Description,
				Protocol:    dbCluster.ImageRegistryProtocol(protocol),
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
				return api.StatusErrorf(http.StatusConflict, "Image registry %q already exists", req.Name)
			}

			return fmt.Errorf("Failed creating image registry %q: %w", req.Name, err)
		}

		// Send image registry lifecycle event.
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryCreated.Event(req.Name, requestor, nil))

		return nil
	}

	args := operations.OperationArgs{
		Type:    operationtype.ImageRegistryCreate,
		Class:   operationtype.OperationClassTask,
		RunHook: run,
		Metadata: map[string]any{
			api.MetadataEntityURL: entity.ImageRegistryURL(req.Name).String(),
		},
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
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
	oldName := r.PathValue("name")

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

	// Ensure that the image registry exists and that built-in image registries cannot be renamed.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		imageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), oldName)
		if err != nil {
			return fmt.Errorf("Failed loading image registry %q: %w", oldName, err)
		}

		if imageRegistry.Builtin {
			return api.NewStatusError(http.StatusBadRequest, "Built-in image registry cannot be renamed")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())

	run := func(ctx context.Context, op *operations.Operation) error {
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.RenameImageRegistry(ctx, tx.Tx(), oldName, req.Name)
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return api.StatusErrorf(http.StatusConflict, "Image registry %q already exists", req.Name)
			}

			return fmt.Errorf("Failed renaming image registry %q: %w", oldName, err)
		}

		// Send image registry lifecycle event.
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": oldName}))

		return nil
	}

	args := operations.OperationArgs{
		Type:      operationtype.ImageRegistryRename,
		Class:     operationtype.OperationClassTask,
		RunHook:   run,
		EntityURL: entity.ImageRegistryURL(oldName),
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
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
//	  "202":
//	    $ref: "#/responses/Operation"
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
//	  "202":
//	    $ref: "#/responses/Operation"
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
	name := r.PathValue("name")

	var existingRegistry *api.ImageRegistry
	var registryID int64
	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	registryCluster := existingRegistry.Config["cluster"]

	requestor := request.CreateRequestor(r.Context())

	run := func(ctx context.Context, op *operations.Operation) error {
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Check that the associated cluster link exists for a LXD image registry.
			if existingRegistry.Protocol == api.ImageRegistryProtocolLXD {
				_, err := dbCluster.GetClusterLink(ctx, tx.Tx(), registryCluster)
				if err != nil {
					if response.IsNotFoundError(err) {
						return api.StatusErrorf(http.StatusBadRequest, "Cluster link %q does not exist", registryCluster)
					}

					return fmt.Errorf("Failed loading cluster link %q for image registry: %w", registryCluster, err)
				}
			}

			// Update the image registry record.
			err = dbCluster.UpdateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistriesRow{
				ID:          registryID,
				Name:        existingRegistry.Name,
				Description: existingRegistry.Description,
				Protocol:    dbCluster.ImageRegistryProtocol(existingRegistry.Protocol),
				Builtin:     false,
			})
			if err != nil {
				return fmt.Errorf("Failed updating image registry %q: %w", name, err)
			}

			// Update the configuration.
			err = dbCluster.CreateImageRegistryConfig(ctx, tx.Tx(), registryID, existingRegistry.Config)
			if err != nil {
				return fmt.Errorf("Failed updating image registry config %q: %w", name, err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Send image registry lifecycle event.
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryUpdated.Event(name, requestor, nil))

		return nil
	}

	args := operations.OperationArgs{
		Type:      operationtype.ImageRegistryUpdate,
		Class:     operationtype.OperationClassTask,
		RunHook:   run,
		EntityURL: entity.ImageRegistryURL(name),
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
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
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRegistryDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	name := r.PathValue("name")

	// Ensure that the image registry exists and that built-in image registries cannot be deleted.
	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		imageRegistry, err := dbCluster.GetImageRegistry(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Failed loading image registry %q: %w", name, err)
		}

		if imageRegistry.Builtin {
			return api.NewStatusError(http.StatusBadRequest, "Built-in image registry cannot be deleted")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())

	run := func(ctx context.Context, op *operations.Operation) error {
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.DeleteImageRegistry(ctx, tx.Tx(), name)
		})
		if err != nil {
			return fmt.Errorf("Failed deleting image registry %q from database: %w", name, err)
		}

		// Send image registry lifecycle event.
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ImageRegistryDeleted.Event(name, requestor, nil))

		return nil
	}

	args := operations.OperationArgs{
		Type:      operationtype.ImageRegistryDelete,
		Class:     operationtype.OperationClassTask,
		RunHook:   run,
		EntityURL: entity.ImageRegistryURL(name),
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}

// imageRegistryIsPublic reports whether an image registry is publicly accessible.
// SimpleStreams registries are always public. LXD registries are public when their
// associated cluster link is a public cluster link.
func imageRegistryIsPublic(ctx context.Context, tx *db.ClusterTx, registry *api.ImageRegistry) (bool, error) {
	if registry.Protocol != api.ImageRegistryProtocolLXD {
		return true, nil
	}

	registryCluster := registry.Config["cluster"]
	clusterLink, err := dbCluster.GetClusterLink(ctx, tx.Tx(), registryCluster)
	if err != nil {
		return false, fmt.Errorf("Failed loading cluster link %q for image registry %q: %w", registryCluster, registry.Name, err)
	}

	return clusterLink.Type == api.ClusterLinkTypePublic, nil
}

// imageRegistryInferProtocol determines the image registry protocol from its configuration.
// The "url" and "cluster" configuration keys are mutually exclusive: "url" selects the
// SimpleStreams protocol and "cluster" selects the LXD protocol.
func imageRegistryInferProtocol(config map[string]string) (string, error) {
	registryURL := config["url"]
	registryCluster := config["cluster"]

	switch {
	case registryURL != "" && registryCluster != "":
		return "", api.NewStatusError(http.StatusBadRequest, "Image registry cannot have both a source URL and a cluster link")
	case registryURL != "":
		return api.ImageRegistryProtocolSimpleStreams, nil
	case registryCluster != "":
		return api.ImageRegistryProtocolLXD, nil
	default:
		return "", api.NewStatusError(http.StatusBadRequest, "Image registry requires either a source URL (for the SimpleStreams protocol) or a cluster link (for the LXD protocol)")
	}
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

		if registryCluster == "" {
			return api.NewStatusError(http.StatusBadRequest, "No cluster link provided for a LXD image registry")
		}

		if registryURL != "" {
			return api.NewStatusError(http.StatusBadRequest, "LXD image registry cannot have a source URL")
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

	err := validate.IsEntityName(name)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Invalid image registry name: %w", err)
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
		//  shortdesc: User-provided free-form key/value pairs
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
