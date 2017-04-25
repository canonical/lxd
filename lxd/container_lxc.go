package main

import (
	"archive/tar"
	"bufio"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/flosch/pongo2.v3"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"

	log "gopkg.in/inconshreveable/log15.v2"
)

// Operation locking
type lxcContainerOperation struct {
	action    string
	chanDone  chan error
	chanReset chan bool
	err       error
	id        int
	reusable  bool
}

func (op *lxcContainerOperation) Create(id int, action string, reusable bool) *lxcContainerOperation {
	op.id = id
	op.action = action
	op.reusable = reusable
	op.chanDone = make(chan error, 0)
	op.chanReset = make(chan bool, 0)

	go func(op *lxcContainerOperation) {
		for {
			select {
			case <-op.chanReset:
				continue
			case <-time.After(time.Second * 30):
				op.Done(fmt.Errorf("Container %s operation timed out after 30 seconds", op.action))
				return
			}
		}
	}(op)

	return op
}

func (op *lxcContainerOperation) Reset() error {
	if !op.reusable {
		return fmt.Errorf("Can't reset a non-reusable operation")
	}

	op.chanReset <- true
	return nil
}

func (op *lxcContainerOperation) Wait() error {
	<-op.chanDone

	return op.err
}

func (op *lxcContainerOperation) Done(err error) {
	lxcContainerOperationsLock.Lock()
	defer lxcContainerOperationsLock.Unlock()

	// Check if already done
	runningOp, ok := lxcContainerOperations[op.id]
	if !ok || runningOp != op {
		return
	}

	op.err = err
	close(op.chanDone)

	delete(lxcContainerOperations, op.id)
}

var lxcContainerOperationsLock sync.Mutex
var lxcContainerOperations map[int]*lxcContainerOperation = make(map[int]*lxcContainerOperation)

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

		// Skip whitespace {"\t", " "}
		line = strings.TrimLeft(line, "\t ")

		// Ignore comments
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Ensure the format is valid
		membs := strings.SplitN(line, "=", 2)
		if len(membs) != 2 {
			return fmt.Errorf("Invalid raw.lxc line: %s", line)
		}

		key := strings.ToLower(strings.Trim(membs[0], " \t"))

		// Blacklist some keys
		if key == "lxc.logfile" {
			return fmt.Errorf("Setting lxc.logfile is not allowed")
		}

		if key == "lxc.syslog" {
			return fmt.Errorf("Setting lxc.syslog is not allowed")
		}

		if key == "lxc.ephemeral" {
			return fmt.Errorf("Setting lxc.ephemeral is not allowed")
		}

		if strings.HasPrefix(key, "lxc.network.") {
			fields := strings.Split(key, ".")
			if len(fields) == 4 && shared.StringInSlice(fields[3], []string{"ipv4", "ipv6"}) {
				continue
			}

			if len(fields) == 5 && shared.StringInSlice(fields[3], []string{"ipv4", "ipv6"}) && fields[4] == "gateway" {
				continue
			}

			return fmt.Errorf("Only interface-specific ipv4/ipv6 lxc.network keys are allowed")
		}
	}

	return nil
}

func lxcStatusCode(state lxc.State) api.StatusCode {
	return map[int]api.StatusCode{
		1: api.Stopped,
		2: api.Starting,
		3: api.Running,
		4: api.Stopping,
		5: api.Aborting,
		6: api.Freezing,
		7: api.Frozen,
		8: api.Thawed,
		9: api.Error,
	}[int(state)]
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
		stateful:     args.Stateful,
		creationDate: args.CreationDate,
		profiles:     args.Profiles,
		localConfig:  args.Config,
		localDevices: args.Devices,
	}

	ctxMap := log.Ctx{"name": c.name,
		"ephemeral": c.ephemeral}

	logger.Info("Creating container", ctxMap)

	// No need to detect storage here, its a new container.
	c.storage = d.Storage

	// Load the config
	err := c.init()
	if err != nil {
		c.Delete()
		logger.Error("Failed creating container", ctxMap)
		return nil, err
	}

	// Look for a rootfs entry
	_, _, err = containerGetRootDiskDevice(c.expandedDevices)
	if err != nil {
		deviceName := "root"
		for {
			if c.expandedDevices[deviceName] == nil {
				break
			}

			deviceName += "_"
		}

		c.localDevices[deviceName] = types.Device{"type": "disk", "path": "/"}

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
			logger.Error("Failed creating container", ctxMap)
			return nil, err
		}
	}

	// Validate expanded config
	err = containerValidConfig(c.daemon, c.expandedConfig, false, true)
	if err != nil {
		c.Delete()
		logger.Error("Failed creating container", ctxMap)
		return nil, err
	}

	err = containerValidDevices(c.expandedDevices, false, true)
	if err != nil {
		c.Delete()
		logger.Error("Failed creating container", ctxMap)
		return nil, err
	}

	// Setup initial idmap config
	var idmap *shared.IdmapSet
	base := int64(0)
	if !c.IsPrivileged() {
		idmap, base, err = findIdmap(
			d,
			args.Name,
			c.expandedConfig["security.idmap.isolated"],
			c.expandedConfig["security.idmap.size"],
			c.expandedConfig["raw.idmap"],
		)

		if err != nil {
			c.Delete()
			logger.Error("Failed creating container", ctxMap)
			return nil, err
		}
	}

	var jsonIdmap string
	if idmap != nil {
		idmapBytes, err := json.Marshal(idmap.Idmap)
		if err != nil {
			c.Delete()
			logger.Error("Failed creating container", ctxMap)
			return nil, err
		}
		jsonIdmap = string(idmapBytes)
	} else {
		jsonIdmap = "[]"
	}

	err = c.ConfigKeySet("volatile.idmap.next", jsonIdmap)
	if err != nil {
		c.Delete()
		logger.Error("Failed creating container", ctxMap)
		return nil, err
	}

	err = c.ConfigKeySet("volatile.idmap.base", fmt.Sprintf("%v", base))
	if err != nil {
		c.Delete()
		logger.Error("Failed creating container", ctxMap)
		return nil, err
	}

	// Invalid idmap cache
	c.idmapset = nil

	// Set last_state to the map we have on disk
	if c.localConfig["volatile.last_state.idmap"] == "" {
		err = c.ConfigKeySet("volatile.last_state.idmap", jsonIdmap)
		if err != nil {
			c.Delete()
			logger.Error("Failed creating container", ctxMap)
			return nil, err
		}
	}

	// Re-run init to update the idmap
	err = c.init()
	if err != nil {
		c.Delete()
		logger.Error("Failed creating container", ctxMap)
		return nil, err
	}

	logger.Info("Created container", ctxMap)

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
		creationDate: args.CreationDate,
		profiles:     args.Profiles,
		localConfig:  args.Config,
		localDevices: args.Devices,
		stateful:     args.Stateful}

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
	creationDate time.Time
	ephemeral    bool
	id           int
	name         string
	stateful     bool

	// Config
	expandedConfig  map[string]string
	expandedDevices types.Devices
	fromHook        bool
	localConfig     map[string]string
	localDevices    types.Devices
	profiles        []string

	// Cache
	c        *lxc.Container
	daemon   *Daemon
	idmapset *shared.IdmapSet
	storage  storage
}

func (c *containerLXC) createOperation(action string, reusable bool, reuse bool) (*lxcContainerOperation, error) {
	op, _ := c.getOperation("")
	if op != nil {
		if reuse && op.reusable {
			op.Reset()
			return op, nil
		}

		return nil, fmt.Errorf("Container is busy running a %s operation", op.action)
	}

	lxcContainerOperationsLock.Lock()
	defer lxcContainerOperationsLock.Unlock()

	op = &lxcContainerOperation{}
	op.Create(c.id, action, reusable)
	lxcContainerOperations[c.id] = op

	return lxcContainerOperations[c.id], nil
}

func (c *containerLXC) getOperation(action string) (*lxcContainerOperation, error) {
	lxcContainerOperationsLock.Lock()
	defer lxcContainerOperationsLock.Unlock()

	op := lxcContainerOperations[c.id]

	if op == nil {
		return nil, fmt.Errorf("No running %s container operation", action)
	}

	if action != "" && op.action != action {
		return nil, fmt.Errorf("Container is running a %s operation, not a %s operation", op.action, action)
	}

	return op, nil
}

func (c *containerLXC) waitOperation() error {
	op, _ := c.getOperation("")
	if op != nil {
		err := op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

func idmapSize(daemon *Daemon, isolatedStr string, size string) (int64, error) {
	isolated := false
	if shared.IsTrue(isolatedStr) {
		isolated = true
	}

	var idMapSize int64
	if size == "" || size == "auto" {
		if isolated {
			idMapSize = 65536
		} else {
			if len(daemon.IdmapSet.Idmap) != 2 {
				return 0, fmt.Errorf("bad initial idmap: %v", daemon.IdmapSet)
			}

			idMapSize = daemon.IdmapSet.Idmap[0].Maprange
		}
	} else {
		size, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			return 0, err
		}

		idMapSize = size
	}

	return idMapSize, nil
}

var idmapLock sync.Mutex

func parseRawIdmap(value string) ([]shared.IdmapEntry, error) {
	getRange := func(r string) (int64, int64, error) {
		entries := strings.Split(r, "-")
		if len(entries) > 2 {
			return -1, -1, fmt.Errorf("invalid raw.idmap range %s", r)
		}

		base, err := strconv.ParseInt(entries[0], 10, 64)
		if err != nil {
			return -1, -1, err
		}

		size := int64(1)
		if len(entries) > 1 {
			size, err = strconv.ParseInt(entries[1], 10, 64)
			if err != nil {
				return -1, -1, err
			}

			size -= base
			size += 1
		}

		return base, size, nil
	}

	ret := shared.IdmapSet{}

	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			continue
		}

		entries := strings.Split(line, " ")
		if len(entries) != 3 {
			return nil, fmt.Errorf("invalid raw.idmap line %s", line)
		}

		outsideBase, outsideSize, err := getRange(entries[1])
		if err != nil {
			return nil, err
		}

		insideBase, insideSize, err := getRange(entries[2])
		if err != nil {
			return nil, err
		}

		if insideSize != outsideSize {
			return nil, fmt.Errorf("idmap ranges of different sizes %s", line)
		}

		entry := shared.IdmapEntry{
			Hostid:   outsideBase,
			Nsid:     insideBase,
			Maprange: insideSize,
		}

		switch entries[0] {
		case "both":
			entry.Isuid = true
			entry.Isgid = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}
		case "uid":
			entry.Isuid = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}
		case "gid":
			entry.Isgid = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("invalid raw.idmap type %s", line)
		}
	}

	return ret.Idmap, nil
}

func findIdmap(daemon *Daemon, cName string, isolatedStr string, configSize string, rawIdmap string) (*shared.IdmapSet, int64, error) {
	isolated := false
	if shared.IsTrue(isolatedStr) {
		isolated = true
	}

	rawMaps, err := parseRawIdmap(rawIdmap)
	if err != nil {
		return nil, 0, err
	}

	if !isolated {
		newIdmapset := shared.IdmapSet{Idmap: make([]shared.IdmapEntry, len(daemon.IdmapSet.Idmap))}
		copy(newIdmapset.Idmap, daemon.IdmapSet.Idmap)

		for _, ent := range rawMaps {
			newIdmapset.AddSafe(ent)
		}

		return &newIdmapset, 0, nil
	}

	idmapLock.Lock()
	defer idmapLock.Unlock()

	cs, err := dbContainersList(daemon.db, cTypeRegular)
	if err != nil {
		return nil, 0, err
	}

	offset := daemon.IdmapSet.Idmap[0].Hostid + 65536
	size, err := idmapSize(daemon, isolatedStr, configSize)
	if err != nil {
		return nil, 0, err
	}

	mapentries := shared.ByHostid{}
	for _, name := range cs {
		/* Don't change our map Just Because. */
		if name == cName {
			continue
		}

		container, err := containerLoadByName(daemon, name)
		if err != nil {
			return nil, 0, err
		}

		if container.IsPrivileged() {
			continue
		}

		if !shared.IsTrue(container.ExpandedConfig()["security.idmap.isolated"]) {
			continue
		}

		cBase := int64(0)
		if container.ExpandedConfig()["volatile.idmap.base"] != "" {
			cBase, err = strconv.ParseInt(container.ExpandedConfig()["volatile.idmap.base"], 10, 64)
			if err != nil {
				return nil, 0, err
			}
		}

		cSize, err := idmapSize(daemon, container.ExpandedConfig()["security.idmap.isolated"], container.ExpandedConfig()["security.idmap.size"])
		if err != nil {
			return nil, 0, err
		}

		mapentries = append(mapentries, &shared.IdmapEntry{Hostid: int64(cBase), Maprange: cSize})
	}

	sort.Sort(mapentries)

	mkIdmap := func(offset int64, size int64) *shared.IdmapSet {
		set := &shared.IdmapSet{Idmap: []shared.IdmapEntry{
			{Isuid: true, Nsid: 0, Hostid: offset, Maprange: size},
			{Isgid: true, Nsid: 0, Hostid: offset, Maprange: size},
		}}

		for _, ent := range rawMaps {
			set.AddSafe(ent)
		}

		return set
	}

	for i := range mapentries {
		if i == 0 {
			if mapentries[0].Hostid < offset+size {
				offset = mapentries[0].Hostid + mapentries[0].Maprange
				continue
			}

			return mkIdmap(offset, size), offset, nil
		}

		if mapentries[i-1].Hostid+mapentries[i-1].Maprange > offset {
			offset = mapentries[i-1].Hostid + mapentries[i-1].Maprange
			continue
		}

		offset = mapentries[i-1].Hostid + mapentries[i-1].Maprange
		if offset+size < mapentries[i].Hostid {
			return mkIdmap(offset, size), offset, nil
		}
		offset = mapentries[i].Hostid + mapentries[i].Maprange
	}

	if offset+size < daemon.IdmapSet.Idmap[0].Hostid+daemon.IdmapSet.Idmap[0].Maprange {
		return mkIdmap(offset, size), offset, nil
	}

	return nil, 0, fmt.Errorf("Not enough uid/gid available for the container.")
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

	return nil
}

func (c *containerLXC) initLXC() error {
	// No need to go through all that for snapshots
	if c.IsSnapshot() {
		return nil
	}

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
	toDrop := "sys_time sys_module sys_rawio"
	if !aaStacking || aaStacked {
		toDrop = toDrop + " mac_admin mac_override"
	}

	err = lxcSetConfigItem(cc, "lxc.cap.drop", toDrop)
	if err != nil {
		return err
	}

	// Set an appropriate /proc, /sys/ and /sys/fs/cgroup
	mounts := []string{}
	if c.IsPrivileged() && !runningInUserns {
		mounts = append(mounts, "proc:mixed")
		mounts = append(mounts, "sys:mixed")
	} else {
		mounts = append(mounts, "proc:rw")
		mounts = append(mounts, "sys:rw")
	}

	if !shared.PathExists("/proc/self/ns/cgroup") {
		mounts = append(mounts, "cgroup:mixed")
	}

	err = lxcSetConfigItem(cc, "lxc.mount.auto", strings.Join(mounts, " "))
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

	bindMounts := []string{
		"/dev/fuse",
		"/dev/net/tun",
		"/proc/sys/fs/binfmt_misc",
		"/sys/firmware/efi/efivars",
		"/sys/fs/fuse/connections",
		"/sys/fs/pstore",
		"/sys/kernel/debug",
		"/sys/kernel/security"}

	if c.IsPrivileged() && !runningInUserns {
		err = lxcSetConfigItem(cc, "lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional")
		if err != nil {
			return err
		}
	} else {
		bindMounts = append(bindMounts, "/dev/mqueue")
	}

	for _, mnt := range bindMounts {
		if !shared.PathExists(mnt) {
			continue
		}

		if shared.IsDir(mnt) {
			err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none rbind,create=dir,optional", mnt, strings.TrimPrefix(mnt, "/")))
			if err != nil {
				return err
			}
		} else {
			err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none bind,create=file,optional", mnt, strings.TrimPrefix(mnt, "/")))
			if err != nil {
				return err
			}
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

		devices := []string{
			"b *:* m",      // Allow mknod of block devices
			"c *:* m",      // Allow mknod of char devices
			"c 136:* rwm",  // /dev/pts devices
			"c 1:3 rwm",    // /dev/null
			"c 1:5 rwm",    // /dev/zero
			"c 1:7 rwm",    // /dev/full
			"c 1:8 rwm",    // /dev/random
			"c 1:9 rwm",    // /dev/urandom
			"c 5:0 rwm",    // /dev/tty
			"c 5:1 rwm",    // /dev/console
			"c 5:2 rwm",    // /dev/ptmx
			"c 10:229 rwm", // /dev/fuse
			"c 10:200 rwm", // /dev/net/tun
		}

		for _, dev := range devices {
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

	logLevel := "warn"
	if debug {
		logLevel = "trace"
	} else if verbose {
		logLevel = "info"
	}

	err = lxcSetConfigItem(cc, "lxc.loglevel", logLevel)
	if err != nil {
		return err
	}

	// Setup architecture
	personality, err := osarch.ArchitecturePersonality(c.architecture)
	if err != nil {
		personality, err = osarch.ArchitecturePersonality(c.daemon.architectures[0])
		if err != nil {
			return err
		}
	}

	err = lxcSetConfigItem(cc, "lxc.arch", personality)
	if err != nil {
		return err
	}

	// Setup the hooks
	err = lxcSetConfigItem(cc, "lxc.hook.pre-start", fmt.Sprintf("%s callhook %s %d start", execPath, shared.VarPath(""), c.id))
	if err != nil {
		return err
	}

	err = lxcSetConfigItem(cc, "lxc.hook.post-stop", fmt.Sprintf("%s callhook %s %d stop", execPath, shared.VarPath(""), c.id))
	if err != nil {
		return err
	}

	// Setup the console
	err = lxcSetConfigItem(cc, "lxc.tty", "0")
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
			profile := AAProfileFull(c)

			/* In the nesting case, we want to enable the inside
			 * LXD to load its profile. Unprivileged containers can
			 * load profiles, but privileged containers cannot, so
			 * let's not use a namespace so they can fall back to
			 * the old way of nesting, i.e. using the parent's
			 * profile.
			 */
			if aaStacking && !aaStacked {
				profile = fmt.Sprintf("%s//&:%s:", profile, AANamespace(c))
			}

			err := lxcSetConfigItem(cc, "lxc.aa_profile", profile)
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
	idmapset, err := c.IdmapSet()
	if err != nil {
		return err
	}

	if idmapset != nil {
		lines := idmapset.ToLxcString()
		for _, line := range lines {
			err := lxcSetConfigItem(cc, "lxc.id_map", strings.TrimSuffix(line, "\n"))
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
				valueInt, err = shared.ParseByteSizeString(memory)
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
				if cgSwapAccounting && (memorySwap == "" || shared.IsTrue(memorySwap)) {
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
				// Set soft limit to value 10% less than hard limit
				err = lxcSetConfigItem(cc, "lxc.cgroup.memory.soft_limit_in_bytes", fmt.Sprintf("%.0f", float64(valueInt)*0.9))
				if err != nil {
					return err
				}
			}
		}

		// Configure the swappiness
		if memorySwap != "" && !shared.IsTrue(memorySwap) {
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

	// Disk limits
	if cgBlkioController {
		diskPriority := c.expandedConfig["limits.disk.priority"]
		if diskPriority != "" {
			priorityInt, err := strconv.Atoi(diskPriority)
			if err != nil {
				return err
			}

			// Minimum valid value is 10
			priority := priorityInt * 100
			if priority == 0 {
				priority = 10
			}

			err = lxcSetConfigItem(cc, "lxc.cgroup.blkio.weight", fmt.Sprintf("%d", priority))
			if err != nil {
				return err
			}
		}

		hasDiskLimits := false
		for _, name := range c.expandedDevices.DeviceNames() {
			m := c.expandedDevices[name]
			if m["type"] != "disk" {
				continue
			}

			if m["limits.read"] != "" || m["limits.write"] != "" || m["limits.max"] != "" {
				hasDiskLimits = true
				break
			}
		}

		if hasDiskLimits {
			diskLimits, err := c.getDiskLimits()
			if err != nil {
				return err
			}

			for block, limit := range diskLimits {
				if limit.readBps > 0 {
					err = lxcSetConfigItem(cc, "lxc.cgroup.blkio.throttle.read_bps_device", fmt.Sprintf("%s %d", block, limit.readBps))
					if err != nil {
						return err
					}
				}

				if limit.readIops > 0 {
					err = lxcSetConfigItem(cc, "lxc.cgroup.blkio.throttle.read_iops_device", fmt.Sprintf("%s %d", block, limit.readIops))
					if err != nil {
						return err
					}
				}

				if limit.writeBps > 0 {
					err = lxcSetConfigItem(cc, "lxc.cgroup.blkio.throttle.write_bps_device", fmt.Sprintf("%s %d", block, limit.writeBps))
					if err != nil {
						return err
					}
				}

				if limit.writeIops > 0 {
					err = lxcSetConfigItem(cc, "lxc.cgroup.blkio.throttle.write_iops_device", fmt.Sprintf("%s %d", block, limit.writeIops))
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// Processes
	if cgPidsController {
		processes := c.expandedConfig["limits.processes"]
		if processes != "" {
			valueInt, err := strconv.ParseInt(processes, 10, 64)
			if err != nil {
				return err
			}

			err = lxcSetConfigItem(cc, "lxc.cgroup.pids.max", fmt.Sprintf("%d", valueInt))
			if err != nil {
				return err
			}
		}
	}

	// Setup devices
	for _, k := range c.expandedDevices.DeviceNames() {
		m := c.expandedDevices[k]
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
			if err != nil {
				return err
			}

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

			err = lxcSetConfigItem(cc, "lxc.network.flags", "up")
			if err != nil {
				return err
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
			isOptional := shared.IsTrue(m["optional"])
			isReadOnly := shared.IsTrue(m["readonly"])
			isRecursive := shared.IsTrue(m["recursive"])
			isFile := !shared.IsDir(srcPath) && !deviceIsBlockdev(srcPath)

			// Deal with a rootfs
			if tgtPath == "" {
				// Set the rootfs backend type if supported (must happen before any other lxc.rootfs)
				err := lxcSetConfigItem(cc, "lxc.rootfs.backend", "dir")
				if err == nil {
					value := cc.ConfigItem("lxc.rootfs.backend")
					if len(value) == 0 || value[0] != "dir" {
						lxcSetConfigItem(cc, "lxc.rootfs.backend", "")
					}
				}

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
				rbind := ""
				options := []string{}
				if isReadOnly {
					options = append(options, "ro")
				}

				if isOptional {
					options = append(options, "optional")
				}

				if isRecursive {
					rbind = "r"
				}

				if isFile {
					options = append(options, "create=file")
				} else {
					options = append(options, "create=dir")
				}

				err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none %sbind,%s", devPath, tgtPath, rbind, strings.Join(options, ",")))
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
	devices := types.Devices{}

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

	// Sanity checks for devices
	for _, name := range c.expandedDevices.DeviceNames() {
		m := c.expandedDevices[name]
		switch m["type"] {
		case "disk":
			if m["source"] != "" && !shared.PathExists(m["source"]) {
				return "", fmt.Errorf("Missing source '%s' for disk '%s'", m["source"], name)
			}
		case "nic":
			if m["parent"] != "" && !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", m["parent"])) {
				return "", fmt.Errorf("Missing parent '%s' for nic '%s'", m["parent"], name)
			}
		case "unix-char", "unix-block":
			if m["path"] != "" && m["major"] == "" && m["minor"] == "" && !shared.PathExists(m["path"]) {
				return "", fmt.Errorf("Missing source '%s' for device '%s'", m["path"], name)
			}
		}
	}

	// Load any required kernel modules
	kernelModules := c.expandedConfig["linux.kernel_modules"]
	if kernelModules != "" {
		for _, module := range strings.Split(kernelModules, ",") {
			module = strings.TrimPrefix(module, " ")
			err := loadModule(module)
			if err != nil {
				return "", fmt.Errorf("Failed to load kernel module '%s': %s", module, err)
			}
		}
	}

	/* Deal with idmap changes */
	idmap, err := c.IdmapSet()
	if err != nil {
		return "", err
	}

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
		logger.Debugf("Container idmap changed, remapping")

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

		var mode os.FileMode
		var uid int64
		var gid int64

		if c.IsPrivileged() {
			mode = 0700
		} else {
			mode = 0755
			if idmap != nil {
				uid, gid = idmap.ShiftIntoNs(0, 0)
			}
		}

		err = os.Chmod(c.Path(), mode)
		if err != nil {
			return "", err
		}

		err = os.Chown(c.Path(), int(uid), int(gid))
		if err != nil {
			return "", err
		}

		err = c.StorageStop()
		if err != nil {
			return "", err
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

	diskDevices := map[string]types.Device{}

	// Create the devices
	for _, k := range c.expandedDevices.DeviceNames() {
		m := c.expandedDevices[k]
		if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
			// Unix device
			devPath, err := c.createUnixDevice(m)
			if err != nil {
				return "", err
			}

			if c.IsPrivileged() && !runningInUserns && cgDevicesController {
				// Add the new device cgroup rule
				dType, dMajor, dMinor, err := deviceGetAttributes(devPath)
				if err != nil {
					return "", err
				}

				err = lxcSetConfigItem(c.c, "lxc.cgroup.devices.allow", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor))
				if err != nil {
					return "", fmt.Errorf("Failed to add cgroup rule for device")
				}
			}
		} else if m["type"] == "disk" {
			if m["path"] != "/" {
				diskDevices[k] = m
			}
		}
	}

	err = c.addDiskDevices(diskDevices, func(name string, d types.Device) error {
		_, err := c.createDiskDevice(name, d)
		return err
	})
	if err != nil {
		return "", err
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
	for _, k := range c.expandedDevices.DeviceNames() {
		v := c.expandedDevices[k]
		if v["type"] == "nic" {
			netNames = append(netNames, k)
		}
	}

	for k := range c.localConfig {
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

	// Rotate the log file
	logfile := c.LogFilePath()
	if shared.PathExists(logfile) {
		os.Remove(logfile + ".old")
		err := os.Rename(logfile, logfile+".old")
		if err != nil {
			return "", err
		}
	}

	// Generate the LXC config
	configPath := filepath.Join(c.LogPath(), "lxc.conf")
	err = c.c.SaveConfigFile(configPath)
	if err != nil {
		os.Remove(configPath)
		return "", err
	}

	return configPath, nil
}

func (c *containerLXC) Start(stateful bool) error {
	var ctxMap log.Ctx

	// Setup a new operation
	op, err := c.createOperation("start", false, false)
	if err != nil {
		return err
	}
	defer op.Done(nil)

	err = setupSharedMounts()
	if err != nil {
		return fmt.Errorf("Daemon failed to setup shared mounts base: %s.\nDoes security.nesting need to be turned on?", err)
	}

	// Run the shared start code
	configPath, err := c.startCommon()
	if err != nil {
		return err
	}

	ctxMap = log.Ctx{"name": c.name,
		"action":        op.action,
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral,
		"stateful":      stateful}

	logger.Info("Starting container", ctxMap)

	// If stateful, restore now
	if stateful {
		if !c.stateful {
			return fmt.Errorf("Container has no existing state to restore.")
		}

		err := c.Migrate(lxc.MIGRATE_RESTORE, c.StatePath(), "snapshot", false, false)
		if err != nil && !c.IsRunning() {
			return err
		}

		os.RemoveAll(c.StatePath())
		c.stateful = false

		err = dbContainerSetStateful(c.daemon.db, c.id, false)
		if err != nil {
			logger.Error("Failed starting container", ctxMap)
			return err
		}

		logger.Info("Started container", ctxMap)

		return err
	} else if c.stateful {
		/* stateless start required when we have state, let's delete it */
		err := os.RemoveAll(c.StatePath())
		if err != nil {
			return err
		}

		c.stateful = false
		err = dbContainerSetStateful(c.daemon.db, c.id, false)
		if err != nil {
			return err
		}
	}

	// Start the LXC container
	out, err := shared.RunCommand(
		execPath,
		"forkstart",
		c.name,
		c.daemon.lxcpath,
		configPath)

	// Capture debug output
	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			logger.Debugf("forkstart: %s", line)
		}
	}

	if err != nil && !c.IsRunning() {
		// Attempt to extract the LXC errors
		lxcLog := ""
		logPath := filepath.Join(c.LogPath(), "lxc.log")
		if shared.PathExists(logPath) {
			logContent, err := ioutil.ReadFile(logPath)
			if err == nil {
				for _, line := range strings.Split(string(logContent), "\n") {
					fields := strings.Fields(line)
					if len(fields) < 4 {
						continue
					}

					// We only care about errors
					if fields[2] != "ERROR" {
						continue
					}

					// Prepend the line break
					if len(lxcLog) == 0 {
						lxcLog += "\n"
					}

					lxcLog += fmt.Sprintf("  %s\n", strings.Join(fields[0:], " "))
				}
			}
		}

		logger.Error("Failed starting container", ctxMap)

		// Return the actual error
		return fmt.Errorf(
			"Error calling 'lxd forkstart %s %s %s': err='%v'%s",
			c.name,
			c.daemon.lxcpath,
			filepath.Join(c.LogPath(), "lxc.conf"),
			err, lxcLog)
	}

	logger.Info("Started container", ctxMap)

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
	key := "volatile.apply_template"
	if c.localConfig[key] != "" {
		// Run any template that needs running
		err = c.templateApplyNow(c.localConfig[key])
		if err != nil {
			AADestroy(c)
			c.StorageStop()
			return err
		}

		// Remove the volatile key from the DB
		err := dbContainerConfigRemove(c.daemon.db, c.id, key)
		if err != nil {
			AADestroy(c)
			c.StorageStop()
			return err
		}
	}

	err = c.templateApplyNow("start")
	if err != nil {
		AADestroy(c)
		c.StorageStop()
		return err
	}

	// Trigger a rebalance
	deviceTaskSchedulerTrigger("container", c.name, "started")

	// Apply network priority
	if c.expandedConfig["limits.network.priority"] != "" {
		go func(c *containerLXC) {
			c.fromHook = false
			err := c.setNetworkPriority()
			if err != nil {
				logger.Error("Failed to apply network priority", log.Ctx{"container": c.name, "err": err})
			}
		}(c)
	}

	// Apply network limits
	for _, name := range c.expandedDevices.DeviceNames() {
		m := c.expandedDevices[name]
		if m["type"] != "nic" {
			continue
		}

		if m["limits.max"] == "" && m["limits.ingress"] == "" && m["limits.egress"] == "" {
			continue
		}

		go func(c *containerLXC, name string, m types.Device) {
			c.fromHook = false
			err = c.setNetworkLimits(name, m)
			if err != nil {
				logger.Error("Failed to apply network limits", log.Ctx{"container": c.name, "err": err})
			}
		}(c, name, m)
	}

	// Record current state
	err = dbContainerSetState(c.daemon.db, c.id, "RUNNING")
	if err != nil {
		return err
	}

	return nil
}

// Stop functions
func (c *containerLXC) Stop(stateful bool) error {
	var ctxMap log.Ctx

	// Check that we're not already stopped
	if !c.IsRunning() {
		return fmt.Errorf("The container is already stopped")
	}

	// Setup a new operation
	op, err := c.createOperation("stop", false, true)
	if err != nil {
		return err
	}

	ctxMap = log.Ctx{"name": c.name,
		"action":        op.action,
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral,
		"stateful":      stateful}

	logger.Info("Stopping container", ctxMap)

	// Handle stateful stop
	if stateful {
		// Cleanup any existing state
		stateDir := c.StatePath()
		os.RemoveAll(stateDir)

		err := os.MkdirAll(stateDir, 0700)
		if err != nil {
			op.Done(err)
			logger.Error("Failed stopping container", ctxMap)
			return err
		}

		// Checkpoint
		err = c.Migrate(lxc.MIGRATE_DUMP, stateDir, "snapshot", true, false)
		if err != nil {
			op.Done(err)
			logger.Error("Failed stopping container", ctxMap)
			return err
		}

		c.stateful = true
		err = dbContainerSetStateful(c.daemon.db, c.id, true)
		if err != nil {
			op.Done(err)
			logger.Error("Failed stopping container", ctxMap)
			return err
		}

		op.Done(nil)
		logger.Info("Stopped container", ctxMap)
		return nil
	}

	// Load the go-lxc struct
	err = c.initLXC()
	if err != nil {
		op.Done(err)
		logger.Error("Failed stopping container", ctxMap)
		return err
	}

	// Attempt to freeze the container first, helps massively with fork bombs
	freezer := make(chan bool, 1)
	go func() {
		c.Freeze()
		freezer <- true
	}()

	select {
	case <-freezer:
	case <-time.After(time.Second * 5):
		c.Unfreeze()
	}

	if err := c.c.Stop(); err != nil {
		op.Done(err)
		logger.Error("Failed stopping container", ctxMap)
		return err
	}

	err = op.Wait()
	if err != nil && c.IsRunning() {
		logger.Error("Failed stopping container", ctxMap)
		return err
	}

	logger.Info("Stopped container", ctxMap)
	return nil
}

func (c *containerLXC) Shutdown(timeout time.Duration) error {
	var ctxMap log.Ctx

	// Check that we're not already stopped
	if !c.IsRunning() {
		return fmt.Errorf("The container is already stopped")
	}

	// Setup a new operation
	op, err := c.createOperation("stop", true, true)
	if err != nil {
		return err
	}

	ctxMap = log.Ctx{"name": c.name,
		"action":        "shutdown",
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral,
		"timeout":       timeout}

	logger.Info("Shutting down container", ctxMap)

	// Load the go-lxc struct
	err = c.initLXC()
	if err != nil {
		op.Done(err)
		logger.Error("Failed shutting down container", ctxMap)
		return err
	}

	if err := c.c.Shutdown(timeout); err != nil {
		op.Done(err)
		logger.Error("Failed shutting down container", ctxMap)
		return err
	}

	err = op.Wait()
	if err != nil && c.IsRunning() {
		logger.Error("Failed shutting down container", ctxMap)
		return err
	}

	logger.Info("Shut down container", ctxMap)

	return nil
}

func (c *containerLXC) OnStop(target string) error {
	// Validate target
	if !shared.StringInSlice(target, []string{"stop", "reboot"}) {
		logger.Error("Container sent invalid target to OnStop", log.Ctx{"container": c.Name(), "target": target})
		return fmt.Errorf("Invalid stop target: %s", target)
	}

	// Get operation
	op, _ := c.getOperation("")
	if op != nil && op.action != "stop" {
		return fmt.Errorf("Container is already running a %s operation", op.action)
	}

	// Make sure we can't call go-lxc functions by mistake
	c.fromHook = true

	// Stop the storage for this container
	err := c.StorageStop()
	if err != nil {
		if op != nil {
			op.Done(err)
		}

		return err
	}

	// Log user actions
	if op == nil {
		ctxMap := log.Ctx{"name": c.name,
			"action":    target,
			"created":   c.creationDate,
			"ephemeral": c.ephemeral,
			"stateful":  false}

		logger.Info(fmt.Sprintf("Container initiated %s", target), ctxMap)
	}

	go func(c *containerLXC, target string, op *lxcContainerOperation) {
		c.fromHook = false
		err = nil

		// Unlock on return
		if op != nil {
			defer op.Done(err)
		}

		// Wait for other post-stop actions to be done
		c.IsRunning()

		// Unload the apparmor profile
		err = AADestroy(c)
		if err != nil {
			logger.Error("Failed to destroy apparmor namespace", log.Ctx{"container": c.Name(), "err": err})
		}

		// Clean all the unix devices
		err = c.removeUnixDevices()
		if err != nil {
			logger.Error("Unable to remove unix devices", log.Ctx{"container": c.Name(), "err": err})
		}

		// Clean all the disk devices
		err = c.removeDiskDevices()
		if err != nil {
			logger.Error("Unable to remove disk devices", log.Ctx{"container": c.Name(), "err": err})
		}

		// Reboot the container
		if target == "reboot" {
			// Start the container again
			err = c.Start(false)
			return
		}

		// Trigger a rebalance
		deviceTaskSchedulerTrigger("container", c.name, "stopped")

		// Record current state
		err = dbContainerSetState(c.daemon.db, c.id, "STOPPED")
		if err != nil {
			logger.Error("Failed to set container state", log.Ctx{"container": c.Name(), "err": err})
		}

		// Destroy ephemeral containers
		if c.ephemeral {
			err = c.Delete()
		}
	}(c, target, op)

	return nil
}

// Freezer functions
func (c *containerLXC) Freeze() error {
	ctxMap := log.Ctx{"name": c.name,
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral}

	// Check that we're not already frozen
	if c.IsFrozen() {
		return fmt.Errorf("The container is already frozen")
	}

	// Check that we're running
	if !c.IsRunning() {
		return fmt.Errorf("The container isn't running")
	}

	logger.Info("Freezing container", ctxMap)

	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		ctxMap["err"] = err
		logger.Error("Failed freezing container", ctxMap)
		return err
	}

	err = c.c.Freeze()
	if err != nil {
		ctxMap["err"] = err
		logger.Error("Failed freezing container", ctxMap)
		return err
	}

	logger.Info("Froze container", ctxMap)

	return err
}

func (c *containerLXC) Unfreeze() error {
	ctxMap := log.Ctx{"name": c.name,
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral}

	// Check that we're frozen
	if !c.IsFrozen() {
		return fmt.Errorf("The container is already running")
	}

	// Check that we're running
	if !c.IsRunning() {
		return fmt.Errorf("The container isn't running")
	}

	logger.Info("Unfreezing container", ctxMap)

	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		logger.Error("Failed unfreezing container", ctxMap)
		return err
	}

	err = c.c.Unfreeze()
	if err != nil {
		logger.Error("Failed unfreezing container", ctxMap)
	}

	logger.Info("Unfroze container", ctxMap)

	return err
}

var LxcMonitorStateError = fmt.Errorf("Monitor is hung")

// Get lxc container state, with 1 second timeout
// If we don't get a reply, assume the lxc monitor is hung
func (c *containerLXC) getLxcState() (lxc.State, error) {
	if c.IsSnapshot() {
		return lxc.StateMap["STOPPED"], nil
	}

	monitor := make(chan lxc.State, 1)

	go func(c *lxc.Container) {
		monitor <- c.State()
	}(c.c)

	select {
	case state := <-monitor:
		return state, nil
	case <-time.After(5 * time.Second):
		return lxc.StateMap["FROZEN"], LxcMonitorStateError
	}
}

func (c *containerLXC) Render() (interface{}, error) {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil, err
	}

	// Ignore err as the arch string on error is correct (unknown)
	architectureName, _ := osarch.ArchitectureName(c.architecture)

	if c.IsSnapshot() {
		return &api.ContainerSnapshot{
			Architecture:    architectureName,
			Config:          c.localConfig,
			CreationDate:    c.creationDate,
			Devices:         c.localDevices,
			Ephemeral:       c.ephemeral,
			ExpandedConfig:  c.expandedConfig,
			ExpandedDevices: c.expandedDevices,
			Name:            c.name,
			Profiles:        c.profiles,
			Stateful:        c.stateful,
		}, nil
	} else {
		// FIXME: Render shouldn't directly access the go-lxc struct
		cState, err := c.getLxcState()
		if err != nil {
			return nil, err
		}
		statusCode := lxcStatusCode(cState)

		ct := api.Container{
			ExpandedConfig:  c.expandedConfig,
			ExpandedDevices: c.expandedDevices,
			Name:            c.name,
			Status:          statusCode.String(),
			StatusCode:      statusCode,
		}

		ct.Architecture = architectureName
		ct.Config = c.localConfig
		ct.CreatedAt = c.creationDate
		ct.Devices = c.localDevices
		ct.Ephemeral = c.ephemeral
		ct.Profiles = c.profiles
		ct.Stateful = c.stateful

		return &ct, nil
	}
}

func (c *containerLXC) RenderState() (*api.ContainerState, error) {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return nil, err
	}

	cState, err := c.getLxcState()
	if err != nil {
		return nil, err
	}
	statusCode := lxcStatusCode(cState)
	status := api.ContainerState{
		Status:     statusCode.String(),
		StatusCode: statusCode,
	}

	if c.IsRunning() {
		pid := c.InitPID()
		status.Disk = c.diskState()
		status.Memory = c.memoryState()
		status.Network = c.networkState()
		status.Pid = int64(pid)
		status.Processes = c.processesState()
	}

	return &status, nil
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
	var ctxMap log.Ctx

	// Check if we can restore the container
	err := c.storage.ContainerCanRestore(c, sourceContainer)
	if err != nil {
		return err
	}

	/* let's also check for CRIU if necessary, before doing a bunch of
	 * filesystem manipulations
	 */
	if shared.PathExists(c.StatePath()) {
		_, err := exec.LookPath("criu")
		if err != nil {
			return fmt.Errorf("Failed to restore container state. CRIU isn't installed.")
		}
	}

	// Stop the container
	wasRunning := false
	if c.IsRunning() {
		wasRunning = true
		if err := c.Stop(false); err != nil {
			return err
		}
	}

	ctxMap = log.Ctx{"name": c.name,
		"creation date":    c.creationDate,
		"ephemeral":        c.ephemeral,
		"source container": sourceContainer.Name()}

	logger.Info("Restoring container", ctxMap)

	// Restore the rootfs
	err = c.storage.ContainerRestore(c, sourceContainer)
	if err != nil {
		logger.Error("Failed restoring container filesystem", ctxMap)
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
		logger.Error("Failed restoring container configuration", ctxMap)
		return err
	}

	// If the container wasn't running but was stateful, should we restore
	// it as running?
	if shared.PathExists(c.StatePath()) {
		if err := c.Migrate(lxc.MIGRATE_RESTORE, c.StatePath(), "snapshot", false, false); err != nil {
			return err
		}

		// Remove the state from the parent container; we only keep
		// this in snapshots.
		err2 := os.RemoveAll(c.StatePath())
		if err2 != nil {
			logger.Error("Failed to delete snapshot state", log.Ctx{"path": c.StatePath(), "err": err2})
		}

		if err != nil {
			logger.Info("Failed restoring container", ctxMap)
			return err
		}

		logger.Info("Restored container", ctxMap)
		return nil
	}

	// Restart the container
	if wasRunning {
		logger.Info("Restored container", ctxMap)
		return c.Start(false)
	}

	logger.Info("Restored container", ctxMap)

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
	os.Remove(c.DevicesPath())

	// Remove the shmounts path
	os.RemoveAll(shared.VarPath("shmounts", c.Name()))
}

func (c *containerLXC) Delete() error {
	ctxMap := log.Ctx{"name": c.name,
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral}

	logger.Info("Deleting container", ctxMap)

	if c.IsSnapshot() {
		// Remove the snapshot
		if err := c.storage.ContainerSnapshotDelete(c); err != nil {
			logger.Warn("Failed to delete snapshot", log.Ctx{"name": c.Name(), "err": err})
			return err
		}
	} else {
		// Remove all snapshot
		if err := containerDeleteSnapshots(c.daemon, c.Name()); err != nil {
			logger.Warn("Failed to delete snapshots", log.Ctx{"name": c.Name(), "err": err})
			return err
		}

		// Clean things up
		c.cleanup()

		// Delete the container from disk
		if shared.PathExists(c.Path()) {
			if err := c.storage.ContainerDelete(c); err != nil {
				logger.Error("Failed deleting container storage", ctxMap)
				return err
			}
		}
	}

	// Remove the database record
	if err := dbContainerRemove(c.daemon.db, c.Name()); err != nil {
		logger.Error("Failed deleting container entry", ctxMap)
		return err
	}

	logger.Info("Deleted container", ctxMap)

	return nil
}

func (c *containerLXC) Rename(newName string) error {
	oldName := c.Name()
	ctxMap := log.Ctx{"name": c.name,
		"creation date": c.creationDate,
		"ephemeral":     c.ephemeral,
		"newname":       newName}

	logger.Info("Renaming container", ctxMap)

	// Sanity checks
	if !c.IsSnapshot() && !shared.ValidHostname(newName) {
		return fmt.Errorf("Invalid container name")
	}

	if c.IsRunning() {
		return fmt.Errorf("Renaming of running container not allowed")
	}

	// Clean things up
	c.cleanup()

	// Rename the logging path
	os.RemoveAll(shared.LogPath(newName))
	if shared.PathExists(c.LogPath()) {
		err := os.Rename(c.LogPath(), shared.LogPath(newName))
		if err != nil {
			logger.Error("Failed renaming container", ctxMap)
			return err
		}
	}

	// Rename the storage entry
	if c.IsSnapshot() {
		if err := c.storage.ContainerSnapshotRename(c, newName); err != nil {
			logger.Error("Failed renaming container", ctxMap)
			return err
		}
	} else {
		if err := c.storage.ContainerRename(c, newName); err != nil {
			logger.Error("Failed renaming container", ctxMap)
			return err
		}
	}

	// Rename the database entry
	if err := dbContainerRename(c.daemon.db, oldName, newName); err != nil {
		logger.Error("Failed renaming container", ctxMap)
		return err
	}

	if !c.IsSnapshot() {
		// Rename all the snapshots
		results, err := dbContainerGetSnapshots(c.daemon.db, oldName)
		if err != nil {
			logger.Error("Failed renaming container", ctxMap)
			return err
		}

		for _, sname := range results {
			// Rename the snapshot
			baseSnapName := filepath.Base(sname)
			newSnapshotName := newName + shared.SnapshotDelimiter + baseSnapName
			if err := dbContainerRename(c.daemon.db, sname, newSnapshotName); err != nil {
				logger.Error("Failed renaming container", ctxMap)
				return err
			}
		}
	}

	// Set the new name in the struct
	c.name = newName

	// Invalidate the go-lxc cache
	c.c = nil

	logger.Info("Renamed container", ctxMap)

	return nil
}

func (c *containerLXC) CGroupGet(key string) (string, error) {
	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return "", err
	}

	// Make sure the container is running
	if !c.IsRunning() {
		return "", fmt.Errorf("Can't get cgroups on a stopped container")
	}

	value := c.c.CgroupItem(key)
	return strings.Join(value, "\n"), nil
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
		args.Devices = types.Devices{}
	}

	if args.Profiles == nil {
		args.Profiles = []string{}
	}

	// Validate the new config
	err := containerValidConfig(c.daemon, args.Config, false, false)
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
		_, err = osarch.ArchitectureName(args.Architecture)
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

	oldExpandedDevices := types.Devices{}
	err = shared.DeepCopy(&c.expandedDevices, &oldExpandedDevices)
	if err != nil {
		return err
	}

	oldExpandedConfig := map[string]string{}
	err = shared.DeepCopy(&c.expandedConfig, &oldExpandedConfig)
	if err != nil {
		return err
	}

	oldLocalDevices := types.Devices{}
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

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path.  Track whether or not we want to undo the changes
	// using a closure.
	undoChanges := true
	defer func() {
		if undoChanges {
			c.architecture = oldArchitecture
			c.ephemeral = oldEphemeral
			c.expandedConfig = oldExpandedConfig
			c.expandedDevices = oldExpandedDevices
			c.localConfig = oldLocalConfig
			c.localDevices = oldLocalDevices
			c.profiles = oldProfiles
			c.c = nil
			c.initLXC()
			deviceTaskSchedulerTrigger("container", c.name, "changed")
		}
	}()

	// Apply the various changes
	c.architecture = args.Architecture
	c.ephemeral = args.Ephemeral
	c.localConfig = args.Config
	c.localDevices = args.Devices
	c.profiles = args.Profiles

	// Expand the config and refresh the LXC config
	err = c.expandConfig()
	if err != nil {
		return err
	}

	err = c.expandDevices()
	if err != nil {
		return err
	}

	// Diff the configurations
	changedConfig := []string{}
	for key := range oldExpandedConfig {
		if oldExpandedConfig[key] != c.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range c.expandedConfig {
		if oldExpandedConfig[key] != c.expandedConfig[key] {
			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices
	removeDevices, addDevices, updateDevices := oldExpandedDevices.Update(c.expandedDevices)

	// Do some validation of the config diff
	err = containerValidConfig(c.daemon, c.expandedConfig, false, true)
	if err != nil {
		return err
	}

	// Do some validation of the devices diff
	err = containerValidDevices(c.expandedDevices, false, true)
	if err != nil {
		return err
	}

	// Run through initLXC to catch anything we missed
	c.c = nil
	err = c.initLXC()
	if err != nil {
		return err
	}

	// If apparmor changed, re-validate the apparmor profile
	if shared.StringInSlice("raw.apparmor", changedConfig) || shared.StringInSlice("security.nesting", changedConfig) {
		err = AAParseProfile(c)
		if err != nil {
			return err
		}
	}

	if shared.StringInSlice("security.idmap.isolated", changedConfig) || shared.StringInSlice("security.idmap.size", changedConfig) || shared.StringInSlice("raw.idmap", changedConfig) || shared.StringInSlice("security.privileged", changedConfig) {
		var idmap *shared.IdmapSet
		base := int64(0)
		if !c.IsPrivileged() {
			// update the idmap
			idmap, base, err = findIdmap(
				c.daemon,
				c.Name(),
				c.expandedConfig["security.idmap.isolated"],
				c.expandedConfig["security.idmap.size"],
				c.expandedConfig["raw.idmap"],
			)
			if err != nil {
				return err
			}
		}

		var jsonIdmap string
		if idmap != nil {
			idmapBytes, err := json.Marshal(idmap.Idmap)
			if err != nil {
				return err
			}
			jsonIdmap = string(idmapBytes)
		} else {
			jsonIdmap = "[]"
		}
		c.localConfig["volatile.idmap.next"] = jsonIdmap
		c.localConfig["volatile.idmap.base"] = fmt.Sprintf("%v", base)

		// Invalid idmap cache
		c.idmapset = nil
	}

	// Apply disk quota changes
	for _, m := range addDevices {
		if m["type"] != "disk" || m["path"] != "/" {
			continue
		}

		var oldRootfsSize string
		for _, n := range oldExpandedDevices {
			if n["type"] == "disk" && n["path"] == "/" {
				oldRootfsSize = n["size"]
				break
			}
		}

		if m["size"] != oldRootfsSize {
			size, err := shared.ParseByteSizeString(m["size"])
			if err != nil {
				return err
			}

			err = c.storage.ContainerSetQuota(c, size)
			if err != nil {
				return err
			}
		}
	}

	// Apply the live changes
	if c.IsRunning() {
		// Confirm that the rootfs source didn't change
		var oldRootfs types.Device
		for _, m := range oldExpandedDevices {
			if m["type"] == "disk" && m["path"] == "/" {
				oldRootfs = m
				break
			}
		}

		var newRootfs types.Device
		for _, name := range c.expandedDevices.DeviceNames() {
			m := c.expandedDevices[name]
			if m["type"] == "disk" && m["path"] == "/" {
				newRootfs = m
				break
			}
		}

		if oldRootfs["source"] != newRootfs["source"] {
			return fmt.Errorf("Cannot change the rootfs path of a running container")
		}

		// Live update the container config
		for _, key := range changedConfig {
			value := c.expandedConfig[key]

			if key == "raw.apparmor" || key == "security.nesting" {
				// Update the AppArmor profile
				err = AALoadProfile(c)
				if err != nil {
					return err
				}
			} else if key == "linux.kernel_modules" && value != "" {
				for _, module := range strings.Split(value, ",") {
					module = strings.TrimPrefix(module, " ")
					err := loadModule(module)
					if err != nil {
						return fmt.Errorf("Failed to load kernel module '%s': %s", module, err)
					}
				}
			} else if key == "limits.disk.priority" {
				if !cgBlkioController {
					continue
				}

				priorityInt := 5
				diskPriority := c.expandedConfig["limits.disk.priority"]
				if diskPriority != "" {
					priorityInt, err = strconv.Atoi(diskPriority)
					if err != nil {
						return err
					}
				}

				// Minimum valid value is 10
				priority := priorityInt * 100
				if priority == 0 {
					priority = 10
				}

				err = c.CGroupSet("blkio.weight", fmt.Sprintf("%d", priority))
				if err != nil {
					return err
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
					valueInt, err := shared.ParseByteSizeString(memory)
					if err != nil {
						return err
					}
					memory = fmt.Sprintf("%d", valueInt)
				}

				// Store the old values for revert
				oldMemswLimit := ""
				if cgSwapAccounting {
					oldMemswLimit, err = c.CGroupGet("memory.memsw.limit_in_bytes")
					if err != nil {
						oldMemswLimit = ""
					}
				}

				oldLimit, err := c.CGroupGet("memory.limit_in_bytes")
				if err != nil {
					oldLimit = ""
				}

				oldSoftLimit, err := c.CGroupGet("memory.soft_limit_in_bytes")
				if err != nil {
					oldSoftLimit = ""
				}

				revertMemory := func() {
					if oldSoftLimit != "" {
						c.CGroupSet("memory.soft_limit_in_bytes", oldSoftLimit)
					}

					if oldLimit != "" {
						c.CGroupSet("memory.limit_in_bytes", oldLimit)
					}

					if oldMemswLimit != "" {
						c.CGroupSet("memory.memsw.limit_in_bytes", oldMemswLimit)
					}
				}

				// Reset everything
				if cgSwapAccounting {
					err = c.CGroupSet("memory.memsw.limit_in_bytes", "-1")
					if err != nil {
						revertMemory()
						return err
					}
				}

				err = c.CGroupSet("memory.limit_in_bytes", "-1")
				if err != nil {
					revertMemory()
					return err
				}

				err = c.CGroupSet("memory.soft_limit_in_bytes", "-1")
				if err != nil {
					revertMemory()
					return err
				}

				// Set the new values
				if memoryEnforce == "soft" {
					// Set new limit
					err = c.CGroupSet("memory.soft_limit_in_bytes", memory)
					if err != nil {
						revertMemory()
						return err
					}
				} else {
					if cgSwapAccounting && (memorySwap == "" || shared.IsTrue(memorySwap)) {
						err = c.CGroupSet("memory.limit_in_bytes", memory)
						if err != nil {
							revertMemory()
							return err
						}

						err = c.CGroupSet("memory.memsw.limit_in_bytes", memory)
						if err != nil {
							revertMemory()
							return err
						}
					} else {
						err = c.CGroupSet("memory.limit_in_bytes", memory)
						if err != nil {
							revertMemory()
							return err
						}
					}

					// Set soft limit to value 10% less than hard limit
					valueInt, err := strconv.ParseInt(memory, 10, 64)
					if err != nil {
						revertMemory()
						return err
					}

					err = c.CGroupSet("memory.soft_limit_in_bytes", fmt.Sprintf("%.0f", float64(valueInt)*0.9))
					if err != nil {
						revertMemory()
						return err
					}
				}

				// Configure the swappiness
				if key == "limits.memory.swap" || key == "limits.memory.swap.priority" {
					memorySwap := c.expandedConfig["limits.memory.swap"]
					memorySwapPriority := c.expandedConfig["limits.memory.swap.priority"]
					if memorySwap != "" && !shared.IsTrue(memorySwap) {
						err = c.CGroupSet("memory.swappiness", "0")
						if err != nil {
							return err
						}
					} else {
						priority := 0
						if memorySwapPriority != "" {
							priority, err = strconv.Atoi(memorySwapPriority)
							if err != nil {
								return err
							}
						}

						err = c.CGroupSet("memory.swappiness", fmt.Sprintf("%d", 60-10+priority))
						if err != nil {
							return err
						}
					}
				}
			} else if key == "limits.network.priority" {
				err := c.setNetworkPriority()
				if err != nil {
					return err
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
					return err
				}

				err = c.CGroupSet("cpu.shares", cpuShares)
				if err != nil {
					return err
				}

				err = c.CGroupSet("cpu.cfs_period_us", cpuCfsPeriod)
				if err != nil {
					return err
				}

				err = c.CGroupSet("cpu.cfs_quota_us", cpuCfsQuota)
				if err != nil {
					return err
				}
			} else if key == "limits.processes" {
				if !cgPidsController {
					continue
				}

				if value == "" {
					err = c.CGroupSet("pids.max", "max")
					if err != nil {
						return err
					}
				} else {
					valueInt, err := strconv.ParseInt(value, 10, 64)
					if err != nil {
						return err
					}

					err = c.CGroupSet("pids.max", fmt.Sprintf("%d", valueInt))
					if err != nil {
						return err
					}
				}
			}
		}

		// Live update the devices
		for k, m := range removeDevices {
			if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
				err = c.removeUnixDevice(m)
				if err != nil {
					return err
				}
			} else if m["type"] == "disk" && m["path"] != "/" {
				err = c.removeDiskDevice(k, m)
				if err != nil {
					return err
				}
			} else if m["type"] == "nic" {
				err = c.removeNetworkDevice(k, m)
				if err != nil {
					return err
				}
			}
		}

		diskDevices := map[string]types.Device{}

		for k, m := range addDevices {
			if shared.StringInSlice(m["type"], []string{"unix-char", "unix-block"}) {
				err = c.insertUnixDevice(k, m)
				if err != nil {
					return err
				}
			} else if m["type"] == "disk" && m["path"] != "/" {
				diskDevices[k] = m
			} else if m["type"] == "nic" {
				err = c.insertNetworkDevice(k, m)
				if err != nil {
					return err
				}
			}
		}

		err = c.addDiskDevices(diskDevices, c.insertDiskDevice)
		if err != nil {
			return err
		}

		updateDiskLimit := false
		for k, m := range updateDevices {
			if m["type"] == "disk" {
				updateDiskLimit = true
			} else if m["type"] == "nic" {
				err = c.setNetworkLimits(k, m)
				if err != nil {
					return err
				}
			}
		}

		// Disk limits parse all devices, so just apply them once
		if updateDiskLimit && cgBlkioController {
			diskLimits, err := c.getDiskLimits()
			if err != nil {
				return err
			}

			for block, limit := range diskLimits {
				err = c.CGroupSet("blkio.throttle.read_bps_device", fmt.Sprintf("%s %d", block, limit.readBps))
				if err != nil {
					return err
				}

				err = c.CGroupSet("blkio.throttle.read_iops_device", fmt.Sprintf("%s %d", block, limit.readIops))
				if err != nil {
					return err
				}

				err = c.CGroupSet("blkio.throttle.write_bps_device", fmt.Sprintf("%s %d", block, limit.writeBps))
				if err != nil {
					return err
				}

				err = c.CGroupSet("blkio.throttle.write_iops_device", fmt.Sprintf("%s %d", block, limit.writeIops))
				if err != nil {
					return err
				}
			}
		}
	}

	// Finally, apply the changes to the database
	tx, err := dbBegin(c.daemon.db)
	if err != nil {
		return err
	}

	err = dbContainerConfigClear(tx, c.id)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbContainerConfigInsert(tx, c.id, args.Config)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbContainerProfilesInsert(tx, c.id, args.Profiles)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbDevicesAdd(tx, "container", int64(c.id), args.Devices)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = dbContainerUpdate(tx, c.id, c.architecture, c.ephemeral)
	if err != nil {
		tx.Rollback()
		return err
	}

	if err := txCommit(tx); err != nil {
		return err
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}

func (c *containerLXC) Export(w io.Writer, properties map[string]string) error {
	ctxMap := log.Ctx{"name": c.name,
		"created":   c.creationDate,
		"ephemeral": c.ephemeral}

	if c.IsRunning() {
		return fmt.Errorf("Cannot export a running container as an image")
	}

	logger.Info("Exporting container", ctxMap)

	// Start the storage
	err := c.StorageStart()
	if err != nil {
		logger.Error("Failed exporting container", ctxMap)
		return err
	}
	defer c.StorageStop()

	// Unshift the container
	idmap, err := c.LastIdmapSet()
	if err != nil {
		logger.Error("Failed exporting container", ctxMap)
		return err
	}

	if idmap != nil {
		if err := idmap.UnshiftRootfs(c.RootfsPath()); err != nil {
			logger.Error("Failed exporting container", ctxMap)
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
		if err != nil {
			return err
		}

		err = c.tarStoreFile(linkmap, offset, tw, path, fi)
		if err != nil {
			logger.Debugf("Error tarring up %s: %s", path, err)
			return err
		}
		return nil
	}

	// Look for metadata.yaml
	fnam := filepath.Join(cDir, "metadata.yaml")
	if !shared.PathExists(fnam) {
		// Generate a new metadata.yaml
		tempDir, err := ioutil.TempDir("", "lxd_lxd_metadata_")
		if err != nil {
			tw.Close()
			logger.Error("Failed exporting container", ctxMap)
			return err
		}
		defer os.RemoveAll(tempDir)

		// Get the container's architecture
		var arch string
		if c.IsSnapshot() {
			parentName, _, _ := containerGetParentAndSnapshotName(c.name)
			parent, err := containerLoadByName(c.daemon, parentName)
			if err != nil {
				tw.Close()
				logger.Error("Failed exporting container", ctxMap)
				return err
			}

			arch, _ = osarch.ArchitectureName(parent.Architecture())
		} else {
			arch, _ = osarch.ArchitectureName(c.architecture)
		}

		if arch == "" {
			arch, err = osarch.ArchitectureName(c.daemon.architectures[0])
			if err != nil {
				logger.Error("Failed exporting container", ctxMap)
				return err
			}
		}

		// Fill in the metadata
		meta := imageMetadata{}
		meta.Architecture = arch
		meta.CreationDate = time.Now().UTC().Unix()
		meta.Properties = properties

		data, err := yaml.Marshal(&meta)
		if err != nil {
			tw.Close()
			logger.Error("Failed exporting container", ctxMap)
			return err
		}

		// Write the actual file
		fnam = filepath.Join(tempDir, "metadata.yaml")
		err = ioutil.WriteFile(fnam, data, 0644)
		if err != nil {
			tw.Close()
			logger.Error("Failed exporting container", ctxMap)
			return err
		}

		fi, err := os.Lstat(fnam)
		if err != nil {
			tw.Close()
			logger.Error("Failed exporting container", ctxMap)
			return err
		}

		tmpOffset := len(path.Dir(fnam)) + 1
		if err := c.tarStoreFile(linkmap, tmpOffset, tw, fnam, fi); err != nil {
			tw.Close()
			logger.Debugf("Error writing to tarfile: %s", err)
			logger.Error("Failed exporting container", ctxMap)
			return err
		}
	} else {
		if properties != nil {
			// Parse the metadata
			content, err := ioutil.ReadFile(fnam)
			if err != nil {
				tw.Close()
				logger.Error("Failed exporting container", ctxMap)
				return err
			}

			metadata := new(imageMetadata)
			err = yaml.Unmarshal(content, &metadata)
			if err != nil {
				tw.Close()
				logger.Error("Failed exporting container", ctxMap)
				return err
			}
			metadata.Properties = properties

			// Generate a new metadata.yaml
			tempDir, err := ioutil.TempDir("", "lxd_lxd_metadata_")
			if err != nil {
				tw.Close()
				logger.Error("Failed exporting container", ctxMap)
				return err
			}
			defer os.RemoveAll(tempDir)

			data, err := yaml.Marshal(&metadata)
			if err != nil {
				tw.Close()
				logger.Error("Failed exporting container", ctxMap)
				return err
			}

			// Write the actual file
			fnam = filepath.Join(tempDir, "metadata.yaml")
			err = ioutil.WriteFile(fnam, data, 0644)
			if err != nil {
				tw.Close()
				logger.Error("Failed exporting container", ctxMap)
				return err
			}
		}

		// Include metadata.yaml in the tarball
		fi, err := os.Lstat(fnam)
		if err != nil {
			tw.Close()
			logger.Debugf("Error statting %s during export", fnam)
			logger.Error("Failed exporting container", ctxMap)
			return err
		}

		if properties != nil {
			tmpOffset := len(path.Dir(fnam)) + 1
			err = c.tarStoreFile(linkmap, tmpOffset, tw, fnam, fi)
		} else {
			err = c.tarStoreFile(linkmap, offset, tw, fnam, fi)
		}
		if err != nil {
			tw.Close()
			logger.Debugf("Error writing to tarfile: %s", err)
			logger.Error("Failed exporting container", ctxMap)
			return err
		}
	}

	// Include all the rootfs files
	fnam = c.RootfsPath()
	err = filepath.Walk(fnam, writeToTar)
	if err != nil {
		logger.Error("Failed exporting container", ctxMap)
		return err
	}

	// Include all the templates
	fnam = c.TemplatesPath()
	if shared.PathExists(fnam) {
		err = filepath.Walk(fnam, writeToTar)
		if err != nil {
			logger.Error("Failed exporting container", ctxMap)
			return err
		}
	}

	err = tw.Close()
	if err != nil {
		logger.Error("Failed exporting container", ctxMap)
		return err
	}

	logger.Info("Exported container", ctxMap)
	return nil
}

func collectCRIULogFile(c container, imagesDir string, function string, method string) error {
	t := time.Now().Format(time.RFC3339)
	newPath := shared.LogPath(c.Name(), fmt.Sprintf("%s_%s_%s.log", function, method, t))
	return shared.FileCopy(filepath.Join(imagesDir, fmt.Sprintf("%s.log", method)), newPath)
}

func getCRIULogErrors(imagesDir string, method string) (string, error) {
	f, err := os.Open(path.Join(imagesDir, fmt.Sprintf("%s.log", method)))
	if err != nil {
		return "", err
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)
	ret := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Error") || strings.Contains(line, "Warn") {
			ret = append(ret, scanner.Text())
		}
	}

	return strings.Join(ret, "\n"), nil
}

func (c *containerLXC) Migrate(cmd uint, stateDir string, function string, stop bool, actionScript bool) error {
	ctxMap := log.Ctx{"name": c.name,
		"created":      c.creationDate,
		"ephemeral":    c.ephemeral,
		"statedir":     stateDir,
		"actionscript": actionScript,
		"stop":         stop}

	_, err := exec.LookPath("criu")
	if err != nil {
		return fmt.Errorf("Unable to perform container live migration. CRIU isn't installed.")
	}

	logger.Info("Migrating container", ctxMap)

	prettyCmd := ""
	switch cmd {
	case lxc.MIGRATE_PRE_DUMP:
		prettyCmd = "pre-dump"
	case lxc.MIGRATE_DUMP:
		prettyCmd = "dump"
	case lxc.MIGRATE_RESTORE:
		prettyCmd = "restore"
	default:
		prettyCmd = "unknown"
		logger.Warn("unknown migrate call", log.Ctx{"cmd": cmd})
	}

	preservesInodes := c.storage.PreservesInodes()
	/* This feature was only added in 2.0.1, let's not ask for it
	 * before then or migrations will fail.
	 */
	if !lxc.VersionAtLeast(2, 0, 1) {
		preservesInodes = false
	}

	var migrateErr error

	/* For restore, we need an extra fork so that we daemonize monitor
	 * instead of having it be a child of LXD, so let's hijack the command
	 * here and do the extra fork.
	 */
	if cmd == lxc.MIGRATE_RESTORE {
		// Run the shared start
		_, err := c.startCommon()
		if err != nil {
			return err
		}

		/*
		 * For unprivileged containers we need to shift the
		 * perms on the images images so that they can be
		 * opened by the process after it is in its user
		 * namespace.
		 */
		if !c.IsPrivileged() {
			idmapset, err := c.IdmapSet()
			if err != nil {
				return err
			}

			err = c.StorageStart()
			if err != nil {
				return err
			}

			err = idmapset.ShiftRootfs(stateDir)
			err2 := c.StorageStop()
			if err != nil {
				return err
			}

			if err2 != nil {
				return err2
			}
		}

		configPath := filepath.Join(c.LogPath(), "lxc.conf")

		var out string
		out, migrateErr = shared.RunCommand(
			execPath,
			"forkmigrate",
			c.name,
			c.daemon.lxcpath,
			configPath,
			stateDir,
			fmt.Sprintf("%v", preservesInodes))

		if string(out) != "" {
			for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
				logger.Debugf("forkmigrate: %s", line)
			}
		}

		if migrateErr != nil && !c.IsRunning() {
			migrateErr = fmt.Errorf(
				"Error calling 'lxd forkmigrate %s %s %s %s': err='%v' out='%v'",
				c.name,
				c.daemon.lxcpath,
				filepath.Join(c.LogPath(), "lxc.conf"),
				stateDir,
				err,
				string(out))
		}

	} else {
		err := c.initLXC()
		if err != nil {
			return err
		}

		script := ""
		if actionScript {
			script = filepath.Join(stateDir, "action.sh")
		}

		// TODO: make this configurable? Ultimately I think we don't
		// want to do that; what we really want to do is have "modes"
		// of criu operation where one is "make this succeed" and the
		// other is "make this fast". Anyway, for now, let's choose a
		// really big size so it almost always succeeds, even if it is
		// slow.
		ghostLimit := uint64(256 * 1024 * 1024)

		opts := lxc.MigrateOptions{
			Stop:            stop,
			Directory:       stateDir,
			Verbose:         true,
			PreservesInodes: preservesInodes,
			ActionScript:    script,
			GhostLimit:      ghostLimit,
		}

		migrateErr = c.c.Migrate(cmd, opts)
	}

	collectErr := collectCRIULogFile(c, stateDir, function, prettyCmd)
	if collectErr != nil {
		logger.Error("Error collecting checkpoint log file", log.Ctx{"err": collectErr})
	}

	if migrateErr != nil {
		log, err2 := getCRIULogErrors(stateDir, prettyCmd)
		if err2 == nil {
			logger.Info("Failed migrating container", ctxMap)
			migrateErr = fmt.Errorf("%s %s failed\n%s", function, prettyCmd, log)
		}

		return migrateErr
	}

	logger.Info("Migrated container", ctxMap)

	return nil
}

func (c *containerLXC) TemplateApply(trigger string) error {
	// "create" and "copy" are deferred until next start
	if shared.StringInSlice(trigger, []string{"create", "copy"}) {
		// The two events are mutually exclusive so only keep the last one
		err := c.ConfigKeySet("volatile.apply_template", trigger)
		if err != nil {
			return err
		}

		return nil
	}

	return c.templateApplyNow(trigger)
}

func (c *containerLXC) templateApplyNow(trigger string) error {
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
			if template.CreateOnly {
				continue
			}

			// Open the existing file
			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}
		} else {
			// Create a new one
			uid := int64(0)
			gid := int64(0)

			// Get the right uid and gid for the container
			if !c.IsPrivileged() {
				idmapset, err := c.IdmapSet()
				if err != nil {
					return err
				}

				uid, gid = idmapset.ShiftIntoNs(0, 0)
			}

			// Create the directories leading to the file
			shared.MkdirAllOwner(path.Dir(fullpath), 0755, int(uid), int(gid))

			// Create the file itself
			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}

			// Fix ownership and mode
			if !c.IsPrivileged() {
				w.Chown(int(uid), int(gid))
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
		arch, err := osarch.ArchitectureName(c.architecture)
		if err != nil {
			arch, err = osarch.ArchitectureName(c.daemon.architectures[0])
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

func (c *containerLXC) FileExists(path string) error {
	// Setup container storage if needed
	if !c.IsRunning() {
		err := c.StorageStart()
		if err != nil {
			return err
		}
	}

	// Check if the file exists in the container
	out, err := shared.RunCommand(
		execPath,
		"forkcheckfile",
		c.RootfsPath(),
		fmt.Sprintf("%d", c.InitPID()),
		path,
	)

	// Tear down container storage if needed
	if !c.IsRunning() {
		err := c.StorageStop()
		if err != nil {
			return err
		}
	}

	// Process forkcheckfile response
	if string(out) != "" {
		if strings.HasPrefix(string(out), "error:") {
			return fmt.Errorf(strings.TrimPrefix(strings.TrimSuffix(string(out), "\n"), "error: "))
		}

		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			logger.Debugf("forkcheckfile: %s", line)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkcheckfile %s %d %s': err='%v'",
			c.RootfsPath(),
			c.InitPID(),
			path,
			err)
	}

	return nil
}

func (c *containerLXC) FilePull(srcpath string, dstpath string) (int64, int64, os.FileMode, error) {
	// Setup container storage if needed
	if !c.IsRunning() {
		err := c.StorageStart()
		if err != nil {
			return -1, -1, 0, err
		}
	}

	// Get the file from the container
	out, err := shared.RunCommand(
		execPath,
		"forkgetfile",
		c.RootfsPath(),
		fmt.Sprintf("%d", c.InitPID()),
		dstpath,
		srcpath,
	)

	// Tear down container storage if needed
	if !c.IsRunning() {
		err := c.StorageStop()
		if err != nil {
			return -1, -1, 0, err
		}
	}

	uid := int64(-1)
	gid := int64(-1)
	mode := -1
	var errStr string

	// Process forkgetfile response
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}

		// Extract errors
		if strings.HasPrefix(line, "error: ") {
			errStr = strings.TrimPrefix(line, "error: ")
			continue
		}

		if strings.HasPrefix(line, "errno: ") {
			errno := strings.TrimPrefix(line, "errno: ")
			if errno == "2" {
				return -1, -1, 0, os.ErrNotExist
			}

			return -1, -1, 0, fmt.Errorf(errStr)
		}

		// Extract the uid
		if strings.HasPrefix(line, "uid: ") {
			uid, err = strconv.ParseInt(strings.TrimPrefix(line, "uid: "), 10, 64)
			if err != nil {
				return -1, -1, 0, err
			}

			continue
		}

		// Extract the gid
		if strings.HasPrefix(line, "gid: ") {
			gid, err = strconv.ParseInt(strings.TrimPrefix(line, "gid: "), 10, 64)
			if err != nil {
				return -1, -1, 0, err
			}

			continue
		}

		// Extract the mode
		if strings.HasPrefix(line, "mode: ") {
			mode, err = strconv.Atoi(strings.TrimPrefix(line, "mode: "))
			if err != nil {
				return -1, -1, 0, err
			}

			continue
		}

		logger.Debugf("forkgetfile: %s", line)
	}

	if err != nil {
		return -1, -1, 0, fmt.Errorf(
			"Error calling 'lxd forkgetfile %s %d %s %s': err='%v'",
			c.RootfsPath(),
			c.InitPID(),
			dstpath,
			srcpath,
			err)
	}

	// Unmap uid and gid if needed
	if !c.IsRunning() {
		idmapset, err := c.LastIdmapSet()
		if err != nil {
			return -1, -1, 0, err
		}

		if idmapset != nil {
			uid, gid = idmapset.ShiftFromNs(uid, gid)
		}
	}

	return uid, gid, os.FileMode(mode), nil
}

func (c *containerLXC) FilePush(srcpath string, dstpath string, uid int64, gid int64, mode int) error {
	var rootUid int64
	var rootGid int64
	var errStr string

	// Map uid and gid if needed
	if !c.IsRunning() {
		idmapset, err := c.LastIdmapSet()
		if err != nil {
			return err
		}

		if idmapset != nil {
			uid, gid = idmapset.ShiftIntoNs(uid, gid)
			rootUid, rootGid = idmapset.ShiftIntoNs(0, 0)
		}
	}

	// Setup container storage if needed
	if !c.IsRunning() {
		err := c.StorageStart()
		if err != nil {
			return err
		}
	}

	// Push the file to the container
	out, err := shared.RunCommand(
		execPath,
		"forkputfile",
		c.RootfsPath(),
		fmt.Sprintf("%d", c.InitPID()),
		srcpath,
		dstpath,
		fmt.Sprintf("%d", uid),
		fmt.Sprintf("%d", gid),
		fmt.Sprintf("%d", mode),
		fmt.Sprintf("%d", rootUid),
		fmt.Sprintf("%d", rootGid),
		fmt.Sprintf("%d", int(os.FileMode(0640)&os.ModePerm)),
	)

	// Tear down container storage if needed
	if !c.IsRunning() {
		err := c.StorageStop()
		if err != nil {
			return err
		}
	}

	// Process forkgetfile response
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}

		// Extract errors
		if strings.HasPrefix(line, "error: ") {
			errStr = strings.TrimPrefix(line, "error: ")
			continue
		}

		if strings.HasPrefix(line, "errno: ") {
			errno := strings.TrimPrefix(line, "errno: ")
			if errno == "2" {
				return os.ErrNotExist
			}

			return fmt.Errorf(errStr)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkputfile %s %d %s %s %d %d %d %d %d %d': err='%v'",
			c.RootfsPath(),
			c.InitPID(),
			srcpath,
			dstpath,
			uid,
			gid,
			mode,
			rootUid,
			rootGid,
			int(os.FileMode(0640)&os.ModePerm),
			err)
	}

	return nil
}

func (c *containerLXC) FileRemove(path string) error {
	var errStr string

	// Setup container storage if needed
	if !c.IsRunning() {
		err := c.StorageStart()
		if err != nil {
			return err
		}
	}

	// Remove the file from the container
	out, err := shared.RunCommand(
		execPath,
		"forkremovefile",
		c.RootfsPath(),
		fmt.Sprintf("%d", c.InitPID()),
		path,
	)

	// Tear down container storage if needed
	if !c.IsRunning() {
		err := c.StorageStop()
		if err != nil {
			return err
		}
	}

	// Process forkremovefile response
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}

		// Extract errors
		if strings.HasPrefix(line, "error: ") {
			errStr = strings.TrimPrefix(line, "error: ")
			continue
		}

		if strings.HasPrefix(line, "errno: ") {
			errno := strings.TrimPrefix(line, "errno: ")
			if errno == "2" {
				return os.ErrNotExist
			}

			return fmt.Errorf(errStr)
		}
	}

	if err != nil {
		return fmt.Errorf(
			"Error calling 'lxd forkremovefile %s %d %s': err='%v'",
			c.RootfsPath(),
			c.InitPID(),
			path,
			err)
	}

	return nil
}

func (c *containerLXC) Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File, wait bool) (*exec.Cmd, int, int, error) {
	envSlice := []string{}

	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	args := []string{execPath, "forkexec", c.name, c.daemon.lxcpath, filepath.Join(c.LogPath(), "lxc.conf")}

	args = append(args, "--")
	args = append(args, "env")
	args = append(args, envSlice...)

	args = append(args, "--")
	args = append(args, "cmd")
	args = append(args, command...)

	cmd := exec.Cmd{}
	cmd.Path = execPath
	cmd.Args = args
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	r, w, err := shared.Pipe()
	defer r.Close()
	if err != nil {
		logger.Errorf("%s", err)
		return nil, -1, -1, err
	}

	cmd.ExtraFiles = []*os.File{w}
	err = cmd.Start()
	if err != nil {
		w.Close()
		return nil, -1, -1, err
	}
	w.Close()

	attachedPid := -1
	if err := json.NewDecoder(r).Decode(&attachedPid); err != nil {
		logger.Errorf("Failed to retrieve PID of executing child process: %s", err)
		return nil, -1, -1, err
	}

	// It's the callers responsibility to wait or not wait.
	if !wait {
		return &cmd, -1, attachedPid, nil
	}

	err = cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			if ok {
				return nil, status.ExitStatus(), attachedPid, nil
			}

			if status.Signaled() {
				// 128 + n == Fatal error signal "n"
				return nil, 128 + int(status.Signal()), attachedPid, nil
			}
		}

		return nil, -1, -1, err
	}

	return nil, 0, attachedPid, nil
}

func (c *containerLXC) diskState() map[string]api.ContainerStateDisk {
	disk := map[string]api.ContainerStateDisk{}

	for _, name := range c.expandedDevices.DeviceNames() {
		d := c.expandedDevices[name]
		if d["type"] != "disk" {
			continue
		}

		if d["path"] != "/" {
			continue
		}

		usage, err := c.storage.ContainerGetUsage(c)
		if err != nil {
			continue
		}

		disk[name] = api.ContainerStateDisk{Usage: usage}
	}

	return disk
}

func (c *containerLXC) memoryState() api.ContainerStateMemory {
	memory := api.ContainerStateMemory{}

	if !cgMemoryController {
		return memory
	}

	// Memory in bytes
	value, err := c.CGroupGet("memory.usage_in_bytes")
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		valueInt = -1
	}
	memory.Usage = valueInt

	// Memory peak in bytes
	value, err = c.CGroupGet("memory.max_usage_in_bytes")
	valueInt, err = strconv.ParseInt(value, 10, 64)
	if err != nil {
		valueInt = -1
	}

	memory.UsagePeak = valueInt

	if cgSwapAccounting {
		// Swap in bytes
		value, err := c.CGroupGet("memory.memsw.usage_in_bytes")
		valueInt, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			valueInt = -1
		}

		memory.SwapUsage = valueInt - memory.Usage

		// Swap peak in bytes
		value, err = c.CGroupGet("memory.memsw.max_usage_in_bytes")
		valueInt, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			valueInt = -1
		}

		memory.SwapUsagePeak = valueInt - memory.UsagePeak
	}

	return memory
}

func (c *containerLXC) networkState() map[string]api.ContainerStateNetwork {
	result := map[string]api.ContainerStateNetwork{}

	pid := c.InitPID()
	if pid < 1 {
		return result
	}

	// Get the network state from the container
	out, err := shared.RunCommand(
		execPath,
		"forkgetnet",
		fmt.Sprintf("%d", pid))

	// Process forkgetnet response
	if err != nil {
		logger.Error("Error calling 'lxd forkgetnet", log.Ctx{"container": c.name, "output": string(out), "pid": pid})
		return result
	}

	networks := map[string]api.ContainerStateNetwork{}

	err = json.Unmarshal([]byte(out), &networks)
	if err != nil {
		logger.Error("Failure to read forkgetnet json", log.Ctx{"container": c.name, "err": err})
		return result
	}

	// Add HostName field
	for netName, net := range networks {
		net.HostName = c.getHostInterface(netName)
		result[netName] = net
	}

	return result
}

func (c *containerLXC) processesState() int64 {
	// Return 0 if not running
	pid := c.InitPID()
	if pid == -1 {
		return 0
	}

	if cgPidsController {
		value, err := c.CGroupGet("pids.current")
		valueInt, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return -1
		}

		return valueInt
	}

	pids := []int64{int64(pid)}

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
			pid, err := strconv.ParseInt(content[j], 10, 64)
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	return int64(len(pids))
}

func (c *containerLXC) tarStoreFile(linkmap map[uint64]string, offset int, tw *tar.Writer, path string, fi os.FileInfo) error {
	var err error
	var major, minor, nlink int
	var ino uint64

	link := ""
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(path)
		if err != nil {
			return fmt.Errorf("failed to resolve symlink: %s", err)
		}
	}

	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return fmt.Errorf("failed to create tar info header: %s", err)
	}

	hdr.Name = path[offset:]
	if fi.IsDir() || fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		hdr.Size = 0
	} else {
		hdr.Size = fi.Size()
	}

	hdr.Uid, hdr.Gid, major, minor, ino, nlink, err = shared.GetFileStat(path)
	if err != nil {
		return fmt.Errorf("failed to get file stat: %s", err)
	}

	// Unshift the id under /rootfs/ for unpriv containers
	if !c.IsPrivileged() && strings.HasPrefix(hdr.Name, "/rootfs") {
		idmapset, err := c.IdmapSet()
		if err != nil {
			return err
		}

		huid, hgid := idmapset.ShiftFromNs(int64(hdr.Uid), int64(hdr.Gid))
		hdr.Uid = int(huid)
		hdr.Gid = int(hgid)
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

	// Handle xattrs (for real files only)
	if link == "" {
		hdr.Xattrs, err = shared.GetAllXattr(path)
		if err != nil {
			return fmt.Errorf("failed to read xattr: %s", err)
		}
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("failed to write tar header: %s", err)
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open the file: %s", err)
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("failed to copy file content: %s", err)
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

	return c.storage.ContainerStart(c.Name(), c.Path())
}

func (c *containerLXC) StorageStop() error {
	if c.IsSnapshot() {
		return c.storage.ContainerSnapshotStop(c)
	}

	return c.storage.ContainerStop(c.Name(), c.Path())
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

	out, err := shared.RunCommand(execPath, "forkmount", pidStr, mntsrc, target)

	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			logger.Debugf("forkmount: %s", line)
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
		return fmt.Errorf("Can't remove mount from stopped container")
	}

	// Remove the mount from the container
	pidStr := fmt.Sprintf("%d", pid)
	out, err := shared.RunCommand(execPath, "forkumount", pidStr, mount)

	if string(out) != "" {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			logger.Debugf("forkumount: %s", line)
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
func (c *containerLXC) createUnixDevice(m types.Device) (string, error) {
	var err error
	var major, minor int

	// Our device paths
	srcPath := m["path"]
	tgtPath := strings.TrimPrefix(srcPath, "/")
	devName := fmt.Sprintf("unix.%s", strings.Replace(tgtPath, "/", "-", -1))
	devPath := filepath.Join(c.DevicesPath(), devName)

	// Extra checks for nesting
	if runningInUserns {
		for key, value := range m {
			if shared.StringInSlice(key, []string{"major", "minor", "mode", "uid", "gid"}) && value != "" {
				return "", fmt.Errorf("The \"%s\" property may not be set when adding a device to a nested container", key)
			}
		}
	}

	// Get the major/minor of the device we want to create
	if m["major"] == "" && m["minor"] == "" {
		// If no major and minor are set, use those from the device on the host
		_, major, minor, err = deviceGetAttributes(srcPath)
		if err != nil {
			return "", fmt.Errorf("Failed to get device attributes for %s: %s", m["path"], err)
		}
	} else if m["major"] == "" || m["minor"] == "" {
		return "", fmt.Errorf("Both major and minor must be supplied for device: %s", m["path"])
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
		if runningInUserns {
			syscall.Unmount(devPath, syscall.MNT_DETACH)
		}

		err = os.Remove(devPath)
		if err != nil {
			return "", fmt.Errorf("Failed to remove existing entry: %s", err)
		}
	}

	// Create the new entry
	if !runningInUserns {
		encoded_device_number := (minor & 0xff) | (major << 8) | ((minor & ^0xff) << 12)
		if err := syscall.Mknod(devPath, uint32(mode), encoded_device_number); err != nil {
			return "", fmt.Errorf("Failed to create device %s for %s: %s", devPath, m["path"], err)
		}

		if err := os.Chown(devPath, uid, gid); err != nil {
			return "", fmt.Errorf("Failed to chown device %s: %s", devPath, err)
		}

		// Needed as mknod respects the umask
		if err := os.Chmod(devPath, mode); err != nil {
			return "", fmt.Errorf("Failed to chmod device %s: %s", devPath, err)
		}

		idmapset, err := c.IdmapSet()
		if err != nil {
			return "", err
		}

		if idmapset != nil {
			if err := idmapset.ShiftFile(devPath); err != nil {
				// uidshift failing is weird, but not a big problem.  Log and proceed
				logger.Debugf("Failed to uidshift device %s: %s\n", m["path"], err)
			}
		}
	} else {
		f, err := os.Create(devPath)
		if err != nil {
			return "", err
		}
		f.Close()

		err = deviceMountDisk(srcPath, devPath, false, false)
		if err != nil {
			return "", err
		}
	}

	return devPath, nil
}

func (c *containerLXC) insertUnixDevice(name string, m types.Device) error {
	// Check that the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't insert device into stopped container")
	}

	// Create the device on the host
	devPath, err := c.createUnixDevice(m)
	if err != nil {
		return fmt.Errorf("Failed to setup device: %s", err)
	}

	// Bind-mount it into the container
	tgtPath := strings.TrimSuffix(m["path"], "/")
	err = c.insertMount(devPath, tgtPath, "none", syscall.MS_BIND)
	if err != nil {
		return fmt.Errorf("Failed to add mount for device: %s", err)
	}

	// Check if we've been passed major and minor numbers already.
	var tmp int
	dMajor := -1
	if m["major"] != "" {
		tmp, err = strconv.Atoi(m["major"])
		if err == nil {
			dMajor = tmp
		}
	}

	dMinor := -1
	if m["minor"] != "" {
		tmp, err = strconv.Atoi(m["minor"])
		if err == nil {
			dMinor = tmp
		}
	}

	dType := ""
	if m["type"] == "unix-char" {
		dType = "c"
	} else if m["type"] == "unix-block" {
		dType = "b"
	}

	if dType == "" || dMajor < 0 || dMinor < 0 {
		dType, dMajor, dMinor, err = deviceGetAttributes(devPath)
		if err != nil {
			return err
		}
	}

	if c.IsPrivileged() && !runningInUserns && cgDevicesController {
		// Add the new device cgroup rule
		if err := c.CGroupSet("devices.allow", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor)); err != nil {
			return fmt.Errorf("Failed to add cgroup rule for device")
		}
	}

	return nil
}

func (c *containerLXC) removeUnixDevice(m types.Device) error {
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

	// Check if we've been passed major and minor numbers already.
	var tmp int
	var err error
	dMajor := -1
	if m["major"] != "" {
		tmp, err = strconv.Atoi(m["major"])
		if err == nil {
			dMajor = tmp
		}
	}

	dMinor := -1
	if m["minor"] != "" {
		tmp, err = strconv.Atoi(m["minor"])
		if err == nil {
			dMinor = tmp
		}
	}

	dType := ""
	if m["type"] != "" {
		dType = m["type"]
	}

	if dType == "" || dMajor < 0 || dMinor < 0 {
		dType, dMajor, dMinor, err = deviceGetAttributes(devPath)
		if err != nil {
			return err
		}
	}

	if c.IsPrivileged() && !runningInUserns && cgDevicesController {
		// Remove the device cgroup rule
		err = c.CGroupSet("devices.deny", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor))
		if err != nil {
			return err
		}
	}

	// Remove the bind-mount from the container
	if c.FileExists(tgtPath) == nil {
		err = c.removeMount(m["path"])
		if err != nil {
			return fmt.Errorf("Error unmounting the device: %s", err)
		}

		err = c.FileRemove(tgtPath)
		if err != nil {
			return fmt.Errorf("Error removing the device: %s", err)
		}
	}

	// Remove the host side
	if runningInUserns {
		syscall.Unmount(devPath, syscall.MNT_DETACH)
	}

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
		devicePath := filepath.Join(c.DevicesPath(), f.Name())
		err := os.Remove(devicePath)
		if err != nil {
			logger.Error("failed removing unix device", log.Ctx{"err": err, "path": devicePath})
		}
	}

	return nil
}

// Network device handling
func (c *containerLXC) createNetworkDevice(name string, m types.Device) (string, error) {
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

		_, err := shared.RunCommand("ip", "link", "add", n1, "type", "veth", "peer", "name", n2)
		if err != nil {
			return "", fmt.Errorf("Failed to create the veth interface: %s", err)
		}

		_, err = shared.RunCommand("ip", "link", "set", n1, "up")
		if err != nil {
			return "", fmt.Errorf("Failed to bring up the veth interface %s: %s", n1, err)
		}

		if m["nictype"] == "bridged" {
			if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", m["parent"])) {
				_, err = shared.RunCommand("ip", "link", "set", n1, "master", m["parent"])
				if err != nil {
					deviceRemoveInterface(n2)
					return "", fmt.Errorf("Failed to add interface to bridge: %s", err)
				}
			} else {
				_, err = shared.RunCommand("ovs-vsctl", "add-port", m["parent"], n1)
				if err != nil {
					deviceRemoveInterface(n2)
					return "", fmt.Errorf("Failed to add interface to bridge: %s", err)
				}
			}

			// Attempt to disable IPv6 on the host side interface
			if shared.PathExists(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", n1)) {
				ioutil.WriteFile(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", n1), []byte("1"), 0644)
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

		_, err := shared.RunCommand("ip", "link", "add", n1, "link", m["parent"], "type", "macvlan", "mode", "bridge")
		if err != nil {
			return "", fmt.Errorf("Failed to create the new macvlan interface: %s", err)
		}

		dev = n1
	}

	// Set the MAC address
	if m["hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", dev, "address", m["hwaddr"])
		if err != nil {
			deviceRemoveInterface(dev)
			return "", fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Bring the interface up
	_, err := shared.RunCommand("ip", "link", "set", "dev", dev, "up")
	if err != nil {
		deviceRemoveInterface(dev)
		return "", fmt.Errorf("Failed to bring up the interface: %s", err)
	}

	return dev, nil
}

func (c *containerLXC) fillNetworkDevice(name string, m types.Device) (types.Device, error) {
	newDevice := types.Device{}
	err := shared.DeepCopy(&m, &newDevice)
	if err != nil {
		return nil, err
	}

	// Function to try and guess an available name
	nextInterfaceName := func() (string, error) {
		devNames := []string{}

		// Include all static interface names
		for _, k := range c.expandedDevices.DeviceNames() {
			v := c.expandedDevices[k]
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

	updateKey := func(key string, value string) error {
		tx, err := dbBegin(c.daemon.db)
		if err != nil {
			return err
		}

		err = dbContainerConfigInsert(tx, c.id, map[string]string{key: value})
		if err != nil {
			tx.Rollback()
			return err
		}

		err = txCommit(tx)
		if err != nil {
			return err
		}

		return nil
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

			// Update the database
			err = updateKey(configKey, volatileHwaddr)
			if err != nil {
				// Check if something else filled it in behind our back
				value, err1 := dbContainerConfigGet(c.daemon.db, c.id, configKey)
				if err1 != nil || value == "" {
					return nil, err
				}

				c.localConfig[configKey] = value
				c.expandedConfig[configKey] = value
			} else {
				c.localConfig[configKey] = volatileHwaddr
				c.expandedConfig[configKey] = volatileHwaddr
			}
		}
		newDevice["hwaddr"] = volatileHwaddr
	}

	// Fill in the name
	if m["name"] == "" {
		configKey := fmt.Sprintf("volatile.%s.name", name)
		volatileName := c.localConfig[configKey]
		if volatileName == "" {
			// Generate a new interface name
			volatileName, err = nextInterfaceName()
			if err != nil {
				return nil, err
			}

			// Update the database
			err = updateKey(configKey, volatileName)
			if err != nil {
				// Check if something else filled it in behind our back
				value, err1 := dbContainerConfigGet(c.daemon.db, c.id, configKey)
				if err1 != nil || value == "" {
					return nil, err
				}

				c.localConfig[configKey] = value
				c.expandedConfig[configKey] = value
			} else {
				c.localConfig[configKey] = volatileName
				c.expandedConfig[configKey] = volatileName
			}
		}
		newDevice["name"] = volatileName
	}

	// Fill in the host name (but don't generate a static one ourselves)
	if m["host_name"] == "" && shared.StringInSlice(m["nictype"], []string{"bridged", "p2p"}) {
		configKey := fmt.Sprintf("volatile.%s.host_name", name)
		newDevice["host_name"] = c.localConfig[configKey]
	}

	return newDevice, nil
}

func (c *containerLXC) insertNetworkDevice(name string, m types.Device) error {
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

	if m["parent"] != "" && !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", m["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", m["parent"])
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

func (c *containerLXC) removeNetworkDevice(name string, m types.Device) error {
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
func (c *containerLXC) createDiskDevice(name string, m types.Device) (string, error) {
	// Prepare all the paths
	srcPath := m["source"]
	tgtPath := strings.TrimPrefix(m["path"], "/")
	devName := fmt.Sprintf("disk.%s", strings.Replace(tgtPath, "/", "-", -1))
	devPath := filepath.Join(c.DevicesPath(), devName)

	// Check if read-only
	isOptional := shared.IsTrue(m["optional"])
	isReadOnly := shared.IsTrue(m["readonly"])
	isRecursive := shared.IsTrue(m["recursive"])
	isFile := !shared.IsDir(srcPath) && !deviceIsBlockdev(srcPath)

	// Check if the source exists
	if !shared.PathExists(srcPath) {
		if isOptional {
			return "", nil
		}
		return "", fmt.Errorf("Source path %s doesn't exist for device %s", srcPath, name)
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
	err := deviceMountDisk(srcPath, devPath, isReadOnly, isRecursive)
	if err != nil {
		return "", err
	}

	return devPath, nil
}

func (c *containerLXC) insertDiskDevice(name string, m types.Device) error {
	// Check that the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't insert device into stopped container")
	}

	isRecursive := shared.IsTrue(m["recursive"])

	// Create the device on the host
	devPath, err := c.createDiskDevice(name, m)
	if err != nil {
		return fmt.Errorf("Failed to setup device: %s", err)
	}

	flags := syscall.MS_BIND
	if isRecursive {
		flags |= syscall.MS_REC
	}

	// Bind-mount it into the container
	tgtPath := strings.TrimSuffix(m["path"], "/")
	err = c.insertMount(devPath, tgtPath, "none", flags)
	if err != nil {
		return fmt.Errorf("Failed to add mount for device: %s", err)
	}

	return nil
}

type byPath []types.Device

func (a byPath) Len() int {
	return len(a)
}

func (a byPath) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byPath) Less(i, j int) bool {
	return a[i]["path"] < a[j]["path"]
}

func (c *containerLXC) addDiskDevices(devices map[string]types.Device, handler func(string, types.Device) error) error {
	ordered := byPath{}

	for _, d := range devices {
		ordered = append(ordered, d)
	}

	sort.Sort(ordered)
	for _, d := range ordered {
		err := handler(d["path"], d)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *containerLXC) removeDiskDevice(name string, m types.Device) error {
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
	if c.FileExists(tgtPath) == nil {
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
		diskPath := filepath.Join(c.DevicesPath(), f.Name())
		err := os.Remove(diskPath)
		if err != nil {
			logger.Error("Failed to remove disk device path", log.Ctx{"err": err, "path": diskPath})
		}
	}

	return nil
}

// Block I/O limits
func (c *containerLXC) getDiskLimits() (map[string]deviceBlockLimit, error) {
	result := map[string]deviceBlockLimit{}

	// Build a list of all valid block devices
	validBlocks := []string{}

	dents, err := ioutil.ReadDir("/sys/class/block/")
	if err != nil {
		return nil, err
	}

	for _, f := range dents {
		fPath := filepath.Join("/sys/class/block/", f.Name())
		if shared.PathExists(fmt.Sprintf("%s/partition", fPath)) {
			continue
		}

		if !shared.PathExists(fmt.Sprintf("%s/dev", fPath)) {
			continue
		}

		block, err := ioutil.ReadFile(fmt.Sprintf("%s/dev", fPath))
		if err != nil {
			return nil, err
		}

		validBlocks = append(validBlocks, strings.TrimSuffix(string(block), "\n"))
	}

	// Process all the limits
	blockLimits := map[string][]deviceBlockLimit{}
	for _, k := range c.expandedDevices.DeviceNames() {
		m := c.expandedDevices[k]
		if m["type"] != "disk" {
			continue
		}

		// Apply max limit
		if m["limits.max"] != "" {
			m["limits.read"] = m["limits.max"]
			m["limits.write"] = m["limits.max"]
		}

		// Parse the user input
		readBps, readIops, writeBps, writeIops, err := deviceParseDiskLimit(m["limits.read"], m["limits.write"])
		if err != nil {
			return nil, err
		}

		// Set the source path
		source := m["source"]
		if source == "" {
			source = c.RootfsPath()
		}

		// Don't try to resolve the block device behind a non-existing path
		if !shared.PathExists(source) {
			continue
		}

		// Get the backing block devices (major:minor)
		blocks, err := deviceGetParentBlocks(source)
		if err != nil {
			if readBps == 0 && readIops == 0 && writeBps == 0 && writeIops == 0 {
				// If the device doesn't exist, there is no limit to clear so ignore the failure
				continue
			} else {
				return nil, err
			}
		}

		device := deviceBlockLimit{readBps: readBps, readIops: readIops, writeBps: writeBps, writeIops: writeIops}
		for _, block := range blocks {
			blockStr := ""

			if shared.StringInSlice(block, validBlocks) {
				// Straightforward entry (full block device)
				blockStr = block
			} else {
				// Attempt to deal with a partition (guess its parent)
				fields := strings.SplitN(block, ":", 2)
				fields[1] = "0"
				if shared.StringInSlice(fmt.Sprintf("%s:%s", fields[0], fields[1]), validBlocks) {
					blockStr = fmt.Sprintf("%s:%s", fields[0], fields[1])
				}
			}

			if blockStr == "" {
				return nil, fmt.Errorf("Block device doesn't support quotas: %s", block)
			}

			if blockLimits[blockStr] == nil {
				blockLimits[blockStr] = []deviceBlockLimit{}
			}
			blockLimits[blockStr] = append(blockLimits[blockStr], device)
		}
	}

	// Average duplicate limits
	for block, limits := range blockLimits {
		var readBpsCount, readBpsTotal, readIopsCount, readIopsTotal, writeBpsCount, writeBpsTotal, writeIopsCount, writeIopsTotal int64

		for _, limit := range limits {
			if limit.readBps > 0 {
				readBpsCount += 1
				readBpsTotal += limit.readBps
			}

			if limit.readIops > 0 {
				readIopsCount += 1
				readIopsTotal += limit.readIops
			}

			if limit.writeBps > 0 {
				writeBpsCount += 1
				writeBpsTotal += limit.writeBps
			}

			if limit.writeIops > 0 {
				writeIopsCount += 1
				writeIopsTotal += limit.writeIops
			}
		}

		device := deviceBlockLimit{}

		if readBpsCount > 0 {
			device.readBps = readBpsTotal / readBpsCount
		}

		if readIopsCount > 0 {
			device.readIops = readIopsTotal / readIopsCount
		}

		if writeBpsCount > 0 {
			device.writeBps = writeBpsTotal / writeBpsCount
		}

		if writeIopsCount > 0 {
			device.writeIops = writeIopsTotal / writeIopsCount
		}

		result[block] = device
	}

	return result, nil
}

// Network I/O limits
func (c *containerLXC) setNetworkPriority() error {
	// Check that the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't set network priority on stopped container")
	}

	// Don't bother if the cgroup controller doesn't exist
	if !cgNetPrioController {
		return nil
	}

	// Extract the current priority
	networkPriority := c.expandedConfig["limits.network.priority"]
	if networkPriority == "" {
		networkPriority = "0"
	}

	networkInt, err := strconv.Atoi(networkPriority)
	if err != nil {
		return err
	}

	// Get all the interfaces
	netifs, err := net.Interfaces()
	if err != nil {
		return err
	}

	// Check that we at least succeeded to set an entry
	success := false
	var last_error error
	for _, netif := range netifs {
		err = c.CGroupSet("net_prio.ifpriomap", fmt.Sprintf("%s %d", netif.Name, networkInt))
		if err == nil {
			success = true
		} else {
			last_error = err
		}
	}

	if !success {
		return fmt.Errorf("Failed to set network device priority: %s", last_error)
	}

	return nil
}

func (c *containerLXC) getHostInterface(name string) string {
	if c.IsRunning() {
		for i := 0; i < len(c.c.ConfigItem("lxc.network")); i++ {
			nicName := c.c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.name", i))[0]
			if nicName != name {
				continue
			}

			veth := c.c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.veth.pair", i))[0]
			if veth != "" {
				return veth
			}
		}
	}

	for _, k := range c.expandedDevices.DeviceNames() {
		dev := c.expandedDevices[k]
		if dev["type"] != "nic" {
			continue
		}

		m, err := c.fillNetworkDevice(k, dev)
		if err != nil {
			m = dev
		}

		if m["name"] != name {
			continue
		}

		return m["host_name"]
	}

	return ""
}

func (c *containerLXC) setNetworkLimits(name string, m types.Device) error {
	// We can only do limits on some network type
	if m["nictype"] != "bridged" && m["nictype"] != "p2p" {
		return fmt.Errorf("Network limits are only supported on bridged and p2p interfaces")
	}

	// Load the go-lxc struct
	err := c.initLXC()
	if err != nil {
		return err
	}

	// Check that the container is running
	if !c.IsRunning() {
		return fmt.Errorf("Can't set network limits on stopped container")
	}

	// Fill in some fields from volatile
	m, err = c.fillNetworkDevice(name, m)
	if err != nil {
		return nil
	}

	// Look for the host side interface name
	veth := c.getHostInterface(m["name"])

	if veth == "" {
		return fmt.Errorf("LXC doesn't now about this device and the host_name property isn't set, can't find host side veth name")
	}

	// Apply max limit
	if m["limits.max"] != "" {
		m["limits.ingress"] = m["limits.max"]
		m["limits.egress"] = m["limits.max"]
	}

	// Parse the values
	var ingressInt int64
	if m["limits.ingress"] != "" {
		ingressInt, err = shared.ParseBitSizeString(m["limits.ingress"])
		if err != nil {
			return err
		}
	}

	var egressInt int64
	if m["limits.egress"] != "" {
		egressInt, err = shared.ParseBitSizeString(m["limits.egress"])
		if err != nil {
			return err
		}
	}

	// Clean any existing entry
	shared.RunCommand("tc", "qdisc", "del", "dev", veth, "root")
	shared.RunCommand("tc", "qdisc", "del", "dev", veth, "ingress")

	// Apply new limits
	if m["limits.ingress"] != "" {
		out, err := shared.RunCommand("tc", "qdisc", "add", "dev", veth, "root", "handle", "1:0", "htb", "default", "10")
		if err != nil {
			return fmt.Errorf("Failed to create root tc qdisc: %s", out)
		}

		out, err = shared.RunCommand("tc", "class", "add", "dev", veth, "parent", "1:0", "classid", "1:10", "htb", "rate", fmt.Sprintf("%dbit", ingressInt))
		if err != nil {
			return fmt.Errorf("Failed to create limit tc class: %s", out)
		}

		out, err = shared.RunCommand("tc", "filter", "add", "dev", veth, "parent", "1:0", "protocol", "all", "u32", "match", "u32", "0", "0", "flowid", "1:1")
		if err != nil {
			return fmt.Errorf("Failed to create tc filter: %s", out)
		}
	}

	if m["limits.egress"] != "" {
		out, err := shared.RunCommand("tc", "qdisc", "add", "dev", veth, "handle", "ffff:0", "ingress")
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", out)
		}

		out, err = shared.RunCommand("tc", "filter", "add", "dev", veth, "parent", "ffff:0", "protocol", "all", "u32", "match", "u32", "0", "0", "police", "rate", fmt.Sprintf("%dbit", egressInt), "burst", "1024k", "mtu", "64kb", "drop", "flowid", ":1")
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", out)
		}
	}

	return nil
}

// Various state query functions
func (c *containerLXC) IsStateful() bool {
	return c.stateful
}

func (c *containerLXC) IsEphemeral() bool {
	return c.ephemeral
}

func (c *containerLXC) IsFrozen() bool {
	return c.State() == "FROZEN"
}

func (c *containerLXC) IsNesting() bool {
	return shared.IsTrue(c.expandedConfig["security.nesting"])
}

func (c *containerLXC) IsPrivileged() bool {
	return shared.IsTrue(c.expandedConfig["security.privileged"])
}

func (c *containerLXC) IsRunning() bool {
	state := c.State()
	return state != "BROKEN" && state != "STOPPED"
}

func (c *containerLXC) IsSnapshot() bool {
	return c.cType == cTypeSnapshot
}

// Various property query functions
func (c *containerLXC) Architecture() int {
	return c.architecture
}

func (c *containerLXC) CreationDate() time.Time {
	return c.creationDate
}
func (c *containerLXC) ExpandedConfig() map[string]string {
	return c.expandedConfig
}

func (c *containerLXC) ExpandedDevices() types.Devices {
	return c.expandedDevices
}

func (c *containerLXC) Id() int {
	return c.id
}

func (c *containerLXC) IdmapSet() (*shared.IdmapSet, error) {
	var err error

	if c.idmapset != nil {
		return c.idmapset, nil
	}

	if c.IsPrivileged() {
		return nil, nil
	}

	c.idmapset, err = c.NextIdmapSet()
	if err != nil {
		return nil, err
	}

	return c.idmapset, nil
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

func (c *containerLXC) LocalDevices() types.Devices {
	return c.localDevices
}

func (c *containerLXC) idmapsetFromConfig(k string) (*shared.IdmapSet, error) {
	lastJsonIdmap := c.LocalConfig()[k]

	if lastJsonIdmap == "" {
		return c.IdmapSet()
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

func (c *containerLXC) NextIdmapSet() (*shared.IdmapSet, error) {
	if c.localConfig["volatile.idmap.next"] != "" {
		return c.idmapsetFromConfig("volatile.idmap.next")
	} else if c.IsPrivileged() {
		return nil, nil
	} else if c.daemon.IdmapSet != nil {
		return c.daemon.IdmapSet, nil
	}

	return nil, fmt.Errorf("Unable to determine the idmap")
}

func (c *containerLXC) LastIdmapSet() (*shared.IdmapSet, error) {
	return c.idmapsetFromConfig("volatile.last_state.idmap")
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
		return "Broken"
	}

	state, err := c.getLxcState()
	if err != nil {
		return api.Error.String()
	}
	return state.String()
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
	/* FIXME: backwards compatibility: we used to use Join(RootfsPath(),
	 * "state"), which was bad. Let's just check to see if that directory
	 * exists.
	 */
	oldStatePath := filepath.Join(c.RootfsPath(), "state")
	if shared.IsDir(oldStatePath) {
		return oldStatePath
	}
	return filepath.Join(c.Path(), "state")
}
