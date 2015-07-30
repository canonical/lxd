package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
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

	log "gopkg.in/inconshreveable/log15.v2"
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

func (c *lxdContainer) iPsGet() []shared.Ip {
	ips := []shared.Ip{}
	names, err := c.c.Interfaces()
	if err != nil {
		return ips
	}
	for _, n := range names {
		addresses, err := c.c.IPAddress(n)
		if err != nil {
			continue
		}

		veth := ""

		for i := 0; i < len(c.c.ConfigItem("lxc.network")); i++ {
			nicName := c.c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.name", i))[0]
			if nicName != n {
				continue
			}

			interfaceType := c.c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.type", i))
			if interfaceType[0] != "veth" {
				continue
			}

			veth = c.c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.veth.pair", i))[0]
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

type container interface {
	RenderState() (*shared.ContainerState, error)
	Reboot() error
	Freeze() error
	IsPrivileged() bool
	IsRunning() bool
	IsEmpheral() bool
	IsSnapshot() bool
	Shutdown(timeout time.Duration) error
	Start() error
	Stop() error
	Unfreeze() error
	Delete() error
	CreateFromImage(hash string) error
	Restore(sourceContainer container) error
	Copy(source container) error
	Rename(newName string) error
	IDGet() int
	NameGet() string
	ArchitectureGet() int
	PathGet(newName string) string
	RootfsPathGet() string
	TemplatesPathGet() string
	StateDirGet() string
	TemplateApply(trigger string) error
	LogFilePathGet() string
	LogPathGet() string
	InitPidGet() (int, error)
	IdmapSetGet() (*shared.IdmapSet, error)
	ConfigReplace(newConfig containerLXDArgs) error
	ConfigGet() containerLXDArgs

	ExportToTar(snap string, w io.Writer) error

	// TODO: Remove every use of this and remove it.
	LXContainerGet() (*lxc.Container, error)

	DetachMount(m shared.Device) error
	AttachMount(m shared.Device) error
}

func (c *lxdContainer) RenderState() (*shared.ContainerState, error) {
	if _, err := c.LXContainerGet(); err != nil {
		return nil, err
	}

	state := c.c.State()
	pid, _ := c.InitPidGet()
	status := shared.ContainerStatus{State: state.String(), StateCode: shared.State(int(state))}
	if state == lxc.RUNNING {
		status.Init = pid
		status.Ips = c.iPsGet()
	}

	return &shared.ContainerState{
		Name:            c.name,
		Profiles:        c.profiles,
		Config:          c.myConfig,
		ExpandedConfig:  c.config,
		Userdata:        []byte{},
		Status:          status,
		Devices:         c.myDevices,
		ExpandedDevices: c.devices,
		Ephemeral:       c.ephemeral,
	}, nil
}

func (c *lxdContainer) Start() error {
	// Start the storage for this container
	if err := c.Storage.ContainerStart(c); err != nil {
		return err
	}

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

	err = c.TemplateApply("start")
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
			path.Join(c.LogPathGet(), "lxc.conf"),
			err)
	}

	if err == nil && c.ephemeral == true {
		containerWatchEphemeral(c.daemon, c)
	}

	return err
}

func (c *lxdContainer) Reboot() error {
	return c.c.Reboot()
}

func (c *lxdContainer) Freeze() error {
	return c.c.Freeze()
}

func (c *lxdContainer) IsPrivileged() bool {
	switch strings.ToLower(c.config["security.privileged"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *lxdContainer) IsRunning() bool {
	return c.c.Running()
}

func (c *lxdContainer) Shutdown(timeout time.Duration) error {
	if err := c.c.Shutdown(timeout); err != nil {

		// TODO: Not sure this line is right.
		c.Storage.ContainerStop(c)

		return err
	}

	// Stop the storage for this container
	if err := c.Storage.ContainerStop(c); err != nil {
		return err
	}

	return nil
}

func (c *lxdContainer) Stop() error {
	if err := c.c.Stop(); err != nil {
		c.Storage.ContainerStop(c)
		return err
	}

	// Stop the storage for this container
	if err := c.Storage.ContainerStop(c); err != nil {
		return err
	}

	return nil
}

func (c *lxdContainer) Unfreeze() error {
	return c.c.Unfreeze()
}

func (c *lxdContainer) CreateFromImage(hash string) error {
	return c.Storage.ContainerCreate(c, hash)
}

func (c *lxdContainer) Restore(sourceContainer container) error {
	/*
	 * restore steps:
	 * 1. stop container if already running
	 * 2. copy snapshot rootfs to container
	 * 3. overwrite existing config with snapshot config
	 */

	// Stop the container
	// TODO: stateful restore ?
	wasRunning := false
	if c.IsRunning() {
		wasRunning = true
		if err := c.Stop(); err != nil {
			shared.Log.Error(
				"RESTORE => could not stop container",
				log.Ctx{
					"container": c.NameGet(),
					"err":       err})
			return err
		}
		shared.Log.Debug(
			"RESTORE => Stopped container",
			log.Ctx{"container": c.NameGet()})
	}

	// Restore the FS.
	// TODO: I switched the FS and config restore, think thats the correct way
	// (pcdummy)
	err := c.Storage.ContainerRestore(c, sourceContainer)
	if err != nil {
		shared.Log.Error("RESTORE => Restoring the filesystem failed",
			log.Ctx{
				"source":      sourceContainer.NameGet(),
				"destination": c.NameGet()})
		return err
	}

	// Replace the config
	err = c.ConfigReplace(sourceContainer.ConfigGet())
	if err != nil {
		shared.Log.Error("RESTORE => Restore of the configuration failed",
			log.Ctx{
				"source":      sourceContainer.NameGet(),
				"destination": c.NameGet()})

		return err
	}

	if wasRunning {
		c.Start()
	}

	return nil
}

func (c *lxdContainer) Copy(source container) error {
	return c.Storage.ContainerCopy(c, source)
}

func (c *lxdContainer) Delete() error {
	if err := containerDeleteSnapshots(c.daemon, c.NameGet()); err != nil {
		return err
	}

	if err := c.Storage.ContainerDelete(c); err != nil {
		return err
	}

	if err := dbContainerRemove(c.daemon.db, c.NameGet()); err != nil {
		return err
	}

	return nil
}

func (c *lxdContainer) Rename(newName string) error {
	if c.IsRunning() {
		return fmt.Errorf("renaming of running container not allowed")
	}

	if err := c.Storage.ContainerRename(c, newName); err != nil {
		return err
	}
	if err := dbContainerRename(c.daemon.db, c.NameGet(), newName); err != nil {
		return err
	}

	c.name = newName

	// Recreate the LX Container
	c.c = nil
	c.init()

	// TODO: We should rename its snapshots here.

	return nil
}

func (c *lxdContainer) IsEmpheral() bool {
	return c.ephemeral
}

func (c *lxdContainer) IsSnapshot() bool {
	return c.cType == cTypeSnapshot
}

func (c *lxdContainer) IDGet() int {
	return c.id
}

func (c *lxdContainer) NameGet() string {
	return c.name
}

func (c *lxdContainer) ArchitectureGet() int {
	return c.architecture
}

func (c *lxdContainer) PathGet(newName string) string {
	if newName != "" {
		return containerPathGet(newName, c.IsSnapshot())
	}

	return containerPathGet(c.NameGet(), c.IsSnapshot())
}

func (c *lxdContainer) RootfsPathGet() string {
	return path.Join(c.PathGet(""), "rootfs")
}

func (c *lxdContainer) TemplatesPathGet() string {
	return path.Join(c.PathGet(""), "templates")
}

func (c *lxdContainer) StateDirGet() string {
	return path.Join(c.PathGet(""), "state")
}

func (c *lxdContainer) LogPathGet() string {
	return shared.LogPath(c.NameGet())
}

func (c *lxdContainer) LogFilePathGet() string {
	return filepath.Join(c.LogPathGet(), "lxc.log")
}

func (c *lxdContainer) InitPidGet() (int, error) {
	return c.c.InitPid(), nil
}

func (c *lxdContainer) IdmapSetGet() (*shared.IdmapSet, error) {
	return c.idmapset, nil
}

func (c *lxdContainer) LXContainerGet() (*lxc.Container, error) {
	return c.c, nil
}

// ConfigReplace replaces the config of container and tries to live apply
// the new configuration.
func (c *lxdContainer) ConfigReplace(newConfig containerLXDArgs) error {
	/* check to see that the config actually applies to the container
	 * successfully before saving it. in particular, raw.lxc and
	 * raw.apparmor need to be parsed once to make sure they make sense.
	 */
	preDevList := c.devices

	/* Validate devices */
	if err := validateConfig(c, newConfig.Devices); err != nil {
		return err
	}

	if err := c.applyConfig(newConfig.Config, false); err != nil {
		return err
	}

	tx, err := dbBegin(c.daemon.db)
	if err != nil {
		return err
	}

	/* Update config or profiles */
	if err = dbContainerConfigClear(tx, c.id); err != nil {
		shared.Log.Debug(
			"Error clearing configuration for container",
			log.Ctx{"name": c.NameGet()})
		tx.Rollback()
		return err
	}

	if err = dbContainerConfigInsert(tx, c.id, newConfig.Config); err != nil {
		shared.Debugf("Error inserting configuration for container %s\n", c.NameGet())
		tx.Rollback()
		return err
	}

	/* handle profiles */
	if emptyProfile(newConfig.Profiles) {
		_, err := tx.Exec("DELETE from containers_profiles where container_id=?", c.id)
		if err != nil {
			tx.Rollback()
			return err
		}
	} else {
		if err := dbContainerProfilesInsert(tx, c.id, newConfig.Profiles); err != nil {

			tx.Rollback()
			return err
		}
	}

	err = dbDevicesAdd(tx, "container", int64(c.id), newConfig.Devices)
	if err != nil {
		tx.Rollback()
		return err
	}

	if !c.IsRunning() {
		return txCommit(tx)
	}

	// Apply new devices
	if err := devicesApplyDeltaLive(tx, c, preDevList, newConfig.Devices); err != nil {
		return err
	}

	return txCommit(tx)
}

func (c *lxdContainer) ConfigGet() containerLXDArgs {
	newConfig := containerLXDArgs{
		Config:    c.myConfig,
		Devices:   c.myDevices,
		Profiles:  c.profiles,
		Ephemeral: c.ephemeral,
	}

	return newConfig
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
			if strings.HasPrefix(k, "environment.") {
				c.c.SetConfigItem("lxc.environment", fmt.Sprintf("%s=%s", strings.TrimPrefix(k, "environment."), v))
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

func (c *lxdContainer) applyProfile(p string) error {
	q := `SELECT key, value FROM profiles_config
		JOIN profiles ON profiles.id=profiles_config.profile_id
		WHERE profiles.name=?`
	var k, v string
	inargs := []interface{}{p}
	outfmt := []interface{}{k, v}
	result, err := dbQueryScan(c.daemon.db, q, inargs, outfmt)

	if err != nil {
		return err
	}

	config := map[string]string{}
	for _, r := range result {
		k = r[0].(string)
		v = r[1].(string)

		shared.Debugf("applying %s: %s", k, v)
		if k == "raw.lxc" {
			if _, ok := c.config["raw.lxc"]; ok {
				shared.Debugf("ignoring overridden raw.lxc from profile")
				continue
			}
		}

		config[k] = v
	}

	newdevs, err := dbDevicesGet(c.daemon.db, p, true)
	if err != nil {
		return err
	}
	for k, v := range newdevs {
		c.devices[k] = v
	}

	return c.applyConfig(config, true)
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

func (c *lxdContainer) setupMacAddresses() error {
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

		tx, err := dbBegin(c.daemon.db)
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

func containerPathGet(name string, isSnapshot bool) string {
	if isSnapshot {
		return shared.VarPath("snapshots", name)
	}

	return shared.VarPath("containers", name)
}

func createcontainerLXD(
	d *Daemon, name string, args containerLXDArgs) (container, error) {

	shared.Log.Info(
		"Container create",
		log.Ctx{
			"container":  name,
			"isSnapshot": args.Ctype == cTypeSnapshot})

	if args.Ctype != cTypeSnapshot &&
		strings.Contains(name, shared.SnapshotDelimiter) {
		return nil, fmt.Errorf(
			"The character '%s' is reserved for snapshots.",
			shared.SnapshotDelimiter)
	}

	path := containerPathGet(name, args.Ctype == cTypeSnapshot)
	if shared.PathExists(path) {
		shared.Log.Error(
			"The container already exists on disk",
			log.Ctx{
				"container": name,
				"path":      path})

		return nil, fmt.Errorf(
			"The container already exists on disk, container: '%s', path: '%s'",
			name,
			path)
	}

	if args.Profiles == nil {
		args.Profiles = []string{"default"}
	}

	if args.BaseImage != "" {
		if args.Config == nil {
			args.Config = map[string]string{}
			args.Config["volatile.baseImage"] = args.BaseImage
		}
	}

	if args.Devices == nil {
		args.Devices = shared.Devices{}
	}

	id, err := dbContainerCreate(d.db, name, args)
	if err != nil {
		return nil, err
	}

	shared.Log.Debug(
		"Container created in the DB",
		log.Ctx{"container": name, "id": id})

	c := &lxdContainer{
		daemon:       d,
		id:           id,
		name:         name,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		config:       args.Config,
		profiles:     args.Profiles,
		devices:      args.Devices,
		cType:        args.Ctype,
		myConfig:     args.Config,
		myDevices:    args.Devices}

	// No need to detect storage here, its a new container.
	c.Storage = d.Storage

	if err := c.init(); err != nil {
		c.Delete() // Delete the container from the DB.
		return nil, err
	}

	return c, nil
}

func newLxdContainer(name string, d *Daemon) (container, error) {
	shared.Log.Debug("Container load", log.Ctx{"container": name})

	args, err := dbContainerGet(d.db, name)
	if err != nil {
		return nil, err
	}

	c := &lxdContainer{
		daemon:       d,
		id:           args.ID,
		name:         name,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		config:       args.Config,
		profiles:     args.Profiles,
		devices:      args.Devices,
		cType:        args.Ctype,
		myConfig:     args.Config,
		myDevices:    args.Devices}

	s, err := storageForFilename(d, c.PathGet(""))
	if err != nil {
		shared.Log.Warn("Couldn't detect storage.", log.Ctx{"container": c.NameGet()})
		c.Storage = d.Storage
	} else {
		c.Storage = s
	}

	if err := c.init(); err != nil {
		return nil, err
	}

	return c, nil
}

// init prepares the LXContainer for this LXD Container
// TODO: This gets called on each load of the container,
//       we might be able to split this is up into c.Start().
func (c *lxdContainer) init() error {
	templateConfBase := "ubuntu"
	templateConfDir := os.Getenv("LXC_TEMPLATE_CONFIG")
	if templateConfDir == "" {
		templateConfDir = "/usr/share/lxc/config"
	}

	cc, err := lxc.NewContainer(c.NameGet(), c.daemon.lxcpath)
	if err != nil {
		return err
	}
	c.c = cc

	logfile := c.LogFilePathGet()
	if err := os.MkdirAll(filepath.Dir(logfile), 0700); err != nil {
		return err
	}

	if err = c.c.SetLogFile(logfile); err != nil {
		return err
	}

	personality, err := shared.ArchitecturePersonality(c.architecture)
	if err == nil {
		if err := c.c.SetConfigItem("lxc.arch", personality); err != nil {
			return err
		}
	}

	err = c.c.SetConfigItem("lxc.include", fmt.Sprintf("%s/%s.common.conf", templateConfDir, templateConfBase))
	if err != nil {
		return err
	}

	if !c.IsPrivileged() {
		err = c.c.SetConfigItem("lxc.include", fmt.Sprintf("%s/%s.userns.conf", templateConfDir, templateConfBase))
		if err != nil {
			return err
		}
	}

	if err := c.c.SetConfigItem("lxc.rootfs", c.RootfsPathGet()); err != nil {
		return err
	}
	if err := c.c.SetConfigItem("lxc.loglevel", "0"); err != nil {
		return err
	}
	if err := c.c.SetConfigItem("lxc.utsname", c.NameGet()); err != nil {
		return err
	}
	if err := c.c.SetConfigItem("lxc.tty", "0"); err != nil {
		return err
	}
	if err := setupDevLxdMount(c.c); err != nil {
		return err
	}

	/* apply profiles */
	for _, p := range c.profiles {
		if err := c.applyProfile(p); err != nil {
			return err
		}
	}

	if err := c.setupMacAddresses(); err != nil {
		return err
	}

	/* now add the lxc.* entries for the configured devices */
	if err := c.applyDevices(); err != nil {
		return err
	}

	if !c.IsPrivileged() {
		c.idmapset = c.daemon.IdmapSet // TODO - per-tenant idmaps
	}

	if err := c.mountShared(); err != nil {
		return err
	}

	if err := c.applyIdmapSet(); err != nil {
		return err
	}

	if err := c.applyConfig(c.config, false); err != nil {
		return err
	}

	return nil
}
