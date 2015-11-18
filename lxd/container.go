package main

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/flosch/pongo2.v3"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

// ExtractInterfaceFromConfigName returns "eth0" from "volatile.eth0.hwaddr",
// or an error if the key does not match this pattern.
func extractInterfaceFromConfigName(k string) (string, error) {
	re := regexp.MustCompile("volatile\\.([^.]*)\\.hwaddr")
	m := re.FindStringSubmatch(k)
	if m != nil && len(m) > 1 {
		return m[1], nil
	}

	return "", fmt.Errorf("%s did not match", k)
}

func validateRawLxc(rawLxc string) error {
	for _, line := range strings.Split(rawLxc, "\n") {
		// Ignore empty lines
		if len(line) == 0 {
			continue
		}

		// Ignore comments
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Ensure the format is valid
		membs := strings.SplitN(line, "=", 2)
		if len(membs) != 2 {
			return fmt.Errorf("invalid raw.lxc line: %s", line)
		}

		// Blacklist some keys
		if strings.ToLower(strings.Trim(membs[0], " \t")) == "lxc.logfile" {
			return fmt.Errorf("setting lxc.logfile is not allowed")
		}

		if strings.HasPrefix(strings.ToLower(strings.Trim(membs[0], " \t")), "lxc.network.") {
			return fmt.Errorf("setting lxc.network keys is not allowed")
		}
	}

	return nil
}

func setConfigItem(c *containerLXD, key string, value string) error {
	err := c.c.SetConfigItem(key, value)
	if err != nil {
		return fmt.Errorf("Failed to set LXC config: %s=%s", key, value)
	}

	return nil
}

// GenerateMacAddr generates a mac address from a string template:
// e.g. "00:11:22:xx:xx:xx" -> "00:11:22:af:3e:51"
func generateMacAddr(template string) (string, error) {
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

func containerPath(name string, isSnapshot bool) string {
	if isSnapshot {
		return shared.VarPath("snapshots", name)
	}

	return shared.VarPath("containers", name)
}

// containerLXDArgs contains every argument needed to create an LXD Container
type containerLXDArgs struct {
	ID           int // Leave it empty when you create one.
	Ctype        containerType
	Config       map[string]string
	Profiles     []string
	Ephemeral    bool
	BaseImage    string
	Architecture int
	Devices      shared.Devices
}

type containerLXD struct {
	c            *lxc.Container
	daemon       *Daemon
	id           int
	name         string
	config       map[string]string
	profiles     []string
	devices      shared.Devices
	architecture int
	ephemeral    bool
	idmapset     *shared.IdmapSet
	cType        containerType

	baseConfig  map[string]string
	baseDevices shared.Devices

	storage storage
}

type container interface {
	RenderState() (*shared.ContainerState, error)
	Reboot() error
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start() error
	Stop() error
	Unfreeze() error
	Delete() error
	Restore(sourceContainer container) error
	Rename(newName string) error
	ConfigReplace(newConfig containerLXDArgs) error

	StorageStart() error
	StorageStop() error
	Storage() storage

	IsPrivileged() bool
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool

	ID() int
	Name() string
	Architecture() int
	Config() map[string]string
	ConfigKeySet(key string, value string) error
	Devices() shared.Devices
	Profiles() []string
	Path(newName string) string
	RootfsPath() string
	TemplatesPath() string
	StateDir() string
	LogFilePath() string
	LogPath() string
	InitPID() int
	State() string

	IdmapSet() *shared.IdmapSet
	LastIdmapSet() (*shared.IdmapSet, error)

	TemplateApply(trigger string) error
	ExportToTar(snap string, w io.Writer) error

	Checkpoint(opts lxc.CheckpointOptions) error
	StartFromMigration(imagesDir string) error

	// TODO: Remove every use of this and remove it.
	LXContainerGet() *lxc.Container

	DetachMount(m shared.Device) error
	AttachMount(m shared.Device) error
	AttachUnixDev(dev shared.Device) error
	DetachUnixDev(dev shared.Device) error
}

// unmount and unlink any directories called $path/blk.$(mktemp)
func unmountTempBlocks(path string) {
	dents, err := ioutil.ReadDir(path)
	if err != nil {
		return
	}
	for _, f := range dents {
		bpath := f.Name()
		dpath := filepath.Join(path, bpath)
		if !strings.HasPrefix(bpath, "blk.") {
			continue
		}
		if err = syscall.Unmount(dpath, syscall.MNT_DETACH); err != nil {
			shared.Log.Warn("Failed to unmount block device", log.Ctx{"error": err, "path": dpath})
			continue
		}
		if err = os.Remove(dpath); err != nil {
			shared.Log.Warn("Failed to unlink block device mountpoint", log.Ctx{"error": err, "path": dpath})
			continue
		}
	}
}

func getMountOptions(m shared.Device) ([]string, bool, bool) {
	opts := []string{}
	readonly := false
	if m["readonly"] == "1" || m["readonly"] == "true" {
		readonly = true
		opts = append(opts, "ro")
	}
	optional := false
	if m["optional"] == "1" || m["optional"] == "true" {
		optional = true
		opts = append(opts, "optional")
	}

	return opts, readonly, optional
}

/*
 * This is called at container startup to mount any block
 * devices (since a container with idmap cannot do so)
 */
func mountTmpBlockdev(cntPath string, d shared.Device) ([]string, error) {
	source := d["source"]
	fstype, err := shared.BlockFsDetect(source)
	if err != nil {
		return []string{}, fmt.Errorf("Error setting up %s: %s", source, err)
	}
	opts, readonly, optional := getMountOptions(d)

	// Mount blockdev into $containerdir/blk.$(mktemp)
	fnam := fmt.Sprintf("blk.%s", strings.Replace(source, "/", "-", -1))
	blkmnt := filepath.Join(cntPath, fnam)
	syscall.Unmount(blkmnt, syscall.MNT_DETACH)
	os.Remove(blkmnt)
	if err = os.Mkdir(blkmnt, 0660); err != nil {
		if optional {
			shared.Log.Warn("Failed to create block device mount directory",
				log.Ctx{"error": err, "source": source})
			return []string{}, nil
		}
		return []string{}, fmt.Errorf("Unable to create mountpoint for blockdev %s: %s", source, err)
	}
	flags := 0
	if readonly {
		flags |= syscall.MS_RDONLY
	}
	if err = syscall.Mount(source, blkmnt, fstype, uintptr(flags), ""); err != nil {
		if optional {
			shared.Log.Warn("Failed to mount block device", log.Ctx{"error": err, "source": source})
			return []string{}, nil
		}
		return []string{}, fmt.Errorf("Unable to prepare blockdev mount for %s: %s", source, err)
	}

	opts = append(opts, "bind")
	sb, err := os.Stat(source)
	if err == nil {
		if sb.IsDir() {
			opts = append(opts, "create=dir")
		} else {
			opts = append(opts, "create=file")
		}
	}
	tgtpath := d["path"]
	for len(tgtpath) > 0 && filepath.IsAbs(tgtpath) {
		tgtpath = tgtpath[1:]
	}
	if len(tgtpath) == 0 {
		if optional {
			shared.Log.Warn("Invalid mount target", log.Ctx{"target": d["path"]})
			return []string{}, nil
		}
		return []string{}, fmt.Errorf("Invalid mount target %s", d["path"])
	}
	mtab := fmt.Sprintf("%s %s %s %s 0 0", blkmnt, tgtpath, "none", strings.Join(opts, ","))
	shared.Debugf("adding mount entry for %s: .%s.\n", d["source"], mtab)

	return []string{"lxc.mount.entry", mtab}, nil
}

func containerLXDCreateAsEmpty(d *Daemon, name string,
	args containerLXDArgs) (container, error) {

	// Create the container
	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty storage
	if err := c.storage.ContainerCreate(c); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerLXDCreateFromImage(d *Daemon, name string,
	args containerLXDArgs, hash string) (container, error) {

	// Create the container
	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	if err := dbImageLastAccessUpdate(d.db, hash); err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Now create the storage from an image
	if err := c.storage.ContainerCreateFromImage(c, hash); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerLXDCreateAsCopy(d *Daemon, name string,
	args containerLXDArgs, sourceContainer container) (container, error) {

	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	if err := c.ConfigReplace(args); err != nil {
		c.Delete()
		return nil, err
	}

	if err := c.storage.ContainerCopy(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerLXDCreateAsSnapshot(d *Daemon, name string,
	args containerLXDArgs, sourceContainer container,
	stateful bool) (container, error) {

	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	c.storage = sourceContainer.Storage()
	if err := c.storage.ContainerSnapshotCreate(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	if stateful {
		stateDir := c.StateDir()
		err = os.MkdirAll(stateDir, 0700)
		if err != nil {
			c.Delete()
			return nil, err
		}

		// TODO - shouldn't we freeze for the duration of rootfs snapshot below?
		if !sourceContainer.IsRunning() {
			c.Delete()
			return nil, fmt.Errorf("Container not running")
		}
		opts := lxc.CheckpointOptions{Directory: stateDir, Stop: true, Verbose: true}
		err = sourceContainer.Checkpoint(opts)
		err2 := CollectCRIULogFile(sourceContainer, stateDir, "snapshot", "dump")
		if err2 != nil {
			shared.Log.Warn("failed to collect criu log file", log.Ctx{"error": err2})
		}

		if err != nil {
			c.Delete()
			return nil, err
		}
	}

	return c, nil
}

func validContainerName(name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		return fmt.Errorf(
			"The character '%s' is reserved for snapshots.",
			shared.SnapshotDelimiter)
	}

	return nil
}

func containerLXDCreateInternal(
	d *Daemon, name string, args containerLXDArgs) (*containerLXD, error) {

	shared.Log.Info(
		"Container create",
		log.Ctx{
			"container":  name,
			"isSnapshot": args.Ctype == cTypeSnapshot})

	if args.Ctype != cTypeSnapshot {
		if err := validContainerName(name); err != nil {
			return nil, err
		}
	}

	path := containerPath(name, args.Ctype == cTypeSnapshot)
	if shared.PathExists(path) {
		return nil, fmt.Errorf(
			"The container already exists on disk, container: '%s', path: '%s'",
			name,
			path)
	}

	if args.Profiles == nil {
		args.Profiles = []string{"default"}
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
	}

	if args.Devices == nil {
		args.Devices = shared.Devices{}
	}

	profiles, err := dbProfiles(d.db)
	if err != nil {
		return nil, err
	}

	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile '%s' doesn't exist", profile)
		}
	}

	id, err := dbContainerCreate(d.db, name, args)
	if err != nil {
		return nil, err
	}

	shared.Log.Debug(
		"Container created in the DB",
		log.Ctx{"container": name, "id": id})

	baseConfig := map[string]string{}
	if err := shared.DeepCopy(&args.Config, &baseConfig); err != nil {
		return nil, err
	}
	baseDevices := shared.Devices{}
	if err := shared.DeepCopy(&args.Devices, &baseDevices); err != nil {
		return nil, err
	}

	c := &containerLXD{
		daemon:       d,
		id:           id,
		name:         name,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		config:       args.Config,
		profiles:     args.Profiles,
		devices:      args.Devices,
		cType:        args.Ctype,
		baseConfig:   baseConfig,
		baseDevices:  baseDevices}

	// No need to detect storage here, its a new container.
	c.storage = d.Storage

	if err := c.init(); err != nil {
		c.Delete() // Delete the container from the DB.
		return nil, err
	}

	idmap := c.IdmapSet()

	var jsonIdmap string
	if idmap != nil {
		idmapBytes, err := json.Marshal(idmap.Idmap)
		if err != nil {
			c.Delete()
			return nil, err
		}
		jsonIdmap = string(idmapBytes)
	} else {
		jsonIdmap = "[]"
	}

	err = c.ConfigKeySet("volatile.last_state.idmap", jsonIdmap)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerLXDLoad(d *Daemon, name string) (container, error) {
	shared.Log.Debug("Container load", log.Ctx{"container": name})

	args, err := dbContainerGet(d.db, name)
	if err != nil {
		return nil, err
	}

	baseConfig := map[string]string{}
	if err := shared.DeepCopy(&args.Config, &baseConfig); err != nil {
		return nil, err
	}
	baseDevices := shared.Devices{}
	if err := shared.DeepCopy(&args.Devices, &baseDevices); err != nil {
		return nil, err
	}

	c := &containerLXD{
		daemon:       d,
		id:           args.ID,
		name:         name,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		config:       args.Config,
		profiles:     args.Profiles,
		devices:      args.Devices,
		cType:        args.Ctype,
		baseConfig:   baseConfig,
		baseDevices:  baseDevices}

	s, err := storageForFilename(d, shared.VarPath("containers", strings.Split(name, "/")[0]))
	if err != nil {
		shared.Log.Warn("Couldn't detect storage.", log.Ctx{"container": c.Name()})
		c.storage = d.Storage
	} else {
		c.storage = s
	}

	if err := c.init(); err != nil {
		return nil, err
	}

	return c, nil
}

// init prepares the LXContainer for this LXD Container
// TODO: This gets called on each load of the container,
//       we might be able to split this is up into c.Start().
func (c *containerLXD) init() error {
	templateConfBase := "ubuntu"
	templateConfDir := os.Getenv("LXD_LXC_TEMPLATE_CONFIG")
	if templateConfDir == "" {
		templateConfDir = "/usr/share/lxc/config"
	}

	cc, err := lxc.NewContainer(c.Name(), c.daemon.lxcpath)
	if err != nil {
		return err
	}
	c.c = cc

	logfile := c.LogFilePath()
	if err := os.MkdirAll(filepath.Dir(logfile), 0700); err != nil {
		return err
	}

	if err = c.c.SetLogFile(logfile); err != nil {
		return err
	}

	personality, err := shared.ArchitecturePersonality(c.architecture)
	if err == nil {
		if err := setConfigItem(c, "lxc.arch", personality); err != nil {
			return err
		}
	}

	err = setConfigItem(c, "lxc.include", fmt.Sprintf("%s/%s.common.conf", templateConfDir, templateConfBase))
	if err != nil {
		return err
	}

	if err := setConfigItem(c, "lxc.rootfs", c.RootfsPath()); err != nil {
		return err
	}
	if err := setConfigItem(c, "lxc.loglevel", "0"); err != nil {
		return err
	}
	if err := setConfigItem(c, "lxc.utsname", c.Name()); err != nil {
		return err
	}
	if err := setConfigItem(c, "lxc.tty", "0"); err != nil {
		return err
	}
	if err := setupDevLxdMount(c); err != nil {
		return err
	}

	for _, p := range c.profiles {
		if err := c.applyProfile(p); err != nil {
			return err
		}
	}

	// base per-container config should override profile config, so we apply it second
	if err := c.applyConfig(c.baseConfig); err != nil {
		return err
	}

	if !c.IsPrivileged() || runningInUserns {
		err = setConfigItem(c, "lxc.include", fmt.Sprintf("%s/%s.userns.conf", templateConfDir, templateConfBase))
		if err != nil {
			return err
		}
	}

	if c.IsNesting() {
		shared.Debugf("Setting up %s for nesting", c.name)
		orig := c.c.ConfigItem("lxc.mount.auto")
		auto := ""
		if len(orig) == 1 {
			auto = orig[0]
		}
		if !strings.Contains(auto, "cgroup") {
			auto = fmt.Sprintf("%s %s", auto, "cgroup:mixed")
			err = setConfigItem(c, "lxc.mount.auto", auto)
			if err != nil {
				return err
			}
		}
		/*
		 * mount extra /proc and /sys to work around kernel
		 * restrictions on remounting them when covered
		 */
		err = setConfigItem(c, "lxc.mount.entry", "proc dev/.lxc/proc proc create=dir,optional")
		if err != nil {
			return err
		}
		err = setConfigItem(c, "lxc.mount.entry", "sys dev/.lxc/sys sysfs create=dir,optional")
		if err != nil {
			return err
		}
	}

	/*
	 * Until stacked apparmor profiles are possible, we have to run nested
	 * containers unconfined
	 */
	if aaEnabled {
		if aaConfined() {
			curProfile := aaProfile()
			shared.Debugf("Running %s in current profile %s (nested container)", c.name, curProfile)
			curProfile = strings.TrimSuffix(curProfile, " (enforce)")
			if err := setConfigItem(c, "lxc.aa_profile", curProfile); err != nil {
				return err
			}
		} else if err := setConfigItem(c, "lxc.aa_profile", AAProfileFull(c)); err != nil {
			return err
		}
	}

	if err := setConfigItem(c, "lxc.seccomp", SeccompProfilePath(c)); err != nil {
		return err
	}

	if err := c.setupMacAddresses(); err != nil {
		return err
	}

	// Allow overwrites of devices
	for k, v := range c.baseDevices {
		c.devices[k] = v
	}

	if !c.IsPrivileged() {
		if c.daemon.IdmapSet == nil {
			return fmt.Errorf("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported.")
		}
		c.idmapset = c.daemon.IdmapSet // TODO - per-tenant idmaps
	}

	if err := c.applyIdmapSet(); err != nil {
		return err
	}

	if err := c.applyPostDeviceConfig(); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) RenderState() (*shared.ContainerState, error) {
	statusCode := shared.FromLXCState(int(c.c.State()))
	status := shared.ContainerStatus{
		Status:     statusCode.String(),
		StatusCode: statusCode,
	}

	if c.IsRunning() {
		pid := c.InitPID()
		status.Init = pid
		status.Processcount = c.processcountGet()
		status.Ips = c.iPsGet()
	}

	return &shared.ContainerState{
		Name:            c.name,
		Profiles:        c.profiles,
		Config:          c.baseConfig,
		ExpandedConfig:  c.config,
		Status:          status,
		Devices:         c.baseDevices,
		ExpandedDevices: c.devices,
		Ephemeral:       c.ephemeral,
	}, nil
}

func (c *containerLXD) insertMount(source, target, fstype string, flags int, options string) error {
	pid := c.c.InitPid()
	if pid == -1 { // container not running - we're done
		return nil
	}

	// now live-mount
	var tmpMount string
	var err error
	if shared.IsDir(source) {
		tmpMount, err = ioutil.TempDir(shared.VarPath("shmounts", c.name), "lxdmount_")
	} else {
		f, err := ioutil.TempFile(shared.VarPath("shmounts", c.name), "lxdmount_")
		if err == nil {
			tmpMount = f.Name()
			f.Close()
		}
	}
	if err != nil {
		return err
	}

	err = syscall.Mount(source, tmpMount, fstype, uintptr(flags), "")
	if err != nil {
		return err
	}

	mntsrc := filepath.Join("/dev/.lxd-mounts", filepath.Base(tmpMount))
	// finally we need to move-mount this in the container
	pidstr := fmt.Sprintf("%d", pid)
	err = exec.Command(os.Args[0], "forkmount", pidstr, mntsrc, target).Run()
	syscall.Unmount(tmpMount, syscall.MNT_DETACH) // in case forkmount failed
	os.Remove(tmpMount)

	return err
}

func (c *containerLXD) createUnixDevice(m shared.Device) (string, string, error) {
	devname := m["path"]
	if !filepath.IsAbs(devname) {
		devname = filepath.Join("/", devname)
	}

	// target must be a relative path, so that lxc will DTRT
	tgtname := m["path"]
	for len(tgtname) > 0 && filepath.IsAbs(tgtname) {
		tgtname = tgtname[1:]
	}
	if len(tgtname) == 0 {
		return "", "", fmt.Errorf("Failed to interpret path: %s", devname)
	}

	var err error
	var major, minor int
	if m["major"] == "" && m["minor"] == "" {
		major, minor, err = getDev(devname)
		if err != nil {
			return "", "", fmt.Errorf("Device does not exist: %s", devname)
		}
	} else if m["major"] == "" || m["minor"] == "" {
		return "", "", fmt.Errorf("Both major and minor must be supplied for devices")
	} else {
		/* ok we have a major:minor and need to create it */
		major, err = strconv.Atoi(m["major"])
		if err != nil {
			return "", "", fmt.Errorf("Bad major %s in device %s", m["major"], m["path"])
		}
		minor, err = strconv.Atoi(m["minor"])
		if err != nil {
			return "", "", fmt.Errorf("Bad minor %s in device %s", m["minor"], m["path"])
		}
	}

	name := strings.Replace(m["path"], "/", "-", -1)
	devpath := path.Join(c.Path(""), name)
	mode := os.FileMode(0660)
	if m["type"] == "unix-block" {
		mode |= syscall.S_IFBLK
	} else {
		mode |= syscall.S_IFCHR
	}

	if m["mode"] != "" {
		tmp, err := devModeOct(m["mode"])
		if err != nil {
			return "", "", fmt.Errorf("Bad mode %s in device %s", m["mode"], m["path"])
		}
		mode = os.FileMode(tmp)
	}

	os.Remove(devpath)
	if err := syscall.Mknod(devpath, uint32(mode), minor|(major<<8)); err != nil {
		if shared.PathExists(devname) {
			return devname, tgtname, nil
		}
		return "", "", fmt.Errorf("Failed to create device %s for %s: %s", devpath, m["path"], err)
	}

	if err := c.idmapset.ShiftFile(devpath); err != nil {
		// uidshift failing is weird, but not a big problem.  Log and proceed
		shared.Debugf("Failed to uidshift device %s: %s\n", m["path"], err)
	}

	return devpath, tgtname, nil
}

func (c *containerLXD) setupUnixDev(m shared.Device) error {
	source, target, err := c.createUnixDevice(m)
	if err != nil {
		return fmt.Errorf("Failed to setup device %s: %s", m["path"], err)
	}

	options, err := devGetOptions(m)
	if err != nil {
		return err
	}

	if c.c.Running() {
		// insert mount from 'source' to 'target'
		err := c.insertMount(source, target, "none", syscall.MS_BIND, options)
		if err != nil {
			return fmt.Errorf("Failed to add mount for device %s: %s", m["path"], err)
		}

		// add the new device cgroup rule
		entry, err := deviceCgroupInfo(m)
		if err != nil {
			return fmt.Errorf("Failed to add cgroup rule for device %s: %s", m["path"], err)
		}
		if err := c.c.SetCgroupItem("devices.allow", entry); err != nil {
			return fmt.Errorf("Failed to add cgroup rule %s for device %s: %s", entry, m["path"], err)
		}
	}

	entry := fmt.Sprintf("%s %s none %s", source, target, options)
	return c.c.SetConfigItem("lxc.mount.entry", entry)
}

func (c *containerLXD) Start() error {
	if c.IsRunning() {
		return fmt.Errorf("the container is already running")
	}

	// Start the storage for this container
	if err := c.StorageStart(); err != nil {
		return err
	}

	/* (Re)Load the AA profile; we set it in the container's config above
	 * in init()
	 */
	if err := AALoadProfile(c); err != nil {
		c.StorageStop()
		return err
	}

	if err := SeccompCreateProfile(c); err != nil {
		c.StorageStop()
		return err
	}

	if err := c.mountShared(); err != nil {
		return err
	}

	/*
	 * add the lxc.* entries for the configured devices,
	 * and create if necessary
	 */
	if err := c.applyDevices(); err != nil {
		return err
	}

	f, err := ioutil.TempFile("", "lxd_lxc_startconfig_")
	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		return err
	}
	configPath := f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(configPath)
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		return err
	}
	f.Close()

	err = c.c.SaveConfigFile(configPath)
	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		return err
	}

	err = c.TemplateApply("start")
	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		return err
	}

	/* Deal with idmap changes */
	idmap := c.IdmapSet()

	lastIdmap, err := c.LastIdmapSet()
	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		return err
	}

	var jsonIdmap string
	if idmap != nil {
		idmapBytes, err := json.Marshal(idmap.Idmap)
		if err != nil {
			unmountTempBlocks(c.Path(""))
			c.StorageStop()
			return err
		}
		jsonIdmap = string(idmapBytes)
	} else {
		jsonIdmap = "[]"
	}

	if !reflect.DeepEqual(idmap, lastIdmap) {
		shared.Debugf("Container idmap changed, remapping")

		if lastIdmap != nil {
			if err := lastIdmap.UnshiftRootfs(c.RootfsPath()); err != nil {
				unmountTempBlocks(c.Path(""))
				c.StorageStop()
				return err
			}
		}

		if idmap != nil {
			if err := idmap.ShiftRootfs(c.RootfsPath()); err != nil {
				unmountTempBlocks(c.Path(""))
				c.StorageStop()
				return err
			}
		}
	}

	err = c.ConfigKeySet("volatile.last_state.idmap", jsonIdmap)

	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		return err
	}

	/* Actually start the container */
	out, err := exec.Command(
		os.Args[0],
		"forkstart",
		c.name,
		c.daemon.lxcpath,
		configPath).CombinedOutput()

	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			shared.Debugf("forkstart: %s", line)
		}
	}

	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
		err = fmt.Errorf(
			"Error calling 'lxd forkstart %s %s %s': err='%v'",
			c.name,
			c.daemon.lxcpath,
			path.Join(c.LogPath(), "lxc.conf"),
			err)
	}

	if err == nil && c.ephemeral == true {
		containerWatchEphemeral(c.daemon, c)
	}

	if err != nil {
		unmountTempBlocks(c.Path(""))
		c.StorageStop()
	}
	return err
}

func (c *containerLXD) Reboot() error {
	return c.c.Reboot()
}

func (c *containerLXD) Freeze() error {
	return c.c.Freeze()
}

func (c *containerLXD) IsNesting() bool {
	switch strings.ToLower(c.config["security.nesting"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *containerLXD) IsPrivileged() bool {
	switch strings.ToLower(c.config["security.privileged"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *containerLXD) IsRunning() bool {
	return c.c.Running()
}

func (c *containerLXD) IsFrozen() bool {
	return c.State() == "FROZEN"
}

func (c *containerLXD) Shutdown(timeout time.Duration) error {
	if err := c.c.Shutdown(timeout); err != nil {
		return err
	}

	// Stop the storage for this container
	if err := c.StorageStop(); err != nil {
		return err
	}

	unmountTempBlocks(c.Path(""))

	if err := AAUnloadProfile(c); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) Stop() error {
	// Attempt to freeze the container first, helps massively with fork bombs
	c.c.Freeze()

	if err := c.c.Stop(); err != nil {
		return err
	}

	// Stop the storage for this container
	if err := c.StorageStop(); err != nil {
		return err
	}

	// Clean up any mounts from previous runs
	unmountTempBlocks(c.Path(""))

	if err := AAUnloadProfile(c); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) Unfreeze() error {
	return c.c.Unfreeze()
}

func (c *containerLXD) StorageFromImage(hash string) error {
	return c.storage.ContainerCreateFromImage(c, hash)
}

func (c *containerLXD) StorageFromNone() error {
	return c.storage.ContainerCreate(c)
}

func (c *containerLXD) StorageStart() error {
	return c.storage.ContainerStart(c)
}

func (c *containerLXD) StorageStop() error {
	return c.storage.ContainerStop(c)
}

func (c *containerLXD) Storage() storage {
	return c.storage
}

func (c *containerLXD) Restore(sourceContainer container) error {
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
					"container": c.Name(),
					"err":       err})
			return err
		}
		shared.Log.Debug(
			"RESTORE => Stopped container",
			log.Ctx{"container": c.Name()})
	}

	// Restore the FS.
	// TODO: I switched the FS and config restore, think thats the correct way
	// (pcdummy)
	err := c.storage.ContainerRestore(c, sourceContainer)

	if err != nil {
		shared.Log.Error("RESTORE => Restoring the filesystem failed",
			log.Ctx{
				"source":      sourceContainer.Name(),
				"destination": c.Name()})
		return err
	}

	args := containerLXDArgs{
		Ctype:        cTypeRegular,
		Config:       sourceContainer.Config(),
		Profiles:     sourceContainer.Profiles(),
		Ephemeral:    sourceContainer.IsEphemeral(),
		Architecture: sourceContainer.Architecture(),
		Devices:      sourceContainer.Devices(),
	}
	err = c.ConfigReplace(args)
	if err != nil {
		shared.Log.Error("RESTORE => Restore of the configuration failed",
			log.Ctx{
				"source":      sourceContainer.Name(),
				"destination": c.Name()})

		return err
	}

	if wasRunning {
		c.Start()
	}

	return nil
}

func (c *containerLXD) Delete() error {
	shared.Log.Debug("containerLXD.Delete", log.Ctx{"c.name": c.Name(), "type": c.cType})

	switch c.cType {
	case cTypeRegular:
		if err := containerDeleteSnapshots(c.daemon, c.Name()); err != nil {
			return err
		}

		if err := c.storage.ContainerDelete(c); err != nil {
			return err
		}
		unmountTempBlocks(c.Path(""))
	case cTypeSnapshot:
		if err := c.storage.ContainerSnapshotDelete(c); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Unknown cType: %d", c.cType)
	}

	if err := dbContainerRemove(c.daemon.db, c.Name()); err != nil {
		return err
	}

	AADeleteProfile(c)
	SeccompDeleteProfile(c)

	return nil
}

func (c *containerLXD) Rename(newName string) error {
	oldName := c.Name()

	if !c.IsSnapshot() && !shared.ValidHostname(newName) {
		return fmt.Errorf("Invalid container name")
	}

	if c.IsRunning() {
		return fmt.Errorf("renaming of running container not allowed")
	}

	if c.IsSnapshot() {
		if err := c.storage.ContainerSnapshotRename(c, newName); err != nil {
			return err
		}
	} else {
		if err := c.storage.ContainerRename(c, newName); err != nil {
			return err
		}
	}

	if err := dbContainerRename(c.daemon.db, oldName, newName); err != nil {
		return err
	}

	results, err := dbContainerGetSnapshots(c.daemon.db, oldName)
	if err != nil {
		return err
	}

	for _, sname := range results {
		sc, err := containerLXDLoad(c.daemon, sname)
		if err != nil {
			shared.Log.Error(
				"containerDeleteSnapshots: Failed to load the snapshotcontainer",
				log.Ctx{"container": oldName, "snapshot": sname})

			continue
		}
		baseSnapName := filepath.Base(sname)
		newSnapshotName := newName + shared.SnapshotDelimiter + baseSnapName
		if err := sc.Rename(newSnapshotName); err != nil {
			shared.Log.Error(
				"containerDeleteSnapshots: Failed to rename a snapshotcontainer",
				log.Ctx{"container": oldName, "snapshot": sname, "err": err})
		}
	}

	c.name = newName

	// Recreate the LX Container
	c.c = nil
	c.init()

	return nil
}

func (c *containerLXD) IsEphemeral() bool {
	return c.ephemeral
}

func (c *containerLXD) IsSnapshot() bool {
	return c.cType == cTypeSnapshot
}

func (c *containerLXD) ID() int {
	return c.id
}

func (c *containerLXD) Name() string {
	return c.name
}

func (c *containerLXD) Architecture() int {
	return c.architecture
}

func (c *containerLXD) Path(newName string) string {
	if newName != "" {
		return containerPath(newName, c.IsSnapshot())
	}

	return containerPath(c.Name(), c.IsSnapshot())
}

func (c *containerLXD) RootfsPath() string {
	return path.Join(c.Path(""), "rootfs")
}

func (c *containerLXD) TemplatesPath() string {
	return path.Join(c.Path(""), "templates")
}

func (c *containerLXD) StateDir() string {
	return path.Join(c.Path(""), "state")
}

func (c *containerLXD) LogPath() string {
	return shared.LogPath(c.Name())
}

func (c *containerLXD) LogFilePath() string {
	return filepath.Join(c.LogPath(), "lxc.log")
}

func (c *containerLXD) InitPID() int {
	return c.c.InitPid()
}

func (c *containerLXD) State() string {
	return c.c.State().String()
}

func (c *containerLXD) IdmapSet() *shared.IdmapSet {
	return c.idmapset
}

func (c *containerLXD) LastIdmapSet() (*shared.IdmapSet, error) {
	config := c.Config()
	lastJsonIdmap := config["volatile.last_state.idmap"]

	if lastJsonIdmap == "" {
		return c.IdmapSet(), nil
	}

	lastIdmap := new(shared.IdmapSet)
	err := json.Unmarshal([]byte(lastJsonIdmap), &lastIdmap.Idmap)
	if err != nil {
		return nil, err
	}

	if len(lastIdmap.Idmap) == 0 {
		return nil, nil
	}

	return lastIdmap, nil
}

func (c *containerLXD) ConfigKeySet(key string, value string) error {
	c.baseConfig[key] = value

	args := containerLXDArgs{
		Ctype:        c.cType,
		Config:       c.baseConfig,
		Profiles:     c.profiles,
		Ephemeral:    c.ephemeral,
		Architecture: c.architecture,
		Devices:      c.baseDevices,
	}

	return c.ConfigReplace(args)
}

func (c *containerLXD) LXContainerGet() *lxc.Container {
	return c.c
}

// ConfigReplace replaces the config of container and tries to live apply
// the new configuration.
func (c *containerLXD) ConfigReplace(newContainerArgs containerLXDArgs) error {
	/* check to see that the config actually applies to the container
	 * successfully before saving it. in particular, raw.lxc and
	 * raw.apparmor need to be parsed once to make sure they make sense.
	 */
	preDevList := c.devices

	if err := c.applyConfig(newContainerArgs.Config); err != nil {
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
			log.Ctx{"name": c.Name()})
		tx.Rollback()
		return err
	}

	if err = dbContainerConfigInsert(tx, c.id, newContainerArgs.Config); err != nil {
		shared.Debugf("Error inserting configuration for container %s: %s", c.Name(), err)
		tx.Rollback()
		return err
	}

	/* handle profiles */
	if emptyProfile(newContainerArgs.Profiles) {
		_, err := tx.Exec("DELETE from containers_profiles where container_id=?", c.id)
		if err != nil {
			tx.Rollback()
			return err
		}
	} else {
		if err := dbContainerProfilesInsert(tx, c.id, newContainerArgs.Profiles); err != nil {

			tx.Rollback()
			return err
		}
	}

	err = dbDevicesAdd(tx, "container", int64(c.id), newContainerArgs.Devices)
	if err != nil {
		tx.Rollback()
		return err
	}

	if err := c.applyPostDeviceConfig(); err != nil {
		tx.Rollback()
		return err
	}

	c.baseConfig = newContainerArgs.Config
	c.baseDevices = newContainerArgs.Devices

	/* Let's try to load the apparmor profile again, in case the
	 * raw.apparmor config was changed (or deleted). Make sure we do this
	 * before commit, in case it fails because the user screwed something
	 * up so we can roll back and not hose their container.
	 *
	 * For containers that aren't running, we just want to parse the new
	 * profile; this is because this code is called during the start
	 * process after the profile is loaded but before the container starts,
	 * which will cause a container start to fail. If the container is
	 * running, we /do/ want to reload the profile, because we want the
	 * changes to take effect immediately.
	 */
	if !c.IsRunning() {
		AAParseProfile(c)
		return txCommit(tx)
	}

	if err := AALoadProfile(c); err != nil {
		tx.Rollback()
		return err
	}

	if err := txCommit(tx); err != nil {
		return err
	}

	// add devices from new profile list to the desired goal set
	for _, p := range c.profiles {
		profileDevs, err := dbDevices(c.daemon.db, p, true)
		if err != nil {
			return fmt.Errorf("Error reading devices from profile '%s': %v", p, err)
		}

		newContainerArgs.Devices.ExtendFromProfile(preDevList, profileDevs)
	}

	tx, err = dbBegin(c.daemon.db)
	if err != nil {
		return err
	}

	if err := devicesApplyDeltaLive(tx, c, preDevList, newContainerArgs.Devices); err != nil {
		return err
	}

	if err := txCommit(tx); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) Config() map[string]string {
	return c.config
}

func (c *containerLXD) Devices() shared.Devices {
	return c.devices
}

func (c *containerLXD) Profiles() []string {
	return c.profiles
}

/*
 * Export the container to a unshifted tarfile containing:
 * dir/
 *     metadata.yaml
 *     rootfs/
 */
func (c *containerLXD) ExportToTar(snap string, w io.Writer) error {
	if snap == "" && c.IsRunning() {
		return fmt.Errorf("Cannot export a running container as image")
	}

	if err := c.StorageStart(); err != nil {
		return err
	}
	defer c.StorageStop()

	idmap, err := c.LastIdmapSet()
	if err != nil {
		return err
	}

	if idmap != nil {
		if err := idmap.UnshiftRootfs(c.RootfsPath()); err != nil {
			return err
		}

		defer idmap.ShiftRootfs(c.RootfsPath())
	}

	tw := tar.NewWriter(w)

	// keep track of the first path we saw for each path with nlink>1
	linkmap := map[uint64]string{}

	cDir := c.Path("")

	// Path inside the tar image is the pathname starting after cDir
	offset := len(cDir) + 1

	writeToTar := func(path string, fi os.FileInfo, err error) error {
		if err := c.tarStoreFile(linkmap, offset, tw, path, fi); err != nil {
			shared.Debugf("Error tarring up %s: %s", path, err)
			return err
		}
		return nil
	}

	fnam := filepath.Join(cDir, "metadata.yaml")
	if shared.PathExists(fnam) {
		fi, err := os.Lstat(fnam)
		if err != nil {
			shared.Debugf("Error statting %s during exportToTar", fnam)
			tw.Close()
			return err
		}
		if err := c.tarStoreFile(linkmap, offset, tw, fnam, fi); err != nil {
			shared.Debugf("Error writing to tarfile: %s", err)
			tw.Close()
			return err
		}
	}
	fnam = filepath.Join(cDir, "rootfs")
	filepath.Walk(fnam, writeToTar)
	fnam = filepath.Join(cDir, "templates")
	if shared.PathExists(fnam) {
		filepath.Walk(fnam, writeToTar)
	}
	return tw.Close()
}

func (c *containerLXD) TemplateApply(trigger string) error {
	fname := path.Join(c.Path(""), "metadata.yaml")

	if !shared.PathExists(fname) {
		return nil
	}

	content, err := ioutil.ReadFile(fname)
	if err != nil {
		return err
	}

	metadata := new(imageMetadata)
	err = yaml.Unmarshal(content, &metadata)

	if err != nil {
		return fmt.Errorf("Could not parse %s: %v", fname, err)
	}

	for filepath, template := range metadata.Templates {
		var w *os.File

		found := false
		for _, tplTrigger := range template.When {
			if tplTrigger == trigger {
				found = true
				break
			}
		}

		if !found {
			continue
		}

		fullpath := shared.VarPath("containers", c.name, "rootfs", strings.TrimLeft(filepath, "/"))

		if shared.PathExists(fullpath) {
			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}
		} else {
			uid := 0
			gid := 0
			if !c.IsPrivileged() {
				uid, gid = c.idmapset.ShiftIntoNs(0, 0)
			}
			shared.MkdirAllOwner(path.Dir(fullpath), 0755, uid, gid)

			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}

			if !c.IsPrivileged() {
				w.Chown(uid, gid)
			}
			w.Chmod(0644)
		}

		tplString, err := ioutil.ReadFile(shared.VarPath("containers", c.name, "templates", template.Template))
		if err != nil {
			return err
		}

		tpl, err := pongo2.FromString("{% autoescape off %}" + string(tplString) + "{% endautoescape %}")
		if err != nil {
			return err
		}

		containerMeta := make(map[string]string)
		containerMeta["name"] = c.name
		containerMeta["architecture"], _ = shared.ArchitectureName(c.architecture)

		if c.ephemeral {
			containerMeta["ephemeral"] = "true"
		} else {
			containerMeta["ephemeral"] = "false"
		}

		if c.IsPrivileged() {
			containerMeta["privileged"] = "true"
		} else {
			containerMeta["privileged"] = "false"
		}

		configGet := func(confKey, confDefault *pongo2.Value) *pongo2.Value {
			val, ok := c.config[confKey.String()]
			if !ok {
				return confDefault
			}

			return pongo2.AsValue(strings.TrimRight(val, "\r\n"))
		}

		tpl.ExecuteWriter(pongo2.Context{"trigger": trigger,
			"path":       filepath,
			"container":  containerMeta,
			"config":     c.config,
			"devices":    c.devices,
			"properties": template.Properties,
			"config_get": configGet}, w)
	}

	return nil
}

func (c *containerLXD) DetachMount(m shared.Device) error {
	// TODO - in case of reboot, we should remove the lxc.mount.entry.  Trick
	// is, we can't d.c.ClearConfigItem bc that will clear all the keys.  So
	// we should get the full list, clear, then reinsert all but the one we're
	// removing
	shared.Debugf("Mounts detach not yet implemented")

	pid := c.c.InitPid()
	if pid == -1 { // container not running
		return nil
	}
	pidstr := fmt.Sprintf("%d", pid)
	return exec.Command(os.Args[0], "forkumount", pidstr, m["path"]).Run()
}

/* This is called when adding a mount to a *running* container */
func (c *containerLXD) AttachMount(m shared.Device) error {
	dest := m["path"]
	source := m["source"]
	flags := 0

	sb, err := os.Stat(source)
	if err != nil {
		return err
	}

	opts, readonly, optional := getMountOptions(m)
	if readonly {
		flags |= syscall.MS_RDONLY
	}

	if shared.IsBlockdev(sb.Mode()) {
		fstype, err := shared.BlockFsDetect(source)
		if err != nil {
			if optional {
				shared.Log.Warn("Failed to detect fstype for block device",
					log.Ctx{"error": err, "source": source})
				return nil
			}
			return fmt.Errorf("Unable to detect fstype for %s: %s", source, err)
		}

		// Mount blockdev into $containerdir/blk.$(mktemp)
		fnam := fmt.Sprintf("blk.%s", strings.Replace(source, "/", "-", -1))
		blkmnt := filepath.Join(c.Path(""), fnam)
		syscall.Unmount(blkmnt, syscall.MNT_DETACH)
		os.Remove(blkmnt)
		if err = os.Mkdir(blkmnt, 0660); err != nil {
			if optional {
				return nil
			}
			return fmt.Errorf("Unable to create mountpoint for blockdev %s: %s", source, err)
		}
		if err = syscall.Mount(source, blkmnt, fstype, uintptr(flags), ""); err != nil {
			if optional {
				return nil
			}
			return fmt.Errorf("Unable to prepare blockdev mount for %s: %s", source, err)
		}

		source = blkmnt
		opts = append(opts, "create=dir")
	} else if sb.IsDir() {
		opts = append(opts, "create=dir")
	} else {
		opts = append(opts, "create=file")
	}
	opts = append(opts, "bind")
	flags |= syscall.MS_BIND
	optstr := strings.Join(opts, ",")

	entry := fmt.Sprintf("%s %s %s %s 0 0", source, dest, "none", optstr)
	if err := setConfigItem(c, "lxc.mount.entry", entry); err != nil {
		if optional {
			shared.Log.Warn("Failed to setup lxc mount for block device",
				log.Ctx{"error": err, "source": source})
		}
		return fmt.Errorf("Failed to set up lxc mount entry for %s: %s", m["source"], err)
	}

	err = c.insertMount(source, dest, "none", flags, optstr)
	if err != nil {
		if optional {
			shared.Log.Warn("Failed to insert mount for block device",
				log.Ctx{"error": err, "source": m["source"]})
			return nil
		}
		return fmt.Errorf("Failed to insert mount for %s: %s", m["source"], err)
	}
	return nil
}

func (c *containerLXD) applyConfig(config map[string]string) error {
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
				return fmt.Errorf("Bad cpu limit: %s", v)
			}
			cpuset := fmt.Sprintf("0-%d", vint-1)
			err = setConfigItem(c, "lxc.cgroup.cpuset.cpus", cpuset)
		case "limits.memory":
			err = setConfigItem(c, "lxc.cgroup.memory.limit_in_bytes", v)

		default:
			if strings.HasPrefix(k, "environment.") {
				setConfigItem(c, "lxc.environment", fmt.Sprintf("%s=%s", strings.TrimPrefix(k, "environment."), v))
			}

			/* Things like security.privileged need to be propagated */
			c.config[k] = v
		}
		if err != nil {
			shared.Debugf("Error setting %s: %q", k, err)
			return err
		}
	}
	return nil
}

func (c *containerLXD) applyPostDeviceConfig() error {
	// applies config that must be delayed until after devices are
	// instantiated, see bug #588 and fix #635

	if lxcConfig, ok := c.config["raw.lxc"]; ok {
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

func (c *containerLXD) applyProfile(p string) error {
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

		config[k] = v
	}

	newdevs, err := dbDevices(c.daemon.db, p, true)
	if err != nil {
		return err
	}
	for k, v := range newdevs {
		c.devices[k] = v
	}

	return c.applyConfig(config)
}

func (c *containerLXD) updateContainerHWAddr(k, v string) {
	for name, d := range c.devices {
		if d["type"] != "nic" {
			continue
		}

		for key := range c.config {
			device, err := extractInterfaceFromConfigName(key)
			if err == nil && device == name {
				d["hwaddr"] = v
				c.config[key] = v
				return
			}
		}
	}
}

func (c *containerLXD) setupMacAddresses() error {
	newConfigEntries := map[string]string{}

	for name, d := range c.devices {
		if d["type"] != "nic" {
			continue
		}

		found := false

		for key, val := range c.config {
			device, err := extractInterfaceFromConfigName(key)
			if err == nil && device == name {
				found = true
				d["hwaddr"] = val
			}
		}

		if !found {
			var hwaddr string
			var err error
			if d["hwaddr"] != "" {
				hwaddr, err = generateMacAddr(d["hwaddr"])
				if err != nil {
					return err
				}
			} else {
				hwaddr, err = generateMacAddr("00:16:3e:xx:xx:xx")
				if err != nil {
					return err
				}
			}

			if hwaddr != d["hwaddr"] {
				d["hwaddr"] = hwaddr
				key := fmt.Sprintf("volatile.%s.hwaddr", name)
				c.config[key] = hwaddr
				c.baseConfig[key] = hwaddr
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
					shared.Debugf("Error adding mac address to container")
					tx.Rollback()
					return err
				}
			} else if err != nil {
				tx.Rollback()
				return err
			} else if strings.Contains(racer, "x") {
				_, err = ustmt.Exec(v, c.id, k)
				if err != nil {
					shared.Debugf("Error updating mac address to container")
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

func (c *containerLXD) applyIdmapSet() error {
	if c.idmapset == nil {
		return nil
	}
	lines := c.idmapset.ToLxcString()
	for _, line := range lines {
		err := setConfigItem(c, "lxc.id_map", line+"\n")
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *containerLXD) applyDevices() error {
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

		configs, err := deviceToLxc(c.Path(""), d)
		if err != nil {
			return fmt.Errorf("Failed configuring device %s: %s", name, err)
		}
		for _, line := range configs {
			err := setConfigItem(c, line[0], line[1])
			if err != nil {
				return fmt.Errorf("Failed configuring device %s: %s", name, err)
			}
		}
		if d["type"] == "unix-block" || d["type"] == "unix-char" {
			if err := c.setupUnixDev(d); err != nil {
				return fmt.Errorf("Failed creating device %s: %s", d["name"], err)
			}
		}
	}
	return nil
}

func (c *containerLXD) iPsGet() []shared.Ip {
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

func (c *containerLXD) processcountGet() int {
	pid := c.c.InitPid()
	if pid == -1 { // container not running - we're done
		return 0
	}

	pids := make([]int, 0)

	pids = append(pids, pid)

	for i := 0; i < len(pids); i++ {
		fname := fmt.Sprintf("/proc/%d/task/%d/children", pids[i], pids[i])
		fcont, err := ioutil.ReadFile(fname)
		if err != nil {
			// the process terminated during execution of this loop
			continue
		}
		content := strings.Split(string(fcont), " ")
		for j := 0; j < len(content); j++ {
			pid, err := strconv.Atoi(content[j])
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	return len(pids)

}

func (c *containerLXD) tarStoreFile(linkmap map[uint64]string, offset int, tw *tar.Writer, path string, fi os.FileInfo) error {
	var err error
	var major, minor, nlink int
	var ino uint64

	link := ""
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(path)
		if err != nil {
			return err
		}
	}
	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	hdr.Name = path[offset:]
	if fi.IsDir() || fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		hdr.Size = 0
	} else {
		hdr.Size = fi.Size()
	}

	hdr.Uid, hdr.Gid, major, minor, ino, nlink, err = shared.GetFileStat(path)
	if err != nil {
		return fmt.Errorf("error getting file info: %s", err)
	}

	// unshift the id under /rootfs/ for unpriv containers
	if !c.IsPrivileged() && strings.HasPrefix(hdr.Name, "/rootfs") {
		hdr.Uid, hdr.Gid = c.idmapset.ShiftFromNs(hdr.Uid, hdr.Gid)
		if hdr.Uid == -1 || hdr.Gid == -1 {
			return nil
		}
	}
	if major != -1 {
		hdr.Devmajor = int64(major)
		hdr.Devminor = int64(minor)
	}

	// If it's a hardlink we've already seen use the old name
	if fi.Mode().IsRegular() && nlink > 1 {
		if firstpath, found := linkmap[ino]; found {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = firstpath
			hdr.Size = 0
		} else {
			linkmap[ino] = hdr.Name
		}
	}

	// todo - handle xattrs

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("error writing header: %s", err)
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("tarStoreFile: error opening file: %s", err)
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("error copying file %s", err)
		}
	}
	return nil
}

func (c *containerLXD) mkdirAllContainerRoot(path string, perm os.FileMode) error {
	var uid int
	var gid int
	if !c.IsPrivileged() {
		uid, gid = c.idmapset.ShiftIntoNs(0, 0)
		if uid == -1 {
			uid = 0
		}
		if gid == -1 {
			gid = 0
		}
	}
	return shared.MkdirAllOwner(path, perm, uid, gid)
}

func (c *containerLXD) mountShared() error {
	source := shared.VarPath("shmounts", c.Name())
	entry := fmt.Sprintf("%s dev/.lxd-mounts none bind,create=dir 0 0", source)
	if !shared.PathExists(source) {
		if err := c.mkdirAllContainerRoot(source, 0755); err != nil {
			return err
		}
	}
	return setConfigItem(c, "lxc.mount.entry", entry)
}

func (c *containerLXD) Checkpoint(opts lxc.CheckpointOptions) error {
	return c.c.Checkpoint(opts)
}

func (c *containerLXD) StartFromMigration(imagesDir string) error {
	f, err := ioutil.TempFile("", "lxd_lxc_migrateconfig_")
	if err != nil {
		return err
	}

	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	f.Close()
	os.Remove(f.Name())

	if err := c.c.SaveConfigFile(f.Name()); err != nil {
		return err
	}

	/* (Re)Load the AA profile; we set it in the container's config above
	 * in init()
	 */
	if err := AALoadProfile(c); err != nil {
		c.StorageStop()
		return err
	}

	if err := SeccompCreateProfile(c); err != nil {
		c.StorageStop()
		return err
	}

	cmd := exec.Command(
		os.Args[0],
		"forkmigrate",
		c.name,
		c.c.ConfigPath(),
		f.Name(),
		imagesDir,
	)

	return cmd.Run()
}
