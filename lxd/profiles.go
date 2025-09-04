package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var profilesCmd = APIEndpoint{
	Path:        "profiles",
	MetricsType: entity.TypeProfile,

	Get:  APIEndpointAction{Handler: profilesGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: profilesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateProfiles)},
}

var profileCmd = APIEndpoint{
	Path:        "profiles/{name}",
	MetricsType: entity.TypeProfile,

	Delete: APIEndpointAction{Handler: profileDelete, AccessHandler: profileAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: profileGet, AccessHandler: profileAccessHandler(auth.EntitlementCanView)},
	Patch:  APIEndpointAction{Handler: profilePatch, AccessHandler: profileAccessHandler(auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: profilePost, AccessHandler: profileAccessHandler(auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: profilePut, AccessHandler: profileAccessHandler(auth.EntitlementCanEdit)},
}

// ctxProfileDetails should be used only for getting/setting profileDetails in the request context.
const ctxProfileDetails request.CtxKey = "profile-details"

// profileDetails contains fields that are determined prior to the access check. This is set in the request context when
// addProfileDetailsToRequestContext is called.
type profileDetails struct {
	profileName      string
	effectiveProject api.Project
}

// addProfileDetailsToRequestContext sets the effective project in the request.Info and sets ctxProfileDetails (profileDetails)
// in the request context.
func addProfileDetailsToRequestContext(s *state.State, r *http.Request) error {
	profileName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return err
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProject, err := project.ProfileProject(s.DB.Cluster, requestProjectName)
	if err != nil {
		return fmt.Errorf("Failed to check project %q profile feature: %w", requestProjectName, err)
	}

	request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProject.Name)
	request.SetContextValue(r, ctxProfileDetails, profileDetails{
		profileName:      profileName,
		effectiveProject: *effectiveProject,
	})

	return nil
}

// profileAccessHandler calls addProfileDetailsToRequestContext, then uses the details to perform an access check with
// the given auth.Entitlement.
func profileAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		err := addProfileDetailsToRequestContext(s, r)
		if err != nil {
			return response.SmartError(err)
		}

		details, err := request.GetContextValue[profileDetails](r.Context(), ctxProfileDetails)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.ProfileURL(request.ProjectParam(r), details.profileName), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

// swagger:operation GET /1.0/profiles profiles profiles_get
//
//  Get the profiles
//
//  Returns a list of profiles (URLs).
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
//      name: all-projects
//      description: Retrieve profiles from all projects
//      type: boolean
//      example: true
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
//                "/1.0/profiles/default",
//                "/1.0/profiles/foo"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/profiles?recursion=1 profiles profiles_get_recursion1
//
//	Get the profiles
//
//	Returns a list of profiles (structs).
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
//	    name: all-projects
//	    description: Retrieve profiles from all projects
//	    type: boolean
//	    example: true
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
//	          description: List of profiles
//	          items:
//	            $ref: "#/definitions/Profile"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func profilesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	requestProjectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	var effectiveProjectName string
	if !allProjects {
		p, err := project.ProfileProject(s.DB.Cluster, requestProjectName)
		if err != nil {
			return response.SmartError(err)
		}

		effectiveProjectName = p.Name
		request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	}

	recursion := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeProfile, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeProfile)
	if err != nil {
		return response.InternalError(err)
	}

	var apiProfiles []*api.Profile
	var profileURLs []string
	urlToProfile := make(map[*api.URL]auth.EntitlementReporter)
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var profiles []dbCluster.Profile
		if !allProjects {
			filter := dbCluster.ProfileFilter{
				Project: &effectiveProjectName,
			}

			profiles, err = dbCluster.GetProfiles(ctx, tx.Tx(), filter)
			if err != nil {
				return err
			}
		} else {
			profiles, err = dbCluster.GetProfiles(ctx, tx.Tx())
			if err != nil {
				return err
			}
		}

		if recursion {
			profileConfigs, err := dbCluster.GetConfig(ctx, tx.Tx(), "profile")
			if err != nil {
				return err
			}

			profileDevices, err := dbCluster.GetDevices(ctx, tx.Tx(), "profile")
			if err != nil {
				return err
			}

			apiProfiles = make([]*api.Profile, 0, len(profiles))
			for _, profile := range profiles {
				if !userHasPermission(entity.ProfileURL(requestProjectName, profile.Name)) {
					continue
				}

				apiProfile, err := profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
				if err != nil {
					return err
				}

				apiProfile.UsedBy, err = profileUsedBy(ctx, tx, profile)
				if err != nil {
					return err
				}

				apiProfiles = append(apiProfiles, apiProfile)
				urlToProfile[entity.ProfileURL(requestProjectName, profile.Name)] = apiProfile
			}
		} else {
			profileURLs = make([]string, 0, len(profiles))
			for _, profile := range profiles {
				profileURL := entity.ProfileURL(requestProjectName, profile.Name)
				if userHasPermission(profileURL) {
					profileURLs = append(profileURLs, profileURL.String())
				}
			}
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		return response.SyncResponse(true, profileURLs)
	}

	for _, apiProfile := range apiProfiles {
		apiProfile.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, apiProfile.UsedBy)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeProfile, withEntitlements, urlToProfile)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiProfiles)
}

// profileUsedBy returns all the instance URLs that are using the given profile.
func profileUsedBy(ctx context.Context, tx *db.ClusterTx, profile dbCluster.Profile) ([]string, error) {
	instances, err := dbCluster.GetProfileInstances(ctx, tx.Tx(), profile.ID)
	if err != nil {
		return nil, err
	}

	usedBy := make([]string, len(instances))
	for i, inst := range instances {
		apiInst := &api.Instance{Name: inst.Name}
		usedBy[i] = apiInst.URL(version.APIVersion, inst.Project).String()
	}

	return usedBy, nil
}

// swagger:operation POST /1.0/profiles profiles profiles_post
//
//	Add a profile
//
//	Creates a new profile.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: profile
//	    description: Profile
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProfilesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func profilesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	p, err := project.ProfileProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ProfilesPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(errors.New("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(errors.New("Profile names may not contain slashes"))
	}

	if slices.Contains([]string{".", ".."}, req.Name) {
		return response.BadRequest(fmt.Errorf("Invalid profile name %q", req.Name))
	}

	err = instance.ValidConfig(d.os, req.Config, false, instancetype.Any)
	if err != nil {
		return response.BadRequest(err)
	}

	// At this point we don't know the instance type, so just use instancetype.Any type for validation.
	err = instance.ValidDevices(s, *p, instancetype.Any, deviceConfig.NewDevices(req.Devices), nil)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update DB entry.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		devices, err := dbCluster.APIToDevices(req.Devices)
		if err != nil {
			return err
		}

		current, _ := dbCluster.GetProfile(ctx, tx.Tx(), p.Name, req.Name)
		if current != nil {
			return errors.New("The profile already exists")
		}

		profile := dbCluster.Profile{
			Project:     p.Name,
			Name:        req.Name,
			Description: req.Description,
		}

		id, err := dbCluster.CreateProfile(ctx, tx.Tx(), profile)
		if err != nil {
			return err
		}

		err = dbCluster.CreateProfileConfig(ctx, tx.Tx(), id, req.Config)
		if err != nil {
			return err
		}

		err = dbCluster.CreateProfileDevices(ctx, tx.Tx(), id, devices)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error inserting %q into database: %w", req.Name, err))
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ProfileCreated.Event(req.Name, p.Name, requestor, nil)
	s.Events.SendLifecycle(p.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/profiles/{name} profiles profile_get
//
//	Get the profile
//
//	Gets a specific profile.
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
//	responses:
//	  "200":
//	    description: Profile
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
//	          $ref: "#/definitions/Profile"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func profileGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetContextValue[profileDetails](r.Context(), ctxProfileDetails)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeProfile, false)
	if err != nil {
		return response.SmartError(err)
	}

	var resp *api.Profile

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile, err := dbCluster.GetProfile(ctx, tx.Tx(), details.effectiveProject.Name, details.profileName)
		if err != nil {
			return fmt.Errorf("Fetch profile: %w", err)
		}

		profileConfigs, err := dbCluster.GetConfig(ctx, tx.Tx(), "profile")
		if err != nil {
			return err
		}

		profileDevices, err := dbCluster.GetDevices(ctx, tx.Tx(), "profile")
		if err != nil {
			return err
		}

		resp, err = profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
		if err != nil {
			return err
		}

		resp.UsedBy, err = profileUsedBy(ctx, tx, *profile)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	resp.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, resp.UsedBy)

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeProfile, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ProfileURL(details.effectiveProject.Name, details.profileName): resp})
		if err != nil {
			return response.SmartError(err)
		}
	}

	etag := []any{resp.Config, resp.Description, resp.Devices}
	return response.SyncResponseETag(true, resp, etag)
}

// swagger:operation PUT /1.0/profiles/{name} profiles profile_put
//
//	Update the profile
//
//	Updates the entire profile configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: profile
//	    description: Profile configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProfilePut"
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
func profilePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetContextValue[profileDetails](r.Context(), ctxProfileDetails)
	if err != nil {
		return response.SmartError(err)
	}

	if isClusterNotification(r) {
		// In this case the ProfilePut request payload contains information about the old profile, since
		// the new one has already been saved in the database.
		old := api.ProfilePut{}
		err := json.NewDecoder(r.Body).Decode(&old)
		if err != nil {
			return response.BadRequest(err)
		}

		err = doProfileUpdateCluster(r.Context(), s, details.effectiveProject.Name, details.profileName, old)
		return response.SmartError(err)
	}

	var profile *api.Profile

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := dbCluster.GetProfile(ctx, tx.Tx(), details.effectiveProject.Name, details.profileName)
		if err != nil {
			return fmt.Errorf("Failed to retrieve profile %q: %w", details.profileName, err)
		}

		profile, err = current.ToAPI(ctx, tx.Tx(), nil, nil)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []any{profile.Config, profile.Description, profile.Devices}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ProfilePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = doProfileUpdate(r.Context(), s, details.effectiveProject, details.profileName, profile, req)

	if err == nil && !isClusterNotification(r) {
		// Notify all other nodes. If a node is down, it will be ignored.
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(_ db.NodeInfo, client lxd.InstanceServer) error {
			return client.UseProject(details.effectiveProject.Name).UpdateProfile(details.profileName, profile.Writable(), "")
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(details.effectiveProject.Name, lifecycle.ProfileUpdated.Event(details.profileName, details.effectiveProject.Name, requestor, nil))

	return response.SmartError(err)
}

// swagger:operation PATCH /1.0/profiles/{name} profiles profile_patch
//
//	Partially update the profile
//
//	Updates a subset of the profile configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: profile
//	    description: Profile configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProfilePut"
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
func profilePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetContextValue[profileDetails](r.Context(), ctxProfileDetails)
	if err != nil {
		return response.SmartError(err)
	}

	var profile *api.Profile

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := dbCluster.GetProfile(ctx, tx.Tx(), details.effectiveProject.Name, details.profileName)
		if err != nil {
			return fmt.Errorf("Failed to retrieve profile=%q: %w", details.profileName, err)
		}

		profile, err = current.ToAPI(ctx, tx.Tx(), nil, nil)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []any{profile.Config, profile.Description, profile.Devices}
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

	req := api.ProfilePut{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get Description.
	_, err = reqRaw.GetString("description")
	if err != nil {
		req.Description = profile.Description
	}

	// Get Config.
	if req.Config == nil {
		req.Config = profile.Config
	} else {
		for k, v := range profile.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	// Get Devices.
	if req.Devices == nil {
		req.Devices = profile.Devices
	} else {
		for k, v := range profile.Devices {
			_, ok := req.Devices[k]
			if !ok {
				req.Devices[k] = v
			}
		}
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(details.effectiveProject.Name, lifecycle.ProfileUpdated.Event(details.profileName, details.effectiveProject.Name, requestor, nil))

	return response.SmartError(doProfileUpdate(r.Context(), s, details.effectiveProject, details.profileName, profile, req))
}

// swagger:operation POST /1.0/profiles/{name} profiles profile_post
//
//	Rename the profile
//
//	Renames an existing profile.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: profile
//	    description: Profile rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProfilePost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func profilePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetContextValue[profileDetails](r.Context(), ctxProfileDetails)
	if err != nil {
		return response.SmartError(err)
	}

	if details.profileName == "default" {
		return response.Forbidden(errors.New(`The "default" profile cannot be renamed`))
	}

	req := api.ProfilePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(errors.New("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(errors.New("Profile names may not contain slashes"))
	}

	if slices.Contains([]string{".", ".."}, req.Name) {
		return response.BadRequest(fmt.Errorf("Invalid profile name %q", req.Name))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = dbCluster.GetProfile(ctx, tx.Tx(), details.effectiveProject.Name, req.Name)
		if err == nil {
			return fmt.Errorf("Name %q already in use", req.Name)
		}

		return dbCluster.RenameProfile(ctx, tx.Tx(), details.effectiveProject.Name, details.profileName, req.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ProfileRenamed.Event(req.Name, details.effectiveProject.Name, requestor, logger.Ctx{"old_name": details.profileName})
	s.Events.SendLifecycle(details.effectiveProject.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/profiles/{name} profiles profile_delete
//
//	Delete the profile
//
//	Removes the profile.
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
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func profileDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetContextValue[profileDetails](r.Context(), ctxProfileDetails)
	if err != nil {
		return response.SmartError(err)
	}

	err = doProfileDelete(r.Context(), s, details.profileName, details.effectiveProject.Name)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// doProfileDelete deletes a named profile in the given project.
// Returns an [api.StatusError] with an HTTP status code on failure.
func doProfileDelete(ctx context.Context, s *state.State, name string, effectiveProjectName string) error {
	if name == "default" {
		return api.StatusErrorf(http.StatusForbidden, `The "default" profile cannot be deleted`)
	}

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		profile, err := dbCluster.GetProfile(ctx, tx.Tx(), effectiveProjectName, name)
		if err != nil {
			return err
		}

		usedBy, err := profileUsedBy(ctx, tx, *profile)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return errors.New("Profile is currently in use")
		}

		return dbCluster.DeleteProfile(ctx, tx.Tx(), effectiveProjectName, name)
	})
	if err != nil {
		return err
	}

	requestor := request.CreateRequestor(ctx)
	s.Events.SendLifecycle(effectiveProjectName, lifecycle.ProfileDeleted.Event(name, effectiveProjectName, requestor, nil))

	return nil
}
