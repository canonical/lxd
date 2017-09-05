package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

/* This is used for both profiles post and profile put */
func profilesGet(d *Daemon, r *http.Request) Response {
	results, err := d.db.Profiles()
	if err != nil {
		return SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	resultString := make([]string, len(results))
	resultMap := make([]*api.Profile, len(results))
	i := 0
	for _, name := range results {
		if !recursion {
			url := fmt.Sprintf("/%s/profiles/%s", version.APIVersion, name)
			resultString[i] = url
		} else {
			profile, err := doProfileGet(d.State(), name)
			if err != nil {
				logger.Error("Failed to get profile", log.Ctx{"profile": name})
				continue
			}
			resultMap[i] = profile
		}
		i++
	}

	if !recursion {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func profilesPost(d *Daemon, r *http.Request) Response {
	req := api.ProfilesPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	_, profile, _ := d.db.ProfileGet(req.Name)
	if profile != nil {
		return BadRequest(fmt.Errorf("The profile already exists"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return BadRequest(fmt.Errorf("Invalid profile name '%s'", req.Name))
	}

	err := containerValidConfig(d.os, req.Config, true, false)
	if err != nil {
		return BadRequest(err)
	}

	err = containerValidDevices(req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	// Update DB entry
	_, err = d.db.ProfileCreate(req.Name, req.Description, req.Config, req.Devices)
	if err != nil {
		return SmartError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, req.Name))
}

var profilesCmd = Command{
	name: "profiles",
	get:  profilesGet,
	post: profilesPost}

func doProfileGet(s *state.State, name string) (*api.Profile, error) {
	_, profile, err := s.DB.ProfileGet(name)
	return profile, err
}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	resp, err := doProfileGet(d.State(), name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, resp)
}

func getContainersWithProfile(s *state.State, storage storage, profile string) []container {
	results := []container{}

	output, err := s.DB.ProfileContainersGet(profile)
	if err != nil {
		return results
	}

	for _, name := range output {
		c, err := containerLoadByName(s, storage, name)
		if err != nil {
			logger.Error("Failed opening container", log.Ctx{"container": name})
			continue
		}
		results = append(results, c)
	}

	return results
}

func profilePut(d *Daemon, r *http.Request) Response {
	// Get the profile
	name := mux.Vars(r)["name"]
	id, profile, err := d.db.ProfileGet(name)
	if err != nil {
		return SmartError(fmt.Errorf("Failed to retrieve profile='%s'", name))
	}

	req := api.ProfilePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	return doProfileUpdate(d, name, id, profile, req)
}

// The handler for the post operation.
func profilePost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	req := api.ProfilePost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	// Check that the name isn't already in use
	id, _, _ := d.db.ProfileGet(req.Name)
	if id > 0 {
		return Conflict
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return BadRequest(fmt.Errorf("Invalid profile name '%s'", req.Name))
	}

	err := d.db.ProfileUpdate(name, req.Name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, req.Name))
}

// The handler for the delete operation.
func profileDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	_, err := doProfileGet(d.State(), name)
	if err != nil {
		return SmartError(err)
	}

	clist := getContainersWithProfile(d.State(), d.Storage, name)
	if len(clist) != 0 {
		return BadRequest(fmt.Errorf("Profile is currently in use"))
	}

	err = d.db.ProfileDelete(name)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var profileCmd = Command{name: "profiles/{name}", get: profileGet, put: profilePut, delete: profileDelete, post: profilePost}
