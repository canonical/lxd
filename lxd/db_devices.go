package main

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

func dbDeviceTypeToString(t int) (string, error) {
	switch t {
	case 0:
		return "none", nil
	case 1:
		return "nic", nil
	case 2:
		return "disk", nil
	case 3:
		return "unix-char", nil
	case 4:
		return "unix-block", nil
	default:
		return "", fmt.Errorf("Invalid device type %d", t)
	}
}

func deviceTypeToDbType(t string) (int, error) {
	switch t {
	case "none":
		return 0, nil
	case "nic":
		return 1, nil
	case "disk":
		return 2, nil
	case "unix-char":
		return 3, nil
	case "unix-block":
		return 4, nil
	default:
		return -1, fmt.Errorf("Invalid device type %s", t)
	}
}

func validDeviceConfigKey(t, k string) bool {
	if k == "type" {
		return true
	}

	switch t {
	case "unix-char":
		switch k {
		case "path":
			return true
		case "major":
			return true
		case "minor":
			return true
		case "uid":
			return true
		case "gid":
			return true
		case "mode":
			return true
		default:
			return false
		}
	case "unix-block":
		switch k {
		case "path":
			return true
		case "major":
			return true
		case "minor":
			return true
		case "uid":
			return true
		case "gid":
			return true
		case "mode":
			return true
		default:
			return false
		}
	case "nic":
		switch k {
		case "parent":
			return true
		case "name":
			return true
		case "hwaddr":
			return true
		case "mtu":
			return true
		case "nictype":
			return true
		default:
			return false
		}
	case "disk":
		switch k {
		case "path":
			return true
		case "source":
			return true
		case "readonly":
			return true
		case "optional":
			return true
		default:
			return false
		}
	case "none":
		return false
	default:
		return false
	}
}

func validateDevices(devices shared.Devices) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	// Check each device individually
	for _, m := range devices {
		for k, _ := range m {
			if !validDeviceConfigKey(m["type"], k) {
				return fmt.Errorf("Invalid device configuration key for %s: %s", m["type"], k)
			}
		}

		if m["type"] == "nic" {
			if m["nictype"] == "" {
				return fmt.Errorf("Missing nic type")
			}

			if !shared.StringInSlice(m["nictype"], []string{"bridged", "physical", "p2p", "macvlan"}) {
				return fmt.Errorf("Bad nic type: %s", m["nictype"])
			}

			if shared.StringInSlice(m["nictype"], []string{"bridged", "physical", "macvlan"}) && m["parent"] == "" {
				return fmt.Errorf("Missing parent for %s type nic.", m["nictype"])
			}
		} else if m["type"] == "disk" {
			if m["path"] == "" {
				return fmt.Errorf("Disk entry is missing the required \"path\" property.")
			}

			if m["source"] == "" && m["path"] != "/" {
				return fmt.Errorf("Disk entry is missing the required \"source\" property.")
			}
		} else if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
			if m["path"] == "" {
				return fmt.Errorf("Unix device entry is missing the required \"path\" property.")
			}
		} else if m["type"] == "none" {
			continue
		} else {
			return fmt.Errorf("Invalid device type: %s", m["type"])
		}
	}

	return nil
}

func dbDevicesAdd(tx *sql.Tx, w string, cID int64, devices shared.Devices) error {
	// Validate everything ahead of time
	err := validateDevices(devices)
	if err != nil {
		return err
	}

	// Prepare the devices entry SQL
	str1 := fmt.Sprintf("INSERT INTO %ss_devices (%s_id, name, type) VALUES (?, ?, ?)", w, w)
	stmt1, err := tx.Prepare(str1)
	if err != nil {
		return err
	}
	defer stmt1.Close()

	// Prepare the devices config entry SQL
	str2 := fmt.Sprintf("INSERT INTO %ss_devices_config (%s_device_id, key, value) VALUES (?, ?, ?)", w, w)
	stmt2, err := tx.Prepare(str2)
	if err != nil {
		return err
	}
	defer stmt2.Close()

	// Insert all the devices
	for k, v := range devices {
		t, err := deviceTypeToDbType(v["type"])
		if err != nil {
			return err
		}

		result, err := stmt1.Exec(cID, k, t)
		if err != nil {
			return err
		}

		id64, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("Error inserting device %s into database", k)
		}
		id := int(id64)

		for ck, cv := range v {
			_, err = stmt2.Exec(id, ck, cv)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func dbDeviceConfig(db *sql.DB, id int, isprofile bool) (shared.Device, error) {
	var query string
	var key, value string
	newdev := shared.Device{} // That's a map[string]string
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}

	if isprofile {
		query = `SELECT key, value FROM profiles_devices_config WHERE profile_device_id=?`
	} else {
		query = `SELECT key, value FROM containers_devices_config WHERE container_device_id=?`
	}

	results, err := dbQueryScan(db, query, inargs, outfmt)

	if err != nil {
		return newdev, err
	}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)
		newdev[key] = value
	}

	return newdev, nil
}

func dbDevices(db *sql.DB, qName string, isprofile bool) (shared.Devices, error) {
	var q string
	if isprofile {
		q = `SELECT profiles_devices.id, profiles_devices.name, profiles_devices.type
			FROM profiles_devices JOIN profiles
			ON profiles_devices.profile_id = profiles.id
   		WHERE profiles.name=?`
	} else {
		q = `SELECT containers_devices.id, containers_devices.name, containers_devices.type
			FROM containers_devices JOIN containers
			ON containers_devices.container_id = containers.id
			WHERE containers.name=?`
	}
	var id, dtype int
	var name, stype string
	inargs := []interface{}{qName}
	outfmt := []interface{}{id, name, dtype}
	results, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	devices := shared.Devices{}
	for _, r := range results {
		id = r[0].(int)
		name = r[1].(string)
		stype, err = dbDeviceTypeToString(r[2].(int))
		if err != nil {
			return nil, err
		}
		newdev, err := dbDeviceConfig(db, id, isprofile)
		if err != nil {
			return nil, err
		}
		newdev["type"] = stype
		devices[name] = newdev
	}

	return devices, nil
}
