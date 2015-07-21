package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os/exec"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"
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

func dbDeviceTypeToString(t int) (string, error) {
	switch t {
	case 0:
		return "disk", nil
	case 1:
		return "nic", nil
	case 2:
		return "unix-char", nil
	case 3:
		return "unix-block", nil
	case 4:
		return "none", nil
	default:
		return "", fmt.Errorf("Invalid device type %d\n", t)
	}
}

func DeviceTypeToDbType(t string) (int, error) {
	switch t {
	case "disk":
		return 0, nil
	case "nic":
		return 1, nil
	case "unix-char":
		return 2, nil
	case "unix-block":
		return 3, nil
	case "none":
		return 4, nil
	default:
		return -1, fmt.Errorf("Invalid device type %s\n", t)
	}
}

func ValidDeviceType(t string) bool {
	_, err := DeviceTypeToDbType(t)
	return err == nil
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

func AddDevices(tx *sql.Tx, w string, cID int, devices shared.Devices) error {
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
		t, err := DeviceTypeToDbType(v["type"])
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

func tempNic() string {
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	return "lxd" + hex.EncodeToString(randBytes)
}

func inList(l []string, s string) bool {
	for _, ls := range l {
		if ls == s {
			return true
		}
	}
	return false
}

func nextUnusedNic(c *lxdContainer) string {
	list, err := c.c.Interfaces()
	if err != nil || len(list) == 0 {
		return "eth0"
	}
	i := 0
	// is it worth sorting list?
	for {
		nic := fmt.Sprintf("eth%d", i)
		if !inList(list, nic) {
			return nic
		}
		i = i + 1
	}
}

func setupNic(c *lxdContainer, d map[string]string) (string, error) {
	if d["nictype"] != "bridged" {
		return "", fmt.Errorf("Unsupported nic type: %s\n", d["nictype"])
	}
	if d["parent"] == "" {
		return "", fmt.Errorf("No bridge given\n")
	}
	if d["name"] == "" {
		d["name"] = nextUnusedNic(c)
	}

	n1 := tempNic()
	n2 := tempNic()

	err := exec.Command("ip", "link", "add", n1, "type", "veth", "peer", "name", n2).Run()
	if err != nil {
		return "", err
	}
	err = exec.Command("brctl", "addif", d["parent"], n1).Run()
	if err != nil {
		RemoveInterface(n2)
		return "", err
	}

	return n2, nil
}

func RemoveInterface(nic string) {
	_ = exec.Command("ip", "link", "del", nic).Run()
}

/*
 * Detach an interface in a container
 * The thing is, there doesn't seem to be a good way of doing
 * this without relying on /sys in the container or /sbin/ip
 * in the container being reliable.  We can look at the
 * /sys/devices/virtual/net/$name/ifindex (i.e. if 7, then delete 8 on host)
 * we can just ip link del $name in the container.
 *
 * if we just did a lxc config device add of this nic, then
 * lxc simply doesn't know the peername for this nic
 *
 * probably the thing to do is re-exec ourselves asking to
 * setns into the container's netns (only) and remove the nic.  for
 * now just don't do it, but don't fail either.
 */
func detachInterface(c *lxdContainer, key string) error {
	options := lxc.DefaultAttachOptions
	options.ClearEnv = false
	options.Namespaces = syscall.CLONE_NEWNET
	command := []string{"ip", "link", "del", key}
	_, err := c.c.RunCommand(command, options)
	return err
}

func txUpdateNic(tx *sql.Tx, cId int, devname string, nicname string) error {
	q := `
	SELECT id FROM containers_devices
	WHERE container_id == ? AND type == 1 AND name == ?`
	var dId int
	err := tx.QueryRow(q, cId, devname).Scan(&dId)
	if err != nil {
		return err
	}

	stmt := `INSERT into containers_devices_config (container_device_id, key, value) VALUES (?, ?, ?)`
	_, err = tx.Exec(stmt, dId, "name", nicname)
	return err
}

/*
 * Given a running container and a list of devices before and after a
 * config change, update the devices in the container.
 *
 * Currently we only support nics.  Disks will be supported once we
 * decide how best to insert them.
 */
func devicesApplyDeltaLive(tx *sql.Tx, c *lxdContainer, preDevList shared.Devices, postDevList shared.Devices) error {
	rmList, addList := preDevList.Update(postDevList)
	var err error

	// note - currently Devices.Update() only returns nics
	for key, nic := range rmList {
		if nic["name"] == "" {
			return fmt.Errorf("Do not know a name for the nic for device %s\n", key)
		}
		if err := detachInterface(c, nic["name"]); err != nil {
			return fmt.Errorf("Error removing device %s (nic %s) from container %s: %s", key, nic["name"], c.name, err)
		}
	}

	for key, nic := range addList {
		var tmpName string
		if tmpName, err = setupNic(c, nic); err != nil {
			return fmt.Errorf("Unable to create nic %s for container %s: %s", nic["name"], c.name, err)
		}
		if err := c.c.AttachInterface(tmpName, nic["name"]); err != nil {
			RemoveInterface(tmpName)
			return fmt.Errorf("Unable to move nic %s into container %s as %s: %s", tmpName, c.name, nic["name"], err)
		}
		// Now we need to add the name to the database
		if err := txUpdateNic(tx, c.id, key, nic["name"]); err != nil {
			shared.Debugf("Warning: failed to update database entry for new nic %s: %s\n", key, err)
		}
	}

	return nil
}
