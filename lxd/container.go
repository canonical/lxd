package main

import (
	"archive/tar"
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
	"syscall"
	"time"

	"gopkg.in/flosch/pongo2.v3"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/migration"
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
		membs := strings.SplitN(line, "=", 2)
		if strings.ToLower(strings.Trim(membs[0], " \t")) == "lxc.logfile" {
			return fmt.Errorf("setting lxc.logfile is not allowed")
		}
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

func containerPathGet(name string, isSnapshot bool) string {
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

	// These two will contain the containers data without profiles
	myConfig  map[string]string
	myDevices shared.Devices

	Storage storage
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

	IsPrivileged() bool
	IsRunning() bool
	IsEmpheral() bool
	IsSnapshot() bool

	IDGet() int
	NameGet() string
	ArchitectureGet() int
	PathGet(newName string) string
	RootfsPathGet() string
	TemplatesPathGet() string
	StateDirGet() string
	LogFilePathGet() string
	LogPathGet() string
	InitPidGet() (int, error)
	IdmapSetGet() (*shared.IdmapSet, error)
	ConfigGet() containerLXDArgs

	TemplateApply(trigger string) error
	ExportToTar(snap string, w io.Writer) error

	// TODO: Remove every use of this and remove it.
	LXContainerGet() (*lxc.Container, error)

	DetachMount(m shared.Device) error
	AttachMount(m shared.Device) error
}

func containerLXDCreateAsEmpty(d *Daemon, name string,
	args containerLXDArgs) (container, error) {

	// Create the container
	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty storage
	if err := c.Storage.ContainerCreate(c); err != nil {
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

	if err := dbUpdateImageLastAccess(d, hash); err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Now create the storage from an image
	if err := c.Storage.ContainerCreateFromImage(c, hash); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerLXDCreateAsCopy(d *Daemon, name string,
	args containerLXDArgs, sourceContainer container) (container, error) {

	// Create the container
	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	// Replace the config
	if err := c.ConfigReplace(sourceContainer.ConfigGet()); err != nil {
		c.Delete()
		return nil, err
	}

	// Now copy the source
	sourceContainer.StorageStart()
	defer sourceContainer.StorageStop()

	if err := c.Storage.ContainerCopy(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerLXDCreateAsSnapshot(d *Daemon, name string,
	args containerLXDArgs, sourceContainer container,
	stateful bool) (container, error) {

	// Create the container
	c, err := containerLXDCreateInternal(d, name, args)
	if err != nil {
		return nil, err
	}

	if err := c.Storage.ContainerSnapshotCreate(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	if stateful {
		stateDir := c.StateDirGet()
		err = os.MkdirAll(stateDir, 0700)
		if err != nil {
			c.Delete()
			return nil, err
		}

		// TODO - shouldn't we freeze for the duration of rootfs snapshot below?
		if !sourceContainer.IsRunning() {
			c.Delete()
			return nil, fmt.Errorf("Container not running\n")
		}
		opts := lxc.CheckpointOptions{Directory: stateDir, Stop: true, Verbose: true}
		source, err := sourceContainer.LXContainerGet()
		if err != nil {
			c.Delete()
			return nil, err
		}

		err = source.Checkpoint(opts)
		err2 := migration.CollectCRIULogFile(source, stateDir, "snapshot", "dump")
		if err != nil {
			shared.Log.Warn("failed to collect criu log file", log.Ctx{"error": err2})
		}

		if err != nil {
			c.Delete()
			return nil, err
		}
	}

	return c, nil
}

func containerLXDCreateInternal(
	d *Daemon, name string, args containerLXDArgs) (*containerLXD, error) {

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

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
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

func containerLXDLoad(d *Daemon, name string) (container, error) {
	shared.Log.Debug("Container load", log.Ctx{"container": name})

	args, err := dbContainerGet(d.db, name)
	if err != nil {
		return nil, err
	}

	myConfig := map[string]string{}
	if err := shared.DeepCopy(&args.Config, &myConfig); err != nil {
		return nil, err
	}
	myDevices := shared.Devices{}
	if err := shared.DeepCopy(&args.Devices, &myDevices); err != nil {
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
		myConfig:     myConfig,
		myDevices:    myDevices}

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
func (c *containerLXD) init() error {
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

	// Apply the config of this container over the profile(s) above.
	if err := c.applyConfig(c.myConfig, false); err != nil {
		return err
	}

	if err := c.setupMacAddresses(); err != nil {
		return err
	}

	// Allow overwrites of devices
	for k, v := range c.myDevices {
		c.devices[k] = v
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

	return nil
}

func (c *containerLXD) RenderState() (*shared.ContainerState, error) {
	state := c.c.State()
	status := shared.ContainerStatus{State: state.String(), StateCode: shared.State(int(state))}
	if c.IsRunning() {
		pid, _ := c.InitPidGet()
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

func (c *containerLXD) Start() error {
	if c.IsRunning() {
		return fmt.Errorf("the container is already running")
	}

	// Start the storage for this container
	if err := c.StorageStart(); err != nil {
		return err
	}

	f, err := ioutil.TempFile("", "lxd_lxc_startconfig_")
	if err != nil {
		c.StorageStop()
		return err
	}
	configPath := f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(configPath)
		c.StorageStop()
		return err
	}
	f.Close()

	err = c.c.SaveConfigFile(configPath)
	if err != nil {
		c.StorageStop()
		return err
	}

	err = c.TemplateApply("start")
	if err != nil {
		c.StorageStop()
		return err
	}

	err = exec.Command(
		os.Args[0],
		"forkstart",
		c.name,
		c.daemon.lxcpath,
		configPath).Run()

	if err != nil {
		c.StorageStop()
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

func (c *containerLXD) Reboot() error {
	return c.c.Reboot()
}

func (c *containerLXD) Freeze() error {
	return c.c.Freeze()
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

func (c *containerLXD) Shutdown(timeout time.Duration) error {
	if err := c.c.Shutdown(timeout); err != nil {
		// Still try to unload the storage.
		c.StorageStop()
		return err
	}

	// Stop the storage for this container
	if err := c.StorageStop(); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) Stop() error {
	if err := c.c.Stop(); err != nil {
		// Still try to unload the storage.
		c.StorageStop()
		return err
	}

	// Stop the storage for this container
	if err := c.StorageStop(); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) Unfreeze() error {
	return c.c.Unfreeze()
}

func (c *containerLXD) StorageFromImage(hash string) error {
	return c.Storage.ContainerCreateFromImage(c, hash)
}

func (c *containerLXD) StorageFromNone() error {
	return c.Storage.ContainerCreate(c)
}

func (c *containerLXD) StorageStart() error {
	return c.Storage.ContainerStart(c)
}

func (c *containerLXD) StorageStop() error {
	return c.Storage.ContainerStop(c)
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
	sourceContainer.StorageStart()
	err := c.Storage.ContainerRestore(c, sourceContainer)
	sourceContainer.StorageStop()

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

func (c *containerLXD) Delete() error {
	shared.Log.Debug("containerLXD.Delete", log.Ctx{"c.name": c.NameGet(), "type": c.cType})
	switch c.cType {
	case cTypeRegular:
		if err := containerDeleteSnapshots(c.daemon, c.NameGet()); err != nil {
			return err
		}

		if err := c.Storage.ContainerDelete(c); err != nil {
			return err
		}
	case cTypeSnapshot:
		if err := c.Storage.ContainerSnapshotDelete(c); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Unknown cType: %d", c.cType)
	}

	if err := dbContainerRemove(c.daemon.db, c.NameGet()); err != nil {
		return err
	}

	return nil
}

func (c *containerLXD) Rename(newName string) error {
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

func (c *containerLXD) IsEmpheral() bool {
	return c.ephemeral
}

func (c *containerLXD) IsSnapshot() bool {
	return c.cType == cTypeSnapshot
}

func (c *containerLXD) IDGet() int {
	return c.id
}

func (c *containerLXD) NameGet() string {
	return c.name
}

func (c *containerLXD) ArchitectureGet() int {
	return c.architecture
}

func (c *containerLXD) PathGet(newName string) string {
	if newName != "" {
		return containerPathGet(newName, c.IsSnapshot())
	}

	return containerPathGet(c.NameGet(), c.IsSnapshot())
}

func (c *containerLXD) RootfsPathGet() string {
	return path.Join(c.PathGet(""), "rootfs")
}

func (c *containerLXD) TemplatesPathGet() string {
	return path.Join(c.PathGet(""), "templates")
}

func (c *containerLXD) StateDirGet() string {
	return path.Join(c.PathGet(""), "state")
}

func (c *containerLXD) LogPathGet() string {
	return shared.LogPath(c.NameGet())
}

func (c *containerLXD) LogFilePathGet() string {
	return filepath.Join(c.LogPathGet(), "lxc.log")
}

func (c *containerLXD) InitPidGet() (int, error) {
	return c.c.InitPid(), nil
}

func (c *containerLXD) IdmapSetGet() (*shared.IdmapSet, error) {
	return c.idmapset, nil
}

func (c *containerLXD) LXContainerGet() (*lxc.Container, error) {
	return c.c, nil
}

// ConfigReplace replaces the config of container and tries to live apply
// the new configuration.
func (c *containerLXD) ConfigReplace(newConfig containerLXDArgs) error {
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

	if err := txCommit(tx); err != nil {
		return err
	}

	c.myConfig = newConfig.Config
	c.myDevices = newConfig.Devices

	return nil
}

func (c *containerLXD) ConfigGet() containerLXDArgs {
	newConfig := containerLXDArgs{
		Config:    c.myConfig,
		Devices:   c.myDevices,
		Profiles:  c.profiles,
		Ephemeral: c.ephemeral,
	}

	return newConfig
}

/*
 * Export the container to a unshifted tarfile containing:
 * dir/
 *     metadata.yaml
 *     rootfs/
 */
func (c *containerLXD) ExportToTar(snap string, w io.Writer) error {
	if snap != "" && c.IsRunning() {
		return fmt.Errorf("Cannot export a running container as image")
	}

	tw := tar.NewWriter(w)

	// keep track of the first path we saw for each path with nlink>1
	linkmap := map[uint64]string{}

	cDir := c.PathGet("")

	// Path inside the tar image is the pathname starting after cDir
	offset := len(cDir) + 1

	fnam := filepath.Join(cDir, "metadata.yaml")
	writeToTar := func(path string, fi os.FileInfo, err error) error {
		if err := c.tarStoreFile(linkmap, offset, tw, path, fi); err != nil {
			shared.Debugf("Error tarring up %s: %s\n", path, err)
			return err
		}
		return nil
	}

	fnam = filepath.Join(cDir, "metadata.yaml")
	if shared.PathExists(fnam) {
		fi, err := os.Lstat(fnam)
		if err != nil {
			shared.Debugf("Error statting %s during exportToTar\n", fnam)
			tw.Close()
			return err
		}
		if err := c.tarStoreFile(linkmap, offset, tw, fnam, fi); err != nil {
			shared.Debugf("Error writing to tarfile: %s\n", err)
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
	fname := path.Join(c.PathGet(""), "metadata.yaml")

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

func (c *containerLXD) AttachMount(m shared.Device) error {
	dest := m["path"]
	source := m["source"]

	opts := ""
	fstype := "none"
	flags := 0
	sb, err := os.Stat(source)
	if err != nil {
		return err
	}
	if sb.IsDir() {
		flags |= syscall.MS_BIND
		opts = "bind,create=dir"
	} else {
		if !shared.IsBlockdev(sb.Mode()) {
			// Not sure if we want to try dealing with loopdevs, but
			// since we'd need to deal with partitions i think not.
			// We also might want to support file bind mounting, but
			// this doesn't do that.
			return fmt.Errorf("non-block device file not supported\n")
		}

		fstype, err = shared.BlockFsDetect(source)
		if err != nil {
			return fmt.Errorf("Unable to detect fstype for %s: %s\n", source, err)
		}
	}

	// add a lxc.mount.entry = souce destination, in case of reboot
	if m["readonly"] == "1" || m["readonly"] == "true" {
		if opts == "" {
			opts = "ro"
		} else {
			opts = opts + ",ro"
		}
	}
	optional := false
	if m["optional"] == "1" || m["optional"] == "true" {
		optional = true
		opts = opts + ",optional"
	}

	entry := fmt.Sprintf("%s %s %s %s 0 0", source, dest, fstype, opts)
	if err := c.c.SetConfigItem("lxc.mount.entry", entry); err != nil {
		return err
	}

	pid := c.c.InitPid()
	if pid == -1 { // container not running - we're done
		return nil
	}

	// now live-mount
	tmpMount, err := ioutil.TempDir(shared.VarPath("shmounts", c.name), "lxdmount_")
	if err != nil {
		return err
	}

	err = syscall.Mount(m["source"], tmpMount, fstype, uintptr(flags), "")
	if err != nil {
		return err
	}

	mntsrc := filepath.Join("/.lxdmounts", filepath.Base(tmpMount))
	// finally we need to move-mount this in the container
	pidstr := fmt.Sprintf("%d", pid)
	err = exec.Command(os.Args[0], "forkmount", pidstr, mntsrc, m["path"]).Run()
	syscall.Unmount(tmpMount, syscall.MNT_DETACH) // in case forkmount failed
	os.Remove(tmpMount)

	if err != nil && !optional {
		return err
	}
	return nil
}

func (c *containerLXD) applyConfig(config map[string]string, fromProfile bool) error {
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
			shared.Debugf("Error setting %s: %q\n", k, err)
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

		shared.Debugf("Applying %s: %s", k, v)
		if k == "raw.lxc" {
			if _, ok := c.config["raw.lxc"]; ok {
				shared.Debugf("Ignoring overridden raw.lxc from profile")
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

func (c *containerLXD) applyIdmapSet() error {
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

		configs, err := deviceToLxc(d)
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
		return fmt.Errorf("error getting file info: %s\n", err)
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
		return fmt.Errorf("error writing header: %s\n", err)
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("tarStoreFile: error opening file: %s\n", err)
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("error copying file %s\n", err)
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
	source := shared.VarPath("shmounts", c.NameGet())
	entry := fmt.Sprintf("%s .lxdmounts none bind,create=dir 0 0", source)
	if !shared.PathExists(source) {
		if err := c.mkdirAllContainerRoot(source, 0755); err != nil {
			return err
		}
	}
	return c.c.SetConfigItem("lxc.mount.entry", entry)
}
