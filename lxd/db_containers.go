package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type containerType int

const (
	cTypeRegular  containerType = 0
	cTypeSnapshot containerType = 1
)

func dbContainerRemove(db *sql.DB, name string) error {
	id, err := dbContainerId(db, name)
	if err != nil {
		return err
	}

	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	err = dbContainerConfigClear(tx, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	_, err = tx.Exec("DELETE FROM containers WHERE id=?", id)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

func dbContainerName(db *sql.DB, id int) (string, error) {
	q := "SELECT name FROM containers WHERE id=?"
	name := ""
	arg1 := []interface{}{id}
	arg2 := []interface{}{&name}
	err := dbQueryRowScan(db, q, arg1, arg2)
	return name, err
}

func dbContainerId(db *sql.DB, name string) (int, error) {
	q := "SELECT id FROM containers WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := dbQueryRowScan(db, q, arg1, arg2)
	return id, err
}

func dbContainerGet(db *sql.DB, name string) (containerArgs, error) {
	var used *time.Time // Hold the db-returned time

	args := containerArgs{}
	args.Name = name

	ephemInt := -1
	statefulInt := -1
	q := "SELECT id, architecture, type, ephemeral, stateful, creation_date, last_use_date FROM containers WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&args.Id, &args.Architecture, &args.Ctype, &ephemInt, &statefulInt, &args.CreationDate, &used}
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		return args, err
	}

	if args.Id == -1 {
		return args, fmt.Errorf("Unknown container")
	}

	if ephemInt == 1 {
		args.Ephemeral = true
	}

	if statefulInt == 1 {
		args.Stateful = true
	}

	if used != nil {
		args.LastUsedDate = *used
	} else {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	config, err := dbContainerConfig(db, args.Id)
	if err != nil {
		return args, err
	}
	args.Config = config

	profiles, err := dbContainerProfiles(db, args.Id)
	if err != nil {
		return args, err
	}
	args.Profiles = profiles

	/* get container_devices */
	args.Devices = shared.Devices{}
	newdevs, err := dbDevices(db, name, false)
	if err != nil {
		return args, err
	}

	for k, v := range newdevs {
		args.Devices[k] = v
	}

	return args, nil
}

func dbContainerCreate(db *sql.DB, args containerArgs) (int, error) {
	id, err := dbContainerId(db, args.Name)
	if err == nil {
		return 0, util.DbErrAlreadyDefined
	}

	tx, err := dbBegin(db)
	if err != nil {
		return 0, err
	}

	ephemInt := 0
	if args.Ephemeral == true {
		ephemInt = 1
	}

	statefulInt := 0
	if args.Stateful == true {
		statefulInt = 1
	}

	args.CreationDate = time.Now().UTC()
	args.LastUsedDate = time.Unix(0, 0).UTC()

	str := fmt.Sprintf("INSERT INTO containers (name, architecture, type, ephemeral, creation_date, last_use_date, stateful) VALUES (?, ?, ?, ?, ?, ?, ?)")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	result, err := stmt.Exec(args.Name, args.Architecture, args.Ctype, ephemInt, args.CreationDate.Unix(), args.LastUsedDate.Unix(), statefulInt)
	if err != nil {
		tx.Rollback()
		return 0, err
	}

	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("Error inserting %s into database", args.Name)
	}
	// TODO: is this really int64? we should fix it everywhere if so
	id = int(id64)
	if err := dbContainerConfigInsert(tx, id, args.Config); err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := dbContainerProfilesInsert(tx, id, args.Profiles); err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := dbDevicesAdd(tx, "container", int64(id), args.Devices); err != nil {
		tx.Rollback()
		return 0, err
	}

	return id, txCommit(tx)
}

func dbContainerConfigClear(tx *sql.Tx, id int) error {
	_, err := tx.Exec("DELETE FROM containers_config WHERE container_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM containers_profiles WHERE container_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM containers_devices_config WHERE id IN
		(SELECT containers_devices_config.id
		 FROM containers_devices_config JOIN containers_devices
		 ON containers_devices_config.container_device_id=containers_devices.id
		 WHERE containers_devices.container_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM containers_devices WHERE container_id=?", id)
	return err
}

func dbContainerConfigInsert(tx *sql.Tx, id int, config map[string]string) error {
	str := "INSERT INTO containers_config (container_id, key, value) values (?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		_, err := stmt.Exec(id, k, v)
		if err != nil {
			shared.LogDebugf("Error adding configuration item %s = %s to container %d",
				k, v, id)
			return err
		}
	}

	return nil
}

func dbContainerConfigGet(db *sql.DB, id int, key string) (string, error) {
	q := "SELECT value FROM containers_config WHERE container_id=? AND key=?"
	value := ""
	arg1 := []interface{}{id, key}
	arg2 := []interface{}{&value}
	err := dbQueryRowScan(db, q, arg1, arg2)
	return value, err
}

func dbContainerConfigRemove(db *sql.DB, id int, name string) error {
	_, err := dbExec(db, "DELETE FROM containers_config WHERE key=? AND container_id=?", name, id)
	return err
}

func dbContainerSetStateful(db *sql.DB, id int, stateful bool) error {
	statefulInt := 0
	if stateful {
		statefulInt = 1
	}

	_, err := dbExec(db, "UPDATE containers SET stateful=? WHERE id=?", statefulInt, id)
	return err
}

func dbContainerProfilesInsert(tx *sql.Tx, id int, profiles []string) error {
	applyOrder := 1
	str := `INSERT INTO containers_profiles (container_id, profile_id, apply_order) VALUES
		(?, (SELECT id FROM profiles WHERE name=?), ?);`
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range profiles {
		_, err = stmt.Exec(id, p, applyOrder)
		if err != nil {
			shared.LogDebugf("Error adding profile %s to container: %s",
				p, err)
			return err
		}
		applyOrder = applyOrder + 1
	}

	return nil
}

// Get a list of profiles for a given container id.
func dbContainerProfiles(db *sql.DB, containerId int) ([]string, error) {
	var name string
	var profiles []string

	query := `
        SELECT name FROM containers_profiles
        JOIN profiles ON containers_profiles.profile_id=profiles.id
		WHERE container_id=?
        ORDER BY containers_profiles.apply_order`
	inargs := []interface{}{containerId}
	outfmt := []interface{}{name}

	results, err := dbQueryScan(db, query, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range results {
		name = r[0].(string)

		profiles = append(profiles, name)
	}

	return profiles, nil
}

// dbContainerConfig gets the container configuration map from the DB
func dbContainerConfig(db *sql.DB, containerId int) (map[string]string, error) {
	var key, value string
	q := `SELECT key, value FROM containers_config WHERE container_id=?`

	inargs := []interface{}{containerId}
	outfmt := []interface{}{key, value}

	// Results is already a slice here, not db Rows anymore.
	results, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return nil, err //response.SmartError will wrap this and make "not found" errors pretty
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

func dbContainersList(db *sql.DB, cType containerType) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? ORDER BY name")
	inargs := []interface{}{cType}
	var container string
	outfmt := []interface{}{container}
	result, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	var ret []string
	for _, container := range result {
		ret = append(ret, container[0].(string))
	}

	return ret, nil
}

func dbContainerSetState(db *sql.DB, id int, state string) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	// Clear any existing entry
	str := fmt.Sprintf("DELETE FROM containers_config WHERE container_id = ? AND key = 'volatile.last_state.power'")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	if _, err := stmt.Exec(id); err != nil {
		tx.Rollback()
		return err
	}

	// Insert the new one
	str = fmt.Sprintf("INSERT INTO containers_config (container_id, key, value) VALUES (?, 'volatile.last_state.power', ?)")
	stmt, err = tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	if _, err = stmt.Exec(id, state); err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

func dbContainerRename(db *sql.DB, oldName string, newName string) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	str := fmt.Sprintf("UPDATE containers SET name = ? WHERE name = ?")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	shared.LogDebug(
		"Calling SQL Query",
		log.Ctx{
			"query":   "UPDATE containers SET name = ? WHERE name = ?",
			"oldName": oldName,
			"newName": newName})
	if _, err := stmt.Exec(newName, oldName); err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

func dbContainerUpdate(tx *sql.Tx, id int, architecture int, ephemeral bool) error {
	str := fmt.Sprintf("UPDATE containers SET architecture=?, ephemeral=? WHERE id=?")
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ephemeralInt := 0
	if ephemeral {
		ephemeralInt = 1
	}

	if _, err := stmt.Exec(architecture, ephemeralInt, id); err != nil {
		return err
	}

	return nil
}

func dbContainerLastUsedUpdate(db *sql.DB, id int, date time.Time) error {
	stmt := `UPDATE containers SET last_use_date=? WHERE id=?`
	_, err := dbExec(db, stmt, date, id)
	return err
}

func dbContainerGetSnapshots(db *sql.DB, name string) ([]string, error) {
	result := []string{}

	regexp := name + shared.SnapshotDelimiter
	length := len(regexp)
	q := "SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?"
	inargs := []interface{}{cTypeSnapshot, length, regexp}
	outfmt := []interface{}{name}
	dbResults, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		result = append(result, r[0].(string))
	}

	return result, nil
}
