package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"

	log "gopkg.in/inconshreveable/log15.v2"
)

func addBlockDev(dev string) ([]string, error) {
	stat := syscall.Stat_t{}
	err := syscall.Stat(dev, &stat)
	if err != nil {
		return []string{}, err
	}
	k := "lxc.cgroup.devices.allow"
	v := fmt.Sprintf("b %d:%d rwm", uint(stat.Rdev/256), uint(stat.Rdev%256))
	line := []string{k, v}
	return line, err
}

func devGetOptions(d shared.Device) (string, error) {
	opts := []string{"bind", "create=file"}
	if d["uid"] != "" {
		u, err := strconv.Atoi(d["uid"])
		if err != nil {
			return "", err
		}
		opts = append(opts, fmt.Sprintf("uid=%d", u))
	}
	if d["gid"] != "" {
		g, err := strconv.Atoi(d["gid"])
		if err != nil {
			return "", err
		}
		opts = append(opts, fmt.Sprintf("gid=%d", g))
	}
	if d["mode"] != "" {
		m, err := devModeOct(d["mode"])
		if err != nil {
			return "", err
		}
		opts = append(opts, fmt.Sprintf("mode=%0d", m))
	} else {
		opts = append(opts, "mode=0660")
	}

	return strings.Join(opts, ","), nil
}

func devModeOct(strmode string) (int, error) {
	// todo - parse strmode
	return 0660, nil
}

func devModeString(strmode string) string {
	// todo - parse strmode
	return "rwm"
}

func getDev(path string) (int, int, error) {
	stat := syscall.Stat_t{}
	err := syscall.Stat(path, &stat)
	if err != nil {
		return 0, 0, err
	}
	major := int(stat.Rdev / 256)
	minor := int(stat.Rdev % 256)
	return major, minor, nil
}

func deviceCgroupInfo(dev shared.Device) (string, error) {
	var err error

	t := dev["type"]
	switch t {
	case "unix-char":
		t = "c"
	case "unix-block":
		t = "b"
	default: // internal error - look at how we were called
		return "", fmt.Errorf("BUG: bad device type %s", dev["type"])
	}

	var major, minor int
	if dev["major"] == "" && dev["minor"] == "" {
		devname := dev["path"]
		if !filepath.IsAbs(devname) {
			devname = filepath.Join("/", devname)
		}
		major, minor, err = getDev(devname)
		if err != nil {
			return "", err
		}
	} else if dev["major"] != "" && dev["minor"] != "" {
		major, err = strconv.Atoi(dev["major"])
		if err != nil {
			return "", err
		}
		minor, err = strconv.Atoi(dev["minor"])
		if err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("Both major and minor must be supplied for devices")
	}

	devcg := fmt.Sprintf("%s %d:%d %s", t, major, minor, devModeString(dev["mode"]))
	return devcg, nil
}

/*
 * unixDevCgroup only grabs the cgroup devices.allow statement
 * we need.  We'll add a mount.entry to bind mount the actual
 * device later.
 */
func unixDevCgroup(dev shared.Device) ([][]string, error) {
	devcg, err := deviceCgroupInfo(dev)
	if err != nil {
		return [][]string{}, err
	}
	entry := []string{"lxc.cgroup.devices.allow", devcg}
	return [][]string{entry}, nil
}

func deviceToLxc(d shared.Device) ([][]string, error) {
	switch d["type"] {
	case "unix-char":
		return unixDevCgroup(d)
	case "unix-block":
		return unixDevCgroup(d)

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
		configLines := [][]string{}
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
		fstype := "none"
		options := []string{}
		var err error
		if shared.IsBlockdevPath(d["source"]) {
			fstype, err = shared.BlockFsDetect(d["source"])
			if err != nil {
				return nil, fmt.Errorf("Error setting up %s: %s\n", d["name"], err)
			}
			l, err := addBlockDev(d["source"])
			if err != nil {
				return nil, fmt.Errorf("Error adding blockdev: %s\n", err)
			}
			configLines = append(configLines, l)
		} else if shared.IsDir(source) {
			options = append(options, "bind")
			options = append(options, "create=dir")
		} else /* file bind mount */ {
			/* Todo - can we distinguish between file bind mount and
			 * a qcow2 (or other fs container) file? */
			options = append(options, "bind")
			options = append(options, "create=file")
		}
		if d["readonly"] == "1" || d["readonly"] == "true" {
			options = append(options, "ro")
		}
		if d["optional"] == "1" || d["optional"] == "true" {
			options = append(options, "optional")
		}
		opts := strings.Join(options, ",")
		if opts == "" {
			opts = "defaults"
		}
		l := []string{"lxc.mount.entry", fmt.Sprintf("%s %s %s %s 0 0", source, p, fstype, opts)}
		configLines = append(configLines, l)
		return configLines, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("Bad device type")
	}
}

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
		return "", fmt.Errorf("Invalid device type %d\n", t)
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
		return -1, fmt.Errorf("Invalid device type %s\n", t)
	}
}

func validDeviceConfig(t, k, v string) bool {
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

func tempNic() string {
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	return "veth" + hex.EncodeToString(randBytes)
}

func inList(l []string, s string) bool {
	for _, ls := range l {
		if ls == s {
			return true
		}
	}
	return false
}

func nextUnusedNic(c container) string {
	lxContainer := c.LXContainerGet()

	list, err := lxContainer.Interfaces()
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

func setupNic(tx *sql.Tx, c container, name string, d map[string]string) (string, error) {
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
		removeInterface(n2)
		return "", err
	}

	key := fmt.Sprintf("volatile.%s.hwaddr", name)
	config := c.ConfigGet()
	hwaddr := config[key]

	if hwaddr == "" {
		if d["hwaddr"] != "" {
			hwaddr, err = generateMacAddr(d["hwaddr"])
			if err != nil {
				return "", err
			}
		} else {
			hwaddr, err = generateMacAddr("00:16:3e:xx:xx:xx")
			if err != nil {
				return "", err
			}
		}

		if hwaddr != d["hwaddr"] {
			stmt := `INSERT OR REPLACE into containers_config (container_id, key, value) VALUES (?, ?, ?)`
			_, err = tx.Exec(stmt, c.IDGet(), key, hwaddr)

			if err != nil {
				removeInterface(n2)
				return "", err
			}
		}
	}

	err = exec.Command("ip", "link", "set", "dev", n2, "address", hwaddr).Run()
	if err != nil {
		removeInterface(n2)
		return "", err
	}

	return n2, nil
}

func removeInterface(nic string) {
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
func detachInterface(c container, key string) error {
	options := lxc.DefaultAttachOptions
	options.ClearEnv = false
	options.Namespaces = syscall.CLONE_NEWNET
	nullDev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0666)
	if err != nil {
		return err
	}
	defer nullDev.Close()
	nullfd := nullDev.Fd()
	options.StdinFd = nullfd
	options.StdoutFd = nullfd
	options.StderrFd = nullfd
	command := []string{"ip", "link", "del", key}
	lxContainer := c.LXContainerGet()
	_, err = lxContainer.RunCommand(command, options)
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

	stmt := `INSERT OR REPLACE into containers_devices_config (container_device_id, key, value) VALUES (?, ?, ?)`
	_, err = tx.Exec(stmt, dId, "name", nicname)
	return err
}

func (c *containerLXD) DetachUnixDev(dev shared.Device) error {
	cginfo, err := deviceCgroupInfo(dev)
	if err != nil {
		return err
	}
	c.c.SetCgroupItem("devices.remove", cginfo)
	pid := c.c.InitPid()
	if pid == -1 { // container not running
		return nil
	}
	pidstr := fmt.Sprintf("%d", pid)
	if err := exec.Command(os.Args[0], "forkumount", pidstr, dev["path"]).Run(); err != nil {
		shared.Log.Warn("Error unmounting device", log.Ctx{"Error": err})
		return err
	}
	if err := os.Remove(fmt.Sprintf("/proc/%d/root/%s", pid, dev["path"])); err != nil {
		shared.Log.Warn("Error removing device", log.Ctx{"Error": err})
		return err
	}

	return nil
}

func (c *containerLXD) AttachUnixDev(dev shared.Device) error {
	return c.setupUnixDev(dev)
}

/*
 * Given a running container and a list of devices before and after a
 * config change, update the devices in the container.
 *
 * Currently we only support nics.  Disks will be supported once we
 * decide how best to insert them.
 */
func devicesApplyDeltaLive(tx *sql.Tx, c container, preDevList shared.Devices, postDevList shared.Devices) error {
	rmList, addList := preDevList.Update(postDevList)
	var err error

	for key, dev := range rmList {
		switch dev["type"] {
		case "nic":
			if dev["name"] == "" {
				return fmt.Errorf("Do not know a name for the nic for device %s\n", key)
			}
			if err := detachInterface(c, dev["name"]); err != nil {
				return fmt.Errorf("Error removing device %s (nic %s) from container %s: %s", key, dev["name"], c.NameGet(), err)
			}
		case "disk":
			return c.DetachMount(dev)
		case "unix-block":
			return c.DetachUnixDev(dev)
		case "unix-char":
			return c.DetachUnixDev(dev)
		}
	}

	lxContainer := c.LXContainerGet()

	for key, dev := range addList {
		switch dev["type"] {
		case "nic":
			var tmpName string
			if tmpName, err = setupNic(tx, c, key, dev); err != nil {
				return fmt.Errorf("Unable to create nic %s for container %s: %s", dev["name"], c.NameGet(), err)
			}
			if err := lxContainer.AttachInterface(tmpName, dev["name"]); err != nil {
				removeInterface(tmpName)
				return fmt.Errorf("Unable to move nic %s into container %s as %s: %s", tmpName, c.NameGet(), dev["name"], err)
			}

			if err := txUpdateNic(tx, c.IDGet(), key, dev["name"]); err != nil {
				shared.Debugf("Warning: failed to update database entry for new nic %s: %s", key, err)
				return err
			}
		case "disk":
			if dev["source"] == "" || dev["path"] == "" {
				return fmt.Errorf("no source or destination given")
			}
			return c.AttachMount(dev)
		case "unix-block":
			return c.AttachUnixDev(dev)
		case "unix-char":
			return c.AttachUnixDev(dev)
		}
	}

	return nil
}

func validateConfig(c container, devs shared.Devices) error {
	for _, dev := range devs {
		if dev["type"] == "disk" && shared.IsBlockdevPath(dev["source"]) {
			if !c.IsPrivileged() {
				return fmt.Errorf("Only privileged containers may mount block devices")
			}
		}
	}
	return nil
}
