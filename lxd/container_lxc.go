package main

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/flosch/pongo2.v3"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

// Global variables
var lxcStoppingContainersLock sync.Mutex
var lxcStoppingContainers map[int]*sync.WaitGroup = make(map[int]*sync.WaitGroup)

// Helper functions
func lxcSetConfigItem(c *lxc.Container, key string, value string) error {
	if c == nil {
		return fmt.Errorf("Uninitialized go-lxc struct")
	}

	err := c.SetConfigItem(key, value)
	if err != nil {
		return fmt.Errorf("Failed to set LXC config: %s=%s", key, value)
	}

	return nil
}

func lxcValidConfig(rawLxc string) error {
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
			return fmt.Errorf("Invalid raw.lxc line: %s", line)
		}

		// Blacklist some keys
		if strings.ToLower(strings.Trim(membs[0], " \t")) == "lxc.logfile" {
			return fmt.Errorf("Setting lxc.logfile is not allowed")
		}

		if strings.HasPrefix(strings.ToLower(strings.Trim(membs[0], " \t")), "lxc.network.") {
			return fmt.Errorf("Setting lxc.network keys is not allowed")
		}
	}

	return nil
}

// Loader functions
func containerLXCCreate(d *Daemon, args containerArgs) (container, error) {
	// Create the container struct
	c := &containerLXC{
		daemon:       d,
		id:           args.Id,
		name:         args.Name,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		cType:        args.Ctype,
		profiles:     args.Profiles,
		localConfig:  args.Config,
		localDevices: args.Devices}

	// No need to detect storage here, its a new container.
	c.storage = d.Storage

	// Load the config
	err := c.init()
	if err != nil {
		c.Delete()
		return nil, err
	}

	// Look for a rootfs entry
	rootfs := false
	for _, m := range c.expandedDevices {
		if m["type"] == "disk" && m["path"] == "/" {
			rootfs = true
			break
		}
	}

	if !rootfs {
		deviceName := "root"
		for {
			if c.expandedDevices[deviceName] == nil {
				break
			}

			deviceName += "_"
		}

		c.localDevices[deviceName] = shared.Device{"type": "disk", "path": "/"}

		updateArgs := containerArgs{
			Architecture: c.architecture,
			Config:       c.localConfig,
			Devices:      c.localDevices,
			Ephemeral:    c.ephemeral,
			Profiles:     c.profiles,
		}

		err = c.Update(updateArgs, false)
		if err != nil {
			c.Delete()
			return nil, err
		}
	}

	// Validate expanded config
	err = containerValidConfig(c.expandedConfig, false, true)
	if err != nil {
		c.Delete()
		return nil, err
	}

	err = containerValidDevices(c.expandedDevices, false, true)
	if err != nil {
		c.Delete()
		return nil, err
	}

	// Setup initial idmap config
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

func containerLXCLoad(d *Daemon, args containerArgs) (container, error) {
	// Create the container struct
	c := &containerLXC{
		daemon:       d,
		id:           args.Id,
		name:         args.Name,
		ephemeral:    args.Ephemeral,
		architecture: args.Architecture,
		cType:        args.Ctype,
		profiles:     args.Profiles,
		localConfig:  args.Config,
		localDevices: args.Devices}

	// Detect the storage backend
	s, err := storageForFilename(d, shared.VarPath("containers", strings.Split(c.name, "/")[0]))
	if err != nil {
		return nil, err
	}
	c.storage = s

	// Load the config
	err = c.init()
	if err != nil {
		return nil, err
	}

	return c, nil
}

// The LXC container driver
type containerLXC struct {
	// Properties
	architecture int
	cType        containerType
	ephemeral    bool
	id           int
	name         string

	// Config
	expandedConfig  map[string]string
	expandedDevices shared.Devices
	fromHook        bool
	localConfig     map[string]string
	localDevices    shared.Devices
	profiles        []string

	// Cache
	c        *lxc.Container
	daemon   *Daemon
	idmapset *shared.IdmapSet
	storage  storage
}

func (c *containerLXC) init() error {
	// Compute the expanded config and device list
	err := c.expandConfig()
	if err != nil {
		return err
	}

	err = c.expandDevices()
	if err != nil {
		return err
	}

	// Setup the Idmap
	if !c.IsPrivileged() {
		if c.daemon.IdmapSet == nil {
			return fmt.Errorf("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported.")
		}
		c.idmapset = c.daemon.IdmapSet
	}

	return nil
}

func (c *containerLXC) initLXC() error {
	// Check if being called from a hook
	if c.fromHook {
		return fmt.Errorf("You can't use go-lxc from inside a LXC hook.")
	}

	// Check if already initialized
	if c.c != nil {
		return nil
	}

	// Load the go-lxc struct
	cc, err := lxc.NewContainer(c.Name(), c.daemon.lxcpath)
	if err != nil {
		return err
	}

	// Base config
	err = lxcSetConfigItem(cc, "lxc.cap.drop", "mac_admin mac_override sys_time sys_module")
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.mount.auto", "cgroup:mixed proc:mixed sys:mixed")
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.autodev", "1")
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.pts", "1024")
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional")
	if err != nil {
		return err
	}

	for _, mnt := range []string{"/proc/sys/fs/binfmt_misc", "/sys/firmware/efi/efivars", "/sys/fs/fuse/connections", "/sys/fs/pstore", "/sys/kernel/debug", "/sys/kernel/security"} {
		err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none bind,optional", mnt, strings.TrimPrefix(mnt, "/")))
		if err != nil {
			return err
		}
	}

	// For lxcfs
	templateConfDir := os.Getenv("LXD_LXC_TEMPLATE_CONFIG")
	if templateConfDir == "" {
		templateConfDir = "/usr/share/lxc/config"
	}

	if shared.PathExists(fmt.Sprintf("%s/common.conf.d/", templateConfDir)) {
		err = lxcSetConfigItem(cc, "lxc.include", fmt.Sprintf("%s/common.conf.d/", templateConfDir))
		if err != nil {
			return err
		}
	}

	// Configure devices cgroup
	if c.IsPrivileged() && !runningInUserns && cgDevicesController {
		err = lxcSetConfigItem(cc, "lxc.cgroup.devices.deny", "a")
		if err != nil {
			return err
		}

		for _, dev := range []string{"c *:* m", "b *:* m", "c 5:1 rwm", "c 1:7 rwm", "c 1:3 rwm", "c 1:8 rwm", "c 1:9 rwm", "c 5:2 rwm", "c 136:* rwm"} {
			err = lxcSetConfigItem(cc, "lxc.cgroup.devices.allow", dev)
			if err != nil {
				return err
			}
		}
	}

	if c.IsNesting() {
		/*
		 * mount extra /proc and /sys to work around kernel
		 * restrictions on remounting them when covered
		 */
		err = lxcSetConfigItem(cc, "lxc.mount.entry", "proc dev/.lxc/proc proc create=dir,optional")
		if err != nil {
			return err
		}

		err = lxcSetConfigItem(cc, "lxc.mount.entry", "sys dev/.lxc/sys sysfs create=dir,optional")
		if err != nil {
			return err
		}
	}

	// Setup logging
	logfile := c.LogFilePath()

	err = cc.SetLogFile(logfile)
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.loglevel", "0")
	if err != nil {
		return err
	}

	// Setup architecture
	personality, err := shared.ArchitecturePersonality(c.architecture)
	if err != nil {
		personality, err = shared.ArchitecturePersonality(c.daemon.architectures[0])
		if err != nil {
			return err
		}
	}

	err = lxcSetConfigItem(cc, "lxc.arch", personality)
	if err != nil {
		return err
	}

	// Setup the hooks
	err = lxcSetConfigItem(cc, "lxc.hook.pre-start", fmt.Sprintf("%s callhook %s %d start", c.daemon.execPath, shared.VarPath(""), c.id))
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.hook.post-stop", fmt.Sprintf("%s callhook %s %d stop", c.daemon.execPath, shared.VarPath(""), c.id))
	if err != nil {
		return err
	}

	// Setup the console
	err = lxcSetConfigItem(cc, "lxc.tty", "0")
	if err != nil {
		return err
	}

	// FIXME: Should go away once CRIU supports checkpoint/restore of /dev/console
	err = lxcSetConfigItem(cc, "lxc.console", "none")
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.cgroup.devices.deny", "c 5:1 rwm")
	if err != nil {
		return err
	}

	// Setup the hostname
	err = lxcSetConfigItem(cc, "lxc.utsname", c.Name())
	if err != nil {
		return err
	}

	// Setup devlxd
	err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s dev/lxd none bind,create=dir 0 0", shared.VarPath("devlxd")))
	if err != nil {
		return err
	}

	// Setup AppArmor
	if aaAvailable {
		if aaConfined || !aaAdmin {
			// If confined but otherwise able to use AppArmor, use our own profile
			curProfile := aaProfile()
			curProfile = strings.TrimSuffix(curProfile, " (enforce)")
			err = lxcSetConfigItem(cc, "lxc.aa_profile", curProfile)
			if err != nil {
				return err
			}
		} else {
			// If not currently confined, use the container's profile
			err := lxcSetConfigItem(cc, "lxc.aa_profile", AAProfileFull(c))
			if err != nil {
				return err
			}
		}
	}

	// Setup Seccomp
	err = lxcSetConfigItem(cc, "lxc.seccomp", SeccompProfilePath(c))
	if err != nil {
		return err
	}

	// Setup idmap
	if c.idmapset != nil {
		lines := c.idmapset.ToLxcString()
		for _, line := range lines {
			err := lxcSetConfigItem(cc, "lxc.id_map", line+"\n")
			if err != nil {
				return err
			}
		}
	}

	// Setup environment
	for k, v := range c.expandedConfig {
		if strings.HasPrefix(k, "environment.") {
			err = lxcSetConfigItem(cc, "lxc.environment", fmt.Sprintf("%s=%s", strings.TrimPrefix(k, "environment."), v))
			if err != nil {
				return err
			}
		}
	}

	// Memory limits
	if cgMemoryController {
		memory := c.expandedConfig["limits.memory"]
		memoryEnforce := c.expandedConfig["limits.memory.enforce"]
		memorySwap := c.expandedConfig["limits.memory.swap"]
		memorySwapPriority := c.expandedConfig["limits.memory.swap.priority"]

		// Configure the memory limits
		if memory != "" {
			var valueInt int64
			if strings.HasSuffix(memory, "%") {
				percent, err := strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
				if err != nil {
					return err
				}

				memoryTotal, err := deviceTotalMemory()
				if err != nil {
					return err
				}

				valueInt = int64((memoryTotal / 100) * percent)
			} else {
				valueInt, err = deviceParseBytes(memory)
				if err != nil {
					return err
				}
			}

			if memoryEnforce == "soft" {
				err = lxcSetConfigItem(cc, "lxc.cgroup.memory.soft_limit_in_bytes", fmt.Sprintf("%d", valueInt))
				if err != nil {
					return err
				}
			} else {
				if memorySwap != "false" && cgSwapAccounting {
					err = lxcSetConfigItem(cc, "lxc.cgroup.memory.limit_in_bytes", fmt.Sprintf("%d", valueInt))
					if err != nil {
						return err
					}
					err = lxcSetConfigItem(cc, "lxc.cgroup.memory.memsw.limit_in_bytes", fmt.Sprintf("%d", valueInt))
					if err != nil {
						return err
					}
				} else {
					err = lxcSetConfigItem(cc, "lxc.cgroup.memory.limit_in_bytes", fmt.Sprintf("%d", valueInt))
					if err != nil {
						return err
					}
				}
			}
		}

		// Configure the swappiness
		if memorySwap == "false" {
			err = lxcSetConfigItem(cc, "lxc.cgroup.memory.swappiness", "0")
			if err != nil {
				return err
			}
		} else if memorySwapPriority != "" {
			priority, err := strconv.Atoi(memorySwapPriority)
			if err != nil {
				return err
			}

			err = lxcSetConfigItem(cc, "lxc.cgroup.memory.swappiness", fmt.Sprintf("%d", 60-10+priority))
			if err != nil {
				return err
			}
		}
	}

	// CPU limits
	cpuPriority := c.expandedConfig["limits.cpu.priority"]
	cpuAllowance := c.expandedConfig["limits.cpu.allowance"]

	if (cpuPriority != "" || cpuAllowance != "") && cgCpuController {
		cpuShares, cpuCfsQuota, cpuCfsPeriod, err := deviceParseCPU(cpuAllowance, cpuPriority)
		if err != nil {
			return err
		}

		if cpuShares != "1024" {
			err = lxcSetConfigItem(cc, "lxc.cgroup.cpu.shares", cpuShares)
			if err != nil {
				return err
			}
		}

		if cpuCfsPeriod != "-1" {
			err = lxcSetConfigItem(cc, "lxc.cgroup.cpu.cfs_period_us", cpuCfsPeriod)
			if err != nil {
				return err
			}
		}

		if cpuCfsQuota != "-1" {
			err = lxcSetConfigItem(cc, "lxc.cgroup.cpu.cfs_quota_us", cpuCfsQuota)
			if err != nil {
				return err
			}
		}
	}

	// Setup devices
	for k, m := range c.expandedDevices {
		if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
			// Prepare all the paths
			srcPath := m["path"]
			tgtPath := strings.TrimPrefix(srcPath, "/")
			devName := fmt.Sprintf("unix.%s", strings.Replace(tgtPath, "/", "-", -1))
			devPath := filepath.Join(c.DevicesPath(), devName)

			// Set the bind-mount entry
			err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none bind,create=file", devPath, tgtPath))
			if err != nil {
				return err
			}
		} else if m["type"] == "nic" {
			// Fill in some fields from volatile
			m, err = c.fillNetworkDevice(k, m)

			// Interface type specific configuration
			if shared.StringInSlice(m["nictype"], []string{"bridged", "p2p"}) {
				err = lxcSetConfigItem(cc, "lxc.network.type", "veth")
				if err != nil {
					return err
				}
			} else if m["nictype"] == "physical" {
				err = lxcSetConfigItem(cc, "lxc.network.type", "phys")
				if err != nil {
					return err
				}
			} else if m["nictype"] == "macvlan" {
				err = lxcSetConfigItem(cc, "lxc.network.type", "macvlan")
				if err != nil {
					return err
				}

				err = lxcSetConfigItem(cc, "lxc.network.macvlan.mode", "bridge")
				if err != nil {
					return err
				}
			}
			if shared.StringInSlice(m["nictype"], []string{"bridged", "physical", "macvlan"}) {
				err = lxcSetConfigItem(cc, "lxc.network.link", m["parent"])
				if err != nil {
					return err
				}
			}

			// Host Virtual NIC name
			if m["host_name"] != "" {
				err = lxcSetConfigItem(cc, "lxc.network.veth.pair", m["host_name"])
				if err != nil {
					return err
				}
			}

			// MAC address
			if m["hwaddr"] != "" {
				err = lxcSetConfigItem(cc, "lxc.network.hwaddr", m["hwaddr"])
				if err != nil {
					return err
				}
			}

			// MTU
			if m["mtu"] != "" {
				err = lxcSetConfigItem(cc, "lxc.network.mtu", m["mtu"])
				if err != nil {
					return err
				}
			}

			// Name
			if m["name"] != "" {
				err = lxcSetConfigItem(cc, "lxc.network.name", m["name"])
				if err != nil {
					return err
				}
			}
		} else if m["type"] == "disk" {
			// Prepare all the paths
			srcPath := m["source"]
			tgtPath := strings.TrimPrefix(m["path"], "/")
			devName := fmt.Sprintf("disk.%s", strings.Replace(tgtPath, "/", "-", -1))
			devPath := filepath.Join(c.DevicesPath(), devName)

			// Various option checks
			isOptional := m["optional"] == "1" || m["optional"] == "true"
			isReadOnly := m["readonly"] == "1" || m["readonly"] == "true"
			isFile := !shared.IsDir(srcPath) && !deviceIsDevice(srcPath)

			// Deal with a rootfs
			if tgtPath == "" {
				// Set the rootfs path
				err = lxcSetConfigItem(cc, "lxc.rootfs", c.RootfsPath())
				if err != nil {
					return err
				}

				// Read-only rootfs (unlikely to work very well)
				if isReadOnly {
					err = lxcSetConfigItem(cc, "lxc.rootfs.options", "ro")
					if err != nil {
						return err
					}
				}
			} else {
				options := []string{}
				if isReadOnly {
					options = append(options, "ro")
				}

				if isOptional {
					options = append(options, "optional")
				}

				if isFile {
					options = append(options, "create=file")
				} else {
					options = append(options, "create=dir")
				}

				err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none bind,%s", devPath, tgtPath, strings.Join(options, ",")))
				if err != nil {
					return err
				}
			}
		}
	}

	// Setup shmounts
	err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s dev/.lxd-mounts none bind,create=dir 0 0", shared.VarPath("shmounts", c.Name())))
	if err != nil {
		return err
	}

	// Apply raw.lxc
	if lxcConfig, ok := c.expandedConfig["raw.lxc"]; ok {
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

		if err := cc.LoadConfigFile(f.Name()); err != nil {
			return fmt.Errorf("Failed to load raw.lxc")
		}
	}

	c.c = cc

	return nil
}

// Config handling
func (c *containerLXC) expandConfig() error {
	config := map[string]string{}

	// Apply all the profiles
	for _, name := range c.profiles {
		profileConfig, err := dbProfileConfig(c.daemon.db, name)
		if err != nil {
			return err
		}

		for k, v := range profileConfig {
			config[k] = v
		}
	}

	// Stick the local config on top
	for k, v := range c.localConfig {
		config[k] = v
	}

	c.expandedConfig = config
	return nil
}

func (c *containerLXC) expandDevices() error {
	devices := shared.Devices{}

	// Apply all the profiles
	for _, p := range c.profiles {
		profileDevices, err := dbDevices(c.daemon.db, p, true)
		if err != nil {
			return err
		}

		for k, v := range profileDevices {
			devices[k] = v
		}
	}

	// Stick local devices on top
	for k, v := range c.localDevices {
		devices[k] = v
	}

	c.expandedDevices = devices
	return nil
}

// Start functions
func (c *containerLXC) startCommon() (string, error) {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return "", err
	}

	// Check that we're not already running
	if c.IsRunning() {
		return "", fmt.Errorf("The container is already running")
	}

	// Load any required kernel modules
	kernelModules := c.expandedConfig["linux.kernel_modules"]
	if kernelModules != "" {
		for _, module := range strings.Split(kernelModules, ",") {
			module = strings.TrimPrefix(module, " ")
			out, err := exec.Command("modprobe", module).CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("Failed to load kernel module '%s': %s", module, out)
			}
		}
	}

	/* Deal with idmap changes */
	idmap := c.IdmapSet()

	lastIdmap, err := c.LastIdmapSet()
	if err != nil {
		return "", err
	}

	var jsonIdmap string
	if idmap != nil {
		idmapBytes, err := json.Marshal(idmap.Idmap)
		if err != nil {
			return "", err
		}
		jsonIdmap = string(idmapBytes)
	} else {
		jsonIdmap = "[]"
	}

	if !reflect.DeepEqual(idmap, lastIdmap) {
		shared.Debugf("Container idmap changed, remapping")

		err := c.StorageStart()
		if err != nil {
			return "", err
		}

		if lastIdmap != nil {
			err = lastIdmap.UnshiftRootfs(c.RootfsPath())
			if err != nil {
				c.StorageStop()
				return "", err
			}
		}

		if idmap != nil {
			err = idmap.ShiftRootfs(c.RootfsPath())
			if err != nil {
				c.StorageStop()
				return "", err
			}
		}
	}

	err = c.ConfigKeySet("volatile.last_state.idmap", jsonIdmap)
	if err != nil {
		return "", err
	}

	// Generate the Seccomp profile
	if err := SeccompCreateProfile(c); err != nil {
		return "", err
	}

	// Cleanup any existing leftover devices
	c.removeUnixDevices()
	c.removeDiskDevices()

	// Create the devices
	for k, m := range c.expandedDevices {
		if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
			// Unix device
			devPath, err := c.createUnixDevice(k, m)
			if err != nil {
				return "", err
			}

			// Add the new device cgroup rule
			dType, dMajor, dMinor, err := deviceGetAttributes(devPath)
			if err != nil {
				return "", err
			}

			err = lxcSetConfigItem(c.c, "lxc.cgroup.devices.allow", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor))
			if err != nil {
				return "", fmt.Errorf("Failed to add cgroup rule for device")
			}
		} else if m["type"] == "disk" {
			// Disk device
			if m["path"] != "/" {
				_, err := c.createDiskDevice(k, m)
				if err != nil {
					return "", err
				}
			}
		}
	}

	// Create any missing directory
	err = os.MkdirAll(c.LogPath(), 0700)
	if err != nil {
		return "", err
	}

	err = os.MkdirAll(shared.VarPath("devices", c.Name()), 0711)
	if err != nil {
		return "", err
	}

	err = os.MkdirAll(shared.VarPath("shmounts", c.Name()), 0711)
	if err != nil {
		return "", err
	}

	// Cleanup any leftover volatile entries
	netNames := []string{}
	for k, v := range c.expandedDevices {
		if v["type"] == "nic" {
			netNames = append(netNames, k)
		}
	}

	for k, _ := range c.localConfig {
		// We only care about volatile
		if !strings.HasPrefix(k, "volatile.") {
			continue
		}

		// Confirm it's a key of format volatile.<device>.<key>
		fields := strings.SplitN(k, ".", 3)
		if len(fields) != 3 {
			continue
		}

		// The only device keys we care about are name and hwaddr
		if !shared.StringInSlice(fields[2], []string{"name", "hwaddr"}) {
			continue
		}

		// Check if the device still exists
		if shared.StringInSlice(fields[1], netNames) {
			// Don't remove the volatile entry if the device doesn't have the matching field set
			if c.expandedDevices[fields[1]][fields[2]] == "" {
				continue
			}
		}

		// Remove the volatile key from the DB
		err := dbContainerConfigRemove(c.daemon.db, c.id, k)
		if err != nil {
			return "", err
		}

		// Remove the volatile key from the in-memory configs
		delete(c.localConfig, k)
		delete(c.expandedConfig, k)
	}

	// Generate the LXC config
	f, err := ioutil.TempFile("", "lxd_lxc_startconfig_")
	if err != nil {
		return "", err
	}

	configPath := f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(configPath)
		return "", err
	}
	f.Close()

	err = c.c.SaveConfigFile(configPath)
	if err != nil {
		os.Remove(configPath)
		return "", err
	}

	return configPath, nil
}

func (c *containerLXC) Start() error {
	// Wait for container tear down to finish
	wgStopping, stopping := lxcStoppingContainers[c.id]
	if stopping {
		wgStopping.Wait()
	}

	// Run the shared start code
	configPath, err := c.startCommon()
	if err != nil {
		return err
	}

	// Start the LXC container
	out, err := exec.Command(
		c.daemon.execPath,
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
		return fmt.Errorf(
			"Error calling 'lxd forkstart %s %s %s': err='%v'",
			c.name,
			c.daemon.lxcpath,
			filepath.Join(c.LogPath(), "lxc.conf"),
			err)
	}

	return nil
}

func (c *containerLXC) StartFromMigration(imagesDir string) error {
	// Run the shared start code
	configPath, err := c.startCommon()
	if err != nil {
		return err
	}

	// Start the LXC container
	out, err := exec.Command(
		c.daemon.execPath,
		"forkmigrate",
		c.name,
		c.daemon.lxcpath,
		configPath,
		imagesDir).CombinedOutput()

	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			shared.Debugf("forkmigrate: %s", line)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkmigrate %s %s %s %s': err='%v'",
			c.name,
			c.daemon.lxcpath,
			filepath.Join(c.LogPath(), "lxc.conf"),
			imagesDir,
			err)
	}

	return nil
}

func (c *containerLXC) OnStart() error {
	// Make sure we can't call go-lxc functions by mistake
	c.fromHook = true

	// Start the storage for this container
	err := c.StorageStart()
	if err != nil {
		return err
	}

	// Load the container AppArmor profile
	err = AALoadProfile(c)
	if err != nil {
		c.StorageStop()
		return err
	}

	// Template anything that needs templating
	err = c.TemplateApply("start")
	if err != nil {
		c.StorageStop()
		return err
	}

	// Trigger a rebalance
	deviceTaskSchedulerTrigger("container", c.name, "started")

	return nil
}

// Container shutdown locking
func (c *containerLXC) setupStopping() *sync.WaitGroup {
	// Handle locking
	lxcStoppingContainersLock.Lock()
	defer lxcStoppingContainersLock.Unlock()

	// Existing entry
	wg, stopping := lxcStoppingContainers[c.id]
	if stopping {
		return wg
	}

	// Setup new entry
	lxcStoppingContainers[c.id] = &sync.WaitGroup{}

	go func(wg *sync.WaitGroup, id int) {
		wg.Wait()

		lxcStoppingContainersLock.Lock()
		defer lxcStoppingContainersLock.Unlock()

		delete(lxcStoppingContainers, id)
	}(lxcStoppingContainers[c.id], c.id)

	return lxcStoppingContainers[c.id]
}

// Stop functions
func (c *containerLXC) Stop() error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	// Attempt to freeze the container first, helps massively with fork bombs
	c.Freeze()

	// Handle locking
	wg := c.setupStopping()

	// Stop the container
	wg.Add(1)
	if err := c.c.Stop(); err != nil {
		wg.Done()
		return err
	}

	// Mark ourselves as done
	wg.Done()

	// Wait for any other teardown routines to finish
	wg.Wait()

	return nil
}

func (c *containerLXC) Shutdown(timeout time.Duration) error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	// Handle locking
	wg := c.setupStopping()

	// Shutdown the container
	wg.Add(1)
	if err := c.c.Shutdown(timeout); err != nil {
		wg.Done()
		return err
	}

	// Mark ourselves as done
	wg.Done()

	// Wait for any other teardown routines to finish
	wg.Wait()

	return nil
}

func (c *containerLXC) OnStop(target string) error {
	// Get locking
	wg, stopping := lxcStoppingContainers[c.id]
	if wg != nil {
		wg.Add(1)
	}

	// Make sure we can't call go-lxc functions by mistake
	c.fromHook = true

	// Stop the storage for this container
	err := c.StorageStop()
	if err != nil {
		return err
	}

	// Unlock the apparmor profile
	err = AAUnloadProfile(c)
	if err != nil {
		return err
	}

	// FIXME: The go routine can go away once we can rely on LXC_TARGET
	go func(c *containerLXC, target string, wg *sync.WaitGroup) {
		// Unlock on return
		if wg != nil {
			defer wg.Done()
		}

		if target == "unknown" && stopping {
			target = "stop"
		}

		if target == "unknown" {
			time.Sleep(5 * time.Second)

			newContainer, err := containerLoadByName(c.daemon, c.Name())
			if err != nil {
				return
			}

			if newContainer.Id() != c.id {
				return
			}

			if newContainer.IsRunning() {
				return
			}
		}

		// Clean all the unix devices
		err = c.removeUnixDevices()
		if err != nil {
			shared.Log.Error("Unable to remove unix devices")
		}

		// Clean all the disk devices
		err = c.removeDiskDevices()
		if err != nil {
			shared.Log.Error("Unable to remove disk devices")
		}

		// Reboot the container
		if target == "reboot" {
			c.Start()
			return
		}

		// Trigger a rebalance
		deviceTaskSchedulerTrigger("container", c.name, "stopped")

		// Destroy ephemeral containers
		if c.ephemeral {
			c.Delete()
		}
	}(c, target, wg)

	return nil
}

// Freezer functions
func (c *containerLXC) Freeze() error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	return c.c.Freeze()
}

func (c *containerLXC) Unfreeze() error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	return c.c.Unfreeze()
}

func (c *containerLXC) RenderState() (*shared.ContainerState, error) {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil, err
	}

	// FIXME: RenderState shouldn't directly access the go-lxc struct
	statusCode := shared.FromLXCState(int(c.c.State()))
	status := shared.ContainerStatus{
		Status:     statusCode.String(),
		StatusCode: statusCode,
	}

	if c.IsRunning() {
		pid := c.InitPID()
		status.Init = pid
		status.Processcount = c.processcountGet()
		status.Ips = c.ipsGet()
	}

	return &shared.ContainerState{
		Architecture:    c.architecture,
		Config:          c.localConfig,
		Devices:         c.localDevices,
		Ephemeral:       c.ephemeral,
		ExpandedConfig:  c.expandedConfig,
		ExpandedDevices: c.expandedDevices,
		Name:            c.name,
		Profiles:        c.profiles,
		Status:          status,
	}, nil
}

func (c *containerLXC) Snapshots() ([]container, error) {
	// Get all the snapshots
	snaps, err := dbContainerGetSnapshots(c.daemon.db, c.name)
	if err != nil {
		return nil, err
	}

	// Build the snapshot list
	containers := []container{}
	for _, snapName := range snaps {
		snap, err := containerLoadByName(c.daemon, snapName)
		if err != nil {
			return nil, err
		}

		containers = append(containers, snap)
	}

	return containers, nil
}

func (c *containerLXC) Restore(sourceContainer container) error {
	// Check if we can restore the container
	err := c.storage.ContainerCanRestore(c, sourceContainer)
	if err != nil {
		return err
	}

	// Stop the container
	wasRunning := false
	if c.IsRunning() {
		wasRunning = true
		if err := c.Stop(); err != nil {
			shared.Log.Error(
				"Could not stop container",
				log.Ctx{
					"container": c.Name(),
					"err":       err})
			return err
		}
	}

	// Restore the rootfs
	err = c.storage.ContainerRestore(c, sourceContainer)
	if err != nil {
		shared.Log.Error("Restoring the filesystem failed",
			log.Ctx{
				"source":      sourceContainer.Name(),
				"destination": c.Name()})
		return err
	}

	// Restore the configuration
	args := containerArgs{
		Architecture: sourceContainer.Architecture(),
		Config:       sourceContainer.LocalConfig(),
		Devices:      sourceContainer.LocalDevices(),
		Ephemeral:    sourceContainer.IsEphemeral(),
		Profiles:     sourceContainer.Profiles(),
	}

	err = c.Update(args, false)
	if err != nil {
		shared.Log.Error("Restoring the configuration failed",
			log.Ctx{
				"source":      sourceContainer.Name(),
				"destination": c.Name()})

		return err
	}

	// Restart the container
	if wasRunning {
		c.Start()
	}

	return nil
}

func (c *containerLXC) cleanup() {
	// Unmount any leftovers
	c.removeUnixDevices()
	c.removeDiskDevices()

	// Remove the security profiles
	AADeleteProfile(c)
	SeccompDeleteProfile(c)

	// Remove the devices path
	os.RemoveAll(c.DevicesPath())

	// Remove the shmounts path
	os.RemoveAll(shared.VarPath("shmounts", c.Name()))
}

func (c *containerLXC) Delete() error {
	if c.IsSnapshot() {
		// Remove the snapshot
		if err := c.storage.ContainerSnapshotDelete(c); err != nil {
			return err
		}
	} else {
		// Remove all snapshot
		if err := containerDeleteSnapshots(c.daemon, c.Name()); err != nil {
			return err
		}

		// Clean things up
		c.cleanup()

		// Delete the container from disk
		if shared.PathExists(c.Path()) {
			if err := c.storage.ContainerDelete(c); err != nil {
				return err
			}
		}
	}

	// Remove the database record
	if err := dbContainerRemove(c.daemon.db, c.Name()); err != nil {
		return err
	}

	return nil
}

func (c *containerLXC) Rename(newName string) error {
	oldName := c.Name()

	// Sanity checks
	if !c.IsSnapshot() && !shared.ValidHostname(newName) {
		return fmt.Errorf("Invalid container name")
	}

	if c.IsRunning() {
		return fmt.Errorf("renaming of running container not allowed")
	}

	// Clean things up
	c.cleanup()

	// Rename the logging path
	os.RemoveAll(shared.LogPath(newName))
	err := os.Rename(c.LogPath(), shared.LogPath(newName))
	if err != nil {
		return err
	}

	// Rename the storage entry
	if c.IsSnapshot() {
		if err := c.storage.ContainerSnapshotRename(c, newName); err != nil {
			return err
		}
	} else {
		if err := c.storage.ContainerRename(c, newName); err != nil {
			return err
		}
	}

	// Rename the database entry
	if err := dbContainerRename(c.daemon.db, oldName, newName); err != nil {
		return err
	}

	if !c.IsSnapshot() {
		// Rename all the snapshots
		results, err := dbContainerGetSnapshots(c.daemon.db, oldName)
		if err != nil {
			return err
		}

		for _, sname := range results {
			// Rename the snapshot
			baseSnapName := filepath.Base(sname)
			newSnapshotName := newName + shared.SnapshotDelimiter + baseSnapName
			if err := dbContainerRename(c.daemon.db, sname, newSnapshotName); err != nil {
				return err
			}
		}
	}

	// Set the new name in the struct
	c.name = newName

	// Invalidate the go-lxc cache
	c.c = nil

	return nil
}

func (c *containerLXC) CGroupSet(key string, value string) error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	// Make sure the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't set cgroups on a stopped container")
	}

	err = c.c.SetCgroupItem(key, value)
	if err != nil {
		return fmt.Errorf("Failed to set cgroup %s=\"%s\": %s", key, value, err)
	}

	return nil
}

func (c *containerLXC) ConfigKeySet(key string, value string) error {
	c.localConfig[key] = value

	args := containerArgs{
		Architecture: c.architecture,
		Config:       c.localConfig,
		Devices:      c.localDevices,
		Ephemeral:    c.ephemeral,
		Profiles:     c.profiles,
	}

	return c.Update(args, false)
}

func (c *containerLXC) Update(args containerArgs, userRequested bool) error {
	// Set sane defaults for unset keys
	if args.Architecture == 0 {
		args.Architecture = c.architecture
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.Devices == nil {
		args.Devices = shared.Devices{}
	}

	if args.Profiles == nil {
		args.Profiles = []string{}
	}

	// Validate the new config
	err := containerValidConfig(args.Config, false, false)
	if err != nil {
		return err
	}

	// Validate the new devices
	err = containerValidDevices(args.Devices, false, false)
	if err != nil {
		return err
	}

	// Validate the new profiles
	profiles, err := dbProfiles(c.daemon.db)
	if err != nil {
		return err
	}

	for _, name := range args.Profiles {
		if !shared.StringInSlice(name, profiles) {
			return fmt.Errorf("Profile doesn't exist: %s", name)
		}
	}

	// Validate the new architecture
	if args.Architecture != 0 {
		_, err = shared.ArchitectureName(args.Architecture)
		if err != nil {
			return fmt.Errorf("Invalid architecture id: %s", err)
		}
	}

	// Check that volatile wasn't modified
	if userRequested {
		for k, v := range args.Config {
			if strings.HasPrefix(k, "volatile.") && c.localConfig[k] != v {
				return fmt.Errorf("Volatile keys are read-only.")
			}
		}

		for k, v := range c.localConfig {
			if strings.HasPrefix(k, "volatile.") && args.Config[k] != v {
				return fmt.Errorf("Volatile keys are read-only.")
			}
		}
	}

	// Get a copy of the old configuration
	oldArchitecture := 0
	err = shared.DeepCopy(&c.architecture, &oldArchitecture)
	if err != nil {
		return err
	}

	oldEphemeral := false
	err = shared.DeepCopy(&c.ephemeral, &oldEphemeral)
	if err != nil {
		return err
	}

	oldExpandedDevices := shared.Devices{}
	err = shared.DeepCopy(&c.expandedDevices, &oldExpandedDevices)
	if err != nil {
		return err
	}

	oldExpandedConfig := map[string]string{}
	err = shared.DeepCopy(&c.expandedConfig, &oldExpandedConfig)
	if err != nil {
		return err
	}

	oldLocalDevices := shared.Devices{}
	err = shared.DeepCopy(&c.localDevices, &oldLocalDevices)
	if err != nil {
		return err
	}

	oldLocalConfig := map[string]string{}
	err = shared.DeepCopy(&c.localConfig, &oldLocalConfig)
	if err != nil {
		return err
	}

	oldProfiles := []string{}
	err = shared.DeepCopy(&c.profiles, &oldProfiles)
	if err != nil {
		return err
	}

	// Define a function which reverts everything
	undoChanges := func() {
		c.architecture = oldArchitecture
		c.ephemeral = oldEphemeral
		c.expandedConfig = oldExpandedConfig
		c.expandedDevices = oldExpandedDevices
		c.localConfig = oldLocalConfig
		c.localDevices = oldLocalDevices
		c.profiles = oldProfiles
		c.initLXC()
		deviceTaskSchedulerTrigger("container", c.name, "changed")
	}

	// Apply the various changes
	c.architecture = args.Architecture
	c.ephemeral = args.Ephemeral
	c.localConfig = args.Config
	c.localDevices = args.Devices
	c.profiles = args.Profiles

	// Expand the config and refresh the LXC config
	err = c.expandConfig()
	if err != nil {
		undoChanges()
		return err
	}

	err = c.expandDevices()
	if err != nil {
		undoChanges()
		return err
	}

	err = c.initLXC()
	if err != nil {
		undoChanges()
		return err
	}

	// Diff the configurations
	changedConfig := []string{}
	for key, _ := range oldExpandedConfig {
		if oldExpandedConfig[key] != c.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key, _ := range c.expandedConfig {
		if oldExpandedConfig[key] != c.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices
	removeDevices, addDevices := oldExpandedDevices.Update(c.expandedDevices)

	// Do some validation of the config diff
	err = containerValidConfig(c.expandedConfig, false, true)
	if err != nil {
		undoChanges()
		return err
	}

	// Do some validation of the devices diff
	err = containerValidDevices(c.expandedDevices, false, true)
	if err != nil {
		undoChanges()
		return err
	}

	// If apparmor changed, re-validate the apparmor profile
	for _, key := range changedConfig {
		if key == "raw.apparmor" || key == "security.nesting" {
			err = AAParseProfile(c)
			if err != nil {
				undoChanges()
				return err
			}
		}
	}

	// Apply disk quota changes
	for _, m := range addDevices {
		var oldRootfsSize string
		for _, m := range oldExpandedDevices {
			if m["type"] == "disk" && m["path"] == "/" {
				oldRootfsSize = m["size"]
				break
			}
		}

		if m["size"] != oldRootfsSize {
			size, err := deviceParseBytes(m["size"])
			if err != nil {
				undoChanges()
				return err
			}

			err = c.storage.ContainerSetQuota(c, size)
			if err != nil {
				undoChanges()
				return err
			}
		}
	}

	// Apply the live changes
	if c.IsRunning() {
		// Confirm that the rootfs source didn't change
		var oldRootfs shared.Device
		for _, m := range oldExpandedDevices {
			if m["type"] == "disk" && m["path"] == "/" {
				oldRootfs = m
				break
			}
		}

		var newRootfs shared.Device
		for _, m := range c.expandedDevices {
			if m["type"] == "disk" && m["path"] == "/" {
				newRootfs = m
				break
			}
		}

		if oldRootfs["source"] != newRootfs["source"] {
			undoChanges()
			return fmt.Errorf("Cannot change the rootfs path of a running container")
		}

		// Live update the container config
		for _, key := range changedConfig {
			value := c.expandedConfig[key]

			if key == "raw.apparmor" || key == "security.nesting" {
				// Update the AppArmor profile
				err = AALoadProfile(c)
				if err != nil {
					undoChanges()
					return err
				}
			} else if key == "linux.kernel_modules" && value != "" {
				for _, module := range strings.Split(value, ",") {
					module = strings.TrimPrefix(module, " ")
					out, err := exec.Command("modprobe", module).CombinedOutput()
					if err != nil {
						undoChanges()
						return fmt.Errorf("Failed to load kernel module '%s': %s", module, out)
					}
				}
			} else if key == "limits.memory" || strings.HasPrefix(key, "limits.memory.") {
				// Skip if no memory CGroup
				if !cgMemoryController {
					continue
				}

				// Set the new memory limit
				memory := c.expandedConfig["limits.memory"]
				memoryEnforce := c.expandedConfig["limits.memory.enforce"]
				memorySwap := c.expandedConfig["limits.memory.swap"]

				// Parse memory
				if memory == "" {
					memory = "-1"
				} else if strings.HasSuffix(memory, "%") {
					percent, err := strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
					if err != nil {
						return err
					}

					memoryTotal, err := deviceTotalMemory()
					if err != nil {
						return err
					}

					memory = fmt.Sprintf("%d", int64((memoryTotal/100)*percent))
				} else {
					valueInt, err := deviceParseBytes(memory)
					if err != nil {
						undoChanges()
						return err
					}
					memory = fmt.Sprintf("%d", valueInt)
				}

				// Reset everything
				if cgSwapAccounting {
					err = c.CGroupSet("memory.memsw.limit_in_bytes", "-1")
					if err != nil {
						undoChanges()
						return err
					}
				}

				err = c.CGroupSet("memory.limit_in_bytes", "-1")
				if err != nil {
					undoChanges()
					return err
				}

				err = c.CGroupSet("memory.soft_limit_in_bytes", "-1")
				if err != nil {
					undoChanges()
					return err
				}

				// Set the new values
				if memoryEnforce == "soft" {
					// Set new limit
					err = c.CGroupSet("memory.soft_limit_in_bytes", memory)
					if err != nil {
						undoChanges()
						return err
					}
				} else {
					if memorySwap != "false" && cgSwapAccounting {
						err = c.CGroupSet("memory.limit_in_bytes", memory)
						if err != nil {
							undoChanges()
							return err
						}
						err = c.CGroupSet("memory.memsw.limit_in_bytes", memory)
						if err != nil {
							undoChanges()
							return err
						}
					} else {
						err = c.CGroupSet("memory.limit_in_bytes", memory)
						if err != nil {
							undoChanges()
							return err
						}
					}
				}

				// Configure the swappiness
				if key == "limits.memory.swap" || key == "limits.memory.swap.priority" {
					memorySwap := c.expandedConfig["limits.memory.swap"]
					memorySwapPriority := c.expandedConfig["limits.memory.swap.priority"]
					if memorySwap == "false" {
						err = c.CGroupSet("memory.swappiness", "0")
						if err != nil {
							undoChanges()
							return err
						}
					} else {
						priority := 0
						if memorySwapPriority != "" {
							priority, err = strconv.Atoi(memorySwapPriority)
							if err != nil {
								undoChanges()
								return err
							}
						}

						err = c.CGroupSet("memory.swappiness", fmt.Sprintf("%d", 60-10+priority))
						if err != nil {
							undoChanges()
							return err
						}
					}
				}
			} else if key == "limits.cpu" {
				// Trigger a scheduler re-run
				deviceTaskSchedulerTrigger("container", c.name, "changed")
			} else if key == "limits.cpu.priority" || key == "limits.cpu.allowance" {
				// Skip if no cpu CGroup
				if !cgCpuController {
					continue
				}

				// Apply new CPU limits
				cpuShares, cpuCfsQuota, cpuCfsPeriod, err := deviceParseCPU(c.expandedConfig["limits.cpu.allowance"], c.expandedConfig["limits.cpu.priority"])
				if err != nil {
					undoChanges()
					return err
				}

				err = c.CGroupSet("cpu.shares", cpuShares)
				if err != nil {
					undoChanges()
					return err
				}

				err = c.CGroupSet("cpu.cfs_period_us", cpuCfsPeriod)
				if err != nil {
					undoChanges()
					return err
				}

				err = c.CGroupSet("cpu.cfs_quota_us", cpuCfsQuota)
				if err != nil {
					undoChanges()
					return err
				}
			}
		}

		// Live update the devices
		for k, m := range removeDevices {
			if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
				err = c.removeUnixDevice(k, m)
				if err != nil {
					undoChanges()
					return err
				}
			} else if m["type"] == "disk" && m["path"] != "/" {
				err = c.removeDiskDevice(k, m)
				if err != nil {
					undoChanges()
					return err
				}
			} else if m["type"] == "nic" {
				err = c.removeNetworkDevice(k, m)
				if err != nil {
					undoChanges()
					return err
				}
			}
		}

		for k, m := range addDevices {
			if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
				err = c.insertUnixDevice(k, m)
				if err != nil {
					undoChanges()
					return err
				}
			} else if m["type"] == "disk" && m["path"] != "/" {
				err = c.insertDiskDevice(k, m)
				if err != nil {
					undoChanges()
					return err
				}
			} else if m["type"] == "nic" {
				err = c.insertNetworkDevice(k, m)
				if err != nil {
					undoChanges()
					return err
				}
			}
		}
	}

	// Finally, apply the changes to the database
	tx, err := dbBegin(c.daemon.db)
	if err != nil {
		undoChanges()
		return err
	}

	err = dbContainerConfigClear(tx, c.id)
	if err != nil {
		tx.Rollback()
		undoChanges()
		return err
	}

	err = dbContainerConfigInsert(tx, c.id, args.Config)
	if err != nil {
		tx.Rollback()
		undoChanges()
		return err
	}

	err = dbContainerProfilesInsert(tx, c.id, args.Profiles)
	if err != nil {
		tx.Rollback()
		undoChanges()
		return err
	}

	err = dbDevicesAdd(tx, "container", int64(c.id), args.Devices)
	if err != nil {
		tx.Rollback()
		undoChanges()
		return err
	}

	if err := txCommit(tx); err != nil {
		undoChanges()
		return err
	}

	return nil
}

func (c *containerLXC) Export(w io.Writer) error {
	if c.IsRunning() {
		return fmt.Errorf("Cannot export a running container as image")
	}

	// Start the storage
	err := c.StorageStart()
	if err != nil {
		return err
	}
	defer c.StorageStop()

	// Unshift the container
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

	// Create the tarball
	tw := tar.NewWriter(w)

	// Keep track of the first path we saw for each path with nlink>1
	linkmap := map[uint64]string{}
	cDir := c.Path()

	// Path inside the tar image is the pathname starting after cDir
	offset := len(cDir) + 1

	writeToTar := func(path string, fi os.FileInfo, err error) error {
		if err := c.tarStoreFile(linkmap, offset, tw, path, fi); err != nil {
			shared.Debugf("Error tarring up %s: %s", path, err)
			return err
		}
		return nil
	}

	// Look for metadata.yaml
	fnam := filepath.Join(cDir, "metadata.yaml")
	if !shared.PathExists(fnam) {
		// Generate a new metadata.yaml
		f, err := ioutil.TempFile("", "lxd_lxd_metadata_")
		if err != nil {
			tw.Close()
			return err
		}
		defer os.Remove(f.Name())

		// Get the container's architecture
		var arch string
		if c.IsSnapshot() {
			parentName := strings.SplitN(c.name, shared.SnapshotDelimiter, 2)[0]
			parent, err := containerLoadByName(c.daemon, parentName)
			if err != nil {
				tw.Close()
				return err
			}

			arch, _ = shared.ArchitectureName(parent.Architecture())
		} else {
			arch, _ = shared.ArchitectureName(c.architecture)
		}

		if arch == "" {
			arch, err = shared.ArchitectureName(c.daemon.architectures[0])
			if err != nil {
				return err
			}
		}

		// Fill in the metadata
		meta := imageMetadata{}
		meta.Architecture = arch
		meta.CreationDate = time.Now().UTC().Unix()

		data, err := yaml.Marshal(&meta)
		if err != nil {
			tw.Close()
			return err
		}

		// Write the actual file
		f.Write(data)
		f.Close()

		fi, err := os.Lstat(f.Name())
		if err != nil {
			tw.Close()
			return err
		}

		tmpOffset := len(path.Dir(f.Name())) + 1
		if err := c.tarStoreFile(linkmap, tmpOffset, tw, f.Name(), fi); err != nil {
			shared.Debugf("Error writing to tarfile: %s", err)
			tw.Close()
			return err
		}

		fnam = f.Name()
	} else {
		// Include metadata.yaml in the tarball
		fi, err := os.Lstat(fnam)
		if err != nil {
			shared.Debugf("Error statting %s during export", fnam)
			tw.Close()
			return err
		}

		if err := c.tarStoreFile(linkmap, offset, tw, fnam, fi); err != nil {
			shared.Debugf("Error writing to tarfile: %s", err)
			tw.Close()
			return err
		}
	}

	// Include all the rootfs files
	fnam = c.RootfsPath()
	filepath.Walk(fnam, writeToTar)

	// Include all the templates
	fnam = c.TemplatesPath()
	if shared.PathExists(fnam) {
		filepath.Walk(fnam, writeToTar)
	}

	return tw.Close()
}

func (c *containerLXC) Checkpoint(opts lxc.CheckpointOptions) error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	return c.c.Checkpoint(opts)
}

func (c *containerLXC) TemplateApply(trigger string) error {
	// If there's no metadata, just return
	fname := filepath.Join(c.Path(), "metadata.yaml")
	if !shared.PathExists(fname) {
		return nil
	}

	// Parse the metadata
	content, err := ioutil.ReadFile(fname)
	if err != nil {
		return err
	}

	metadata := new(imageMetadata)
	err = yaml.Unmarshal(content, &metadata)

	if err != nil {
		return fmt.Errorf("Could not parse %s: %v", fname, err)
	}

	// Go through the templates
	for templatePath, template := range metadata.Templates {
		var w *os.File

		// Check if the template should be applied now
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

		// Open the file to template, create if needed
		fullpath := filepath.Join(c.RootfsPath(), strings.TrimLeft(templatePath, "/"))
		if shared.PathExists(fullpath) {
			// Open the existing file
			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}
		} else {
			// Create a new one
			uid := 0
			gid := 0

			// Get the right uid and gid for the container
			if !c.IsPrivileged() {
				uid, gid = c.idmapset.ShiftIntoNs(0, 0)
			}

			// Create the directories leading to the file
			shared.MkdirAllOwner(path.Dir(fullpath), 0755, uid, gid)

			// Create the file itself
			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}

			// Fix ownership and mode
			if !c.IsPrivileged() {
				w.Chown(uid, gid)
			}
			w.Chmod(0644)
		}
		defer w.Close()

		// Read the template
		tplString, err := ioutil.ReadFile(filepath.Join(c.TemplatesPath(), template.Template))
		if err != nil {
			return err
		}

		tpl, err := pongo2.FromString("{% autoescape off %}" + string(tplString) + "{% endautoescape %}")
		if err != nil {
			return err
		}

		// Figure out the architecture
		arch, err := shared.ArchitectureName(c.architecture)
		if err != nil {
			arch, err = shared.ArchitectureName(c.daemon.architectures[0])
			if err != nil {
				return err
			}
		}

		// Generate the metadata
		containerMeta := make(map[string]string)
		containerMeta["name"] = c.name
		containerMeta["architecture"] = arch

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
			val, ok := c.expandedConfig[confKey.String()]
			if !ok {
				return confDefault
			}

			return pongo2.AsValue(strings.TrimRight(val, "\r\n"))
		}

		// Render the template
		tpl.ExecuteWriter(pongo2.Context{"trigger": trigger,
			"path":       templatePath,
			"container":  containerMeta,
			"config":     c.expandedConfig,
			"devices":    c.expandedDevices,
			"properties": template.Properties,
			"config_get": configGet}, w)
	}

	return nil
}

func (c *containerLXC) FilePull(srcpath string, dstpath string) error {
	// Setup container storage if needed
	if !c.IsRunning() {
		err := c.StorageStart()
		if err != nil {
			return err
		}
	}

	// Get the file from the container
	out, err := exec.Command(
		c.daemon.execPath,
		"forkgetfile",
		c.RootfsPath(),
		fmt.Sprintf("%d", c.InitPID()),
		dstpath,
		srcpath,
	).CombinedOutput()

	// Tear down container storage if needed
	if !c.IsRunning() {
		err := c.StorageStop()
		if err != nil {
			return err
		}
	}

	// Process forkgetfile response
	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			shared.Debugf("forkgetfile: %s", line)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkgetfile %s %d %s': err='%v'",
			dstpath,
			c.InitPID(),
			srcpath,
			err)
	}

	return nil
}

func (c *containerLXC) FilePush(srcpath string, dstpath string, uid int, gid int, mode os.FileMode) error {
	// Map uid and gid if needed
	idmapset, err := c.LastIdmapSet()
	if err != nil {
		return err
	}

	if idmapset != nil {
		uid, gid = idmapset.ShiftIntoNs(uid, gid)
	}

	// Setup container storage if needed
	if !c.IsRunning() {
		err := c.StorageStart()
		if err != nil {
			return err
		}
	}

	// Push the file to the container
	out, err := exec.Command(
		c.daemon.execPath,
		"forkputfile",
		c.RootfsPath(),
		fmt.Sprintf("%d", c.InitPID()),
		srcpath,
		dstpath,
		fmt.Sprintf("%d", uid),
		fmt.Sprintf("%d", gid),
		fmt.Sprintf("%d", mode&os.ModePerm),
	).CombinedOutput()

	// Tear down container storage if needed
	if !c.IsRunning() {
		err := c.StorageStop()
		if err != nil {
			return err
		}
	}

	// Process forkputfile response
	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			shared.Debugf("forkgetfile: %s", line)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkputfile %s %d %s %d %d %d': err='%v'",
			srcpath,
			c.InitPID(),
			dstpath,
			uid,
			gid,
			mode,
			err)
	}

	return nil
}

func (c *containerLXC) ipsGet() []shared.Ip {
	ips := []shared.Ip{}

	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil
	}

	// Return empty list if not running
	if !c.IsRunning() {
		return ips
	}

	// Get the list of interfaces
	names, err := c.c.Interfaces()
	if err != nil {
		return nil
	}

	// Build the IPs list by iterating through all the interfaces
	for _, n := range names {
		// Get all the IPs
		addresses, err := c.c.IPAddress(n)
		if err != nil {
			continue
		}

		// Look for the host side interface name
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

		// Render the result
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

func (c *containerLXC) processcountGet() int {
	// Return 0 if not running
	pid := c.InitPID()
	if pid == -1 {
		return 0
	}

	pids := []int{pid}

	// Go through the pid list, adding new pids at the end so we go through them all
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

func (c *containerLXC) tarStoreFile(linkmap map[uint64]string, offset int, tw *tar.Writer, path string, fi os.FileInfo) error {
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

	// Unshift the id under /rootfs/ for unpriv containers
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

	// TODO: handle xattrs
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

// Storage functions
func (c *containerLXC) Storage() storage {
	return c.storage
}

func (c *containerLXC) StorageStart() error {
	if c.IsSnapshot() {
		return c.storage.ContainerSnapshotStart(c)
	}

	return c.storage.ContainerStart(c)
}

func (c *containerLXC) StorageStop() error {
	if c.IsSnapshot() {
		return c.storage.ContainerSnapshotStop(c)
	}

	return c.storage.ContainerStop(c)
}

// Mount handling
func (c *containerLXC) insertMount(source, target, fstype string, flags int) error {
	var err error

	// Get the init PID
	pid := c.InitPID()
	if pid == -1 {
		// Container isn't running
		return fmt.Errorf("Can't insert mount into stopped container")
	}

	// Create the temporary mount target
	var tmpMount string
	if shared.IsDir(source) {
		tmpMount, err = ioutil.TempDir(shared.VarPath("shmounts", c.name), "lxdmount_")
		if err != nil {
			return fmt.Errorf("Failed to create shmounts path: %s", err)
		}
	} else {
		f, err := ioutil.TempFile(shared.VarPath("shmounts", c.name), "lxdmount_")
		if err != nil {
			return fmt.Errorf("Failed to create shmounts path: %s", err)
		}

		tmpMount = f.Name()
		f.Close()
	}
	defer os.Remove(tmpMount)

	// Mount the filesystem
	err = syscall.Mount(source, tmpMount, fstype, uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("Failed to setup temporary mount: %s", err)
	}
	defer syscall.Unmount(tmpMount, syscall.MNT_DETACH)

	// Move the mount inside the container
	mntsrc := filepath.Join("/dev/.lxd-mounts", filepath.Base(tmpMount))
	pidStr := fmt.Sprintf("%d", pid)

	out, err := exec.Command(c.daemon.execPath, "forkmount", pidStr, mntsrc, target).CombinedOutput()

	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			shared.Debugf("forkmount: %s", line)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkmount %s %s %s': err='%v'",
			pidStr,
			mntsrc,
			target,
			err)
	}

	return nil
}

func (c *containerLXC) removeMount(mount string) error {
	// Get the init PID
	pid := c.InitPID()
	if pid == -1 {
		// Container isn't running
		return fmt.Errorf("Can't insert mount into stopped container")
	}

	// Remove the mount from the container
	pidStr := fmt.Sprintf("%d", pid)
	out, err := exec.Command(c.daemon.execPath, "forkumount", pidStr, mount).CombinedOutput()

	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			shared.Debugf("forkumount: %s", line)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkumount %s %s': err='%v'",
			pidStr,
			mount,
			err)
	}

	return nil
}

// Unix devices handling
func (c *containerLXC) createUnixDevice(name string, m shared.Device) (string, error) {
	var err error
	var major, minor int

	// Our device paths
	srcPath := m["path"]
	tgtPath := strings.TrimPrefix(srcPath, "/")
	devName := fmt.Sprintf("unix.%s", strings.Replace(tgtPath, "/", "-", -1))
	devPath := filepath.Join(c.DevicesPath(), devName)

	// Get the major/minor of the device we want to create
	if m["major"] == "" && m["minor"] == "" {
		// If no major and minor are set, use those from the device on the host
		_, major, minor, err = deviceGetAttributes(srcPath)
		if err != nil {
			return "", fmt.Errorf("Failed to get device attributes: %s", err)
		}
	} else if m["major"] == "" || m["minor"] == "" {
		return "", fmt.Errorf("Both major and minor must be supplied for devices")
	} else {
		major, err = strconv.Atoi(m["major"])
		if err != nil {
			return "", fmt.Errorf("Bad major %s in device %s", m["major"], m["path"])
		}

		minor, err = strconv.Atoi(m["minor"])
		if err != nil {
			return "", fmt.Errorf("Bad minor %s in device %s", m["minor"], m["path"])
		}
	}

	// Get the device mode
	mode := os.FileMode(0660)
	if m["mode"] != "" {
		tmp, err := deviceModeOct(m["mode"])
		if err != nil {
			return "", fmt.Errorf("Bad mode %s in device %s", m["mode"], m["path"])
		}
		mode = os.FileMode(tmp)
	}

	if m["type"] == "unix-block" {
		mode |= syscall.S_IFBLK
	} else {
		mode |= syscall.S_IFCHR
	}

	// Get the device owner
	uid := 0
	gid := 0

	if m["uid"] != "" {
		uid, err = strconv.Atoi(m["uid"])
		if err != nil {
			return "", fmt.Errorf("Invalid uid %s in device %s", m["uid"], m["path"])
		}
	}

	if m["gid"] != "" {
		gid, err = strconv.Atoi(m["gid"])
		if err != nil {
			return "", fmt.Errorf("Invalid gid %s in device %s", m["gid"], m["path"])
		}
	}

	// Create the devices directory if missing
	if !shared.PathExists(c.DevicesPath()) {
		os.Mkdir(c.DevicesPath(), 0711)
		if err != nil {
			return "", fmt.Errorf("Failed to create devices path: %s", err)
		}
	}

	// Clean any existing entry
	if shared.PathExists(devPath) {
		err = os.Remove(devPath)
		if err != nil {
			return "", fmt.Errorf("Failed to remove existing entry: %s", err)
		}
	}

	// Create the new entry
	if err := syscall.Mknod(devPath, uint32(mode), minor|(major<<8)); err != nil {
		return "", fmt.Errorf("Failed to create device %s for %s: %s", devPath, m["path"], err)
	}

	if err := os.Chown(devPath, uid, gid); err != nil {
		return "", fmt.Errorf("Failed to chown device %s: %s", devPath, err)
	}

	if c.idmapset != nil {
		if err := c.idmapset.ShiftFile(devPath); err != nil {
			// uidshift failing is weird, but not a big problem.  Log and proceed
			shared.Debugf("Failed to uidshift device %s: %s\n", m["path"], err)
		}
	}

	return devPath, nil
}

func (c *containerLXC) insertUnixDevice(name string, m shared.Device) error {
	// Check that the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't insert device into stopped container")
	}

	// Create the device on the host
	devPath, err := c.createUnixDevice(name, m)
	if err != nil {
		return fmt.Errorf("Failed to setup device: %s", err)
	}

	// Bind-mount it into the container
	tgtPath := strings.TrimSuffix(m["path"], "/")
	err = c.insertMount(devPath, tgtPath, "none", syscall.MS_BIND)
	if err != nil {
		return fmt.Errorf("Failed to add mount for device: %s", err)
	}

	// Add the new device cgroup rule
	dType, dMajor, dMinor, err := deviceGetAttributes(devPath)
	if err != nil {
		return fmt.Errorf("Failed to get device attributes: %s", err)
	}

	if err := c.CGroupSet("devices.allow", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor)); err != nil {
		return fmt.Errorf("Failed to add cgroup rule for device")
	}

	return nil
}

func (c *containerLXC) removeUnixDevice(name string, m shared.Device) error {
	// Check that the container is running
	pid := c.InitPID()
	if pid == -1 {
		return fmt.Errorf("Can't remove device from stopped container")
	}

	// Figure out the paths
	srcPath := m["path"]
	tgtPath := strings.TrimPrefix(srcPath, "/")
	devName := fmt.Sprintf("unix.%s", strings.Replace(tgtPath, "/", "-", -1))
	devPath := filepath.Join(c.DevicesPath(), devName)

	// Remove the device cgroup rule
	dType, dMajor, dMinor, err := deviceGetAttributes(devPath)
	if err != nil {
		return err
	}

	err = c.CGroupSet("devices.deny", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor))
	if err != nil {
		return err
	}

	// Remove the bind-mount from the container
	ctnPath := fmt.Sprintf("/proc/%d/root/%s", pid, tgtPath)

	if shared.PathExists(ctnPath) {
		err = c.removeMount(m["path"])
		if err != nil {
			return fmt.Errorf("Error unmounting the device: %s", err)
		}

		err = os.Remove(ctnPath)
		if err != nil {
			return fmt.Errorf("Error removing the device: %s", err)
		}
	}

	// Remove the host side
	err = os.Remove(devPath)
	if err != nil {
		return err
	}

	return nil
}

func (c *containerLXC) removeUnixDevices() error {
	// Check that we indeed have devices to remove
	if !shared.PathExists(c.DevicesPath()) {
		return nil
	}

	// Load the directory listing
	dents, err := ioutil.ReadDir(c.DevicesPath())
	if err != nil {
		return err
	}

	// Go through all the unix devices
	for _, f := range dents {
		// Skip non-Unix devices
		if !strings.HasPrefix(f.Name(), "unix.") {
			continue
		}

		// Remove the entry
		err := os.Remove(filepath.Join(c.DevicesPath(), f.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

// Network device handling
func (c *containerLXC) createNetworkDevice(name string, m shared.Device) (string, error) {
	var dev, n1 string

	if shared.StringInSlice(m["nictype"], []string{"bridged", "p2p", "macvlan"}) {
		// Host Virtual NIC name
		if m["host_name"] != "" {
			n1 = m["host_name"]
		} else {
			n1 = deviceNextVeth()
		}
	}

	// Handle bridged and p2p
	if shared.StringInSlice(m["nictype"], []string{"bridged", "p2p"}) {
		n2 := deviceNextVeth()

		err := exec.Command("ip", "link", "add", n1, "type", "veth", "peer", "name", n2).Run()
		if err != nil {
			return "", fmt.Errorf("Failed to create the veth interface: %s", err)
		}

		err = exec.Command("ip", "link", "set", n1, "up").Run()
		if err != nil {
			return "", fmt.Errorf("Failed to bring up the veth interface %s: %s", n1, err)
		}

		if m["nictype"] == "bridged" {
			err = exec.Command("brctl", "addif", m["parent"], n1).Run()
			if err != nil {
				deviceRemoveInterface(n2)
				return "", fmt.Errorf("Failed to add interface to bridge: %s", err)
			}
		}

		dev = n2
	}

	// Handle physical
	if m["nictype"] == "physical" {
		dev = m["parent"]
	}

	// Handle macvlan
	if m["nictype"] == "macvlan" {

		err := exec.Command("ip", "link", "add", n1, "link", m["parent"], "type", "macvlan", "mode", "bridge").Run()
		if err != nil {
			return "", fmt.Errorf("Failed to create the new macvlan interface: %s", err)
		}

		dev = n1
	}

	// Set the MAC address
	if m["hwaddr"] != "" {
		err := exec.Command("ip", "link", "set", "dev", dev, "address", m["hwaddr"]).Run()
		if err != nil {
			deviceRemoveInterface(dev)
			return "", fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Bring the interface up
	err := exec.Command("ip", "link", "set", "dev", dev, "up").Run()
	if err != nil {
		deviceRemoveInterface(dev)
		return "", fmt.Errorf("Failed to bring up the interface: %s", err)
	}

	return dev, nil
}

func (c *containerLXC) fillNetworkDevice(name string, m shared.Device) (shared.Device, error) {
	newDevice := shared.Device{}
	err := shared.DeepCopy(&m, &newDevice)
	if err != nil {
		return nil, err
	}

	// Function to try and guess an available name
	nextInterfaceName := func() (string, error) {
		devNames := []string{}

		// Include all static interface names
		for _, v := range c.expandedDevices {
			if v["name"] != "" && !shared.StringInSlice(v["name"], devNames) {
				devNames = append(devNames, v["name"])
			}
		}

		// Include all currently allocated interface names
		for k, v := range c.expandedConfig {
			if !strings.HasPrefix(k, "volatile.") {
				continue
			}

			fields := strings.SplitN(k, ".", 3)
			if len(fields) != 3 {
				continue
			}

			if fields[2] != "name" || shared.StringInSlice(v, devNames) {
				continue
			}

			devNames = append(devNames, v)
		}

		// Attempt to include all existing interfaces
		cc, err := lxc.NewContainer(c.Name(), c.daemon.lxcpath)
		if err == nil {
			interfaces, err := cc.Interfaces()
			if err == nil {
				for _, name := range interfaces {
					if shared.StringInSlice(name, devNames) {
						continue
					}

					devNames = append(devNames, name)
				}
			}
		}

		// Find a free ethX device
		i := 0
		for {
			name := fmt.Sprintf("eth%d", i)
			if !shared.StringInSlice(name, devNames) {
				return name, nil
			}

			i += 1
		}
	}

	// Fill in the MAC address
	if m["nictype"] != "physical" && m["hwaddr"] == "" {
		configKey := fmt.Sprintf("volatile.%s.hwaddr", name)
		volatileHwaddr := c.localConfig[configKey]
		if volatileHwaddr == "" {
			// Generate a new MAC address
			volatileHwaddr, err = deviceNextInterfaceHWAddr()
			if err != nil {
				return nil, err
			}

			c.localConfig[configKey] = volatileHwaddr
			c.expandedConfig[configKey] = volatileHwaddr

			// Update the database
			tx, err := dbBegin(c.daemon.db)
			if err != nil {
				return nil, err
			}

			err = dbContainerConfigInsert(tx, c.id, map[string]string{configKey: volatileHwaddr})
			if err != nil {
				tx.Rollback()
				return nil, err
			}

			err = txCommit(tx)
			if err != nil {
				return nil, err
			}
		}
		newDevice["hwaddr"] = volatileHwaddr
	}

	// File in the name
	if m["name"] == "" {
		configKey := fmt.Sprintf("volatile.%s.name", name)
		volatileName := c.localConfig[configKey]
		if volatileName == "" {
			// Generate a new interface name
			volatileName, err = nextInterfaceName()
			if err != nil {
				return nil, err
			}

			c.localConfig[configKey] = volatileName
			c.expandedConfig[configKey] = volatileName

			// Update the database
			tx, err := dbBegin(c.daemon.db)
			if err != nil {
				return nil, err
			}

			err = dbContainerConfigInsert(tx, c.id, map[string]string{configKey: volatileName})
			if err != nil {
				tx.Rollback()
				return nil, err
			}

			err = txCommit(tx)
			if err != nil {
				return nil, err
			}
		}
		newDevice["name"] = volatileName
	}

	return newDevice, nil
}

func (c *containerLXC) insertNetworkDevice(name string, m shared.Device) error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil
	}

	// Fill in some fields from volatile
	m, err = c.fillNetworkDevice(name, m)
	if err != nil {
		return nil
	}

	if m["hwaddr"] == "" || m["name"] == "" {
		return fmt.Errorf("wtf? hwaddr=%s name=%s", m["hwaddr"], m["name"])
	}

	// Return empty list if not running
	if !c.IsRunning() {
		return fmt.Errorf("Can't insert device into stopped container")
	}

	// Create the interface
	devName, err := c.createNetworkDevice(name, m)
	if err != nil {
		return err
	}

	// Add the interface to the container
	err = c.c.AttachInterface(devName, m["name"])
	if err != nil {
		return fmt.Errorf("Failed to attach interface: %s: %s", devName, err)
	}

	return nil
}

func (c *containerLXC) removeNetworkDevice(name string, m shared.Device) error {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil
	}

	// Fill in some fields from volatile
	m, err = c.fillNetworkDevice(name, m)
	if err != nil {
		return nil
	}

	// Return empty list if not running
	if !c.IsRunning() {
		return fmt.Errorf("Can't insert device into stopped container")
	}

	// Get a temporary device name
	var hostName string
	if m["nictype"] == "physical" {
		hostName = m["parent"]
	} else {
		hostName = deviceNextVeth()
	}

	// For some reason, having network config confuses detach, so get our own go-lxc struct
	cc, err := lxc.NewContainer(c.Name(), c.daemon.lxcpath)
	if err != nil {
		return err
	}

	// Remove the interface from the container
	err = cc.DetachInterfaceRename(m["name"], hostName)
	if err != nil {
		return fmt.Errorf("Failed to detach interface: %s: %s", m["name"], err)
	}

	// If a veth, destroy it
	if m["nictype"] != "physical" {
		deviceRemoveInterface(hostName)
	}

	return nil
}

// Disk device handling
func (c *containerLXC) createDiskDevice(name string, m shared.Device) (string, error) {
	// Prepare all the paths
	srcPath := m["source"]
	tgtPath := strings.TrimPrefix(m["path"], "/")
	devName := fmt.Sprintf("disk.%s", strings.Replace(tgtPath, "/", "-", -1))
	devPath := filepath.Join(c.DevicesPath(), devName)

	// Check if read-only
	isOptional := m["optional"] == "1" || m["optional"] == "true"
	isReadOnly := m["readonly"] == "1" || m["readonly"] == "true"
	isFile := !shared.IsDir(srcPath) && !deviceIsDevice(srcPath)

	// Check if the source exists
	if !shared.PathExists(srcPath) {
		if isOptional {
			return "", nil
		}
		return "", fmt.Errorf("Source path doesn't exist")
	}

	// Create the devices directory if missing
	if !shared.PathExists(c.DevicesPath()) {
		err := os.Mkdir(c.DevicesPath(), 0711)
		if err != nil {
			return "", err
		}
	}

	// Clean any existing entry
	if shared.PathExists(devPath) {
		err := os.Remove(devPath)
		if err != nil {
			return "", err
		}
	}

	// Create the mount point
	if isFile {
		f, err := os.Create(devPath)
		if err != nil {
			return "", err
		}

		f.Close()
	} else {
		err := os.Mkdir(devPath, 0700)
		if err != nil {
			return "", err
		}
	}

	// Mount the fs
	err := deviceMountDisk(srcPath, devPath, isReadOnly)
	if err != nil {
		return "", err
	}

	return devPath, nil
}

func (c *containerLXC) insertDiskDevice(name string, m shared.Device) error {
	// Check that the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't insert device into stopped container")
	}

	// Create the device on the host
	devPath, err := c.createDiskDevice(name, m)
	if err != nil {
		return fmt.Errorf("Failed to setup device: %s", err)
	}

	// Bind-mount it into the container
	tgtPath := strings.TrimSuffix(m["path"], "/")
	err = c.insertMount(devPath, tgtPath, "none", syscall.MS_BIND)
	if err != nil {
		return fmt.Errorf("Failed to add mount for device: %s", err)
	}

	return nil
}

func (c *containerLXC) removeDiskDevice(name string, m shared.Device) error {
	// Check that the container is running
	pid := c.InitPID()
	if pid == -1 {
		return fmt.Errorf("Can't remove device from stopped container")
	}

	// Figure out the paths
	tgtPath := strings.TrimPrefix(m["path"], "/")
	devName := fmt.Sprintf("disk.%s", strings.Replace(tgtPath, "/", "-", -1))
	devPath := filepath.Join(c.DevicesPath(), devName)

	// Remove the bind-mount from the container
	ctnPath := fmt.Sprintf("/proc/%d/root/%s", pid, tgtPath)

	if shared.PathExists(ctnPath) {
		err := c.removeMount(m["path"])
		if err != nil {
			return fmt.Errorf("Error unmounting the device: %s", err)
		}
	}

	// Unmount the host side
	err := syscall.Unmount(devPath, syscall.MNT_DETACH)
	if err != nil {
		return err
	}

	// Remove the host side
	err = os.Remove(devPath)
	if err != nil {
		return err
	}

	return nil
}

func (c *containerLXC) removeDiskDevices() error {
	// Check that we indeed have devices to remove
	if !shared.PathExists(c.DevicesPath()) {
		return nil
	}

	// Load the directory listing
	dents, err := ioutil.ReadDir(c.DevicesPath())
	if err != nil {
		return err
	}

	// Go through all the unix devices
	for _, f := range dents {
		// Skip non-Unix devices
		if !strings.HasPrefix(f.Name(), "disk.") {
			continue
		}

		// Always try to unmount the host side
		_ = syscall.Unmount(filepath.Join(c.DevicesPath(), f.Name()), syscall.MNT_DETACH)

		// Remove the entry
		err := os.Remove(filepath.Join(c.DevicesPath(), f.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

// Various state query functions
func (c *containerLXC) IsEphemeral() bool {
	return c.ephemeral
}

func (c *containerLXC) IsFrozen() bool {
	return c.State() == "FROZEN"
}

func (c *containerLXC) IsNesting() bool {
	switch strings.ToLower(c.expandedConfig["security.nesting"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *containerLXC) IsPrivileged() bool {
	switch strings.ToLower(c.expandedConfig["security.privileged"]) {
	case "1":
		return true
	case "true":
		return true
	}
	return false
}

func (c *containerLXC) IsRunning() bool {
	return c.State() != "STOPPED"
}

func (c *containerLXC) IsSnapshot() bool {
	return c.cType == cTypeSnapshot
}

// Various property query functions
func (c *containerLXC) Architecture() int {
	return c.architecture
}

func (c *containerLXC) ExpandedConfig() map[string]string {
	return c.expandedConfig
}

func (c *containerLXC) ExpandedDevices() shared.Devices {
	return c.expandedDevices
}

func (c *containerLXC) Id() int {
	return c.id
}

func (c *containerLXC) IdmapSet() *shared.IdmapSet {
	return c.idmapset
}

func (c *containerLXC) InitPID() int {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return -1
	}

	return c.c.InitPid()
}

func (c *containerLXC) LocalConfig() map[string]string {
	return c.localConfig
}

func (c *containerLXC) LocalDevices() shared.Devices {
	return c.localDevices
}

func (c *containerLXC) LastIdmapSet() (*shared.IdmapSet, error) {
	lastJsonIdmap := c.LocalConfig()["volatile.last_state.idmap"]

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

func (c *containerLXC) LXContainerGet() *lxc.Container {
	// FIXME: This function should go away

	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil
	}

	return c.c
}

func (c *containerLXC) Daemon() *Daemon {
	// FIXME: This function should go away
	return c.daemon
}

func (c *containerLXC) Name() string {
	return c.name
}

func (c *containerLXC) Profiles() []string {
	return c.profiles
}

func (c *containerLXC) State() string {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return ""
	}

	return c.c.State().String()
}

// Various container paths
func (c *containerLXC) Path() string {
	return containerPath(c.Name(), c.IsSnapshot())
}

func (c *containerLXC) DevicesPath() string {
	return shared.VarPath("devices", c.Name())
}

func (c *containerLXC) LogPath() string {
	return shared.LogPath(c.Name())
}

func (c *containerLXC) LogFilePath() string {
	return filepath.Join(c.LogPath(), "lxc.log")
}

func (c *containerLXC) RootfsPath() string {
	return filepath.Join(c.Path(), "rootfs")
}

func (c *containerLXC) TemplatesPath() string {
	return filepath.Join(c.Path(), "templates")
}

func (c *containerLXC) StatePath() string {
	return filepath.Join(c.Path(), "state")
}
