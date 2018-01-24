package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
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

	err = containerValidDevices(d.cluster, req.Devices, true, false)
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
	_, profile, err := s.Node.ProfileGet(name)
	if err != nil {
		return nil, err
	}

	cts, err := s.Node.ProfileContainersGet(name)
	if err != nil {
		return nil, err
	}

	usedBy := []string{}
	for _, ct := range cts {
		usedBy = append(usedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
	}
	profile.UsedBy = usedBy

	return profile, nil
}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	resp, err := doProfileGet(d.State(), name)
	if err != nil {
		return SmartError(err)
	}

	etag := []interface{}{resp.Config, resp.Description, resp.Devices}
	return SyncResponseETag(true, resp, etag)
}

func getContainersWithProfile(s *state.State, profile string) []container {
	results := []container{}

	output, err := s.Node.ProfileContainersGet(profile)
	if err != nil {
		return results
	}

	for _, name := range output {
		c, err := containerLoadByName(s, name)
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

	// Validate the ETag
	etag := []interface{}{profile.Config, profile.Description, profile.Devices}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.ProfilePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	return doProfileUpdate(d, name, id, profile, req)
}

func profilePatch(d *Daemon, r *http.Request) Response {
	// Get the profile
	name := mux.Vars(r)["name"]
	id, profile, err := d.db.ProfileGet(name)
	if err != nil {
		return SmartError(fmt.Errorf("Failed to retrieve profile='%s'", name))
	}

	// Validate the ETag
	etag := []interface{}{profile.Config, profile.Description, profile.Devices}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&reqRaw); err != nil {
		return BadRequest(err)
	}

	req := api.ProfilePut{}
	if err := json.NewDecoder(rdr2).Decode(&req); err != nil {
		return BadRequest(err)
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

	clist := getContainersWithProfile(d.State(), name)
	if len(clist) != 0 {
		return BadRequest(fmt.Errorf("Profile is currently in use"))
	}

	err = d.db.ProfileDelete(name)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var profileCmd = Command{name: "profiles/{name}", get: profileGet, put: profilePut, delete: profileDelete, post: profilePost, patch: profilePatch}
