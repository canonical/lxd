package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

// Helper functions

// Returns the parent container name, snapshot name, and whether it actually was
// a snapshot name.
func containerGetParentAndSnapshotName(name string) (string, string, bool) {
	fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)
	if len(fields) == 1 {
		return name, "", false
	}

	return fields[0], fields[1], true
}

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

	isUint32 := func(key string, value string) error {
		if value == "" {
			return nil
		}

		_, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return fmt.Errorf("Invalid value for uint32: %s: %v", value, err)
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
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			// Percentage based allocation
			_, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
			if err != nil {
				return err
			}

			return nil
		}

		// Time based allocation
		fields := strings.SplitN(value, "/", 2)
		if len(fields) != 2 {
			return fmt.Errorf("Invalid allowance: %s", value)
		}

		_, err := strconv.Atoi(strings.TrimSuffix(fields[0], "ms"))
		if err != nil {
			return err
		}

		_, err = strconv.Atoi(strings.TrimSuffix(fields[1], "ms"))
		if err != nil {
			return err
		}

		return nil
	case "limits.cpu.priority":
		return isInt64(key, value)
	case "limits.disk.priority":
		return isInt64(key, value)
	case "limits.memory":
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			_, err := strconv.ParseInt(strings.TrimSuffix(value, "%"), 10, 64)
			if err != nil {
				return err
			}

			return nil
		}

		_, err := shared.ParseByteSizeString(value)
		if err != nil {
			return err
		}

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
	case "security.idmap.size":
		return isUint32(key, value)
	case "security.idmap.isolated":
		return isBool(key, value)
	case "raw.apparmor":
		return nil
	case "raw.idmap":
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
	case "volatile.idmap.next":
		return nil
	case "volatile.idmap.base":
		return nil
	}

	if strings.HasPrefix(key, "volatile.") {
		if strings.HasSuffix(key, ".hwaddr") {
			return nil
		}

		if strings.HasSuffix(key, ".name") {
			return nil
		}

		if strings.HasSuffix(key, ".host_name") {
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

func containerValidConfig(d *Daemon, config map[string]string, profile bool, expanded bool) error {
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

	if expanded && (config["security.privileged"] == "" || !shared.IsTrue(config["security.privileged"])) && d.IdmapSet == nil {
		return fmt.Errorf("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported.")
	}

	return nil
}

func isRootDiskDevice(device types.Device) bool {
	if device["type"] == "disk" && device["path"] == "/" && device["source"] == "" {
		return true
	}

	return false
}

func containerGetRootDiskDevice(devices types.Devices) (string, types.Device, error) {
	var devName string
	var dev types.Device

	for n, d := range devices {
		if isRootDiskDevice(d) {
			if devName != "" {
				return "", types.Device{}, fmt.Errorf("More than one root device found.")
			}

			devName = n
			dev = d
		}
	}

	if devName != "" {
		return devName, dev, nil
	}

	return "", types.Device{}, fmt.Errorf("No root device could be found.")
}

func containerValidDevices(devices types.Devices, profile bool, expanded bool) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	var diskDevicePaths []string
	// Check each device individually
	for name, m := range devices {
		if m["type"] == "" {
			return fmt.Errorf("Missing device type for device '%s'", name)
		}

		if !shared.StringInSlice(m["type"], []string{"none", "nic", "disk", "unix-char", "unix-block"}) {
			return fmt.Errorf("Invalid device type for device '%s'", name)
		}

		for k := range m {
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
			if !expanded && !shared.StringInSlice(m["path"], diskDevicePaths) {
				diskDevicePaths = append(diskDevicePaths, m["path"])
			} else if !expanded {
				return fmt.Errorf("More than one disk device uses the same path: %s.", m["path"])
			}

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

			if m["major"] == "" || m["minor"] == "" {
				if !shared.PathExists(m["path"]) {
					return fmt.Errorf("The device path doesn't exist on the host and major/minor wasn't specified.")
				}

				dType, _, _, err := deviceGetAttributes(m["path"])
				if err != nil {
					return err
				}

				if m["type"] == "unix-char" && dType != "c" {
					return fmt.Errorf("Path specified for unix-char device is a block device.")
				}

				if m["type"] == "unix-block" && dType != "b" {
					return fmt.Errorf("Path specified for unix-block device is a character device.")
				}
			}
		} else if m["type"] == "none" {
			continue
		} else {
			return fmt.Errorf("Invalid device type: %s", m["type"])
		}
	}

	// Checks on the expanded config
	if expanded {
		_, _, err := containerGetRootDiskDevice(devices)
		if err != nil {
			return err
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
	Devices      types.Devices
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
	Export(w io.Writer, properties map[string]string) error

	// Live configuration
	CGroupGet(key string) (string, error)
	CGroupSet(key string, value string) error
	ConfigKeySet(key string, value string) error

	// File handling
	FilePull(srcpath string, dstpath string) (int64, int64, os.FileMode, error)
	FileExists(path string) error
	FilePush(srcpath string, dstpath string, uid int64, gid int64, mode int) error
	FileRemove(path string) error

	/* Command execution:
		 * 1. passing in false for wait
		 *    - equivalent to calling cmd.Run()
		 * 2. passing in true for wait
	         *    - start the command and return its PID in the first return
	         *      argument and the PID of the attached process in the second
	         *      argument. It's the callers responsibility to wait on the
	         *      command. (Note. The returned PID of the attached process can not
	         *      be waited upon since it's a child of the lxd forkexec command
	         *      (the PID returned in the first return argument). It can however
	         *      be used to e.g. forward signals.)
	*/
	Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File, wait bool) (*exec.Cmd, int, int, error)

	// Status
	Render() (interface{}, error)
	RenderState() (*api.ContainerState, error)
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
	ExpandedDevices() types.Devices
	LocalConfig() map[string]string
	LocalDevices() types.Devices
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
	IdmapSet() (*shared.IdmapSet, error)
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
	// Set the BaseImage field (regardless of previous value)
	args.BaseImage = hash

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
			return nil, fmt.Errorf("Unable to create a stateful snapshot. The container isn't running.")
		}

		_, err := exec.LookPath("criu")
		if err != nil {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. CRIU isn't installed.")
		}

		stateDir := sourceContainer.StatePath()
		err = os.MkdirAll(stateDir, 0700)
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
		args.Devices = types.Devices{}
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
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	if !shared.IntInSlice(args.Architecture, d.architectures) {
		return nil, fmt.Errorf("Requested architecture isn't supported by this host")
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

	// Create the container entry
	id, err := dbContainerCreate(d.db, args)
	if err != nil {
		if err == DbErrAlreadyDefined {
			thing := "Container"
			if shared.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}
			return nil, fmt.Errorf("%s '%s' already exists", thing, args.Name)
		}
		return nil, err
	}

	// Wipe any existing log for this container name
	os.RemoveAll(shared.LogPath(args.Name))

	args.Id = id

	// Read the timestamp from the database
	dbArgs, err := dbContainerGet(d.db, args.Name)
	if err != nil {
		return nil, err
	}
	args.CreationDate = dbArgs.CreationDate

	// Setup the container struct and finish creation (storage and idmap)
	c, err := containerLXCCreate(d, args)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func containerConfigureInternal(c container) error {
	// Find the root device
	_, rootDiskDevice, err := containerGetRootDiskDevice(c.ExpandedDevices())
	if err != nil {
		return err
	}

	if rootDiskDevice["size"] != "" {
		size, err := shared.ParseByteSizeString(rootDiskDevice["size"])
		if err != nil {
			return err
		}

		// Storage is guaranteed to be ready.
		err = c.Storage().ContainerSetQuota(c, size)
		if err != nil {
			return err
		}
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
