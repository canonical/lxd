// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
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
	case 5:
		return "usb", nil
	case 6:
		return "gpu", nil
	case 7:
		return "infiniband", nil
	case 8:
		return "proxy", nil
	case 9:
		return "unix-hotplug", nil
	default:
		return "", fmt.Errorf("Invalid device type %d", t)
	}
}

func dbDeviceTypeToInt(t string) (int, error) {
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
	case "usb":
		return 5, nil
	case "gpu":
		return 6, nil
	case "infiniband":
		return 7, nil
	case "proxy":
		return 8, nil
	case "unix-hotplug":
		return 9, nil
	default:
		return -1, fmt.Errorf("Invalid device type %s", t)
	}
}

// AddDevicesToEntity adds the given devices to the entity of the given type with the
// given ID.
func AddDevicesToEntity(tx *sql.Tx, w string, cID int64, devices deviceConfig.Devices) error {
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
		t, err := dbDeviceTypeToInt(v["type"])
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
			// The type is stored as int in the parent entry
			if ck == "type" || cv == "" {
				continue
			}

			_, err = stmt2.Exec(id, ck, cv)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func dbDeviceConfig(db *sql.DB, id int, isprofile bool) (deviceConfig.Device, error) {
	var query string
	var key, value string
	newdev := deviceConfig.Device{} // That's a map[string]string
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}

	if isprofile {
		query = `SELECT key, value FROM profiles_devices_config WHERE profile_device_id=?`
	} else {
		query = `SELECT key, value FROM instances_devices_config WHERE instance_device_id=?`
	}

	results, err := queryScan(db, query, inargs, outfmt)

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
