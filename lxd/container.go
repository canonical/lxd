package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
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

func containerValidConfigKey(d *Daemon, key string, value string) error {
	f, err := shared.ConfigKeyChecker(key)
	if err != nil {
		return err
	}
	if err = f(value); err != nil {
		return err
	}
	if key == "raw.lxc" {
		return lxcValidConfig(value)
	}
	if key == "security.syscalls.blacklist_compat" {
		for _, arch := range d.architectures {
			if arch == shared.ARCH_64BIT_INTEL_X86 ||
				arch == shared.ARCH_64BIT_ARMV8_LITTLE_ENDIAN ||
				arch == shared.ARCH_64BIT_POWERPC_BIG_ENDIAN {
				return nil
			}
		}
		return fmt.Errorf("security.syscalls.blacklist_compat is only valid on x86_64")
	}
	return nil
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
		case "ipv4.address":
			return true
		case "ipv6.address":
			return true
		case "security.mac_filtering":
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
	case "usb":
		switch k {
		case "vendorid":
			return true
		case "productid":
			return true
		case "mode":
			return true
		case "gid":
			return true
		case "uid":
			return true
		case "required":
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

func containerValidConfig(d *Daemon, config map[string]string, profile bool, expanded bool) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if profile && strings.HasPrefix(k, "volatile.") {
			return fmt.Errorf("Volatile keys can only be set on containers.")
		}

		err := containerValidConfigKey(d, k, v)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, whitelist := config["security.syscalls.whitelist"]
	_, blacklist := config["securtiy.syscalls.blacklist"]
	blacklistDefault := shared.IsTrue(config["security.syscalls.blacklist_default"])
	blacklistCompat := shared.IsTrue(config["security.syscalls.blacklist_compat"])

	if rawSeccomp && (whitelist || blacklist || blacklistDefault || blacklistCompat) {
		return fmt.Errorf("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if whitelist && (blacklist || blacklistDefault || blacklistCompat) {
		return fmt.Errorf("security.syscalls.whitelist is mutually exclusive with security.syscalls.blacklist*")
	}

	return nil
}

func containerValidDevices(devices shared.Devices, profile bool, expanded bool) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	// Check each device individually
	for name, m := range devices {
		if m["type"] == "" {
			return fmt.Errorf("Missing device type for device '%s'", name)
		}

		if !shared.StringInSlice(m["type"], []string{"none", "nic", "disk", "unix-char", "unix-block", "usb"}) {
			return fmt.Errorf("Invalid device type for device '%s'", name)
		}

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
		} else if m["type"] == "usb" {
			if m["productid"] == "" {
				return fmt.Errorf("Missing productid for USB device.")
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
	LastUsedDate time.Time
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
	/* actionScript here is a script called action.sh in the stateDir, to
	 * be passed to CRIU as --action-script
	 */
	Migrate(cmd uint, stateDir string, function string, stop bool, actionScript bool) error
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
	FilePull(srcpath string, dstpath string) (int, int, os.FileMode, string, []string, error)
	FilePush(srcpath string, dstpath string, uid int, gid int, mode int) error

	// Command execution
	Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File) (int, error)

	// Status
	Render() (interface{}, interface{}, error)
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
	LastUsedDate() time.Time
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

		err = sourceContainer.Migrate(lxc.MIGRATE_DUMP, stateDir, "snapshot", false, false)
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
	err := containerValidConfig(d, args.Config, false, false)
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
	args.LastUsedDate = dbArgs.LastUsedDate

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
