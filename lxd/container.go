package main

import (
	"fmt"
	"io"
	"os"
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

func containerValidConfigKey(k string) bool {
	switch k {
	case "boot.autostart":
		return true
	case "boot.autostart.delay":
		return true
	case "boot.autostart.priority":
		return true
	case "limits.cpu":
		return true
	case "limits.cpu.allowance":
		return true
	case "limits.cpu.priority":
		return true
	case "limits.memory":
		return true
	case "limits.memory.enforce":
		return true
	case "limits.memory.swap":
		return true
	case "limits.memory.swap.priority":
		return true
	case "linux.kernel_modules":
		return true
	case "security.privileged":
		return true
	case "security.nesting":
		return true
	case "raw.apparmor":
		return true
	case "raw.lxc":
		return true
	case "volatile.base_image":
		return true
	case "volatile.last_state.idmap":
		return true
	case "volatile.last_state.power":
		return true
	}

	if strings.HasPrefix(k, "volatile.") {
		if strings.HasSuffix(k, ".hwaddr") {
			return true
		}

		if strings.HasSuffix(k, ".name") {
			return true
		}
	}

	if strings.HasPrefix(k, "environment.") {
		return true
	}

	if strings.HasPrefix(k, "user.") {
		return true
	}

	return false
}

func containerValidDeviceConfigKey(t, k string) bool {
	if k == "type" {
		return true
	}

	switch t {
	case "unix-char":
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
	case "unix-block":
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

	for k, _ := range config {
		if profile && strings.HasPrefix(k, "volatile.") {
			return fmt.Errorf("Volatile keys can only be set on containers.")
		}

		if k == "raw.lxc" {
			err := lxcValidConfig(config["raw.lxc"])
			if err != nil {
				return err
			}
		}

		if !containerValidConfigKey(k) {
			return fmt.Errorf("Bad key: %s", k)
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
	Ctype        containerType
	Devices      shared.Devices
	Ephemeral    bool
	Name         string
	Profiles     []string
}

// The container interface
type container interface {
	// Container actions
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start() error
	Stop() error
	Unfreeze() error

	// Snapshots & migration
	Restore(sourceContainer container) error
	Checkpoint(opts lxc.CheckpointOptions) error
	StartFromMigration(imagesDir string) error
	Snapshots() ([]container, error)

	// Config handling
	Rename(newName string) error
	Update(newConfig containerArgs, userRequested bool) error

	Delete() error
	Export(w io.Writer) error

	// Live configuration
	CGroupSet(key string, value string) error
	ConfigKeySet(key string, value string) error

	// File handling
	FilePull(srcpath string, dstpath string) error
	FilePush(srcpath string, dstpath string, uid int, gid int, mode os.FileMode) error

	// Status
	RenderState() (*shared.ContainerState, error)
	IsPrivileged() bool
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool
	IsNesting() bool

	// Hooks
	OnStart() error
	OnStop(target string) error

	// Properties
	Id() int
	Name() string
	Architecture() int
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
	LXContainerGet() *lxc.Container
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

	if err := dbImageLastAccessUpdate(d.db, hash); err != nil {
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

func containerCreateAsSnapshot(d *Daemon, args containerArgs, sourceContainer container, stateful bool) (container, error) {
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

	// Deal with state
	if stateful {
		stateDir := c.StatePath()
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

	return containerLXCCreate(d, args)
}

func containerConfigureInternal(c container) error {
	// Find the root device
	for _, m := range c.ExpandedDevices() {
		if m["type"] != "disk" || m["path"] != "/" || m["size"] == "" {
			continue
		}

		size, err := deviceParseBytes(m["size"])
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
