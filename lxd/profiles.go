package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

func addProfileConfig(tx *sql.Tx, id int, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO profiles_config (profile_id, key, value) VALUES(?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()

	for k, v := range config {
		if !ValidContainerConfigKey(k) {
			return fmt.Errorf("Bad key: %s\n", k)
		}
		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

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

	name := req.Name

	tx, err := dbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}
	result, err := tx.Exec("INSERT INTO profiles (name) VALUES (?)", name)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}
	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return InternalError(fmt.Errorf("Error inserting %s into database", name))
	}
	id := int(id64)

	err = addProfileConfig(tx, id, req.Config)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = AddDevices(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = txCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var profilesCmd = Command{name: "profiles", get: profilesGet, post: profilesPost}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	config, err := dbGetProfileConfig(d.db, name)
	if err != nil {
		return SmartError(err)
	}

	devices, err := dbGetDevices(d.db, name, true)
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

func dbClearProfileConfig(tx *sql.Tx, id int) error {
	_, err := tx.Exec("DELETE FROM profiles_config WHERE profile_id=?", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DELETE FROM profiles_devices_config WHERE id IN
		(SELECT profiles_devices_config.id
		 FROM profiles_devices_config JOIN profiles_devices
		 ON profiles_devices_config.profile_device_id=profiles_devices.id
		 WHERE profiles_devices.profile_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM profiles_devices WHERE profile_id=?", id)
	if err != nil {
		return err
	}
	return nil
}

func getRunningContainersWithProfile(d *Daemon, profile string) []*lxdContainer {
	q := `SELECT containers.name FROM containers JOIN containers_profiles
		ON containers.id == containers_profiles.container_id
		JOIN profiles ON containers_profiles.profile_id == profiles.id
		WHERE profiles.name == ?`
	results := []*lxdContainer{}
	inargs := []interface{}{profile}
	var name string
	outfmt := []interface{}{name}

	output, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return results
	}
	for _, r := range output {
		name := r[0].(string)
		c, err := newLxdContainer(name, d)
		if err != nil {
			shared.Debugf("ERROR: failed opening container %s\n", name)
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

	preDevList, err := dbGetDevices(d.db, name, true)
	if err != nil {
		return InternalError(err)
	}
	clist := getRunningContainersWithProfile(d, name)

	tx, err := dbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}

	rows, err := tx.Query("SELECT id FROM profiles WHERE name=?", name)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}
	var id int
	for rows.Next() {
		var i int
		err = rows.Scan(&i)
		if err != nil {
			shared.Debugf("DBERR: profilePut: scan returned error %q\n", err)
			tx.Rollback()
			return InternalError(err)
		}
		id = i
	}
	rows.Close()
	err = rows.Err()
	if err != nil {
		shared.Debugf("DBERR: profilePut: Err returned an error %q\n", err)
		tx.Rollback()
		return InternalError(err)
	}

	err = dbClearProfileConfig(tx, id)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	err = addProfileConfig(tx, id, req.Config)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = AddDevices(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	postDevList := req.Devices
	// do our best to update the device list for each container using
	// this profile
	for _, c := range clist {
		if !c.c.Running() {
			continue
		}
		fmt.Printf("Updating profile device list for %s\n", c.name)
		if err := devicesApplyDeltaLive(tx, c, preDevList, postDevList); err != nil {
			shared.Debugf("Warning: failed to update device list for container %s (profile %s updated)\n", c.name, name)
		}
	}

	err = txCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func dbProfileDelete(db *sql.DB, name string) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM profiles WHERE name=?", name)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = txCommit(tx)

	return err
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
