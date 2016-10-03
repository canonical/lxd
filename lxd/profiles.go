package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* This is used for both profiles post and profile put */
type profilesPostReq struct {
	Name        string            `json:"name"`
	Config      map[string]string `json:"config"`
	Description string            `json:"description"`
	Devices     shared.Devices    `json:"devices"`
}

func profilesGet(d *Daemon, r *http.Request) Response {
	results, err := dbProfiles(d.db)
	if err != nil {
		return SmartError(err)
	}

	recursion := d.isRecursionRequest(r)

	resultString := make([]string, len(results))
	resultMap := make([]*shared.ProfileConfig, len(results))
	i := 0
	for _, name := range results {
		if !recursion {
			url := fmt.Sprintf("/%s/profiles/%s", shared.APIVersion, name)
			resultString[i] = url
		} else {
			profile, err := doProfileGet(d, name)
			if err != nil {
				shared.LogError("Failed to get profile", log.Ctx{"profile": name})
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
	req := profilesPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	_, profile, _ := dbProfileGet(d.db, req.Name)
	if profile != nil {
		return BadRequest(fmt.Errorf("The profile already exists"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return BadRequest(fmt.Errorf("Invalid profile name '%s'", req.Name))
	}

	err := containerValidConfig(d, req.Config, true, false)
	if err != nil {
		return BadRequest(err)
	}

	err = containerValidDevices(req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	// Update DB entry
	_, err = dbProfileCreate(d.db, req.Name, req.Description, req.Config, req.Devices)
	if err != nil {
		return InternalError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/profiles/%s", shared.APIVersion, req.Name))
}

var profilesCmd = Command{
	name: "profiles",
	get:  profilesGet,
	post: profilesPost}

func doProfileGet(d *Daemon, name string) (*shared.ProfileConfig, error) {
	_, profile, err := dbProfileGet(d.db, name)
	if err != nil {
		return nil, err
	}

	cts, err := dbProfileContainersGet(d.db, name)
	if err != nil {
		return nil, err
	}

	usedBy := []string{}
	for _, ct := range cts {
		usedBy = append(usedBy, fmt.Sprintf("/%s/containers/%s", shared.APIVersion, ct))
	}
	profile.UsedBy = usedBy

	return profile, nil
}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	resp, err := doProfileGet(d, name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseETag(true, resp, resp)
}

func getRunningContainersWithProfile(d *Daemon, profile string) []container {
	results := []container{}

	output, err := dbProfileContainersGet(d.db, profile)
	if err != nil {
		return results
	}

	for _, name := range output {
		c, err := containerLoadByName(d, name)
		if err != nil {
			shared.LogError("Failed opening container", log.Ctx{"container": name})
			continue
		}
		results = append(results, c)
	}
	return results
}

func profilePut(d *Daemon, r *http.Request) Response {
	// Get the profile
	name := mux.Vars(r)["name"]
	id, profile, err := dbProfileGet(d.db, name)
	if err != nil {
		return InternalError(fmt.Errorf("Failed to retrieve profile='%s'", name))
	}

	// Validate the ETag
	err = etagCheck(r, profile)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := profilesPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	return doProfileUpdate(d, name, id, profile, req)
}

func profilePatch(d *Daemon, r *http.Request) Response {
	// Get the profile
	name := mux.Vars(r)["name"]
	id, profile, err := dbProfileGet(d.db, name)
	if err != nil {
		return InternalError(fmt.Errorf("Failed to retrieve profile='%s'", name))
	}

	// Validate the ETag
	err = etagCheck(r, profile)
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

	req := profilesPostReq{}
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

func doProfileUpdate(d *Daemon, name string, id int64, profile *shared.ProfileConfig, req profilesPostReq) Response {
	// Sanity checks
	err := containerValidConfig(d, req.Config, true, false)
	if err != nil {
		return BadRequest(err)
	}

	err = containerValidDevices(req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	// Get the running container list
	clist := getRunningContainersWithProfile(d, name)
	var containers []container
	for _, c := range clist {
		if !c.IsRunning() {
			continue
		}

		containers = append(containers, c)
	}

	// Update the database
	tx, err := dbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}

	if profile.Description != req.Description {
		err = dbProfileDescriptionUpdate(tx, id, req.Description)
		if err != nil {
			tx.Rollback()
			return InternalError(err)
		}
	}

	// Optimize for description-only changes
	if reflect.DeepEqual(profile.Config, req.Config) && reflect.DeepEqual(profile.Devices, req.Devices) {
		err = txCommit(tx)
		if err != nil {
			return InternalError(err)
		}

		return EmptySyncResponse
	}

	err = dbProfileConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	err = dbProfileConfigAdd(tx, id, req.Config)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = dbDevicesAdd(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = txCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	// Update all the containers using the profile. Must be done after txCommit due to DB lock.
	failures := map[string]error{}
	for _, c := range containers {
		err = c.Update(containerArgs{
			Architecture: c.Architecture(),
			Ephemeral:    c.IsEphemeral(),
			Config:       c.LocalConfig(),
			Devices:      c.LocalDevices(),
			Profiles:     c.Profiles()}, true)

		if err != nil {
			failures[c.Name()] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following containers failed to update (profile change still saved):\n"
		for cname, err := range failures {
			msg += fmt.Sprintf(" - %s: %s\n", cname, err)
		}
		return InternalError(fmt.Errorf("%s", msg))
	}

	return EmptySyncResponse
}

// The handler for the post operation.
func profilePost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	req := profilesPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	// Check that the name isn't already in use
	id, _, _ := dbProfileGet(d.db, req.Name)
	if id > 0 {
		return Conflict
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Profile names may not contain slashes"))
	}

	if shared.StringInSlice(req.Name, []string{".", ".."}) {
		return BadRequest(fmt.Errorf("Invalid profile name '%s'", req.Name))
	}

	err := dbProfileUpdate(d.db, name, req.Name)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/profiles/%s", shared.APIVersion, req.Name))
}

// The handler for the delete operation.
func profileDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	_, err := doProfileGet(d, name)
	if err != nil {
		return SmartError(err)
	}

	clist := getRunningContainersWithProfile(d, name)
	if len(clist) != 0 {
		return BadRequest(fmt.Errorf("Profile is currently in use"))
	}

	err = dbProfileDelete(d.db, name)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var profileCmd = Command{name: "profiles/{name}", get: profileGet, put: profilePut, delete: profileDelete, post: profilePost, patch: profilePatch}
