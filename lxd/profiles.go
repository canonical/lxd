package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* This is used for both profiles post and profile put */
type profilesPostReq struct {
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Devices shared.Devices    `json:"devices"`
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
				shared.Log.Error("Failed to get profile", log.Ctx{"profile": name})
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

	err := containerValidConfig(req.Config, true, false)
	if err != nil {
		return BadRequest(err)
	}

	err = containerValidDevices(req.Devices, true, false)
	if err != nil {
		return BadRequest(err)
	}

	// Update DB entry
	_, err = dbProfileCreate(d.db, req.Name, req.Config, req.Devices)
	if err != nil {
		return InternalError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	return EmptySyncResponse
}

var profilesCmd = Command{
	name: "profiles",
	get:  profilesGet,
	post: profilesPost}

func doProfileGet(d *Daemon, name string) (*shared.ProfileConfig, error) {
	config, err := dbProfileConfig(d.db, name)
	if err != nil {
		return nil, err
	}

	devices, err := dbDevices(d.db, name, true)
	if err != nil {
		return nil, err
	}

	return &shared.ProfileConfig{
		Name:    name,
		Config:  config,
		Devices: devices,
	}, nil
}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	resp, err := doProfileGet(d, name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, resp)
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
			shared.Log.Error("Failed opening container", log.Ctx{"container": name})
			continue
		}
		results = append(results, c)
	}
	return results
}

func profilePut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	req := profilesPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	err := containerValidConfig(req.Config, true, false)
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
	id, err := dbProfileID(d.db, name)
	if err != nil {
		return InternalError(fmt.Errorf("Failed to retrieve profile='%s'", name))
	}

	tx, err := dbBegin(d.db)
	if err != nil {
		return InternalError(err)
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

// The handler for the delete operation.
func profileDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	err := dbProfileDelete(d.db, name)

	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var profileCmd = Command{name: "profiles/{name}", get: profileGet, put: profilePut, delete: profileDelete}
