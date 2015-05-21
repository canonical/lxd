package main

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
)

func DeviceToLxc(d shared.Device) ([][]string, error) {
	switch d["type"] {
	case "unix-char":
		return nil, fmt.Errorf("Not implemented")
	case "unix-block":
		return nil, fmt.Errorf("Not implemented")
	case "nic":
		if d["nictype"] != "bridged" && d["nictype"] != "" {
			return nil, fmt.Errorf("Bad nic type: %s\n", d["nictype"])
		}
		var l1 = []string{"lxc.network.type", "veth"}
		var lines = [][]string{l1}
		var l2 []string
		if d["hwaddr"] != "" {
			l2 = []string{"lxc.network.hwaddr", d["hwaddr"]}
			lines = append(lines, l2)
		}
		if d["mtu"] != "" {
			l2 = []string{"lxc.network.mtu", d["mtu"]}
			lines = append(lines, l2)
		}
		if d["parent"] != "" {
			l2 = []string{"lxc.network.link", d["parent"]}
			lines = append(lines, l2)
		}
		if d["name"] != "" {
			l2 = []string{"lxc.network.name", d["name"]}
			lines = append(lines, l2)
		}
		return lines, nil
	case "disk":
		var p string
		if d["path"] == "/" || d["path"] == "" {
			p = ""
		} else if d["path"][0:1] == "/" {
			p = d["path"][1:]
		} else {
			p = d["path"]
		}
		/* TODO - check whether source is a disk, loopback, btrfs subvol, etc */
		/* for now we only handle directory bind mounts */
		source := d["source"]
		opts := "bind"
		if shared.IsDir(source) {
			opts = fmt.Sprintf("%s,create=dir", opts)
		} else {
			opts = fmt.Sprintf("%s,create=file", opts)
		}
		if d["readonly"] == "1" || d["readonly"] == "true" {
			opts = fmt.Sprintf("%s,ro", opts)
		}
		if d["optional"] == "1" || d["optional"] == "true" {
			opts = fmt.Sprintf("%s,optional", opts)
		}
		l := []string{"lxc.mount.entry", fmt.Sprintf("%s %s none %s 0 0", source, p, opts)}
		return [][]string{l}, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("Bad device type")
	}
}

func ValidDeviceType(t string) bool {
	switch t {
	case "unix-char":
		return true
	case "unix-block":
		return true
	case "nic":
		return true
	case "disk":
		return true
	case "none":
		return true
	default:
		return false
	}
}

func ValidDeviceConfig(t, k, v string) bool {
	if k == "type" {
		return false
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
			if v != "bridged" && v != "" {
				return false
			}
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
		case "readonly", "optional":
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

func AddDevices(tx *sql.Tx, w string, cId int, devices shared.Devices) error {
	str1 := fmt.Sprintf("INSERT INTO %ss_devices (%s_id, name, type) VALUES (?, ?, ?)", w, w)
	stmt1, err := tx.Prepare(str1)
	if err != nil {
		return err
	}
	defer stmt1.Close()
	str2 := fmt.Sprintf("INSERT INTO %ss_devices_config (%s_device_id, key, value) VALUES (?, ?, ?)", w, w)
	stmt2, err := tx.Prepare(str2)
	if err != nil {
		return err
	}
	defer stmt2.Close()
	for k, v := range devices {
		if !ValidDeviceType(v["type"]) {
			return fmt.Errorf("Invalid device type %s\n", v["type"])
		}
		result, err := stmt1.Exec(cId, k, v["type"])
		if err != nil {
			return err
		}
		id64, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("Error inserting device %s into database", k)
		}
		// TODO: is this really int64? we should fix it everywhere if so
		id := int(id64)
		for ck, cv := range v {
			if ck == "type" {
				continue
			}
			if !ValidDeviceConfig(v["type"], ck, cv) {
				return fmt.Errorf("Invalid device config %s %s\n", ck, cv)
			}
			_, err = stmt2.Exec(id, ck, cv)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
