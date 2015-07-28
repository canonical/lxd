package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

// ExtractInterfaceFromConfigName returns "eth0" from "volatile.eth0.hwaddr",
// or an error if the key does not match this pattern.
func ExtractInterfaceFromConfigName(k string) (string, error) {
	re := regexp.MustCompile("volatile\\.([^.]*)\\.hwaddr")
	m := re.FindStringSubmatch(k)
	if m != nil && len(m) > 1 {
		return m[1], nil
	}

	return "", fmt.Errorf("%s did not match", k)
}

func getIps(c *lxc.Container) []shared.Ip {
	ips := []shared.Ip{}
	names, err := c.Interfaces()
	if err != nil {
		return ips
	}
	for _, n := range names {
		addresses, err := c.IPAddress(n)
		if err != nil {
			continue
		}

		veth := ""

		for i := 0; i < len(c.ConfigItem("lxc.network")); i++ {
			nicName := c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.name", i))[0]
			if nicName != n {
				continue
			}

			interfaceType := c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.type", i))
			if interfaceType[0] != "veth" {
				continue
			}

			veth = c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.veth.pair", i))[0]
			break
		}

		for _, a := range addresses {
			ip := shared.Ip{Interface: n, Address: a, HostVeth: veth}
			if net.ParseIP(a).To4() == nil {
				ip.Protocol = "IPV6"
			} else {
				ip.Protocol = "IPV4"
			}
			ips = append(ips, ip)
		}
	}
	return ips
}

func newStatus(c *lxc.Container, state lxc.State) shared.ContainerStatus {
	status := shared.ContainerStatus{State: state.String(), StateCode: shared.State(int(state))}
	if state == lxc.RUNNING {
		status.Init = c.InitPid()
		status.Ips = getIps(c)
	}
	return status
}

func (c *lxdContainer) RenderState() (*shared.ContainerState, error) {
	devices, err := dbDevicesGet(c.daemon.db, c.name, false)
	if err != nil {
		return nil, err
	}

	config, err := dbContainerConfigGet(c.daemon.db, c.id)
	if err != nil {
		return nil, err
	}

	return &shared.ContainerState{
		Architecture:    c.architecture,
		Config:          config,
		Devices:         devices,
		Ephemeral:       c.ephemeral,
		ExpandedConfig:  c.config,
		ExpandedDevices: c.devices,
		Name:            c.name,
		Profiles:        c.profiles,
		Status:          newStatus(c.c, c.c.State()),
		Userdata:        []byte{},
	}, nil
}

func (c *lxdContainer) Start() error {
	f, err := ioutil.TempFile("", "lxd_lxc_startconfig_")
	if err != nil {
		return err
	}
	configPath := f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(configPath)
		return err
	}
	f.Close()

	err = c.c.SaveConfigFile(configPath)
	if err != nil {
		return err
	}

	err = templateApply(c, "start")
	if err != nil {
		return err
	}

	err = exec.Command(
		os.Args[0],
		"forkstart",
		c.name,
		c.daemon.lxcpath,
		configPath).Run()

	if err != nil {
		err = fmt.Errorf(
			"Error calling 'lxd forkstart %s %s %s': err='%v'",
			c.name,
			c.daemon.lxcpath,
			shared.LogPath(c.name, "lxc.conf"),
			err)
	}

	if err == nil && c.ephemeral == true {
		containerWatchEphemeral(c)
	}

	return err
}

func (c *lxdContainer) Reboot() error {
	return c.c.Reboot()
}

func (c *lxdContainer) Freeze() error {
	return c.c.Freeze()
}

func (c *lxdContainer) isPrivileged() bool {
	switch strings.ToLower(c.config["security.privileged"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *lxdContainer) Shutdown(timeout time.Duration) error {
	return c.c.Shutdown(timeout)
}

func (c *lxdContainer) Stop() error {
	return c.c.Stop()
}

func (c *lxdContainer) Unfreeze() error {
	return c.c.Unfreeze()
}

func (c *lxdContainer) IsSnapshot() bool {
	return c.cType == cTypeSnapshot
}

func (c *lxdContainer) NameGet() string {
	return c.name
}

func (c *lxdContainer) PathGet() string {
	if c.IsSnapshot() {
		snappieces := strings.SplitN(c.NameGet(), shared.SnapshotDelimiter, 2)
		return shared.VarPath("lxc", snappieces[0], "snapshots", snappieces[1])
	}

	return shared.VarPath("lxc", c.NameGet())
}

func (c *lxdContainer) RootfsPathGet() string {
	return path.Join(c.PathGet(), "rootfs")
}

func validateRawLxc(rawLxc string) error {
	for _, line := range strings.Split(rawLxc, "\n") {
		membs := strings.SplitN(line, "=", 2)
		if strings.ToLower(strings.Trim(membs[0], " \t")) == "lxc.logfile" {
			return fmt.Errorf("setting lxc.logfile is not allowed")
		}
	}

	return nil
}

func (c *lxdContainer) applyConfig(config map[string]string, fromProfile bool) error {
	var err error
	for k, v := range config {
		switch k {
		case "limits.cpus":
			// TODO - Come up with a way to choose cpus for multiple
			// containers
			var vint int
			count, err := fmt.Sscanf(v, "%d", &vint)
			if err != nil {
				return err
			}
			if count != 1 || vint < 0 || vint > 65000 {
				return fmt.Errorf("Bad cpu limit: %s\n", v)
			}
			cpuset := fmt.Sprintf("0-%d", vint-1)
			err = c.c.SetConfigItem("lxc.cgroup.cpuset.cpus", cpuset)
		case "limits.memory":
			err = c.c.SetConfigItem("lxc.cgroup.memory.limit_in_bytes", v)

		default:
			if strings.HasPrefix(k, "user.") {
				// ignore for now
				err = nil
			}

			/* Things like security.privileged need to be propagated */
			c.config[k] = v
		}
		if err != nil {
			shared.Debugf("error setting %s: %q\n", k, err)
			return err
		}
	}

	if fromProfile {
		return nil
	}

	if lxcConfig, ok := config["raw.lxc"]; ok {
		if err := validateRawLxc(lxcConfig); err != nil {
			return err
		}

		f, err := ioutil.TempFile("", "lxd_config_")
		if err != nil {
			return err
		}

		err = shared.WriteAll(f, []byte(lxcConfig))
		f.Close()
		defer os.Remove(f.Name())
		if err != nil {
			return err
		}

		if err := c.c.LoadConfigFile(f.Name()); err != nil {
			return fmt.Errorf("problem applying raw.lxc, perhaps there is a syntax error?")
		}
	}

	return nil
}

func applyProfile(daemon *Daemon, d *lxdContainer, p string) error {
	q := `SELECT key, value FROM profiles_config
		JOIN profiles ON profiles.id=profiles_config.profile_id
		WHERE profiles.name=?`
	var k, v string
	inargs := []interface{}{p}
	outfmt := []interface{}{k, v}
	result, err := dbQueryScan(daemon.db, q, inargs, outfmt)

	if err != nil {
		return err
	}

	config := map[string]string{}
	for _, r := range result {
		k = r[0].(string)
		v = r[1].(string)

		shared.Debugf("applying %s: %s", k, v)
		if k == "raw.lxc" {
			if _, ok := d.config["raw.lxc"]; ok {
				shared.Debugf("ignoring overridden raw.lxc from profile")
				continue
			}
		}

		config[k] = v
	}

	newdevs, err := dbDevicesGet(daemon.db, p, true)
	if err != nil {
		return err
	}
	for k, v := range newdevs {
		d.devices[k] = v
	}

	return d.applyConfig(config, true)
}

// GenerateMacAddr generates a mac address from a string template:
// e.g. "00:11:22:xx:xx:xx" -> "00:11:22:af:3e:51"
func GenerateMacAddr(template string) (string, error) {
	ret := bytes.Buffer{}

	for _, c := range template {
		if c == 'x' {
			c, err := rand.Int(rand.Reader, big.NewInt(16))
			if err != nil {
				return "", err
			}
			ret.WriteString(fmt.Sprintf("%x", c.Int64()))
		} else {
			ret.WriteString(string(c))
		}
	}

	return ret.String(), nil
}

func (c *lxdContainer) updateContainerHWAddr(k, v string) {
	for name, d := range c.devices {
		if d["type"] != "nic" {
			continue
		}

		for key := range c.config {
			device, err := ExtractInterfaceFromConfigName(key)
			if err == nil && device == name {
				d["hwaddr"] = v
				c.config[key] = v
				return
			}
		}
	}
}

func (c *lxdContainer) setupMacAddresses(d *Daemon) error {
	newConfigEntries := map[string]string{}

	for name, d := range c.devices {
		if d["type"] != "nic" {
			continue
		}

		found := false

		for key, val := range c.config {
			device, err := ExtractInterfaceFromConfigName(key)
			if err == nil && device == name {
				found = true
				d["hwaddr"] = val
			}
		}

		if !found {
			var hwaddr string
			var err error
			if d["hwaddr"] != "" {
				hwaddr, err = GenerateMacAddr(d["hwaddr"])
				if err != nil {
					return err
				}
			} else {
				hwaddr, err = GenerateMacAddr("00:16:3e:xx:xx:xx")
				if err != nil {
					return err
				}
			}

			if hwaddr != d["hwaddr"] {
				d["hwaddr"] = hwaddr
				key := fmt.Sprintf("volatile.%s.hwaddr", name)
				c.config[key] = hwaddr
				newConfigEntries[key] = hwaddr
			}
		}
	}

	if len(newConfigEntries) > 0 {

		tx, err := dbBegin(d.db)
		if err != nil {
			return err
		}

		/*
		 * My logic may be flawed here, but it seems to me that one of
		 * the following must be true:
		 * 1. The current database entry equals what we had stored.
		 *    Our update akes precedence
		 * 2. The current database entry is different from what we had
		 *    stored.  Someone updated it since we last grabbed the
		 *    container configuration.  So either
		 *    a. it contains 'x' and is a template.  We have generated
		 *       a real mac, so our update takes precedence
		 *    b. it contains no 'x' and is an hwaddr, not template.  We
		 *       defer to the racer's update since it may be actually
		 *       starting the container.
		 */
		str := "INSERT INTO containers_config (container_id, key, value) values (?, ?, ?)"
		stmt, err := tx.Prepare(str)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer stmt.Close()

		ustr := "UPDATE containers_config SET value=? WHERE container_id=? AND key=?"
		ustmt, err := tx.Prepare(ustr)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer ustmt.Close()

		qstr := "SELECT value FROM containers_config WHERE container_id=? AND key=?"
		qstmt, err := tx.Prepare(qstr)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer qstmt.Close()

		for k, v := range newConfigEntries {
			var racer string
			err := qstmt.QueryRow(c.id, k).Scan(&racer)
			if err == sql.ErrNoRows {
				_, err = stmt.Exec(c.id, k, v)
				if err != nil {
					shared.Debugf("Error adding mac address to container\n")
					tx.Rollback()
					return err
				}
			} else if err != nil {
				tx.Rollback()
				return err
			} else if strings.Contains(racer, "x") {
				_, err = ustmt.Exec(v, c.id, k)
				if err != nil {
					shared.Debugf("Error updating mac address to container\n")
					tx.Rollback()
					return err
				}
			} else {
				// we accept the racing task's update
				c.updateContainerHWAddr(k, v)
			}
		}

		err = txCommit(tx)
		if err != nil {
			fmt.Printf("setupMacAddresses: (TxCommit) error %s\n", err)
		}
		return err
	}

	return nil
}

func (c *lxdContainer) applyIdmapSet() error {
	if c.idmapset == nil {
		return nil
	}
	lines := c.idmapset.ToLxcString()
	for _, line := range lines {
		err := c.c.SetConfigItem("lxc.id_map", line+"\n")
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *lxdContainer) applyDevices() error {
	var keys []string
	for k := range c.devices {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		d := c.devices[name]
		if name == "type" {
			continue
		}

		configs, err := DeviceToLxc(d)
		if err != nil {
			return fmt.Errorf("Failed configuring device %s: %s\n", name, err)
		}
		for _, line := range configs {
			err := c.c.SetConfigItem(line[0], line[1])
			if err != nil {
				return fmt.Errorf("Failed configuring device %s: %s\n", name, err)
			}
		}
	}
	return nil
}

func newLxdContainer(name string, daemon *Daemon) (*lxdContainer, error) {
	d := &lxdContainer{
		daemon:       daemon,
		ephemeral:    false,
		architecture: -1,
		cType:        -1,
		id:           -1}

	ephemInt := -1

	templateConfBase := "ubuntu"
	templateConfDir := os.Getenv("LXC_TEMPLATE_CONFIG")
	if templateConfDir == "" {
		templateConfDir = "/usr/share/lxc/config"
	}

	q := "SELECT id, architecture, type, ephemeral FROM containers WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&d.id, &d.architecture, &d.cType, &ephemInt}
	err := dbQueryRowScan(daemon.db, q, arg1, arg2)
	if err != nil {
		return nil, err
	}
	if d.id == -1 {
		return nil, fmt.Errorf("Unknown container")
	}

	if ephemInt == 1 {
		d.ephemeral = true
	}

	c, err := lxc.NewContainer(name, daemon.lxcpath)
	if err != nil {
		return nil, err
	}
	d.c = c

	dir := shared.LogPath(c.Name())
	err = os.MkdirAll(dir, 0700)
	if err != nil {
		return nil, err
	}

	if err = d.c.SetLogFile(filepath.Join(dir, "lxc.log")); err != nil {
		return nil, err
	}

	personality, err := shared.ArchitecturePersonality(d.architecture)
	if err == nil {
		err = c.SetConfigItem("lxc.arch", personality)
		if err != nil {
			return nil, err
		}
	}

	err = c.SetConfigItem("lxc.include", fmt.Sprintf("%s/%s.common.conf", templateConfDir, templateConfBase))
	if err != nil {
		return nil, err
	}

	if !d.isPrivileged() {
		err = c.SetConfigItem("lxc.include", fmt.Sprintf("%s/%s.userns.conf", templateConfDir, templateConfBase))
		if err != nil {
			return nil, err
		}
	}

	config, err := dbContainerConfigGet(daemon.db, d.id)
	if err != nil {
		return nil, err
	}
	d.config = config

	profiles, err := dbContainerProfilesGet(daemon.db, d.id)
	if err != nil {
		return nil, err
	}
	d.profiles = profiles
	d.devices = shared.Devices{}
	d.name = name

	rootfsPath := shared.VarPath("lxc", name, "rootfs")
	err = c.SetConfigItem("lxc.rootfs", rootfsPath)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.loglevel", "0")
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.utsname", name)
	if err != nil {
		return nil, err
	}
	err = c.SetConfigItem("lxc.tty", "0")
	if err != nil {
		return nil, err
	}

	if err := setupDevLxdMount(c); err != nil {
		return nil, err
	}

	/* apply profiles */
	for _, p := range profiles {
		err := applyProfile(daemon, d, p)
		if err != nil {
			return nil, err
		}
	}

	/* get container_devices */
	newdevs, err := dbDevicesGet(daemon.db, d.name, false)
	if err != nil {
		return nil, err
	}

	for k, v := range newdevs {
		d.devices[k] = v
	}

	if err := d.setupMacAddresses(daemon); err != nil {
		return nil, err
	}

	/* now add the lxc.* entries for the configured devices */
	err = d.applyDevices()
	if err != nil {
		return nil, err
	}

	if !d.isPrivileged() {
		d.idmapset = daemon.IdmapSet // TODO - per-tenant idmaps
	}

	err = d.applyIdmapSet()
	if err != nil {
		return nil, err
	}

	err = d.applyConfig(d.config, false)
	if err != nil {
		return nil, err
	}

	return d, nil
}
