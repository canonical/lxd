package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared"
)

type containerType int

const (
	cTypeRegular  containerType = 0
	cTypeSnapshot containerType = 1
)

func dbRemoveContainer(d *Daemon, name string) error {
	_, err := shared.DbExec(d.db, "DELETE FROM containers WHERE name=?", name)
	return err
}

func dbGetContainerId(db *sql.DB, name string) (int, error) {
	q := "SELECT id FROM containers WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := shared.DbQueryRowScan(db, q, arg1, arg2)
	return id, err
}

type DbCreateContainerArgs struct {
	d            *Daemon
	name         string
	ctype        containerType
	config       map[string]string
	profiles     []string
	ephem        bool
	baseImage    string
	architecture int
}

func dbCreateContainer(args DbCreateContainerArgs) (int, error) {
	id, err := dbGetContainerId(args.d.db, args.name)
	if err == nil {
		return 0, DbErrAlreadyDefined
	}

	if args.profiles == nil {
		args.profiles = []string{"default"}
	}

	if args.baseImage != "" {
		if args.config == nil {
			args.config = map[string]string{}
		}

		args.config["volatile.baseImage"] = args.baseImage
	}

	tx, err := shared.DbBegin(args.d.db)
	if err != nil {
		return 0, err
	}
	ephem_int := 0
	if args.ephem == true {
		ephem_int = 1
	}

	str := fmt.Sprintf("INSERT INTO containers (name, architecture, type, ephemeral) VALUES (?, ?, ?, ?)")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	result, err := stmt.Exec(args.name, args.architecture, args.ctype, ephem_int)
	if err != nil {
		tx.Rollback()
		return 0, err
	}

	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("Error inserting %s into database", args.name)
	}
	// TODO: is this really int64? we should fix it everywhere if so
	id = int(id64)
	if err := dbInsertContainerConfig(tx, id, args.config); err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := dbInsertProfiles(tx, id, args.profiles); err != nil {
		tx.Rollback()
		return 0, err
	}

	return id, shared.TxCommit(tx)
}

func dbClearContainerConfig(tx *sql.Tx, id int) error {
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

func dbInsertContainerConfig(tx *sql.Tx, id int, config map[string]string) error {
	str := "INSERT INTO containers_config (container_id, key, value) values (?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		if k == "raw.lxc" {
			err := validateRawLxc(config["raw.lxc"])
			if err != nil {
				return err
			}
		}

		if !ValidContainerConfigKey(k) {
			return fmt.Errorf("Bad key: %s\n", k)
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			shared.Debugf("Error adding configuration item %s = %s to container %d\n",
				k, v, id)
			return err
		}
	}

	return nil
}

func dbInsertProfiles(tx *sql.Tx, id int, profiles []string) error {
	apply_order := 1
	str := `INSERT INTO containers_profiles (container_id, profile_id, apply_order) VALUES
		(?, (SELECT id FROM profiles WHERE name=?), ?);`
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range profiles {
		_, err = stmt.Exec(id, p, apply_order)
		if err != nil {
			shared.Debugf("Error adding profile %s to container: %s\n",
				p, err)
			return err
		}
		apply_order = apply_order + 1
	}

	return nil
}

func dbRemoveSnapshot(d *Daemon, cname string, sname string) {
	name := fmt.Sprintf("%s/%s", cname, sname)
	_, _ = shared.DbExec(d.db, "DELETE FROM containers WHERE type=? AND name=?", cTypeSnapshot, name)
}

func ValidContainerConfigKey(k string) bool {
	switch k {
	case "limits.cpus":
		return true
	case "limits.memory":
		return true
	case "security.privileged":
		return true
	case "raw.apparmor":
		return true
	case "raw.lxc":
		return true
	case "volatile.baseImage":
		return true
	}

	if _, err := ExtractInterfaceFromConfigName(k); err == nil {
		return true
	}

	return strings.HasPrefix(k, "user.")
}
