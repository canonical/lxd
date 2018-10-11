package db

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/lxd/types"
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
	default:
		return -1, fmt.Errorf("Invalid device type %s", t)
	}
}

// DevicesAdd adds a new device.
func DevicesAdd(tx *sql.Tx, w string, cID int64, devices types.Devices) error {
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
			if ck == "type" {
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

func dbDeviceConfig(db *sql.DB, id int, isprofile bool) (types.Device, error) {
	var query string
	var key, value string
	newdev := types.Device{} // That's a map[string]string
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}

	if isprofile {
		query = `SELECT key, value FROM profiles_devices_config WHERE profile_device_id=?`
	} else {
		query = `SELECT key, value FROM containers_devices_config WHERE container_device_id=?`
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

// Devices returns the devices matching the given filters.
func (c *Cluster) Devices(project, qName string, isprofile bool) (types.Devices, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return err
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var q string
	if isprofile {
		q = `SELECT profiles_devices.id, profiles_devices.name, profiles_devices.type
			FROM profiles_devices
                        JOIN profiles ON profiles_devices.profile_id = profiles.id
                        JOIN projects ON projects.id=profiles.project_id
   		WHERE projects.name=? AND profiles.name=?`
	} else {
		q = `SELECT containers_devices.id, containers_devices.name, containers_devices.type
			FROM containers_devices
                        JOIN containers	ON containers_devices.container_id = containers.id
                        JOIN projects ON projects.id=containers.project_id
			WHERE projects.name=? AND containers.name=?`
	}
	var id, dtype int
	var name, stype string
	inargs := []interface{}{project, qName}
	outfmt := []interface{}{id, name, dtype}
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	devices := types.Devices{}
	for _, r := range results {
		id = r[0].(int)
		name = r[1].(string)
		stype, err = dbDeviceTypeToString(r[2].(int))
		if err != nil {
			return nil, err
		}
		newdev, err := dbDeviceConfig(c.db, id, isprofile)
		if err != nil {
			return nil, err
		}
		newdev["type"] = stype
		devices[name] = newdev
	}

	return devices, nil
}
