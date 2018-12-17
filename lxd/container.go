package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/context"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/robfig/cron.v2"

	"github.com/flosch/pongo2"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/pkg/errors"
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
		return fmt.Errorf("Container name isn't a valid hostname")
	}

	return nil
}

func containerValidConfigKey(os *sys.OS, key string, value string) error {
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
		for _, arch := range os.Architectures {
			if arch == osarch.ARCH_64BIT_INTEL_X86 ||
				arch == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN ||
				arch == osarch.ARCH_64BIT_POWERPC_BIG_ENDIAN {
				return nil
			}
		}
		return fmt.Errorf("security.syscalls.blacklist_compat isn't supported on this architecture")
	}
	return nil
}

var containerNetworkLimitKeys = []string{"limits.max", "limits.ingress", "limits.egress"}

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
		case "source":
			return true
		case "path":
			return true
		case "required":
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
		case "vlan":
			return true
		case "ipv4.address":
			return true
		case "ipv6.address":
			return true
		case "security.mac_filtering":
			return true
		case "maas.subnet.ipv4":
			return true
		case "maas.subnet.ipv6":
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
		case "pool":
			return true
		case "propagation":
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
	case "gpu":
		switch k {
		case "vendorid":
			return true
		case "productid":
			return true
		case "id":
			return true
		case "pci":
			return true
		case "mode":
			return true
		case "gid":
			return true
		case "uid":
			return true
		default:
			return false
		}
	case "infiniband":
		switch k {
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
	case "proxy":
		switch k {
		case "bind":
			return true
		case "connect":
			return true
		case "gid":
			return true
		case "listen":
			return true
		case "mode":
			return true
		case "proxy_protocol":
			return true
		case "nat":
			return true
		case "security.gid":
			return true
		case "security.uid":
			return true
		case "uid":
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

func allowedUnprivilegedOnlyMap(rawIdmap string) error {
	rawMaps, err := parseRawIdmap(rawIdmap)
	if err != nil {
		return err
	}

	for _, ent := range rawMaps {
		if ent.Hostid == 0 {
			return fmt.Errorf("Cannot map root user into container as LXD was configured to only allow unprivileged containers")
		}
	}

	return nil
}

func containerValidConfig(sysOS *sys.OS, config map[string]string, profile bool, expanded bool) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if profile && strings.HasPrefix(k, "volatile.") {
			return fmt.Errorf("Volatile keys can only be set on containers")
		}

		if profile && strings.HasPrefix(k, "image.") {
			return fmt.Errorf("Image keys can only be set on containers")
		}

		err := containerValidConfigKey(sysOS, k, v)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, whitelist := config["security.syscalls.whitelist"]
	_, blacklist := config["security.syscalls.blacklist"]
	blacklistDefault := shared.IsTrue(config["security.syscalls.blacklist_default"])
	blacklistCompat := shared.IsTrue(config["security.syscalls.blacklist_compat"])

	if rawSeccomp && (whitelist || blacklist || blacklistDefault || blacklistCompat) {
		return fmt.Errorf("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if whitelist && (blacklist || blacklistDefault || blacklistCompat) {
		return fmt.Errorf("security.syscalls.whitelist is mutually exclusive with security.syscalls.blacklist*")
	}

	if expanded && (config["security.privileged"] == "" || !shared.IsTrue(config["security.privileged"])) && sysOS.IdmapSet == nil {
		return fmt.Errorf("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported")
	}

	unprivOnly := os.Getenv("LXD_UNPRIVILEGED_ONLY")
	if shared.IsTrue(unprivOnly) {
		if config["raw.idmap"] != "" {
			err := allowedUnprivilegedOnlyMap(config["raw.idmap"])
			if err != nil {
				return err
			}
		}

		if shared.IsTrue(config["security.privileged"]) {
			return fmt.Errorf("LXD was configured to only allow unprivileged containers")
		}
	}

	return nil
}

func containerValidDevices(cluster *db.Cluster, devices types.Devices, profile bool, expanded bool) error {
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

		if !shared.StringInSlice(m["type"], []string{"disk", "gpu", "infiniband", "nic", "none", "proxy", "unix-block", "unix-char", "usb"}) {
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

			if !shared.StringInSlice(m["nictype"], []string{"bridged", "macvlan", "p2p", "physical", "sriov"}) {
				return fmt.Errorf("Bad nic type: %s", m["nictype"])
			}

			if shared.StringInSlice(m["nictype"], []string{"bridged", "macvlan", "physical", "sriov"}) && m["parent"] == "" {
				return fmt.Errorf("Missing parent for %s type nic", m["nictype"])
			}
		} else if m["type"] == "infiniband" {
			if m["nictype"] == "" {
				return fmt.Errorf("Missing nic type")
			}

			if !shared.StringInSlice(m["nictype"], []string{"physical", "sriov"}) {
				return fmt.Errorf("Bad nic type: %s", m["nictype"])
			}

			if m["parent"] == "" {
				return fmt.Errorf("Missing parent for %s type nic", m["nictype"])
			}
		} else if m["type"] == "disk" {
			if !expanded && !shared.StringInSlice(m["path"], diskDevicePaths) {
				diskDevicePaths = append(diskDevicePaths, m["path"])
			} else if !expanded {
				return fmt.Errorf("More than one disk device uses the same path: %s", m["path"])
			}

			if m["path"] == "" {
				return fmt.Errorf("Disk entry is missing the required \"path\" property")
			}

			if m["source"] == "" && m["path"] != "/" {
				return fmt.Errorf("Disk entry is missing the required \"source\" property")
			}

			if m["path"] == "/" && m["source"] != "" {
				return fmt.Errorf("Root disk entry may not have a \"source\" property set")
			}

			if m["size"] != "" && m["path"] != "/" {
				return fmt.Errorf("Only the root disk may have a size quota")
			}

			if (m["path"] == "/" || !shared.IsDir(shared.HostPath(m["source"]))) && m["recursive"] != "" {
				return fmt.Errorf("The recursive option is only supported for additional bind-mounted paths")
			}

			if m["pool"] != "" {
				if filepath.IsAbs(m["source"]) {
					return fmt.Errorf("Storage volumes cannot be specified as absolute paths")
				}

				_, err := cluster.StoragePoolGetID(m["pool"])
				if err != nil {
					return fmt.Errorf("The \"%s\" storage pool doesn't exist", m["pool"])
				}

				if !profile && expanded && m["source"] != "" && m["path"] != "/" {
					isAvailable, err := cluster.StorageVolumeIsAvailable(
						m["pool"], m["source"])
					if err != nil {
						return errors.Wrap(err, "Check if volume is available")
					}
					if !isAvailable {
						return fmt.Errorf(
							"Storage volume %q is already attached to a container "+
								"on a different node", m["source"])
					}
				}
			}

			if m["propagation"] != "" {
				if !util.RuntimeLiblxcVersionAtLeast(3, 0, 0) {
					return fmt.Errorf("liblxc 3.0 is required for mount propagation configuration")
				}

				if !shared.StringInSlice(m["propagation"], []string{"private", "shared", "slave", "unbindable", "rprivate", "rshared", "rslave", "runbindable"}) {
					return fmt.Errorf("Invalid propagation mode '%s'", m["propagation"])
				}
			}
		} else if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
			if m["source"] == "" && m["path"] == "" {
				return fmt.Errorf("Unix device entry is missing the required \"source\" or \"path\" property")
			}

			if (m["required"] == "" || shared.IsTrue(m["required"])) && (m["major"] == "" || m["minor"] == "") {
				srcPath, exist := m["source"]
				if !exist {
					srcPath = m["path"]
				}
				if !shared.PathExists(srcPath) {
					return fmt.Errorf("The device path doesn't exist on the host and major/minor wasn't specified")
				}

				dType, _, _, err := deviceGetAttributes(srcPath)
				if err != nil {
					return err
				}

				if m["type"] == "unix-char" && dType != "c" {
					return fmt.Errorf("Path specified for unix-char device is a block device")
				}

				if m["type"] == "unix-block" && dType != "b" {
					return fmt.Errorf("Path specified for unix-block device is a character device")
				}
			}
		} else if m["type"] == "usb" {
			// Nothing needed for usb.
		} else if m["type"] == "gpu" {
			if m["pci"] != "" && !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s", m["pci"])) {
				return fmt.Errorf("Invalid PCI address (no device found): %s", m["pci"])
			}

			if m["pci"] != "" && (m["id"] != "" || m["productid"] != "" || m["vendorid"] != "") {
				return fmt.Errorf("Cannot use id, productid or vendorid when pci is set")
			}

			if m["id"] != "" && (m["pci"] != "" || m["productid"] != "" || m["vendorid"] != "") {
				return fmt.Errorf("Cannot use pci, productid or vendorid when id is set")
			}
		} else if m["type"] == "proxy" {
			if m["listen"] == "" {
				return fmt.Errorf("Proxy device entry is missing the required \"listen\" property")
			}

			if m["connect"] == "" {
				return fmt.Errorf("Proxy device entry is missing the required \"connect\" property")
			}

			listenAddr, err := parseAddr(m["listen"])
			if err != nil {
				return err
			}

			connectAddr, err := parseAddr(m["connect"])
			if err != nil {
				return err
			}

			if len(connectAddr.addr) > len(listenAddr.addr) {
				// Cannot support single port -> multiple port
				return fmt.Errorf("Cannot map a single port to multiple ports")
			}

			if shared.IsTrue(m["proxy_protocol"]) && !strings.HasPrefix(m["connect"], "tcp") {
				return fmt.Errorf("The PROXY header can only be sent to tcp servers")
			}

			if (!strings.HasPrefix(m["listen"], "unix:") || strings.HasPrefix(m["listen"], "unix:@")) &&
				(m["uid"] != "" || m["gid"] != "" || m["mode"] != "") {
				return fmt.Errorf("Only proxy devices for non-abstract unix sockets can carry uid, gid, or mode properties")
			}

			if shared.IsTrue(m["nat"]) {
				if m["bind"] != "" && m["bind"] != "host" {
					return fmt.Errorf("Only host-bound proxies can use NAT")
				}

				// Support TCP <-> TCP and UDP <-> UDP
				if listenAddr.connType == "unix" || connectAddr.connType == "unix" ||
					listenAddr.connType != connectAddr.connType {
					return fmt.Errorf("Proxying %s <-> %s is not supported when using NAT",
						listenAddr.connType, connectAddr.connType)
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
		_, _, err := shared.GetRootDiskDevice(devices)
		if err != nil {
			return errors.Wrap(err, "Detect root disk device")
		}
	}

	return nil
}

// The container interface
type container interface {
	// Container actions
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start(stateful bool) error
	Stop(stateful bool) error
	Unfreeze() error

	// Snapshots & migration & backups
	Restore(sourceContainer container, stateful bool) error
	/* actionScript here is a script called action.sh in the stateDir, to
	 * be passed to CRIU as --action-script
	 */
	Migrate(args *CriuMigrationArgs) error
	Snapshots() ([]container, error)
	Backups() ([]backup, error)

	// Config handling
	Rename(newName string) error
	Update(newConfig db.ContainerArgs, userRequested bool) error

	Delete() error
	Export(w io.Writer, properties map[string]string) error

	// Live configuration
	CGroupGet(key string) (string, error)
	CGroupSet(key string, value string) error
	ConfigKeySet(key string, value string) error

	// File handling
	FileExists(path string) error
	FilePull(srcpath string, dstpath string) (int64, int64, os.FileMode, string, []string, error)
	FilePush(type_ string, srcpath string, dstpath string, uid int64, gid int64, mode int, write string) error
	FileRemove(path string) error

	// Console - Allocate and run a console tty.
	//
	// terminal  - Bidirectional file descriptor.
	//
	// This function will not return until the console has been exited by
	// the user.
	Console(terminal *os.File) *exec.Cmd
	ConsoleLog(opts lxc.ConsoleLogOptions) (string, error)
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
	Render() (interface{}, interface{}, error)
	RenderFull() (*api.ContainerFull, interface{}, error)
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
	Project() string
	Name() string
	Description() string
	Architecture() int
	CreationDate() time.Time
	LastUsedDate() time.Time
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
	ConsoleBufferLogPath() string
	LogPath() string

	// Storage
	StoragePool() (string, error)

	// Progress reporting
	SetOperation(op *operation)

	// FIXME: Those should be internal functions
	// Needed for migration for now.
	StorageStart() (bool, error)
	StorageStop() (bool, error)
	Storage() storage
	IdmapSet() (*idmap.IdmapSet, error)
	LastIdmapSet() (*idmap.IdmapSet, error)
	TemplateApply(trigger string) error
	DaemonState() *state.State
}

// Loader functions
func containerCreateAsEmpty(d *Daemon, args db.ContainerArgs) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d.State(), args)
	if err != nil {
		return nil, err
	}

	// Now create the empty storage
	err = c.Storage().ContainerCreate(c)
	if err != nil {
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

func containerCreateFromBackup(s *state.State, info backupInfo, data io.ReadSeeker) error {
	var pool storage
	var fixBackupFile = false

	// Get storage pool from index.yaml
	pool, storageErr := storagePoolInit(s, info.Pool)
	if storageErr != nil && storageErr != db.ErrNoSuchObject {
		// Unexpected error
		return storageErr
	}

	if storageErr == db.ErrNoSuchObject {
		// The pool doesn't exist, and the backup is in binary format so we
		// cannot alter the backup.yaml.
		if info.HasBinaryFormat {
			return storageErr
		}

		// Get the default profile
		_, profile, err := s.Cluster.ProfileGet(info.Project, "default")
		if err != nil {
			return err
		}

		_, v, err := shared.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return err
		}

		// Use the default-profile's root pool
		pool, err = storagePoolInit(s, v["pool"])
		if err != nil {
			return err
		}

		fixBackupFile = true
	}

	// Find the compression algorithm
	tarArgs, _, _, err := shared.DetectCompressionFile(data)
	if err != nil {
		return err
	}
	data.Seek(0, 0)

	// Unpack tarball
	err = pool.ContainerBackupLoad(info, data, tarArgs)
	if err != nil {
		return err
	}

	if fixBackupFile {
		// Use the default pool since the pool provided in the backup.yaml
		// doesn't exist.
		err = backupFixStoragePool(s.Cluster, info)
		if err != nil {
			return err
		}
	}

	return nil
}

func containerCreateEmptySnapshot(s *state.State, args db.ContainerArgs) (container, error) {
	// Create the snapshot
	c, err := containerCreateInternal(s, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty snapshot
	err = c.Storage().ContainerSnapshotCreateEmpty(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateFromImage(d *Daemon, args db.ContainerArgs, hash string) (container, error) {
	s := d.State()

	// Get the image properties
	_, img, err := s.Cluster.ImageGet(args.Project, hash, false, false)
	if err != nil {
		return nil, errors.Wrapf(err, "Fetch image %s from database", hash)
	}

	// Check if the image is available locally or it's on another node.
	nodeAddress, err := s.Cluster.ImageLocate(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "Locate image %s in the cluster", hash)
	}
	if nodeAddress != "" {
		// The image is available from another node, let's try to
		// import it.
		logger.Debugf("Transferring image %s from node %s", hash, nodeAddress)
		client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), false)
		if err != nil {
			return nil, err
		}

		client = client.UseProject(args.Project)

		err = imageImportFromNode(filepath.Join(d.os.VarDir, "images"), client, hash)
		if err != nil {
			return nil, err
		}

		err = d.cluster.ImageAssociateNode(args.Project, hash)
		if err != nil {
			return nil, err
		}
	}

	// Set the "image.*" keys
	if img.Properties != nil {
		for k, v := range img.Properties {
			args.Config[fmt.Sprintf("image.%s", k)] = v
		}
	}

	// Set the BaseImage field (regardless of previous value)
	args.BaseImage = hash

	// Create the container
	c, err := containerCreateInternal(s, args)
	if err != nil {
		return nil, errors.Wrap(err, "Create container")
	}

	err = s.Cluster.ImageLastAccessUpdate(hash, time.Now().UTC())
	if err != nil {
		c.Delete()
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Now create the storage from an image
	err = c.Storage().ContainerCreateFromImage(c, hash)
	if err != nil {
		c.Delete()
		return nil, errors.Wrap(err, "Create container from image")
	}

	// Apply any post-storage configuration
	err = containerConfigureInternal(c)
	if err != nil {
		c.Delete()
		return nil, errors.Wrap(err, "Configure container")
	}

	return c, nil
}

func containerCreateAsCopy(s *state.State, args db.ContainerArgs, sourceContainer container, containerOnly bool, refresh bool) (container, error) {
	var ct container
	var err error

	if refresh {
		// Load the target container
		ct, err = containerLoadByProjectAndName(s, args.Project, args.Name)
		if err != nil {
			refresh = false
		}
	}

	if !refresh {
		// Create the container.
		ct, err = containerCreateInternal(s, args)
		if err != nil {
			return nil, err
		}
	}

	if refresh && ct.IsRunning() {
		return nil, fmt.Errorf("Cannot refresh a running container")
	}

	// At this point we have already figured out the parent
	// container's root disk device so we can simply
	// retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := ct.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices)
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	csList := []*container{}
	var snapshots []container

	if !containerOnly {
		if refresh {
			// Compare snapshots
			syncSnapshots, deleteSnapshots, err := containerCompareSnapshots(sourceContainer, ct)
			if err != nil {
				return nil, err
			}

			// Delete extra snapshots
			for _, snap := range deleteSnapshots {
				err := snap.Delete()
				if err != nil {
					return nil, err
				}
			}

			// Only care about the snapshots that need updating
			snapshots = syncSnapshots
		} else {
			// Get snapshots of source container
			snapshots, err = sourceContainer.Snapshots()
			if err != nil {
				ct.Delete()

				return nil, err
			}
		}

		for _, snap := range snapshots {
			fields := strings.SplitN(snap.Name(), shared.SnapshotDelimiter, 2)

			// Ensure that snapshot and parent container have the
			// same storage pool in their local root disk device.
			// If the root disk device for the snapshot comes from a
			// profile on the new instance as well we don't need to
			// do anything.
			snapDevices := snap.LocalDevices()
			if snapDevices != nil {
				snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapDevices)
				if snapLocalRootDiskDeviceKey != "" {
					snapDevices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
				} else {
					snapDevices["root"] = map[string]string{
						"type": "disk",
						"path": "/",
						"pool": parentStoragePool,
					}
				}
			}

			newSnapName := fmt.Sprintf("%s/%s", ct.Name(), fields[1])
			csArgs := db.ContainerArgs{
				Architecture: snap.Architecture(),
				Config:       snap.LocalConfig(),
				Ctype:        db.CTypeSnapshot,
				Devices:      snapDevices,
				Description:  snap.Description(),
				Ephemeral:    snap.IsEphemeral(),
				Name:         newSnapName,
				Profiles:     snap.Profiles(),
				Project:      args.Project,
			}

			// Create the snapshots.
			cs, err := containerCreateInternal(s, csArgs)
			if err != nil {
				if !refresh {
					ct.Delete()
				}

				return nil, err
			}

			// Restore snapshot creation date
			err = s.Cluster.ContainerCreationUpdate(cs.Id(), snap.CreationDate())
			if err != nil {
				if !refresh {
					ct.Delete()
				}

				return nil, err
			}

			csList = append(csList, &cs)
		}
	}

	// Now clone or refresh the storage
	if refresh {
		err = ct.Storage().ContainerRefresh(ct, sourceContainer, snapshots)
		if err != nil {
			return nil, err
		}
	} else {
		err = ct.Storage().ContainerCopy(ct, sourceContainer, containerOnly)
		if err != nil {
			if !refresh {
				ct.Delete()
			}

			return nil, err
		}
	}

	// Apply any post-storage configuration.
	err = containerConfigureInternal(ct)
	if err != nil {
		if !refresh {
			ct.Delete()
		}

		return nil, err
	}

	if !containerOnly {
		for _, cs := range csList {
			// Apply any post-storage configuration.
			err = containerConfigureInternal(*cs)
			if err != nil {
				if !refresh {
					ct.Delete()
				}

				return nil, err
			}
		}
	}

	return ct, nil
}

func containerCreateAsSnapshot(s *state.State, args db.ContainerArgs, sourceContainer container) (container, error) {
	// Deal with state
	if args.Stateful {
		if !sourceContainer.IsRunning() {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. The container isn't running")
		}

		_, err := exec.LookPath("criu")
		if err != nil {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. CRIU isn't installed")
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

		criuMigrationArgs := CriuMigrationArgs{
			cmd:          lxc.MIGRATE_DUMP,
			stateDir:     stateDir,
			function:     "snapshot",
			stop:         false,
			actionScript: false,
			dumpDir:      "",
			preDumpDir:   "",
		}

		err = sourceContainer.Migrate(&criuMigrationArgs)
		if err != nil {
			os.RemoveAll(sourceContainer.StatePath())
			return nil, err
		}
	}

	// Create the snapshot
	c, err := containerCreateInternal(s, args)
	if err != nil {
		return nil, err
	}

	// Clone the container
	err = sourceContainer.Storage().ContainerSnapshotCreate(c, sourceContainer)
	if err != nil {
		c.Delete()
		return nil, err
	}

	ourStart, err := c.StorageStart()
	if err != nil {
		c.Delete()
		return nil, err
	}
	if ourStart {
		defer c.StorageStop()
	}

	err = writeBackupFile(sourceContainer)
	if err != nil {
		c.Delete()
		return nil, err
	}

	// Once we're done, remove the state directory
	if args.Stateful {
		os.RemoveAll(sourceContainer.StatePath())
	}

	eventSendLifecycle(sourceContainer.Project(), "container-snapshot-created",
		fmt.Sprintf("/1.0/containers/%s", sourceContainer.Name()),
		map[string]interface{}{
			"snapshot_name": args.Name,
		})

	return c, nil
}

func containerCreateInternal(s *state.State, args db.ContainerArgs) (container, error) {
	// Set default values
	if args.Project == "" {
		args.Project = "default"
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
		args.Devices = types.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = s.OS.Architectures[0]
	}

	// Validate container name
	if args.Ctype == db.CTypeRegular {
		err := containerValidName(args.Name)
		if err != nil {
			return nil, err
		}
	}

	// Validate container config
	err := containerValidConfig(s.OS, args.Config, false, false)
	if err != nil {
		return nil, err
	}

	// Validate container devices
	err = containerValidDevices(s.Cluster, args.Devices, false, false)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Validate architecture
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	if !shared.IntInSlice(args.Architecture, s.OS.Architectures) {
		return nil, fmt.Errorf("Requested architecture isn't supported by this host")
	}

	// Validate profiles
	profiles, err := s.Cluster.Profiles(args.Project)
	if err != nil {
		return nil, err
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile '%s' doesn't exist", profile)
		}

		if shared.StringInSlice(profile, checkedProfiles) {
			return nil, fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile)
	}

	if args.CreationDate.IsZero() {
		args.CreationDate = time.Now().UTC()
	}

	if args.LastUsedDate.IsZero() {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	var container db.Container
	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		node, err := tx.NodeName()
		if err != nil {
			return err
		}

		// TODO: this check should probably be performed by the db
		// package itself.
		exists, err := tx.ProjectExists(args.Project)
		if err != nil {
			return errors.Wrapf(err, "Check if project %q exists", args.Project)
		}
		if !exists {
			return fmt.Errorf("Project %q does not exist", args.Project)
		}

		// Create the container entry
		container = db.Container{
			Project:      args.Project,
			Name:         args.Name,
			Node:         node,
			Type:         int(args.Ctype),
			Architecture: args.Architecture,
			Ephemeral:    args.Ephemeral,
			CreationDate: args.CreationDate,
			Stateful:     args.Stateful,
			LastUseDate:  args.LastUsedDate,
			Description:  args.Description,
			Config:       args.Config,
			Devices:      args.Devices,
			Profiles:     args.Profiles,
		}

		_, err = tx.ContainerCreate(container)
		if err != nil {
			return errors.Wrap(err, "Add container info to the database")
		}

		// Read back the container, to get ID and creation time.
		c, err := tx.ContainerGet(args.Project, args.Name)
		if err != nil {
			return errors.Wrap(err, "Fetch created container from the database")
		}

		container = *c

		if container.ID < 1 {
			return errors.Wrapf(err, "Unexpected container database ID %d", container.ID)
		}

		return nil
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
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

	args = db.ContainerToArgs(&container)

	// Setup the container struct and finish creation (storage and idmap)
	c, err := containerLXCCreate(s, args)
	if err != nil {
		s.Cluster.ContainerRemove(args.Project, args.Name)
		return nil, errors.Wrap(err, "Create LXC container")
	}

	return c, nil
}

func containerConfigureInternal(c container) error {
	// Find the root device
	_, rootDiskDevice, err := shared.GetRootDiskDevice(c.ExpandedDevices())
	if err != nil {
		return err
	}

	ourStart, err := c.StorageStart()
	if err != nil {
		return err
	}

	// handle quota: at this point, storage is guaranteed to be ready
	storage := c.Storage()
	if rootDiskDevice["size"] != "" {
		storageTypeName := storage.GetStorageTypeName()
		if (storageTypeName == "lvm" || storageTypeName == "ceph") && c.IsRunning() {
			err = c.ConfigKeySet("volatile.apply_quota", rootDiskDevice["size"])
			if err != nil {
				return err
			}
		} else {
			size, err := shared.ParseByteSizeString(rootDiskDevice["size"])
			if err != nil {
				return err
			}

			err = storage.StorageEntitySetQuota(storagePoolVolumeTypeContainer, size, c)
			if err != nil {
				return err
			}
		}
	}

	if ourStart {
		defer c.StorageStop()
	}

	err = writeBackupFile(c)
	if err != nil {
		return err
	}

	return nil
}

func containerLoadById(s *state.State, id int) (container, error) {
	// Get the DB record
	project, name, err := s.Cluster.ContainerProjectAndName(id)
	if err != nil {
		return nil, err
	}

	return containerLoadByProjectAndName(s, project, name)
}

func containerLoadByProjectAndName(s *state.State, project, name string) (container, error) {
	// Get the DB record
	var container *db.Container
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		container, err = tx.ContainerGet(project, name)
		if err != nil {
			return errors.Wrapf(err, "Failed to fetch container %q in project %q", name, project)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	args := db.ContainerToArgs(container)

	c, err := containerLXCLoad(s, args, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load container")
	}

	return c, nil
}

func containerLoadByProject(s *state.State, project string) ([]container, error) {
	// Get all the containers
	var cts []db.Container
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.ContainerFilter{
			Project: project,
			Type:    int(db.CTypeRegular),
		}
		var err error
		cts, err = tx.ContainerList(filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return containerLoadAllInternal(cts, s)
}

// Load all containers across all projects.
func containerLoadFromAllProjects(s *state.State) ([]container, error) {
	var projects []string

	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		projects, err = tx.ProjectNames()
		return err
	})
	if err != nil {
		return nil, err
	}

	containers := []container{}
	for _, project := range projects {
		projectContainers, err := containerLoadByProject(s, project)
		if err != nil {
			return nil, errors.Wrapf(nil, "Load containers in project %s", project)
		}
		containers = append(containers, projectContainers...)
	}

	return containers, nil
}

// Legacy interface.
func containerLoadAll(s *state.State) ([]container, error) {
	return containerLoadByProject(s, "default")
}

// Load all containers of this nodes.
func containerLoadNodeAll(s *state.State) ([]container, error) {
	// Get all the container arguments
	var cts []db.Container
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.ContainerNodeList()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return containerLoadAllInternal(cts, s)
}

// Load all containers of this nodes under the given project.
func containerLoadNodeProjectAll(s *state.State, project string) ([]container, error) {
	// Get all the container arguments
	var cts []db.Container
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.ContainerNodeProjectList(project)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return containerLoadAllInternal(cts, s)
}

func containerLoadAllInternal(cts []db.Container, s *state.State) ([]container, error) {
	// Figure out what profiles are in use
	profiles := map[string]map[string]api.Profile{}
	for _, cArgs := range cts {
		projectProfiles, ok := profiles[cArgs.Project]
		if !ok {
			projectProfiles = map[string]api.Profile{}
			profiles[cArgs.Project] = projectProfiles
		}
		for _, profile := range cArgs.Profiles {
			_, ok := projectProfiles[profile]
			if !ok {
				projectProfiles[profile] = api.Profile{}
			}
		}
	}

	// Get the profile data
	for project, projectProfiles := range profiles {
		for name := range projectProfiles {
			_, profile, err := s.Cluster.ProfileGet(project, name)
			if err != nil {
				return nil, err
			}

			projectProfiles[name] = *profile
		}
	}

	// Load the container structs
	containers := []container{}
	for _, container := range cts {
		// Figure out the container's profiles
		cProfiles := []api.Profile{}
		for _, name := range container.Profiles {
			cProfiles = append(cProfiles, profiles[container.Project][name])
		}

		args := db.ContainerToArgs(&container)

		ct, err := containerLXCLoad(s, args, cProfiles)
		if err != nil {
			return nil, err
		}

		containers = append(containers, ct)
	}

	return containers, nil
}

func containerCompareSnapshots(source container, target container) ([]container, []container, error) {
	// Get the source snapshots
	sourceSnapshots, err := source.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Get the target snapshots
	targetSnapshots, err := target.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Compare source and target
	sourceSnapshotsTime := map[string]time.Time{}
	targetSnapshotsTime := map[string]time.Time{}

	toDelete := []container{}
	toSync := []container{}

	for _, snap := range sourceSnapshots {
		_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())

		sourceSnapshotsTime[snapName] = snap.CreationDate()
	}

	for _, snap := range targetSnapshots {
		_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())

		targetSnapshotsTime[snapName] = snap.CreationDate()
		existDate, exists := sourceSnapshotsTime[snapName]
		if !exists {
			toDelete = append(toDelete, snap)
		} else if existDate != snap.CreationDate() {
			toDelete = append(toDelete, snap)
		}
	}

	for _, snap := range sourceSnapshots {
		_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())

		existDate, exists := targetSnapshotsTime[snapName]
		if !exists || existDate != snap.CreationDate() {
			toSync = append(toSync, snap)
		}
	}

	return toSync, toDelete, nil
}

func autoCreateContainerSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Load all local containers
		allContainers, err := containerLoadNodeAll(d.State())
		if err != nil {
			logger.Error("Failed to load containers for scheduled snapshots", log.Ctx{"err": err})
		}

		// Figure out which need snapshotting (if any)
		containers := []container{}
		for _, c := range allContainers {
			schedule := c.LocalConfig()["snapshots.schedule"]

			if schedule == "" {
				continue
			}

			// Extend our schedule to one that is accepted by the used cron parser
			sched, err := cron.Parse(fmt.Sprintf("* %s", schedule))
			if err != nil {
				continue
			}

			// Check if it's time to snapshot
			now := time.Now()
			next := sched.Next(now)

			if now.Add(time.Minute).Before(next) {
				continue
			}

			// Check if the container is running
			if !shared.IsTrue(c.LocalConfig()["snapshots.schedule.stopped"]) && !c.IsRunning() {
				continue
			}

			containers = append(containers, c)
		}

		if len(containers) == 0 {
			return
		}

		opRun := func(op *operation) error {
			return autoCreateContainerSnapshots(ctx, d, containers)
		}

		op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationSnapshotCreate, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start create snapshot operation", log.Ctx{"err": err})
		}

		logger.Info("Creating scheduled container snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to create scheduled container snapshots", log.Ctx{"err": err})
		}

		logger.Info("Done creating scheduled container snapshots")
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func autoCreateContainerSnapshots(ctx context.Context, d *Daemon, containers []container) error {
	// Make the snapshots
	for _, c := range containers {
		ch := make(chan error)
		go func() {
			snapshotName, err := containerDetermineNextSnapshotName(d, c, "snap%d")
			if err != nil {
				logger.Error("Error retrieving next snapshot name", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			snapshotName = fmt.Sprintf("%s%s%s", c.Name(), shared.SnapshotDelimiter, snapshotName)

			args := db.ContainerArgs{
				Architecture: c.Architecture(),
				Config:       c.LocalConfig(),
				Ctype:        db.CTypeSnapshot,
				Devices:      c.LocalDevices(),
				Ephemeral:    c.IsEphemeral(),
				Name:         snapshotName,
				Profiles:     c.Profiles(),
				Project:      c.Project(),
				Stateful:     false,
			}

			_, err = containerCreateAsSnapshot(d.State(), args, c)
			if err != nil {
				logger.Error("Error creating snapshots", log.Ctx{"err": err, "container": c})
			}

			ch <- nil
		}()
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

func containerDetermineNextSnapshotName(d *Daemon, c container, defaultPattern string) (string, error) {
	var err error

	pattern := c.LocalConfig()["snapshots.pattern"]
	if pattern == "" {
		pattern = defaultPattern
	}

	pattern, err = shared.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		i := d.cluster.ContainerNextSnapshot(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := c.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := containerGetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = fmt.Sprintf("%s-%%d", pattern)
		i := d.cluster.ContainerNextSnapshot(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}
