package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
	_ "github.com/stgraber/lxd-go-sqlite3"
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
	rows, err := shared.DbQuery(d.db, q)
	if err != nil {
		return SmartError(err)
	}
	defer rows.Close()

	result := []string{}
	for rows.Next() {
		name := ""
		err = rows.Scan(&name)
		if err != nil {
			fmt.Printf("DBERR: profilesGet: scan returned error %q\n", err)
			return InternalError(err)
		}

		result = append(result, fmt.Sprintf("/%s/profiles/%s", shared.APIVersion, name))
	}
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: profilesGet: Err returned an error %q\n", err)
		return InternalError(err)
	}

	return SyncResponse(true, result)
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

	tx, err := d.db.Begin()
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

	err = shared.AddDevices(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = shared.TxCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var profilesCmd = Command{name: "profiles", get: profilesGet, post: profilesPost}

func profileGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	config, err := dbGetProfileConfig(d, name)
	if err != nil {
		return SmartError(err)
	}

	devices, err := dbGetDevices(d, name, true)
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

func profilePut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	req := profilesPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	tx, err := d.db.Begin()
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
			fmt.Printf("DBERR: profilePut: scan returned error %q\n", err)
			tx.Rollback()
			return InternalError(err)
		}
		id = i
	}
	rows.Close()
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: profilePut: Err returned an error %q\n", err)
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

	err = shared.AddDevices(tx, "profile", id, req.Devices)
	if err != nil {
		tx.Rollback()
		return SmartError(err)
	}

	err = shared.TxCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func profileDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	tx, err := d.db.Begin()
	if err != nil {
		return InternalError(err)
	}
	_, err = tx.Exec(`DELETE FROM profiles_config
			WHERE id IN (SELECT profiles_config.id FROM
			profiles_config JOIN profiles ON profiles_config.profile_id=profiles.id
			WHERE profiles.name=?)`, name)
	if err != nil {
		tx.Rollback()
		shared.Debugf("Error deleting profile %s: %s\n", name, err)
		return InternalError(err)
	}

	_, err = tx.Exec("DELETE FROM profiles WHERE name=?", name)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	err = shared.TxCommit(tx)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var profileCmd = Command{name: "profiles/{name}", get: profileGet, put: profilePut, delete: profileDelete}
