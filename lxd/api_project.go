package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/operations"
	projecthelpers "github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
)

var projectsCmd = APIEndpoint{
	Path:        "projects",
	MetricsType: entity.TypeProject,

	Get:  APIEndpointAction{Handler: projectsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: projectsPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanCreateProjects)},
}

var projectCmd = APIEndpoint{
	Path:        "projects/{name}",
	MetricsType: entity.TypeProject,

	Delete: APIEndpointAction{Handler: projectDelete, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanDelete, "name")},
	Get:    APIEndpointAction{Handler: projectGet, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanView, "name")},
	Patch:  APIEndpointAction{Handler: projectPatch, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: projectPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: projectPut, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanEdit, "name")},
}

var projectStateCmd = APIEndpoint{
	Path:        "projects/{name}/state",
	MetricsType: entity.TypeProject,

	Get: APIEndpointAction{Handler: projectStateGet, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanView, "name")},
}

var internalProjectVolumeChange = APIEndpoint{
	Path:        "project/volume-change",
	MetricsType: entity.TypeProject,

	Post: APIEndpointAction{Handler: internalProjectPostVolumeChange, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation GET /1.0/projects projects projects_get
//
//  Get the projects
//
//  Returns a list of projects (URLs).
//
//  ---
//  produces:
//    - application/json
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
//                "/1.0/projects/default",
//                "/1.0/projects/foo"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/projects?recursion=1 projects projects_get_recursion1
//
//	Get the projects
//
//	Returns a list of projects (structs).
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
//	          description: List of projects
//	          items:
//	            $ref: "#/definitions/Project"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	recursion := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeProject, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeProject)
	if err != nil {
		return response.InternalError(err)
	}

	var apiProjects []*api.Project
	var projectURLs []string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		allProjects, err := dbCluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		projects := make([]dbCluster.Project, 0, len(allProjects))
		for _, project := range allProjects {
			if userHasPermission(entity.ProjectURL(project.Name)) {
				projects = append(projects, project)
			}
		}

		if recursion {
			apiProjects = make([]*api.Project, 0, len(projects))
			for _, project := range projects {
				apiProject, err := project.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				apiProject.UsedBy, err = projectUsedBy(ctx, tx, &project)
				if err != nil {
					return err
				}

				apiProjects = append(apiProjects, apiProject)
			}
		} else {
			projectURLs = make([]string, 0, len(projects))
			for _, project := range projects {
				projectURLs = append(projectURLs, entity.ProjectURL(project.Name).String())
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		return response.SyncResponse(true, projectURLs)
	}

	for _, apiProject := range apiProjects {
		apiProject.UsedBy = projecthelpers.FilterUsedBy(r.Context(), s.Authorizer, apiProject.UsedBy)
	}

	if len(withEntitlements) > 0 {
		urlToProject := make(map[*api.URL]auth.EntitlementReporter, len(apiProjects))
		for _, p := range apiProjects {
			u := entity.ProjectURL(p.Name)
			urlToProject[u] = p
		}

		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypeProject, withEntitlements, urlToProject)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiProjects)
}

// projectUsedBy returns a list of URLs for all instances, images, profiles,
// storage volumes, networks, and acls that use this project.
func projectUsedBy(ctx context.Context, tx *db.ClusterTx, project *dbCluster.Project) ([]string, error) {
	reportedEntityTypes := []entity.Type{
		entity.TypeInstance,
		entity.TypeProfile,
		entity.TypeImage,
		entity.TypeStorageVolume,
		entity.TypeNetwork,
		entity.TypeNetworkACL,
	}

	entityURLs, err := dbCluster.GetEntityURLs(ctx, tx.Tx(), project.Name, reportedEntityTypes...)
	if err != nil {
		return nil, fmt.Errorf("Failed to get project used-by URLs: %w", err)
	}

	var usedBy []string
	for _, entityIDToURL := range entityURLs {
		for _, u := range entityIDToURL {
			// Omit the project query parameter if it is the default project.
			if u.Query().Get("project") == api.ProjectDefaultName {
				q := u.Query()
				q.Del("project")
				u.RawQuery = q.Encode()
			}

			usedBy = append(usedBy, u.String())
		}
	}

	return usedBy, nil
}

// Mount the volume specified by `daemonStorageVolume` (in the form of pool/volume)
// and create the necessary directory structure to use it as a `storageType` (either `images` or `backups`) storage.
// Returns a Reverter which can be later used to revert what has been done, or must be called as Success().
func projectStorageSetup(s *state.State, daemonStorageVolume string, storageType string, revert *revert.Reverter) error {
	err := mountDaemonStorageVolume(s, daemonStorageVolume)
	if err != nil {
		return fmt.Errorf("Failed to setup project %s storage: %w", storageType, err)
	}

	revert.Add(func() { _ = unmountDaemonStorageVolume(s, daemonStorageVolume) })

	// Ensure the destination directory structure exists within the target volume.
	path := daemonStoragePath(daemonStorageVolume, storageType)
	fileinfo, err := os.Stat(path)
	if err == nil {
		if !fileinfo.IsDir() {
			return fmt.Errorf("Cannot create directory, file already exists: %q", path)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = os.MkdirAll(path, 0700)
		if err != nil {
			return fmt.Errorf("Failed to create directory %q: %w", path, err)
		}

		revert.Add(func() { os.Remove(path) })
	} else {
		return fmt.Errorf("Failed to stat() file %q: %w", path, err)
	}

	// Set ownership & mode.
	err = os.Chmod(path, 0700)
	if err != nil {
		return fmt.Errorf("Failed to set permissions on %q: %w", path, err)
	}

	err = os.Chown(path, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to set ownership on %q: %w", path, err)
	}

	return nil
}

// swagger:operation POST /1.0/projects projects projects_post
//
//	Add a project
//
//	Creates a new project.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	project := api.ProjectsPost{}

	// Set default features.
	if project.Config == nil {
		project.Config = map[string]string{}
	}

	for featureName, featureInfo := range dbCluster.ProjectFeatures {
		_, ok := project.Config[featureName]
		if !ok && featureInfo.DefaultEnabled {
			project.Config[featureName] = "true"
		}
	}

	err := json.NewDecoder(r.Body).Decode(&project)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = projecthelpers.ValidName(project.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the configuration.
	err = projectValidateConfig(s, project.Config, project.Network)
	if err != nil {
		return response.BadRequest(err)
	}

	revert := revert.New()
	defer revert.Fail()
	if project.Config["storage.images_volume"] != "" {
		err = projectStorageSetup(s, project.Config["storage.images_volume"], "images", revert)
		if err != nil {
			return response.SmartError(err)
		}
	}

	if project.Config["storage.backups_volume"] != "" {
		err = projectStorageSetup(s, project.Config["storage.backups_volume"], "backups", revert)
		if err != nil {
			return response.SmartError(err)
		}
	}

	var id int64
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err = dbCluster.CreateProject(ctx, tx.Tx(), dbCluster.Project{Description: project.Description, Name: project.Name})
		if err != nil {
			return fmt.Errorf("Failed adding database record: %w", err)
		}

		err = dbCluster.CreateProjectConfig(ctx, tx.Tx(), id, project.Config)
		if err != nil {
			return fmt.Errorf("Unable to create project config for project %q: %w", project.Name, err)
		}

		if shared.IsTrue(project.Config["features.profiles"]) {
			err = projectCreateDefaultProfile(ctx, tx, project.Name, project.StoragePool, project.Network)
			if err != nil {
				return err
			}

			if project.Config["features.images"] == "false" {
				err = dbCluster.InitProjectWithoutImages(ctx, tx.Tx(), project.Name)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating project %q: %w", project.Name, err))
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ProjectCreated.Event(project.Name, requestor, nil)
	s.Events.SendLifecycle(project.Name, lc)

	revert.Success()
	return response.SyncResponseLocation(true, nil, lc.Source)
}

// Create the default profile of a project.
func projectCreateDefaultProfile(ctx context.Context, tx *db.ClusterTx, project string, storagePool string, network string) error {
	// Create a default profile
	profile := dbCluster.Profile{}
	profile.Project = project
	profile.Name = api.ProjectDefaultName
	profile.Description = "Default LXD profile for project " + project

	profileID, err := dbCluster.CreateProfile(ctx, tx.Tx(), profile)
	if err != nil {
		return fmt.Errorf("Add default profile to database: %w", err)
	}

	devices := map[string]dbCluster.Device{}
	if storagePool != "" {
		rootDev := map[string]string{}
		rootDev["path"] = "/"
		rootDev["pool"] = storagePool
		device := dbCluster.Device{
			Name:   "root",
			Type:   dbCluster.TypeDisk,
			Config: rootDev,
		}

		devices["root"] = device
	}

	if network != "" {
		networkDev := map[string]string{}
		networkDev["network"] = network
		device := dbCluster.Device{
			Name:   "eth0",
			Type:   dbCluster.TypeNIC,
			Config: networkDev,
		}

		devices["eth0"] = device
	}

	if len(devices) > 0 {
		err = dbCluster.CreateProfileDevices(context.TODO(), tx.Tx(), profileID, devices)
		if err != nil {
			return fmt.Errorf("Add root device to default profile of new project: %w", err)
		}
	}

	return nil
}

// swagger:operation GET /1.0/projects/{name} projects project_get
//
//	Get the project
//
//	Gets a specific project.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Project
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
//	          $ref: "#/definitions/Project"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeProject, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the database entry
	var project *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		project.UsedBy, err = projectUsedBy(ctx, tx, dbProject)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypeProject, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ProjectURL(name): project})
		if err != nil {
			return response.SmartError(err)
		}
	}

	etag := []any{
		project.Description,
		project.Config,
	}

	project.UsedBy = projecthelpers.FilterUsedBy(r.Context(), s.Authorizer, project.UsedBy)

	return response.SyncResponseETag(true, project, etag)
}

// swagger:operation PUT /1.0/projects/{name} projects project_put
//
//	Update the project
//
//	Updates the entire project configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectPut"
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
func projectPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the current data
	var project *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		project.UsedBy, err = projectUsedBy(ctx, tx, dbProject)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{
		project.Description,
		project.Config,
	}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request
	req := api.ProjectPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(project.Name, lifecycle.ProjectUpdated.Event(project.Name, requestor, nil))

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	return projectChange(r.Context(), s, project, req, clientType)
}

// swagger:operation PATCH /1.0/projects/{name} projects project_patch
//
//	Partially update the project
//
//	Updates a subset of the project configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectPut"
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
func projectPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the current data
	var project *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		project.UsedBy, err = projectUsedBy(ctx, tx, dbProject)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{
		project.Description,
		project.Config,
	}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))
	rdr2 := io.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&reqRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.ProjectPut{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check what was actually set in the query
	_, err = reqRaw.GetString("description")
	if err != nil {
		req.Description = project.Description
	}

	// Perform config patch
	req.Config = util.CopyConfig(project.Config)
	patches, err := reqRaw.GetMap("config")
	if err == nil {
		for k, v := range patches {
			strVal, ok := v.(string)
			if ok {
				req.Config[k] = strVal
			}
		}
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(project.Name, lifecycle.ProjectUpdated.Event(project.Name, requestor, nil))

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	return projectChange(r.Context(), s, project, req, clientType)
}

// A request for the /internal/project/volume-change endpoint.
type internalProjectPostVolumeChangeRequest struct {
	OldConfig   string `json:"oldconfig" yaml:"oldconfig"`
	NewConfig   string `json:"newconfig" yaml:"newconfig"`
	StorageType string `json:"storagetype" yaml:"storagetype"`
}

// Used to revert project mounted volumes in case some of the nodes in the clutser failed to mount these.
func internalProjectPostVolumeChange(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := internalProjectPostVolumeChangeRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.OldConfig != "" {
		err := unmountDaemonStorageVolume(s, req.OldConfig)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to unmount images storage: %w", err))
		}
	}

	revert := revert.New()
	defer revert.Fail()
	if req.NewConfig != "" {
		err := projectStorageSetup(s, req.NewConfig, req.StorageType, revert)
		if err != nil {
			return response.SmartError(err)
		}
	}

	revert.Success()
	return response.SyncResponse(true, nil)
}

// storageVolumeChange handles changes of one of the storage.images_volume or storage.backups.volume configs.
// As these configs can be changed only on empty projects, we don't move any images around. Instead we only
// mount the volumes as needed.
func storageVolumeChange(s *state.State, projectName string, project api.ProjectPut, oldConfig string, newConfig string, storageType string, clientType clusterRequest.ClientType, revert *revert.Reverter) error {
	// Ask other cluster members to verify and mount the volume.
	if clientType == clusterRequest.ClientTypeNormal {
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		var lock sync.Mutex
		updatedMembers := make([]db.NodeInfo, 0)
		err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
			err := client.UpdateProject(projectName, project, "")
			// Build the list of updated members. If any of them fails, we'll need to revert those which suceeded.
			if err == nil {
				lock.Lock()
				updatedMembers = append(updatedMembers, member)
				lock.Unlock()
			}

			return err
		})

		// If any of the nodes failed to configure the storage, revert those which succeeded.
		if err != nil {
			notifier, innererr := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll, updatedMembers...)
			if innererr != nil {
				return innererr
			}

			req := internalProjectPostVolumeChangeRequest{
				OldConfig:   newConfig,
				NewConfig:   oldConfig,
				StorageType: storageType,
			}
			innererr = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
				_, _, err := client.RawQuery(http.MethodPost, "/internal/project/volume-change", req, "")
				return err
			})
		}

		if err != nil {
			return err
		}
	}

	if oldConfig != "" {
		err := unmountDaemonStorageVolume(s, oldConfig)
		if err != nil {
			return fmt.Errorf("Failed to unmount images storage: %w", err)
		}
	}

	if newConfig != "" {
		err := projectStorageSetup(s, newConfig, storageType, revert)
		if err != nil {
			return err
		}
	}

	return nil
}

// Common logic between PUT and PATCH.
func projectChange(ctx context.Context, s *state.State, project *api.Project, req api.ProjectPut, clientType clusterRequest.ClientType) response.Response {
	// Make a list of config keys that have changed.
	configChanged := []string{}
	for key := range project.Config {
		if req.Config[key] != project.Config[key] {
			configChanged = append(configChanged, key)
		}
	}

	for key := range req.Config {
		_, ok := project.Config[key]
		if !ok {
			configChanged = append(configChanged, key)
		}
	}

	// Record which features have been changed.
	var featuresChanged []string
	var storageConfig string
	for _, configKeyChanged := range configChanged {
		_, isFeature := dbCluster.ProjectFeatures[configKeyChanged]
		if isFeature {
			featuresChanged = append(featuresChanged, configKeyChanged)
		}

		if configKeyChanged == "storage.images_volume" || configKeyChanged == "storage.backups_volume" {
			storageConfig = configKeyChanged
		}
	}

	usedByLen := len(project.UsedBy)
	projectInUse := usedByLen > 1 || (usedByLen == 1 && !strings.Contains(project.UsedBy[0], "/profiles/default"))

	// Quick checks.
	if projectInUse && storageConfig != "" {
		return response.BadRequest(fmt.Errorf("Project config %q cannot be changed on non-empty projects", storageConfig))
	}

	if len(featuresChanged) > 0 {
		if project.Name == api.ProjectDefaultName {
			return response.BadRequest(errors.New("You can't change the features of the default project"))
		}

		// Consider the project empty if it is only used by the default profile.
		if projectInUse {
			// Check if feature is allowed to be changed.
			for _, featureChanged := range featuresChanged {
				// If feature is currently enabled, and it is being changed in the request, it
				// must be being disabled. So prevent it on non-empty projects.
				if shared.IsTrue(project.Config[featureChanged]) {
					return response.BadRequest(fmt.Errorf("Project feature %q cannot be disabled on non-empty projects", featureChanged))
				}

				// If feature is currently disabled, and it is being changed in the request, it
				// must be being enabled. So check if feature can be enabled on non-empty projects.
				if shared.IsFalse(project.Config[featureChanged]) && !dbCluster.ProjectFeatures[featureChanged].CanEnableNonEmpty {
					return response.BadRequest(fmt.Errorf("Project feature %q cannot be enabled on non-empty projects", featureChanged))
				}
			}
		}
	}

	// Validate the configuration.
	err := projectValidateConfig(s, req.Config, "")
	if err != nil {
		return response.BadRequest(err)
	}

	revert := revert.New()
	defer revert.Fail()
	if slices.Contains(configChanged, "storage.images_volume") {
		err = storageVolumeChange(s, project.Name, req, project.Config["storage.images_volume"], req.Config["storage.images_volume"], "images", clientType, revert)
		if err != nil {
			return response.SmartError(err)
		}
	}

	if slices.Contains(configChanged, "storage.backups_volume") {
		err = storageVolumeChange(s, project.Name, req, project.Config["storage.backups_volume"], req.Config["storage.backups_volume"], "backups", clientType, revert)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Don't update the DB on cluster notifications.
	if clientType != clusterRequest.ClientTypeNormal {
		revert.Success()
		return response.EmptySyncResponse
	}

	// Update the database entry.
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := limits.AllowProjectUpdate(ctx, s.GlobalConfig, tx, project.Name, req.Config, configChanged)
		if err != nil {
			return err
		}

		err = dbCluster.UpdateProject(ctx, tx.Tx(), project.Name, req)
		if err != nil {
			return fmt.Errorf("Persist profile changes: %w", err)
		}

		if slices.Contains(configChanged, "features.profiles") {
			if shared.IsTrue(req.Config["features.profiles"]) {
				err = projectCreateDefaultProfile(ctx, tx, project.Name, "", "")
				if err != nil {
					return err
				}
			} else {
				// Delete the project-specific default profile.
				err = dbCluster.DeleteProfile(ctx, tx.Tx(), project.Name, api.ProjectDefaultName)
				if err != nil {
					return fmt.Errorf("Delete project default profile: %w", err)
				}
			}
		}

		if slices.Contains(configChanged, "features.images") && shared.IsFalse(req.Config["features.images"]) && shared.IsTrue(req.Config["features.profiles"]) {
			err = dbCluster.InitProjectWithoutImages(ctx, tx.Tx(), project.Name)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()
	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/projects/{name} projects project_post
//
//	Rename the project
//
//	Renames an existing project.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request.
	req := api.ProjectPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if name == api.ProjectDefaultName {
		return response.Forbidden(errors.New("The 'default' project cannot be renamed"))
	}

	// Perform the rename.
	run := func(op *operations.Operation) error {
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			project, err := dbCluster.GetProject(ctx, tx.Tx(), req.Name)
			if err != nil && !response.IsNotFoundError(err) {
				return fmt.Errorf("Failed checking if project %q exists: %w", req.Name, err)
			}

			if project != nil {
				return fmt.Errorf("A project named %q already exists", req.Name)
			}

			project, err = dbCluster.GetProject(ctx, tx.Tx(), name)
			if err != nil {
				return fmt.Errorf("Failed loading project %q: %w", name, err)
			}

			empty, err := projectIsEmpty(ctx, project, tx)
			if err != nil {
				return err
			}

			if !empty {
				return errors.New("Only empty projects can be renamed")
			}

			err = projecthelpers.ValidName(req.Name)
			if err != nil {
				return err
			}

			return dbCluster.RenameProject(ctx, tx.Tx(), name, req.Name)
		})
		if err != nil {
			return err
		}

		requestor := request.CreateRequestor(r.Context())
		s.Events.SendLifecycle(req.Name, lifecycle.ProjectRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": name}))

		return nil
	}

	op, err := operations.OperationCreate(r.Context(), s, "", operations.OperationClassTask, operationtype.ProjectRename, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/projects/{name} projects project_delete
//
//	Delete the project
//
//	Removes the project.
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
func projectDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	if name == api.ProjectDefaultName {
		return response.Forbidden(errors.New("The 'default' project cannot be deleted"))
	}

	var imagesVolume string
	var backupsVolume string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		project, err := dbCluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Fetch project %q: %w", name, err)
		}

		empty, err := projectIsEmpty(ctx, project, tx)
		if err != nil {
			return err
		}

		if !empty {
			return errors.New("Only empty projects can be removed")
		}

		api, err := project.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		imagesVolume = api.Config["storage.images_volume"]
		backupsVolume = api.Config["storage.backups_volume"]

		return dbCluster.DeleteProject(ctx, tx.Tx(), name)
	})

	if err != nil {
		return response.SmartError(err)
	}

	if imagesVolume != "" {
		err := unmountDaemonStorageVolume(s, imagesVolume)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to unmount images storage: %w", err))
		}
	}

	if backupsVolume != "" {
		err := unmountDaemonStorageVolume(s, backupsVolume)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to unmount backups storage: %w", err))
		}
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(name, lifecycle.ProjectDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/projects/{name}/state projects project_state_get
//
//	Get the project state
//
//	Gets a specific project resource consumption information.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Project state
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
//	          $ref: "#/definitions/ProjectState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectStateGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Setup the state struct.
	state := api.ProjectState{}

	// Get current limits and usage.
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		result, err := limits.GetCurrentAllocations(ctx, s.GlobalConfig.Dump(), tx, name)
		if err != nil {
			return err
		}

		state.Resources = result

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, &state)
}

// Check if a project is empty.
func projectIsEmpty(ctx context.Context, project *dbCluster.Project, tx *db.ClusterTx) (bool, error) {
	instances, err := dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Project: &project.Name})
	if err != nil {
		return false, err
	}

	if len(instances) > 0 {
		return false, nil
	}

	images, err := dbCluster.GetImages(ctx, tx.Tx(), dbCluster.ImageFilter{Project: &project.Name})
	if err != nil {
		return false, err
	}

	if len(images) > 0 {
		return false, nil
	}

	profiles, err := dbCluster.GetProfiles(ctx, tx.Tx(), dbCluster.ProfileFilter{Project: &project.Name})
	if err != nil {
		return false, err
	}

	// Consider the project empty if it is only used by the default profile.
	if len(profiles) > 1 || (len(profiles) == 1 && profiles[0].Name != "default") {
		return false, nil
	}

	volumes, err := tx.GetStorageVolumeURIs(ctx, project.Name)
	if err != nil {
		return false, err
	}

	if len(volumes) > 0 {
		return false, nil
	}

	networks, err := tx.GetNetworkURIs(ctx, project.ID, project.Name)
	if err != nil {
		return false, err
	}

	if len(networks) > 0 {
		return false, nil
	}

	acls, err := tx.GetNetworkACLURIs(ctx, project.ID, project.Name)
	if err != nil {
		return false, err
	}

	if len(acls) > 0 {
		return false, nil
	}

	return true, nil
}

func isEitherAllowOrBlock(value string) error {
	return validate.Optional(validate.IsOneOf("block", "allow"))(value)
}

func isEitherAllowOrBlockOrManaged(value string) error {
	return validate.Optional(validate.IsOneOf("block", "allow", "managed"))(value)
}

// isValidProjectStorageVolume validates a daemon storage volume in the form of "<pool_name>/<volume_name>".
func isValidProjectStorageVolume(s *state.State, daemonStorageVolume string) (err error) {
	_, err = daemonStorageValidate(s, daemonStorageVolume)
	return err
}

// projectValidateConfig validates whether project config keys follow the expected format.
// Any value checks that rely on the state of the database should be performed on AllowProjectUpdate,
// so that we are performing these checks and updating the project in a single transaction.
func projectValidateConfig(s *state.State, config map[string]string, defaultNetwork string) error {
	// Validate the project configuration.
	projectConfigKeys := map[string]func(value string) error{
		// lxdmeta:generate(entities=project; group=specific; key=backups.compression_algorithm)
		// Specify which compression algorithm to use for backups in this project.
		// Possible values are `bzip2`, `gzip`, `lzma`, `xz`, or `none`.
		// ---
		//  type: string
		//  shortdesc: Compression algorithm to use for backups
		"backups.compression_algorithm": validate.IsCompressionAlgorithm,
		// lxdmeta:generate(entities=project; group=features; key=features.profiles)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of profiles for the project
		"features.profiles": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=features; key=features.images)
		// This setting applies to both images and image aliases.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of images for the project
		"features.images": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=features; key=features.storage.volumes)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of storage volumes for the project
		"features.storage.volumes": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=features; key=features.storage.buckets)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of storage buckets for the project
		"features.storage.buckets": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=features; key=features.networks)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `false`
		//  shortdesc: Whether to use a separate set of networks for the project
		"features.networks": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=features; key=features.networks.zones)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `false`
		//  shortdesc: Whether to use a separate set of network zones for the project
		"features.networks.zones": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=specific; key=images.auto_update_cached)
		//
		// ---
		//  type: bool
		//  shortdesc: Whether to automatically update cached images in the project
		"images.auto_update_cached": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=specific; key=images.auto_update_interval)
		// Specify the interval in hours.
		// To disable looking for updates to cached images, set this option to `0`.
		// ---
		//  type: integer
		//  shortdesc: Interval at which to look for updates to cached images
		"images.auto_update_interval": validate.Optional(validate.IsInt64),
		// lxdmeta:generate(entities=project; group=specific; key=images.compression_algorithm)
		// Possible values are `bzip2`, `gzip`, `lzma`, `xz`, or `none`.
		// ---
		//  type: string
		//  shortdesc: Compression algorithm to use for new images in the project
		"images.compression_algorithm": validate.IsCompressionAlgorithm,
		// lxdmeta:generate(entities=project; group=specific; key=images.default_architecture)
		//
		// ---
		//  type: string
		//  shortdesc: Default architecture to use in a mixed-architecture cluster
		"images.default_architecture": validate.Optional(validate.IsArchitecture),
		// lxdmeta:generate(entities=project; group=specific; key=images.remote_cache_expiry)
		// Specify the number of days after which the unused cached image expires.
		// ---
		//  type: integer
		//  shortdesc: When an unused cached remote image is flushed in the project
		"images.remote_cache_expiry": validate.Optional(validate.IsInt64),
		// lxdmeta:generate(entities=project; group=limits; key=limits.instances)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of instances that can be created in the project
		"limits.instances": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=project; group=limits; key=limits.containers)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of containers that can be created in the project
		"limits.containers": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=project; group=limits; key=limits.virtual-machines)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of VMs that can be created in the project
		"limits.virtual-machines": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=project; group=limits; key=limits.memory)
		// The value is the maximum value for the sum of the individual {config:option}`instance-resource-limits:limits.memory` configurations set on the instances of the project.
		// ---
		//  type: string
		//  shortdesc: Usage limit for the host's memory for the project
		"limits.memory": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=project; group=limits; key=limits.processes)
		// This value is the maximum value for the sum of the individual {config:option}`instance-resource-limits:limits.processes` configurations set on the instances of the project.
		// ---
		//  type: integer
		//  shortdesc: Maximum number of processes within the project
		"limits.processes": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=project; group=limits; key=limits.cpu)
		// This value is the maximum value for the sum of the individual {config:option}`instance-resource-limits:limits.cpu` configurations set on the instances of the project.
		// ---
		//  type: integer
		//  shortdesc: Maximum number of CPUs to use in the project
		"limits.cpu": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=project; group=limits; key=limits.disk)
		// This value is the maximum value of the aggregate disk space used by all instance volumes, custom volumes, and images of the project.
		// ---
		//  type: string
		//  shortdesc: Maximum disk space used by the project
		"limits.disk": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=project; group=limits; key=limits.networks)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of networks that the project can have
		"limits.networks": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted)
		// This option must be enabled to allow the `restricted.*` keys to take effect.
		// To temporarily remove the restrictions, you can disable this option instead of clearing the related keys.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether to block access to security-sensitive features
		"restricted": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.backups)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent creating instance or volume backups
		"restricted.backups": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.cluster.groups)
		// If specified, this option prevents targeting cluster groups other than the provided ones.
		// ---
		//  type: string
		//  shortdesc: Cluster groups that can be targeted
		"restricted.cluster.groups": func(value string) error {
			if value == "" {
				return nil
			}

			groupNames := shared.SplitNTrimSpace(value, ",", -1, true)
			return s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
				groups, err := dbCluster.GetClusterGroups(ctx, tx.Tx())
				if err != nil {
					return fmt.Errorf("Failed to validate restricted cluster group configuration: %w", err)
				}

			outer:
				for _, groupName := range groupNames {
					for _, group := range groups {
						if groupName == group.Name {
							continue outer
						}
					}

					return api.StatusErrorf(http.StatusNotFound, "Cluster group %q not found", groupName)
				}

				return nil
			})
		},
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.cluster.target)
		// Possible values are `allow` or `block`.
		// When set to `allow`, this option allows targeting of cluster members (either directly or via a group) when creating or moving instances.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent targeting of cluster members
		"restricted.cluster.target": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.containers.interception)
		// Possible values are `allow`, `block`, or `full`.
		// When set to `allow`, interception options that are usually safe are allowed.
		// File system mounting remains blocked.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using system call interception options
		"restricted.containers.interception": validate.Optional(validate.IsOneOf("allow", "block", "full")),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.containers.nesting)
		// Possible values are `allow` or `block`.
		// When set to `allow`, {config:option}`instance-security:security.nesting` can be set to `true` for an instance.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent running nested LXD
		"restricted.containers.nesting": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.containers.lowlevel)
		// Possible values are `allow` or `block`.
		// When set to `allow`, low-level container options like {config:option}`instance-raw:raw.lxc`, {config:option}`instance-raw:raw.idmap`, `volatile.*`, etc. can be used.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using low-level container options
		"restricted.containers.lowlevel": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.containers.privilege)
		// Possible values are `unprivileged`, `isolated`, and `allow`.
		//
		// - When set to `unpriviliged`, this option prevents setting {config:option}`instance-security:security.privileged` to `true`.
		// - When set to `isolated`, this option prevents setting {config:option}`instance-security:security.privileged` to `true` and forces using a unique idmap per container using {config:option}`instance-security:security.idmap.isolated` set to `true`.
		// - When set to `allow`, there is no restriction.
		// ---
		//  type: string
		//  defaultdesc: `unprivileged`
		//  shortdesc: Which settings for privileged containers to prevent
		"restricted.containers.privilege": validate.Optional(validate.IsOneOf("allow", "unprivileged", "isolated")),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.virtual-machines.lowlevel)
		// Possible values are `allow` or `block`.
		// When set to `allow`, low-level VM options like {config:option}`instance-raw:raw.qemu`, `volatile.*`, etc. can be used.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using low-level VM options
		"restricted.virtual-machines.lowlevel": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.unix-char)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `unix-char`
		"restricted.devices.unix-char": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.unix-block)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `unix-block`
		"restricted.devices.unix-block": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.unix-hotplug)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `unix-hotplug`
		"restricted.devices.unix-hotplug": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.infiniband)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `infiniband`
		"restricted.devices.infiniband": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.gpu)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `gpu`
		"restricted.devices.gpu": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.usb)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `usb`
		"restricted.devices.usb": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.pci)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `pci`
		"restricted.devices.pci": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.proxy)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `proxy`
		"restricted.devices.proxy": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.nic)
		// Possible values are `allow`, `block`, or `managed`.
		//
		// - When set to `block`, this option prevents using all network devices.
		// - When set to `managed`, this option allows using network devices only if `network=` is set.
		// - When set to `allow`, there is no restriction on which network devices can be used.
		// ---
		//  type: string
		//  defaultdesc: `managed`
		//  shortdesc: Which network devices can be used
		"restricted.devices.nic": isEitherAllowOrBlockOrManaged,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.disk)
		// Possible values are `allow`, `block`, or `managed`.
		//
		// - When set to `block`, this option prevents using all disk devices except the root one.
		// - When set to `managed`, this option allows using disk devices only if `pool=` is set.
		// - When set to `allow`, there is no restriction on which disk devices can be used.
		//
		//   ```{important}
		//   When allowing all disk devices, make sure to set
		//   {config:option}`project-restricted:restricted.devices.disk.paths` to a list of
		//   path prefixes that you want to allow.
		//   If you do not restrict the allowed paths, users can attach any disk device, including
		//   shifted devices (`disk` devices with [`shift`](devices-disk-options) set to `true`),
		//   which can be used to gain root access to the system.
		//   ```
		// ---
		//  type: string
		//  defaultdesc: `managed`
		//  shortdesc: Which disk devices can be used
		"restricted.devices.disk": isEitherAllowOrBlockOrManaged,
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.devices.disk.paths)
		// If {config:option}`project-restricted:restricted.devices.disk` is set to `allow`, this option controls which `source` can be used for `disk` devices.
		// Specify a comma-separated list of path prefixes that restrict the `source` setting.
		// If this option is left empty, all paths are allowed.
		// ---
		//  type: string
		//  shortdesc: Which `source` can be used for `disk` devices
		"restricted.devices.disk.paths": validate.Optional(validate.IsListOf(validate.IsAbsFilePath)),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.idmap.uid)
		// This option specifies the host UID ranges that are allowed in the instance's {config:option}`instance-raw:raw.idmap` setting.
		// ---
		//  type: string
		//  shortdesc: Which host UID ranges are allowed in `raw.idmap`
		"restricted.idmap.uid": validate.Optional(validate.IsListOf(validate.IsUint32Range)),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.idmap.gid)
		// This option specifies the host GID ranges that are allowed in the instance's {config:option}`instance-raw:raw.idmap` setting.
		// ---
		//  type: string
		//  shortdesc: Which host GID ranges are allowed in `raw.idmap`
		"restricted.idmap.gid": validate.Optional(validate.IsListOf(validate.IsUint32Range)),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.networks.access)
		// Specify a comma-delimited list of network names that are allowed for use in this project.
		// If this option is not set, all networks are accessible.
		//
		// Note that this setting depends on the {config:option}`project-restricted:restricted.devices.nic` setting.
		// ---
		//  type: string
		//  shortdesc: Which network names are allowed for use in this project
		"restricted.networks.access": validate.Optional(validate.IsListOf(validate.IsAny)),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.networks.uplinks)
		// Specify a comma-delimited list of network names that can be used as uplink for networks in this project.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Which network names can be used as uplink in this project
		"restricted.networks.uplinks": validate.Optional(validate.IsListOf(validate.IsAny)),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.networks.subnets)
		// Specify a comma-delimited list of CIDR network routes from the uplink network's {config:option}`network-physical-network-conf:ipv4.routes` {config:option}`network-physical-network-conf:ipv6.routes` that are allowed for use in this project.
		// Use the form `<uplink>:<subnet>`.
		//
		// Example value: `lxdbr0:192.0.168.0/24,lxdbr0:10.1.19.5/32`
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Which network subnets are allocated for use in this project
		"restricted.networks.subnets": validate.Optional(func(value string) error {
			return projectValidateRestrictedSubnets(s, value)
		}),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.networks.zones)
		// Specify a comma-delimited list of network zones that can be used (or something under them) in this project.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Which network zones can be used in this project
		"restricted.networks.zones": validate.IsListOf(validate.IsAny),
		// lxdmeta:generate(entities=project; group=restricted; key=restricted.snapshots)
		//
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent creating instance or volume snapshots
		"restricted.snapshots": isEitherAllowOrBlock,
		// lxdmeta:generate(entities=project; group=specific; key=storage.backups_volume)
		// Specify the volume using the syntax `POOL/VOLUME`.
		// ---
		//  type: string
		//  shortdesc: Volume to use to store backup tarballs
		"storage.backups_volume": func(daemonStorageVolume string) error {
			return isValidProjectStorageVolume(s, daemonStorageVolume)
		},
		// lxdmeta:generate(entities=project; group=specific; key=storage.images_volume)
		// Specify the volume using the syntax `POOL/VOLUME`.
		// ---
		//  type: string
		//  shortdesc: Volume to use to store the image tarballs
		"storage.images_volume": func(daemonStorageVolume string) error {
			return isValidProjectStorageVolume(s, daemonStorageVolume)
		},
	}

	// Add the storage pool keys.
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load all the pools.
		pools, err := tx.GetStoragePoolNames(ctx)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("Failed loading storage pool names: %w", err)
		}

		// Add the storage-pool specific config keys.
		for _, poolName := range pools {
			// lxdmeta:generate(entities=project; group=limits; key=limits.disk.pool.POOL_NAME)
			// This value is the maximum value of the aggregate disk
			// space used by all instance volumes, custom volumes, and images of the
			// project on this specific storage pool.
			//
			// When set to 0, the pool is excluded from storage pool list for
			// the project.
			// ---
			//  type: string
			//  shortdesc: Maximum disk space used by the project on this pool
			projectConfigKeys["limits.disk.pool."+poolName] = validate.Optional(validate.IsSize)
		}

		// Per-network project limits for uplink IPs only make sense for projects with their own networks.
		if shared.IsTrue(config["features.networks"]) {
			if defaultNetwork != "" {
				return errors.New(i18n.G("A default network device cannot be specified if the networks feature is enabled"))
			}

			// Get networks that are allowed to be used as uplinks by this project.
			allowedUplinkNetworks, err := network.AllowedUplinkNetworks(ctx, tx, config)
			if err != nil {
				return err
			}

			// Add network-specific config keys.
			for _, networkName := range allowedUplinkNetworks {
				// lxdmeta:generate(entities=project; group=limits; key=limits.networks.uplink_ips.ipv4.NETWORK_NAME)
				// Maximum number of IPv4 addresses that this project can consume from the specified uplink network.
				// This number of IPs can be consumed by networks, forwards and load balancers in this project.
				//
				// ---
				//  type: string
				//  shortdesc: Quota of IPv4 addresses from a specified uplink network that can be used by entities in this project
				projectConfigKeys["limits.networks.uplink_ips.ipv4."+networkName] = validate.Optional(validate.IsUint32)

				// lxdmeta:generate(entities=project; group=limits; key=limits.networks.uplink_ips.ipv6.NETWORK_NAME)
				// Maximum number of IPv6 addresses that this project can consume from the specified uplink network.
				// This number of IPs can be consumed by networks, forwards and load balancers in this project.
				//
				// ---
				//  type: string
				//  shortdesc: Quota of IPv6 addresses from a specified uplink network that can be used by entities in this project
				projectConfigKeys["limits.networks.uplink_ips.ipv6."+networkName] = validate.Optional(validate.IsUint32)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	for k, v := range config {
		key := k

		// User keys are free for all.

		// lxdmeta:generate(entities=project; group=specific; key=user.*)
		//
		// ---
		//  type: string
		//  shortdesc: User-provided free-form key/value pairs
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Then validate.
		validator, ok := projectConfigKeys[key]
		if !ok {
			return fmt.Errorf("Invalid project configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid project configuration key %q value: %w", k, err)
		}
	}

	// Ensure that restricted projects have their own profiles. Otherwise restrictions in this project could
	// be bypassed by settings from the default project's profiles that are not checked against this project's
	// restrictions when they are configured.
	if shared.IsTrue(config["restricted"]) && shared.IsFalse(config["features.profiles"]) {
		return errors.New("Projects without their own profiles cannot be restricted")
	}

	// Disallow setting external storage for images for projects without images.
	if config["features.images"] == "false" && config["storage.images_volume"] != "" {
		return errors.New("Projects without images cannot have images storage configured")
	}

	return nil
}

// projectValidateRestrictedSubnets checks that the project's restricted.networks.subnets are properly formatted
// and are within the specified uplink network's routes.
func projectValidateRestrictedSubnets(s *state.State, value string) error {
	for _, subnetRaw := range shared.SplitNTrimSpace(value, ",", -1, false) {
		subnetParts := strings.SplitN(subnetRaw, ":", 2)
		if len(subnetParts) != 2 {
			return fmt.Errorf(`Subnet %q invalid, must be in the format of "<uplink network>:<subnet>"`, subnetRaw)
		}

		uplinkName := subnetParts[0]
		subnetStr := subnetParts[1]

		restrictedSubnetIP, restrictedSubnet, err := net.ParseCIDR(subnetStr)
		if err != nil {
			return err
		}

		if restrictedSubnetIP.String() != restrictedSubnet.IP.String() {
			return fmt.Errorf("Not an IP network address %q", subnetStr)
		}

		var uplink *api.Network

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check uplink exists and load config to compare subnets.
			_, uplink, _, err = tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, uplinkName)

			return err
		})
		if err != nil {
			return fmt.Errorf("Invalid uplink network %q: %w", uplinkName, err)
		}

		// Parse uplink route subnets.
		var uplinkRoutes []*net.IPNet
		for _, k := range []string{"ipv4.routes", "ipv6.routes"} {
			if uplink.Config[k] == "" {
				continue
			}

			uplinkRoutes, err = network.SubnetParseAppend(uplinkRoutes, shared.SplitNTrimSpace(uplink.Config[k], ",", -1, false)...)
			if err != nil {
				return err
			}
		}

		foundMatch := false
		// Check that the restricted subnet is within one of the uplink's routes.
		for _, uplinkRoute := range uplinkRoutes {
			if network.SubnetContains(uplinkRoute, restrictedSubnet) {
				foundMatch = true
				break
			}
		}

		if !foundMatch {
			return fmt.Errorf("Uplink network %q doesn't contain %q in its routes", uplinkName, restrictedSubnet.String())
		}
	}

	return nil
}
