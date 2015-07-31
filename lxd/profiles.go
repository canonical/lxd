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
	q := fmt.Sprintf("SELECT name FROM profiles")
	inargs := []interface{}{}
	var name string
	outfmt := []interface{}{name}
	result, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return SmartError(err)
	}
	response := []string{}
	for _, r := range result {
		name := r[0].(string)
		url := fmt.Sprintf("/%s/profiles/%s", shared.APIVersion, name)
		response = append(response, url)
	}

	return SyncResponse(true, response)
}

func profilesPost(d *Daemon, r *http.Request) Response {
	req := profilesPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	_, err := dbProfileCreate(d.db, req.Name, req.Config, req.Devices)
	if err != nil {
		return InternalError(
			fmt.Errorf("Error inserting %s into database", req.Name))
	}

	return EmptySyncResponse
}

var profilesCmd = Command{name: "profiles", get: profilesGet, post: profilesPost}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	config, err := dbProfileConfigGet(d.db, name)
	if err != nil {
		return SmartError(err)
	}

	devices, err := dbDevicesGet(d.db, name, true)
	if err != nil {
		return SmartError(err)
	}

	resp := &shared.ProfileConfig{
		Name:    name,
		Config:  config,
		Devices: devices,
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
		c, err := containerLXDLoad(d, name)
		if err != nil {
			shared.Log.Error("failed opening container", log.Ctx{"container": name})
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

	preDevList, err := dbDevicesGet(d.db, name, true)
	if err != nil {
		return InternalError(err)
	}
	clist := getRunningContainersWithProfile(d, name)

	id, err := dbProfileIDGet(d.db, name)
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

	postDevList := req.Devices
	// do our best to update the device list for each container using
	// this profile
	for _, c := range clist {
		if !c.IsRunning() {
			continue
		}
		fmt.Printf("Updating profile device list for %s\n", c.NameGet())
		if err := devicesApplyDeltaLive(tx, c, preDevList, postDevList); err != nil {
			shared.Debugf("Warning: failed to update device list for container %s (profile %s updated)\n", c.NameGet(), name)
		}
	}

	err = txCommit(tx)
	if err != nil {
		return InternalError(err)
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
