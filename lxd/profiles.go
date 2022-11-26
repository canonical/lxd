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
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var profilesCmd = APIEndpoint{
	Path: "profiles",

	Get:  APIEndpointAction{Handler: profilesGet, AccessHandler: allowProjectPermission("profiles", "view")},
	Post: APIEndpointAction{Handler: profilesPost, AccessHandler: allowProjectPermission("profiles", "manage-profiles")},
}

var profileCmd = APIEndpoint{
	Path: "profiles/{name}",

	Delete: APIEndpointAction{Handler: profileDelete, AccessHandler: allowProjectPermission("profiles", "manage-profiles")},
	Get:    APIEndpointAction{Handler: profileGet, AccessHandler: allowProjectPermission("profiles", "view")},
	Patch:  APIEndpointAction{Handler: profilePatch, AccessHandler: allowProjectPermission("profiles", "manage-profiles")},
	Post:   APIEndpointAction{Handler: profilePost, AccessHandler: allowProjectPermission("profiles", "manage-profiles")},
	Put:    APIEndpointAction{Handler: profilePut, AccessHandler: allowProjectPermission("profiles", "manage-profiles")},
}

// swagger:operation GET /1.0/profiles profiles profiles_get
//
// Get the profiles
//
// Returns a list of profiles (URLs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/profiles/default",
//               "/1.0/profiles/foo"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/profiles?recursion=1 profiles profiles_get_recursion1
//
// Get the profiles
//
// Returns a list of profiles (structs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of profiles
//           items:
//             $ref: "#/definitions/Profile"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profilesGet(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	var result any
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.ProfileFilter{
			Project: &p.Name,
		}

		profiles, err := dbCluster.GetProfiles(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		apiProfiles := make([]*api.Profile, len(profiles))
		for i, profile := range profiles {
			apiProfiles[i], err = profile.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			apiProfiles[i].UsedBy, err = profileUsedBy(ctx, tx, profile)
			if err != nil {
				return err
			}

			apiProfiles[i].UsedBy = project.FilterUsedBy(r, apiProfiles[i].UsedBy)
		}

		if recursion {
			result = apiProfiles
		} else {
			urls := make([]string, len(apiProfiles))
			for i, apiProfile := range apiProfiles {
				urls[i] = apiProfile.URL(version.APIVersion, p.Name).String()
			}

			result = urls
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
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
// Add a profile
//
// Creates a new profile.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: profile
//     description: Profile
//     required: true
//     schema:
//       $ref: "#/definitions/ProfilesPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profilesPost(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
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
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return response.BadRequest(fmt.Errorf("Invalid profile name %q", req.Name))
	}

	err = instance.ValidConfig(d.os, req.Config, false, instancetype.Any)
	if err != nil {
		return response.BadRequest(err)
	}

	// At this point we don't know the instance type, so just use instancetype.Any type for validation.
	err = instance.ValidDevices(d.State(), *p, instancetype.Any, deviceConfig.NewDevices(req.Devices), nil)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update DB entry.
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		devices, err := dbCluster.APIToDevices(req.Devices)
		if err != nil {
			return err
		}

		current, _ := dbCluster.GetProfile(ctx, tx.Tx(), p.Name, req.Name)
		if current != nil {
			return fmt.Errorf("The profile already exists")
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

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ProfileCreated.Event(req.Name, p.Name, requestor, nil)
	d.State().Events.SendLifecycle(p.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/profiles/{name} profiles profile_get
//
// Get the profile
//
// Gets a specific profile.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     description: Profile
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           $ref: "#/definitions/Profile"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profileGet(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var resp *api.Profile

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile, err := dbCluster.GetProfile(ctx, tx.Tx(), p.Name, name)
		if err != nil {
			return fmt.Errorf("Fetch profile: %w", err)
		}

		resp, err = profile.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		resp.UsedBy, err = profileUsedBy(ctx, tx, *profile)
		if err != nil {
			return err
		}

		resp.UsedBy = project.FilterUsedBy(r, resp.UsedBy)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{resp.Config, resp.Description, resp.Devices}
	return response.SyncResponseETag(true, resp, etag)
}

// swagger:operation PUT /1.0/profiles/{name} profiles profile_put
//
// Update the profile
//
// Updates the entire profile configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: profile
//     description: Profile configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ProfilePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profilePut(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
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

		err = doProfileUpdateCluster(d, p.Name, name, old)
		return response.SmartError(err)
	}

	var id int64
	var profile *api.Profile

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := dbCluster.GetProfile(ctx, tx.Tx(), p.Name, name)
		if err != nil {
			return fmt.Errorf("Failed to retrieve profile %q: %w", name, err)
		}

		profile, err = current.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		id = int64(current.ID)

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

	err = doProfileUpdate(d, *p, name, id, profile, req)

	if err == nil && !isClusterNotification(r) {
		// Notify all other nodes. If a node is down, it will be ignored.
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(client lxd.InstanceServer) error {
			return client.UseProject(p.Name).UpdateProfile(name, profile.ProfilePut, "")
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(p.Name, lifecycle.ProfileUpdated.Event(name, p.Name, requestor, nil))

	return response.SmartError(err)
}

// swagger:operation PATCH /1.0/profiles/{name} profiles profile_patch
//
// Partially update the profile
//
// Updates a subset of the profile configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: profile
//     description: Profile configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ProfilePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profilePatch(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var id int64
	var profile *api.Profile

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := dbCluster.GetProfile(ctx, tx.Tx(), p.Name, name)
		if err != nil {
			return fmt.Errorf("Failed to retrieve profile=%q: %w", name, err)
		}

		profile, err = current.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		id = int64(current.ID)

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

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(p.Name, lifecycle.ProfileUpdated.Event(name, p.Name, requestor, nil))

	return response.SmartError(doProfileUpdate(d, *p, name, id, profile, req))
}

// swagger:operation POST /1.0/profiles/{name} profiles profile_post
//
// Rename the profile
//
// Renames an existing profile.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: profile
//     description: Profile rename request
//     required: true
//     schema:
//       $ref: "#/definitions/ProfilePost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profilePost(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if name == "default" {
		return response.Forbidden(errors.New(`The "default" profile cannot be renamed`))
	}

	req := api.ProfilePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return response.BadRequest(fmt.Errorf("Invalid profile name %q", req.Name))
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = dbCluster.GetProfile(ctx, tx.Tx(), p.Name, req.Name)
		if err == nil {
			return fmt.Errorf("Name %q already in use", req.Name)
		}

		return dbCluster.RenameProfile(ctx, tx.Tx(), p.Name, name, req.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ProfileRenamed.Event(req.Name, p.Name, requestor, logger.Ctx{"old_name": name})
	d.State().Events.SendLifecycle(p.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/profiles/{name} profiles profile_delete
//
// Delete the profile
//
// Removes the profile.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func profileDelete(d *Daemon, r *http.Request) response.Response {
	p, err := project.ProfileProject(d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if name == "default" {
		return response.Forbidden(errors.New(`The "default" profile cannot be deleted`))
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile, err := dbCluster.GetProfile(ctx, tx.Tx(), p.Name, name)
		if err != nil {
			return err
		}

		usedBy, err := profileUsedBy(ctx, tx, *profile)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return fmt.Errorf("Profile is currently in use")
		}

		return dbCluster.DeleteProfile(ctx, tx.Tx(), p.Name, name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(p.Name, lifecycle.ProfileDeleted.Event(name, p.Name, requestor, nil))

	return response.EmptySyncResponse
}
