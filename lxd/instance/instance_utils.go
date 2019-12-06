package instance

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/seccomp"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

// ValidDevices is linked from main.instanceValidDevices to validate device config. Currently
// main.instanceValidDevices uses containerLXC internally and so cannot be moved from main package.
var ValidDevices func(state *state.State, cluster *db.Cluster, instanceType instancetype.Type, instanceName string, devices deviceConfig.Devices, expanded bool) error

// Load is linked from main.instanceLoad to allow different instance types to be load,
// including containerLXC which currently cannot be moved from main package.
var Load func(s *state.State, args db.InstanceArgs, profiles []api.Profile) (Instance, error)

// NetworkGetLeaseAddresses is linked from main.networkGetLeaseAddresses to limit scope of moving
// network related functions into their own package at this time.
var NetworkGetLeaseAddresses func(s *state.State, network string, hwaddr string) ([]api.InstanceStateNetworkAddress, error)

// CompareSnapshots returns a list of snapshots to sync to the target and a list of
// snapshots to remove from the target. A snapshot will be marked as "to sync" if it either doesn't
// exist in the target or its creation date is different to the source. A snapshot will be marked
// as "to delete" if it doesn't exist in the source or creation date is different to the source.
func CompareSnapshots(source Instance, target Instance) ([]Instance, []Instance, error) {
	// Get the source snapshots.
	sourceSnapshots, err := source.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Get the target snapshots.
	targetSnapshots, err := target.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Compare source and target.
	sourceSnapshotsTime := map[string]time.Time{}
	targetSnapshotsTime := map[string]time.Time{}

	toDelete := []Instance{}
	toSync := []Instance{}

	// Generate a list of source snapshot creation dates.
	for _, snap := range sourceSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		sourceSnapshotsTime[snapName] = snap.CreationDate()
	}

	// Generate a list of target snapshot creation times, if the source doesn't contain the
	// the snapshot or the creation time is different on the source then add the target snapshot
	// to the "to delete" list.
	for _, snap := range targetSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		targetSnapshotsTime[snapName] = snap.CreationDate()
		existDate, exists := sourceSnapshotsTime[snapName]
		if !exists {
			// Snapshot doesn't exist in source, mark it for deletion on target.
			toDelete = append(toDelete, snap)
		} else if existDate != snap.CreationDate() {
			// Snapshot creation date is different in source, mark it for deletion on
			// target.
			toDelete = append(toDelete, snap)
		}
	}

	// For each of the source snapshots, decide whether it needs to be synced or not based on
	// whether it already exists in the target and whether the creation dates match.
	for _, snap := range sourceSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		existDate, exists := targetSnapshotsTime[snapName]
		if !exists || existDate != snap.CreationDate() {
			toSync = append(toSync, snap)
		}
	}

	return toSync, toDelete, nil
}

// ValidConfig validates an instance's config.
func ValidConfig(sysOS *sys.OS, config map[string]string, profile bool, expanded bool) error {
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

		err := validConfigKey(sysOS, k, v)
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

	_, err := seccomp.SyscallInterceptMountFilter(config)
	if err != nil {
		return err
	}

	if expanded && (config["security.privileged"] == "" || !shared.IsTrue(config["security.privileged"])) && sysOS.IdmapSet == nil {
		return fmt.Errorf("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported")
	}

	unprivOnly := os.Getenv("LXD_UNPRIVILEGED_ONLY")
	if shared.IsTrue(unprivOnly) {
		if config["raw.idmap"] != "" {
			err := AllowedUnprivilegedOnlyMap(config["raw.idmap"])
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

func validConfigKey(os *sys.OS, key string, value string) error {
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

func lxcParseRawLXC(line string) (string, string, error) {
	// Ignore empty lines
	if len(line) == 0 {
		return "", "", nil
	}

	// Skip whitespace {"\t", " "}
	line = strings.TrimLeft(line, "\t ")

	// Ignore comments
	if strings.HasPrefix(line, "#") {
		return "", "", nil
	}

	// Ensure the format is valid
	membs := strings.SplitN(line, "=", 2)
	if len(membs) != 2 {
		return "", "", fmt.Errorf("Invalid raw.lxc line: %s", line)
	}

	key := strings.ToLower(strings.Trim(membs[0], " \t"))
	val := strings.Trim(membs[1], " \t")
	return key, val, nil
}

func lxcValidConfig(rawLxc string) error {
	for _, line := range strings.Split(rawLxc, "\n") {
		key, _, err := lxcParseRawLXC(line)
		if err != nil {
			return err
		}

		if key == "" {
			continue
		}

		unprivOnly := os.Getenv("LXD_UNPRIVILEGED_ONLY")
		if shared.IsTrue(unprivOnly) {
			if key == "lxc.idmap" || key == "lxc.id_map" || key == "lxc.include" {
				return fmt.Errorf("%s can't be set in raw.lxc as LXD was configured to only allow unprivileged containers", key)
			}
		}

		// Blacklist some keys
		if key == "lxc.logfile" || key == "lxc.log.file" {
			return fmt.Errorf("Setting lxc.logfile is not allowed")
		}

		if key == "lxc.syslog" || key == "lxc.log.syslog" {
			return fmt.Errorf("Setting lxc.log.syslog is not allowed")
		}

		if key == "lxc.ephemeral" {
			return fmt.Errorf("Setting lxc.ephemeral is not allowed")
		}

		if strings.HasPrefix(key, "lxc.prlimit.") {
			return fmt.Errorf(`Process limits should be set via ` +
				`"limits.kernel.[limit name]" and not ` +
				`directly via "lxc.prlimit.[limit name]"`)
		}

		networkKeyPrefix := "lxc.net."
		if !util.RuntimeLiblxcVersionAtLeast(2, 1, 0) {
			networkKeyPrefix = "lxc.network."
		}

		if strings.HasPrefix(key, networkKeyPrefix) {
			fields := strings.Split(key, ".")

			if !util.RuntimeLiblxcVersionAtLeast(2, 1, 0) {
				// lxc.network.X.ipv4 or lxc.network.X.ipv6
				if len(fields) == 4 && shared.StringInSlice(fields[3], []string{"ipv4", "ipv6"}) {
					continue
				}

				// lxc.network.X.ipv4.gateway or lxc.network.X.ipv6.gateway
				if len(fields) == 5 && shared.StringInSlice(fields[3], []string{"ipv4", "ipv6"}) && fields[4] == "gateway" {
					continue
				}
			} else {
				// lxc.net.X.ipv4.address or lxc.net.X.ipv6.address
				if len(fields) == 5 && shared.StringInSlice(fields[3], []string{"ipv4", "ipv6"}) && fields[4] == "address" {
					continue
				}

				// lxc.net.X.ipv4.gateway or lxc.net.X.ipv6.gateway
				if len(fields) == 5 && shared.StringInSlice(fields[3], []string{"ipv4", "ipv6"}) && fields[4] == "gateway" {
					continue
				}
			}

			return fmt.Errorf("Only interface-specific ipv4/ipv6 %s keys are allowed", networkKeyPrefix)
		}
	}

	return nil
}

// AllowedUnprivilegedOnlyMap checks that root user is not mapped into instance.
func AllowedUnprivilegedOnlyMap(rawIdmap string) error {
	rawMaps, err := ParseRawIdmap(rawIdmap)
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

// ParseRawIdmap parses an IDMAP string.
func ParseRawIdmap(value string) ([]idmap.IdmapEntry, error) {
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
			size++
		}

		return base, size, nil
	}

	ret := idmap.IdmapSet{}

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

		entry := idmap.IdmapEntry{
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

// LoadByID loads an instance by ID.
func LoadByID(s *state.State, id int) (Instance, error) {
	// Get the DB record
	project, name, err := s.Cluster.ContainerProjectAndName(id)
	if err != nil {
		return nil, err
	}

	return LoadByProjectAndName(s, project, name)
}

// LoadByProjectAndName loads an instance by project and name.
func LoadByProjectAndName(s *state.State, project, name string) (Instance, error) {
	// Get the DB record
	var container *db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		if strings.Contains(name, shared.SnapshotDelimiter) {
			parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]

			instance, err := tx.InstanceGet(project, instanceName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
			}

			snapshot, err := tx.InstanceSnapshotGet(project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch snapshot %q of instance %q in project %q", snapshotName, instanceName, project)
			}

			c := db.InstanceSnapshotToInstance(instance, snapshot)
			container = &c
		} else {
			container, err = tx.InstanceGet(project, name)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch container %q in project %q", name, project)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	args := db.ContainerToArgs(container)
	inst, err := Load(s, args, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load container")
	}

	return inst, nil
}

// WriteBackupFile writes instance's config to a file.
func WriteBackupFile(state *state.State, inst Instance) error {
	// We only write backup files out for actual instances.
	if inst.IsSnapshot() {
		return nil
	}

	// Immediately return if the instance directory doesn't exist yet.
	if !shared.PathExists(inst.Path()) {
		return os.ErrNotExist
	}

	// Generate the YAML.
	ci, _, err := inst.Render()
	if err != nil {
		return errors.Wrap(err, "Failed to render instance metadata")
	}

	snapshots, err := inst.Snapshots()
	if err != nil {
		return errors.Wrap(err, "Failed to get snapshots")
	}

	var sis []*api.InstanceSnapshot

	for _, s := range snapshots {
		si, _, err := s.Render()
		if err != nil {
			return err
		}

		sis = append(sis, si.(*api.InstanceSnapshot))
	}

	poolName, err := inst.StoragePool()
	if err != nil {
		return err
	}

	poolID, pool, err := state.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	dbType := db.StoragePoolVolumeTypeContainer
	if inst.Type() == instancetype.VM {
		dbType = db.StoragePoolVolumeTypeVM
	}

	_, volume, err := state.Cluster.StoragePoolNodeVolumeGetTypeByProject(inst.Project(), inst.Name(), dbType, poolID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&backup.InstanceConfig{
		Container: ci.(*api.Instance),
		Snapshots: sis,
		Pool:      pool,
		Volume:    volume,
	})
	if err != nil {
		return err
	}

	// Ensure the container is currently mounted.
	if !shared.PathExists(inst.RootfsPath()) {
		logger.Debug("Unable to update backup.yaml at this time", log.Ctx{"name": inst.Name(), "project": inst.Project()})
		return nil
	}

	// Write the YAML
	f, err := os.Create(filepath.Join(inst.Path(), "backup.yaml"))
	if err != nil {
		return err
	}
	defer f.Close()

	err = f.Chmod(0400)
	if err != nil {
		return err
	}

	err = shared.WriteAll(f, data)
	if err != nil {
		return err
	}

	return nil
}

// DeleteSnapshots calls the Delete() function on each of the supplied instance's snapshots.
func DeleteSnapshots(s *state.State, projectName, instanceName string) error {
	results, err := s.Cluster.ContainerGetSnapshots(projectName, instanceName)
	if err != nil {
		return err
	}

	for _, snapName := range results {
		snapInst, err := LoadByProjectAndName(s, projectName, snapName)
		if err != nil {
			logger.Error("DeleteSnapshots: Failed to load the snapshot", log.Ctx{"project": projectName, "instance": instanceName, "snapshot": snapName, "err": err})
			continue
		}

		if err := snapInst.Delete(); err != nil {
			logger.Error("DeleteSnapshots: Failed to delete the snapshot", log.Ctx{"project": projectName, "instance": instanceName, "snapshot": snapName, "err": err})
		}
	}

	return nil
}

// DeviceNextInterfaceHWAddr generates a random MAC address.
func DeviceNextInterfaceHWAddr() (string, error) {
	// Generate a new random MAC address using the usual prefix
	ret := bytes.Buffer{}
	for _, c := range "00:16:3e:xx:xx:xx" {
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

// BackupLoadByName load an instance backup from the database.
func BackupLoadByName(s *state.State, project, name string) (*backup.Backup, error) {
	// Get the backup database record
	args, err := s.Cluster.ContainerGetBackup(project, name)
	if err != nil {
		return nil, errors.Wrap(err, "Load backup from database")
	}

	// Load the instance it belongs to
	instance, err := LoadByID(s, args.InstanceID)
	if err != nil {
		return nil, errors.Wrap(err, "Load container from database")
	}

	return backup.New(s, instance, args.ID, name, args.CreationDate, args.ExpiryDate, args.InstanceOnly, args.OptimizedStorage), nil
}
