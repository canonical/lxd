package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

// Helper functions
func containerPath(name string, isSnapshot bool) string {
	if isSnapshot {
		return shared.VarPath("snapshots", name)
	}

	return shared.VarPath("containers", name)
}

func containerValidName(name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		return fmt.Errorf(
			"The character '%s' is reserved for snapshots.",
			shared.SnapshotDelimiter)
	}

	if !shared.ValidHostname(name) {
		return fmt.Errorf("Container name isn't a valid hostname.")
	}

	return nil
}

func containerValidConfigKey(key string, value string) error {
	isInt64 := func(key string, value string) error {
		if value == "" {
			return nil
		}

		_, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("Invalid value for an integer: %s", value)
		}

		return nil
	}

	isBool := func(key string, value string) error {
		if value == "" {
			return nil
		}

		if !shared.StringInSlice(strings.ToLower(value), []string{"true", "false", "yes", "no", "1", "0", "on", "off"}) {
			return fmt.Errorf("Invalid value for a boolean: %s", value)
		}

		return nil
	}

	isOneOf := func(key string, value string, valid []string) error {
		if value == "" {
			return nil
		}

		if !shared.StringInSlice(value, valid) {
			return fmt.Errorf("Invalid value: %s (not one of %s)", value, valid)
		}

		return nil
	}

	switch key {
	case "boot.autostart":
		return isBool(key, value)
	case "boot.autostart.delay":
		return isInt64(key, value)
	case "boot.autostart.priority":
		return isInt64(key, value)
	case "limits.cpu":
		return nil
	case "limits.cpu.allowance":
		return nil
	case "limits.cpu.priority":
		return isInt64(key, value)
	case "limits.disk.priority":
		return isInt64(key, value)
	case "limits.memory":
		return nil
	case "limits.memory.enforce":
		return isOneOf(key, value, []string{"soft", "hard"})
	case "limits.memory.swap":
		return isBool(key, value)
	case "limits.memory.swap.priority":
		return isInt64(key, value)
	case "limits.network.priority":
		return isInt64(key, value)
	case "limits.processes":
		return isInt64(key, value)
	case "linux.kernel_modules":
		return nil
	case "security.privileged":
		return isBool(key, value)
	case "security.nesting":
		return isBool(key, value)
	case "raw.apparmor":
		return nil
	case "raw.lxc":
		return lxcValidConfig(value)
	case "volatile.apply_template":
		return nil
	case "volatile.base_image":
		return nil
	case "volatile.last_state.idmap":
		return nil
	case "volatile.last_state.power":
		return nil
	}

	if strings.HasPrefix(key, "volatile.") {
		if strings.HasSuffix(key, ".hwaddr") {
			return nil
		}

		if strings.HasSuffix(key, ".name") {
			return nil
		}
	}

	if strings.HasPrefix(key, "environment.") {
		return nil
	}

	if strings.HasPrefix(key, "user.") {
		return nil
	}

	return fmt.Errorf("Bad key: %s", key)
}

func containerValidDeviceConfigKey(t, k string) bool {
	if k == "type" {
		return true
	}

	switch t {
	case "unix-char", "unix-block":
		switch k {
		case "gid":
			return true
		case "major":
			return true
		case "minor":
			return true
		case "mode":
			return true
		case "path":
			return true
		case "uid":
			return true
		default:
			return false
		}
	case "nic":
		switch k {
		case "limits.max":
			return true
		case "limits.ingress":
			return true
		case "limits.egress":
			return true
		case "host_name":
			return true
		case "hwaddr":
			return true
		case "mtu":
			return true
		case "name":
			return true
		case "nictype":
			return true
		case "parent":
			return true
		default:
			return false
		}
	case "disk":
		switch k {
		case "limits.max":
			return true
		case "limits.read":
			return true
		case "limits.write":
			return true
		case "optional":
			return true
		case "path":
			return true
		case "readonly":
			return true
		case "size":
			return true
		case "source":
			return true
		case "recursive":
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

func containerValidConfig(config map[string]string, profile bool, expanded bool) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if profile && strings.HasPrefix(k, "volatile.") {
			return fmt.Errorf("Volatile keys can only be set on containers.")
		}

		err := containerValidConfigKey(k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

func containerValidDevices(devices shared.Devices, profile bool, expanded bool) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	// Check each device individually
	for _, m := range devices {
		for k, _ := range m {
			if !containerValidDeviceConfigKey(m["type"], k) {
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

			if m["path"] == "/" && m["source"] != "" {
				return fmt.Errorf("Root disk entry may not have a \"source\" property set.")
			}

			if m["size"] != "" && m["path"] != "/" {
				return fmt.Errorf("Only the root disk may have a size quota.")
			}

			if (m["path"] == "/" || !shared.IsDir(m["source"])) && m["recursive"] != "" {
				return fmt.Errorf("The recursive option is only supported for additional bind-mounted paths.")
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

	// Checks on the expanded config
	if expanded {
		foundRootfs := false
		for _, m := range devices {
			if m["type"] == "disk" && m["path"] == "/" {
				foundRootfs = true
			}
		}

		if !foundRootfs {
			return fmt.Errorf("Container is lacking rootfs entry")
		}
	}

	return nil
}

// The container arguments
type containerArgs struct {
	// Don't set manually
	Id int

	Architecture int
	BaseImage    string
	Config       map[string]string
	CreationDate time.Time
	Ctype        containerType
	Devices      shared.Devices
	Ephemeral    bool
	Name         string
	Profiles     []string
	Stateful     bool
}

// The container interface
type container interface {
	// Container actions
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start(stateful bool) error
	Stop(stateful bool) error
	Unfreeze() error

	// Snapshots & migration
	Restore(sourceContainer container) error
	Migrate(cmd uint, stateDir string, stop bool) error
	StartFromMigration(imagesDir string) error
	Snapshots() ([]container, error)

	// Config handling
	Rename(newName string) error
	Update(newConfig containerArgs, userRequested bool) error

	Delete() error
	Export(w io.Writer) error

	// Live configuration
	CGroupGet(key string) (string, error)
	CGroupSet(key string, value string) error
	ConfigKeySet(key string, value string) error

	// File handling
	FilePull(srcpath string, dstpath string) (int, int, os.FileMode, error)
	FilePush(srcpath string, dstpath string, uid int, gid int, mode int) error

	// Command execution
	Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File) (int, error)

	// Status
	Render() (interface{}, error)
	RenderState() (*shared.ContainerState, error)
	IsPrivileged() bool
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool
	IsStateful() bool
	IsNesting() bool

	// Hooks
	OnStart() error
	OnStop(target string) error

	// Properties
	Id() int
	Name() string
	Architecture() int
	CreationDate() time.Time
	ExpandedConfig() map[string]string
	ExpandedDevices() shared.Devices
	LocalConfig() map[string]string
	LocalDevices() shared.Devices
	Profiles() []string
	InitPID() int
	State() string

	// Paths
	Path() string
	RootfsPath() string
	TemplatesPath() string
	StatePath() string
	LogFilePath() string
	LogPath() string

	// FIXME: Those should be internal functions
	StorageStart() error
	StorageStop() error
	Storage() storage
	IdmapSet() *shared.IdmapSet
	LastIdmapSet() (*shared.IdmapSet, error)
	TemplateApply(trigger string) error
	Daemon() *Daemon
}

// Loader functions
func containerCreateAsEmpty(d *Daemon, args containerArgs) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty storage
	if err := c.Storage().ContainerCreate(c); err != nil {
		c.Delete()
		return nil, err
	}

	// Apply any post-storage configuration
	err = containerConfigureInternal(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateEmptySnapshot(d *Daemon, args containerArgs) (container, error) {
	// Create the snapshot
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty snapshot
	if err := c.Storage().ContainerSnapshotCreateEmpty(c); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateFromImage(d *Daemon, args containerArgs, hash string) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	if err := dbImageLastAccessUpdate(d.db, hash, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Now create the storage from an image
	if err := c.Storage().ContainerCreateFromImage(c, hash); err != nil {
		c.Delete()
		return nil, err
	}

	// Apply any post-storage configuration
	err = containerConfigureInternal(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateAsCopy(d *Daemon, args containerArgs, sourceContainer container) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Now clone the storage
	if err := c.Storage().ContainerCopy(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	// Apply any post-storage configuration
	err = containerConfigureInternal(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateAsSnapshot(d *Daemon, args containerArgs, sourceContainer container) (container, error) {
	// Deal with state
	if args.Stateful {
		if !sourceContainer.IsRunning() {
			return nil, fmt.Errorf("Container not running, cannot do stateful snapshot")
		}

		if err := findCriu("snapshot"); err != nil {
			return nil, err
		}

		stateDir := sourceContainer.StatePath()
		err := os.MkdirAll(stateDir, 0700)
		if err != nil {
			return nil, err
		}

		/* TODO: ideally we would freeze here and unfreeze below after
		 * we've copied the filesystem, to make sure there are no
		 * changes by the container while snapshotting. Unfortunately
		 * there is abug in CRIU where it doesn't leave the container
		 * in the same state it found it w.r.t. freezing, i.e. CRIU
		 * freezes too, and then /always/ thaws, even if the container
		 * was frozen. Until that's fixed, all calls to Unfreeze()
		 * after snapshotting will fail.
		 */

		err = sourceContainer.Migrate(lxc.MIGRATE_DUMP, stateDir, false)
		err2 := CollectCRIULogFile(sourceContainer, stateDir, "snapshot", "dump")
		if err2 != nil {
			shared.Log.Warn("failed to collect criu log file", log.Ctx{"error": err2})
		}

		if err != nil {
			os.RemoveAll(sourceContainer.StatePath())
			return nil, err
		}
	}

	// Create the snapshot
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Clone the container
	if err := sourceContainer.Storage().ContainerSnapshotCreate(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	// Once we're done, remove the state directory
	if args.Stateful {
		os.RemoveAll(sourceContainer.StatePath())
	}

	return c, nil
}

func containerCreateInternal(d *Daemon, args containerArgs) (container, error) {
	// Set default values
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

	if args.Architecture == 0 {
		args.Architecture = d.architectures[0]
	}

	// Validate container name
	if args.Ctype == cTypeRegular {
		err := containerValidName(args.Name)
		if err != nil {
			return nil, err
		}
	}

	// Validate container config
	err := containerValidConfig(args.Config, false, false)
	if err != nil {
		return nil, err
	}

	// Validate container devices
	err = containerValidDevices(args.Devices, false, false)
	if err != nil {
		return nil, err
	}

	// Validate architecture
	_, err = shared.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	// Validate profiles
	profiles, err := dbProfiles(d.db)
	if err != nil {
		return nil, err
	}

	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile '%s' doesn't exist", profile)
		}
	}

	path := containerPath(args.Name, args.Ctype == cTypeSnapshot)
	if shared.PathExists(path) {
		if shared.IsSnapshot(args.Name) {
			return nil, fmt.Errorf("Snapshot '%s' already exists", args.Name)
		}
		return nil, fmt.Errorf("The container already exists")
	}

	// Wipe any existing log for this container name
	os.RemoveAll(shared.LogPath(args.Name))

	// Create the container entry
	id, err := dbContainerCreate(d.db, args)
	if err != nil {
		return nil, err
	}
	args.Id = id

	// Read the timestamp from the database
	dbArgs, err := dbContainerGet(d.db, args.Name)
	if err != nil {
		return nil, err
	}
	args.CreationDate = dbArgs.CreationDate

	return containerLXCCreate(d, args)
}

func containerConfigureInternal(c container) error {
	// Find the root device
	for _, m := range c.ExpandedDevices() {
		if m["type"] != "disk" || m["path"] != "/" || m["size"] == "" {
			continue
		}

		size, err := shared.ParseByteSizeString(m["size"])
		if err != nil {
			return err
		}

		err = c.Storage().ContainerSetQuota(c, size)
		if err != nil {
			return err
		}

		break
	}

	return nil
}

func containerLoadById(d *Daemon, id int) (container, error) {
	// Get the DB record
	name, err := dbContainerName(d.db, id)
	if err != nil {
		return nil, err
	}

	return containerLoadByName(d, name)
}

func containerLoadByName(d *Daemon, name string) (container, error) {
	// Get the DB record
	args, err := dbContainerGet(d.db, name)
	if err != nil {
		return nil, err
	}

	return containerLXCLoad(d, args)
}
