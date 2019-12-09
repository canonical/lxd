package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var profilesCmd = APIEndpoint{
	Path: "profiles",

	Get:  APIEndpointAction{Handler: profilesGet, AccessHandler: AllowProjectPermission("profiles", "view")},
	Post: APIEndpointAction{Handler: profilesPost, AccessHandler: AllowProjectPermission("profiles", "manage-profiles")},
}

var profileCmd = APIEndpoint{
	Path: "profiles/{name}",

	Delete: APIEndpointAction{Handler: profileDelete, AccessHandler: AllowProjectPermission("profiles", "manage-profiles")},
	Get:    APIEndpointAction{Handler: profileGet, AccessHandler: AllowProjectPermission("profiles", "view")},
	Patch:  APIEndpointAction{Handler: profilePatch, AccessHandler: AllowProjectPermission("profiles", "manage-profiles")},
	Post:   APIEndpointAction{Handler: profilePost, AccessHandler: AllowProjectPermission("profiles", "manage-profiles")},
	Put:    APIEndpointAction{Handler: profilePut, AccessHandler: AllowProjectPermission("profiles", "manage-profiles")},
}

/* This is used for both profiles post and profile put */
func profilesGet(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)

	recursion := util.IsRecursionRequest(r)

	var result interface{}
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		filter := db.ProfileFilter{
			Project: project,
		}
		if recursion {
			profiles, err := tx.ProfileList(filter)
			if err != nil {
				return err
			}
			apiProfiles := make([]*api.Profile, len(profiles))
			for i, profile := range profiles {
				apiProfiles[i] = db.ProfileToAPI(&profile)
			}

			result = apiProfiles
		} else {
			result, err = tx.ProfileURIs(filter)
		}
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
}

func profilesPost(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	req := api.ProfilesPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return response.BadRequest(fmt.Errorf("Invalid profile name '%s'", req.Name))
	}

	err := instance.ValidConfig(d.os, req.Config, true, false)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate instance devices with an empty instanceName to indicate profile validation.
	// At this point we don't know the instance type, so just use Container type for validation.
	err = instanceValidDevices(d.State(), d.cluster, instancetype.Container, "", deviceConfig.NewDevices(req.Devices), false)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update DB entry
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		current, _ := tx.ProfileGet(project, req.Name)
		if current != nil {
			return fmt.Errorf("The profile already exists")
		}

		profile := db.Profile{
			Project:     project,
			Name:        req.Name,
			Description: req.Description,
			Config:      req.Config,
			Devices:     req.Devices,
		}
		_, err = tx.ProfileCreate(profile)
		return err
	})
	if err != nil {
		return response.SmartError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, req.Name))
}

func profileGet(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	var resp *api.Profile

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		profile, err := tx.ProfileGet(project, name)
		if err != nil {
			return errors.Wrap(err, "Fetch profile")
		}

		resp = db.ProfileToAPI(profile)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// For backward-compatibility, we strip the "?project" query parameter
	// in case the project is the default one.
	for i, uri := range resp.UsedBy {
		suffix := "?project=default"
		if strings.HasSuffix(uri, suffix) {
			resp.UsedBy[i] = uri[:len(uri)-len(suffix)]
		}
	}

	etag := []interface{}{resp.Config, resp.Description, resp.Devices}
	return response.SyncResponseETag(true, resp, etag)
}

func profilePut(d *Daemon, r *http.Request) response.Response {
	// Get the project
	project := projectParam(r)

	// Get the profile
	name := mux.Vars(r)["name"]

	if isClusterNotification(r) {
		// In this case the ProfilePut request payload contains
		// information about the old profile, since the new one has
		// already been saved in the database.
		old := api.ProfilePut{}
		err := json.NewDecoder(r.Body).Decode(&old)
		if err != nil {
			return response.BadRequest(err)
		}

		err = doProfileUpdateCluster(d, project, name, old)
		return response.SmartError(err)
	}

	var id int64
	var profile *api.Profile

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		current, err := tx.ProfileGet(project, name)
		if err != nil {
			return errors.Wrapf(err, "Failed to retrieve profile='%s'", name)
		}

		profile = db.ProfileToAPI(current)
		id = int64(current.ID)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{profile.Config, profile.Description, profile.Devices}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ProfilePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	err = doProfileUpdate(d, project, name, id, profile, req)

	if err == nil && !isClusterNotification(r) {
		// Notify all other nodes. If a node is down, it will be ignored.
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(client lxd.InstanceServer) error {
			return client.UseProject(project).UpdateProfile(name, profile.ProfilePut, "")
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SmartError(err)
}

func profilePatch(d *Daemon, r *http.Request) response.Response {
	// Get the project
	project := projectParam(r)

	// Get the profile
	name := mux.Vars(r)["name"]

	var id int64
	var profile *api.Profile

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		current, err := tx.ProfileGet(project, name)
		if err != nil {
			return errors.Wrapf(err, "Failed to retrieve profile='%s'", name)
		}

		profile = db.ProfileToAPI(current)
		id = int64(current.ID)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{profile.Config, profile.Description, profile.Devices}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&reqRaw); err != nil {
		return response.BadRequest(err)
	}

	req := api.ProfilePut{}
	if err := json.NewDecoder(rdr2).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Get Description
	_, err = reqRaw.GetString("description")
	if err != nil {
		req.Description = profile.Description
	}

	// Get Config
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

	// Get Devices
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

	return response.SmartError(doProfileUpdate(d, project, name, id, profile, req))
}

// The handler for the post operation.
func profilePost(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	if name == "default" {
		return response.Forbidden(errors.New("The 'default' profile cannot be renamed"))
	}

	req := api.ProfilePost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return response.BadRequest(fmt.Errorf("Invalid profile name '%s'", req.Name))
	}

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		// Check that the name isn't already in use
		_, err = tx.ProfileGet(project, req.Name)
		if err == nil {
			return fmt.Errorf("Name '%s' already in use", req.Name)
		}

		return tx.ProfileRename(project, name, req.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, req.Name))
}

// The handler for the delete operation.
func profileDelete(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	if name == "default" {
		return response.Forbidden(errors.New("The 'default' profile cannot be deleted"))
	}

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		hasProfiles, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check project features")
		}

		if !hasProfiles {
			project = "default"
		}

		profile, err := tx.ProfileGet(project, name)
		if err != nil {
			return err
		}
		if len(profile.UsedBy) > 0 {
			return fmt.Errorf("Profile is currently in use")
		}

		return tx.ProfileDelete(project, name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
