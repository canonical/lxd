package instance

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/seccomp"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"
)

// ValidDevices is linked from instance/drivers.validDevices to validate device config.
var ValidDevices func(state *state.State, cluster *db.Cluster, projectName string, instanceType instancetype.Type, devices deviceConfig.Devices, expanded bool) error

// Load is linked from instance/drivers.load to allow different instance types to be loaded.
var Load func(s *state.State, args db.InstanceArgs, profiles []api.Profile) (Instance, error)

// Create is linked from instance/drivers.create to allow difference instance types to be created.
// Accepts a reverter that revert steps this function does will be added to. It is up to the caller to call the
// revert's Fail() or Success() function as needed.
var Create func(s *state.State, args db.InstanceArgs, volumeConfig map[string]string, revert *revert.Reverter) (Instance, error)

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

func exclusiveConfigKeys(key1 string, key2 string, config map[string]string) (val string, ok bool, err error) {
	if config[key1] != "" && config[key2] != "" {
		return "", false, fmt.Errorf("Mutually exclusive keys %s and %s are set", key1, key2)
	}

	val, ok = config[key1]
	if ok {
		return
	}

	val, ok = config[key2]
	if ok {
		return
	}

	return "", false, nil
}

// ValidConfig validates an instance's config.
func ValidConfig(sysOS *sys.OS, config map[string]string, expanded bool, instanceType instancetype.Type) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if instanceType == instancetype.Any && !expanded && strings.HasPrefix(k, shared.ConfigVolatilePrefix) {
			return fmt.Errorf("Volatile keys can only be set on instances")
		}

		if instanceType == instancetype.Any && !expanded && strings.HasPrefix(k, "image.") {
			return fmt.Errorf("Image keys can only be set on instances")
		}

		err := validConfigKey(sysOS, k, v, instanceType)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, isAllow, err := exclusiveConfigKeys("security.syscalls.allow", "security.syscalls.whitelist", config)
	if err != nil {
		return err
	}

	_, isDeny, err := exclusiveConfigKeys("security.syscalls.deny", "security.syscalls.blacklist", config)
	if err != nil {
		return err
	}

	val, _, err := exclusiveConfigKeys("security.syscalls.deny_default", "security.syscalls.blacklist_default", config)
	if err != nil {
		return err
	}
	isDenyDefault := shared.IsTrue(val)

	val, _, err = exclusiveConfigKeys("security.syscalls.deny_compat", "security.syscalls.blacklist_compat", config)
	if err != nil {
		return err
	}
	isDenyCompat := shared.IsTrue(val)

	if rawSeccomp && (isAllow || isDeny || isDenyDefault || isDenyCompat) {
		return fmt.Errorf("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if isAllow && (isDeny || isDenyDefault || isDenyCompat) {
		return fmt.Errorf("security.syscalls.allow is mutually exclusive with security.syscalls.deny*")
	}

	_, err = seccomp.SyscallInterceptMountFilter(config)
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

	if shared.IsTrue(config["security.privileged"]) && shared.IsTrue(config["nvidia.runtime"]) {
		return fmt.Errorf("nvidia.runtime is incompatible with privileged containers")
	}

	return nil
}

func validConfigKey(os *sys.OS, key string, value string, instanceType instancetype.Type) error {
	f, err := shared.ConfigKeyChecker(key, instanceType)
	if err != nil {
		return err
	}
	if err = f(value); err != nil {
		return err
	}
	if key == "raw.lxc" {
		return lxcValidConfig(value)
	}
	if key == "security.syscalls.deny_compat" || key == "security.syscalls.blacklist_compat" {
		for _, arch := range os.Architectures {
			if arch == osarch.ARCH_64BIT_INTEL_X86 ||
				arch == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN ||
				arch == osarch.ARCH_64BIT_POWERPC_BIG_ENDIAN {
				return nil
			}
		}
		return fmt.Errorf("%s isn't supported on this architecture", key)
	}
	return nil
}

func lxcParseRawLXC(line string) (string, string, error) {
	// Ignore empty lines
	if len(line) == 0 {
		return "", "", nil
	}

	// Skip space {"\t", " "}
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

		// block some keys
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
	rawMaps, err := idmap.ParseRawIdmap(rawIdmap)
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

// LoadByID loads an instance by ID.
func LoadByID(s *state.State, id int) (Instance, error) {
	// Get the DB record
	project, name, err := s.Cluster.GetInstanceProjectAndName(id)
	if err != nil {
		return nil, err
	}

	return LoadByProjectAndName(s, project, name)
}

// Convenience to load a db.Instance object, accounting for snapshots.
func fetchInstanceDatabaseObject(s *state.State, project, name string) (*db.Instance, error) {
	var container *db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		container, err = LoadInstanceDatabaseObject(tx, project, name)
		return err
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

// LoadInstanceDatabaseObject loads a db.Instance object, accounting for snapshots.
func LoadInstanceDatabaseObject(tx *db.ClusterTx, project, name string) (*db.Instance, error) {
	var container *db.Instance
	var err error

	if strings.Contains(name, shared.SnapshotDelimiter) {
		parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		instanceName := parts[0]
		snapshotName := parts[1]

		instance, err := tx.GetInstance(project, instanceName)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
		}

		snapshot, err := tx.GetInstanceSnapshot(project, instanceName, snapshotName)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to fetch snapshot %q of instance %q in project %q", snapshotName, instanceName, project)
		}

		c := snapshot.ToInstance(instance)
		container = &c
	} else {
		container, err = tx.GetInstance(project, name)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
		}
	}

	return container, nil
}

// LoadByProjectAndName loads an instance by project and name.
func LoadByProjectAndName(s *state.State, project, name string) (Instance, error) {
	// Get the DB record

	var instance *db.InstanceArgs
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		if strings.Contains(name, shared.SnapshotDelimiter) {
			parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]

			inst, err := tx.GetInstance(project, instanceName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
			}

			snapshot, err := tx.GetInstanceSnapshot(project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch snapshot %q of instance %q in project %q", snapshotName, instanceName, project)
			}

			instance, err = snapshot.ToInstanceArgs(tx, inst)
			if err != nil {
				return fmt.Errorf("Failed to get snapshot instance data: %w", err)
			}

			return nil
		}

		inst, err := tx.GetInstance(project, name)
		if err != nil {
			return errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
		}

		apiInst, _, err := inst.ToAPI(tx)
		if err != nil {
			return err
		}

		instance, err = db.InstanceToArgs(*inst, *apiInst)
		return err
	})
	if err != nil {
		return nil, err
	}

	inst, err := Load(s, *instance, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load instance")
	}

	return inst, nil
}

// LoadAllInternal loads a list of db instances into a list of instances.
func LoadAllInternal(s *state.State, args db.InstanceArgs, profiles []api.Profile) (Instance, error) {
	instance, err := Load(s, args, profiles)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// LoadByProject loads all instances in a project.
func LoadByProject(s *state.State, project string) ([]Instance, error) {
	// Get all the instances.
	var instances []Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.InstanceFilter{
			Project: &project,
		}

		cts, err := tx.GetInstances(filter)
		if err != nil {
			return err
		}

		instances = make([]Instance, len(cts))
		for i := range cts {
			apiInst, profiles, err := cts[i].ToAPI(tx)
			if err != nil {
				return err
			}

			args, err := db.InstanceToArgs(cts[i], *apiInst)
			if err != nil {
				return err
			}

			instances[i], err = LoadAllInternal(s, *args, profiles)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// LoadFromAllProjects loads all instances across all projects.
func LoadFromAllProjects(s *state.State) ([]Instance, error) {
	var projects []string

	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		projects, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return nil, err
	}

	instances := []Instance{}
	for _, project := range projects {
		projectInstances, err := LoadByProject(s, project)
		if err != nil {
			return nil, errors.Wrapf(nil, "Load instances in project %s", project)
		}
		instances = append(instances, projectInstances...)
	}

	return instances, nil
}

// LoadNodeAll loads all instances of this nodes.
func LoadNodeAll(s *state.State, instanceType instancetype.Type) ([]Instance, error) {
	// Get all the container arguments
	var instances []Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.InstanceTypeFilter(instanceType)
		insts, err := tx.GetLocalInstancesInProject(filter)
		if err != nil {
			return err
		}

		instances = make([]Instance, len(insts))
		for i := range insts {
			apiInst, profiles, err := insts[i].ToAPI(tx)
			if err != nil {
				return err
			}

			args, err := db.InstanceToArgs(insts[i], *apiInst)
			if err != nil {
				return err
			}

			instances[i], err = LoadAllInternal(s, *args, profiles)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// LoadFromBackup loads from a mounted instance's backup file.
// If applyProfiles is false, then the profiles property will be cleared to prevent profile enrichment from DB.
// Then the expanded config and expanded devices from the backup file will be applied to the local config and
// local devices respectively. This is done to allow an expanded instance to be returned without needing the DB.
func LoadFromBackup(s *state.State, projectName string, instancePath string, applyProfiles bool) (Instance, error) {
	var inst Instance

	backupYamlPath := filepath.Join(instancePath, "backup.yaml")
	backupConf, err := backup.ParseConfigYamlFile(backupYamlPath)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing instance backup file from %q: %w", backupYamlPath, err)
	}

	instDBArgs := backupConf.ToInstanceDBArgs(projectName)

	if !applyProfiles {
		// Stop instance.Load() from expanding profile config from DB, and apply expanded config from
		// backup file to local config. This way we can still see the devices even if DB not available.
		instDBArgs.Profiles = nil
		instDBArgs.Config = backupConf.Container.ExpandedConfig
		instDBArgs.Devices = deviceConfig.NewDevices(backupConf.Container.ExpandedDevices)
	}

	inst, err = Load(s, *instDBArgs, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance from backup file %q: %w", backupYamlPath, err)
	}

	return inst, err
}

// DeleteSnapshots calls the Delete() function on each of the supplied instance's snapshots.
func DeleteSnapshots(s *state.State, projectName, instanceName string) error {
	results, err := s.Cluster.GetInstanceSnapshotsNames(projectName, instanceName)
	if err != nil {
		return err
	}

	for _, snapName := range results {
		snapInst, err := LoadByProjectAndName(s, projectName, snapName)
		if err != nil {
			logger.Error("DeleteSnapshots: Failed to load the snapshot", log.Ctx{"project": projectName, "instance": instanceName, "snapshot": snapName, "err": err})
			continue
		}

		err = snapInst.Delete(true)
		if err != nil {
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
func BackupLoadByName(s *state.State, project, name string) (*backup.InstanceBackup, error) {
	// Get the backup database record
	args, err := s.Cluster.GetInstanceBackup(project, name)
	if err != nil {
		return nil, errors.Wrap(err, "Load backup from database")
	}

	// Load the instance it belongs to
	instance, err := LoadByID(s, args.InstanceID)
	if err != nil {
		return nil, errors.Wrap(err, "Load instance from database")
	}

	return backup.NewInstanceBackup(s, instance, args.ID, name, args.CreationDate, args.ExpiryDate, args.InstanceOnly, args.OptimizedStorage), nil
}

// ResolveImage takes an instance source and returns a hash suitable for instance creation or download.
func ResolveImage(s *state.State, project string, source api.InstanceSource) (string, error) {
	if source.Fingerprint != "" {
		return source.Fingerprint, nil
	}

	if source.Alias != "" {
		if source.Server != "" {
			return source.Alias, nil
		}

		_, alias, err := s.Cluster.GetImageAlias(project, source.Alias, true)
		if err != nil {
			return "", err
		}

		return alias.Target, nil
	}

	if source.Properties != nil {
		if source.Server != "" {
			return "", fmt.Errorf("Property match is only supported for local images")
		}

		hashes, err := s.Cluster.GetImagesFingerprints(project, false)
		if err != nil {
			return "", err
		}

		var image *api.Image
		for _, imageHash := range hashes {
			_, img, err := s.Cluster.GetImage(imageHash, db.ImageFilter{Project: &project})
			if err != nil {
				continue
			}

			if image != nil && img.CreatedAt.Before(image.CreatedAt) {
				continue
			}

			match := true
			for key, value := range source.Properties {
				if img.Properties[key] != value {
					match = false
					break
				}
			}

			if !match {
				continue
			}

			image = img
		}

		if image != nil {
			return image.Fingerprint, nil
		}

		return "", fmt.Errorf("No matching image could be found")
	}

	return "", fmt.Errorf("Must specify one of alias, fingerprint or properties for init from image")
}

// SuitableArchitectures returns a slice of architecture ids based on an instance create request.
//
// An empty list indicates that the request may be handled by any architecture.
// A nil list indicates that we can't tell at this stage, typically for private images.
func SuitableArchitectures(s *state.State, project string, req api.InstancesPost) ([]int, error) {
	// Handle cases where the architecture is already provided.
	if shared.StringInSlice(req.Source.Type, []string{"migration", "none"}) && req.Architecture != "" {
		id, err := osarch.ArchitectureId(req.Architecture)
		if err != nil {
			return nil, err
		}

		return []int{id}, nil
	}

	// For migration, an architecture must be specified in the req.
	if req.Source.Type == "migration" && req.Architecture == "" {
		return nil, fmt.Errorf("An architecture must be specified in migration requests")
	}

	// For none, allow any architecture.
	if req.Source.Type == "none" {
		return []int{}, nil
	}

	// For copy, always use the source architecture.
	if req.Source.Type == "copy" {
		srcProject := req.Source.Project
		if srcProject == "" {
			srcProject = project
		}

		inst, err := fetchInstanceDatabaseObject(s, srcProject, req.Source.Source)
		if err != nil {
			return nil, err
		}

		return []int{inst.Architecture}, nil
	}

	// For image, things get a bit more complicated.
	if req.Source.Type == "image" {
		// Resolve the image.
		hash, err := ResolveImage(s, project, req.Source)
		if err != nil {
			return nil, err
		}

		// Handle local images.
		if req.Source.Server == "" {
			_, img, err := s.Cluster.GetImage(hash, db.ImageFilter{Project: &project})
			if err != nil {
				return nil, err
			}

			id, err := osarch.ArchitectureId(img.Architecture)
			if err != nil {
				return nil, err
			}

			return []int{id}, nil
		}

		// Handle remote images.
		if req.Source.Server != "" {
			// Detect image type based on instance type requested.
			imgType := "container"
			if req.Type == "virtual-machine" {
				imgType = "virtual-machine"
			}

			if req.Source.Secret != "" {
				// We can't retrieve a private image, defer to later processing.
				return nil, nil
			}

			var remote lxd.ImageServer
			if shared.StringInSlice(req.Source.Protocol, []string{"", "lxd"}) {
				// Remote LXD image server.
				remote, err = lxd.ConnectPublicLXD(req.Source.Server, &lxd.ConnectionArgs{
					TLSServerCert: req.Source.Certificate,
					UserAgent:     version.UserAgent,
					Proxy:         s.Proxy,
					CachePath:     s.OS.CacheDir,
					CacheExpiry:   time.Hour,
				})
				if err != nil {
					return nil, err
				}
			} else if req.Source.Protocol == "simplestreams" {
				// Remote simplestreams image server.
				remote, err = lxd.ConnectSimpleStreams(req.Source.Server, &lxd.ConnectionArgs{
					TLSServerCert: req.Source.Certificate,
					UserAgent:     version.UserAgent,
					Proxy:         s.Proxy,
					CachePath:     s.OS.CacheDir,
					CacheExpiry:   time.Hour,
				})
				if err != nil {
					return nil, err
				}
			} else {
				return nil, fmt.Errorf("Unsupported remote image server protocol: %s", req.Source.Protocol)
			}

			// Look for a matching alias.
			entries, err := remote.GetImageAliasArchitectures(imgType, hash)
			if err != nil {
				// Look for a matching image by fingerprint.
				img, _, err := remote.GetImage(hash)
				if err != nil {
					return nil, err
				}

				id, err := osarch.ArchitectureId(img.Architecture)
				if err != nil {
					return nil, err
				}

				return []int{id}, nil
			}

			architectures := []int{}
			for arch := range entries {
				id, err := osarch.ArchitectureId(arch)
				if err != nil {
					return nil, err
				}

				architectures = append(architectures, id)
			}

			return architectures, nil
		}
	}

	// No other known types
	return nil, fmt.Errorf("Unknown instance source type: %s", req.Source.Type)
}

// ValidName validates an instance name. There are different validation rules for instance snapshot names
// so it takes an argument indicating whether the name is to be used for a snapshot or not.
func ValidName(instanceName string, isSnapshot bool) error {
	if isSnapshot {
		parentName, snapshotName, _ := shared.InstanceGetParentAndSnapshotName(instanceName)
		err := shared.ValidHostname(parentName)
		if err != nil {
			return errors.Wrap(err, "Invalid instance name")
		}

		// Snapshot part is more flexible, but doesn't allow space or / character.
		if strings.ContainsAny(snapshotName, " /") {
			return fmt.Errorf("Invalid instance snapshot name: Cannot contain space or / characters")
		}
	} else {
		if strings.Contains(instanceName, shared.SnapshotDelimiter) {
			return fmt.Errorf("The character %q is reserved for snapshots", shared.SnapshotDelimiter)
		}

		err := shared.ValidHostname(instanceName)
		if err != nil {
			return errors.Wrap(err, "Invalid instance name")
		}
	}

	return nil
}

// CreateInternal creates an instance record and storage volume record in the database and sets up devices.
// Accepts an (optionally nil) volumeConfig map that can be used to specify extra custom settings for the volume
// record. Also accepts a reverter that revert steps this function does will be added to. It is up to the caller to
// call the revert's Fail() or Success() function as needed.
// Returns the created instance, along with a "create" operation lock that needs to be marked as Done once the
// instance is fully completed.
func CreateInternal(s *state.State, args db.InstanceArgs, clearLogDir bool, volumeConfig map[string]string, revert *revert.Reverter) (Instance, *operationlock.InstanceOperation, error) {
	// Check instance type requested is supported by this machine.
	if _, supported := s.InstanceTypes[args.Type]; !supported {
		return nil, nil, fmt.Errorf("Instance type %q is not supported on this server", args.Type)
	}

	// Set default values.
	if args.Project == "" {
		args.Project = project.Default
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

	if args.Config["volatile.uuid"] == "" {
		args.Config["volatile.uuid"] = uuid.New()
	}

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = s.OS.Architectures[0]
	}

	err := ValidName(args.Name, args.Snapshot)
	if err != nil {
		return nil, nil, err
	}

	if !args.Snapshot {
		// Unset expiry date since instances don't expire.
		args.ExpiryDate = time.Time{}
	}

	// Validate container config.
	err = ValidConfig(s.OS, args.Config, false, args.Type)
	if err != nil {
		return nil, nil, err
	}

	// Validate container devices with the supplied container name and devices.
	err = ValidDevices(s, s.Cluster, args.Project, args.Type, args.Devices, false)
	if err != nil {
		return nil, nil, errors.Wrap(err, "Invalid devices")
	}

	// Validate architecture.
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, nil, err
	}

	if !shared.IntInSlice(args.Architecture, s.OS.Architectures) {
		return nil, nil, fmt.Errorf("Requested architecture isn't supported by this host")
	}

	// Validate profiles.
	profiles, err := s.Cluster.GetProfileNames(args.Project)
	if err != nil {
		return nil, nil, err
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, nil, fmt.Errorf("Requested profile %q doesn't exist", profile)
		}

		if shared.StringInSlice(profile, checkedProfiles) {
			return nil, nil, fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile)
	}

	if args.CreationDate.IsZero() {
		args.CreationDate = time.Now().UTC()
	}

	if args.LastUsedDate.IsZero() {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	var instanceInfo *db.InstanceArgs
	var op *operationlock.InstanceOperation
	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		node, err := tx.GetLocalNodeName()
		if err != nil {
			return err
		}

		// TODO: this check should probably be performed by the db package itself.
		exists, err := tx.ProjectExists(args.Project)
		if err != nil {
			return errors.Wrapf(err, "Check if project %q exists", args.Project)
		}
		if !exists {
			return fmt.Errorf("Project %q does not exist", args.Project)
		}

		devices, err := db.APIToDevices(args.Devices.CloneNative())
		if err != nil {
			return err
		}

		if args.Snapshot {
			parts := strings.SplitN(args.Name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]
			instance, err := tx.GetInstance(args.Project, instanceName)
			if err != nil {
				return fmt.Errorf("Get instance %q in project %q", instanceName, args.Project)
			}
			snapshot := db.InstanceSnapshot{
				Project:      args.Project,
				Instance:     instanceName,
				Name:         snapshotName,
				CreationDate: args.CreationDate,
				Stateful:     args.Stateful,
				Description:  args.Description,
				ExpiryDate:   sql.NullTime{Time: args.ExpiryDate, Valid: true},
			}
			id, err := tx.CreateInstanceSnapshot(snapshot)
			if err != nil {
				return fmt.Errorf("Add snapshot info to the database: %w", err)
			}

			err = tx.CreateInstanceSnapshotConfig(id, args.Config)
			if err != nil {
				return fmt.Errorf("Unable to fetch instance snapshot config: %w", err)
			}

			for _, device := range devices {
				err = tx.CreateInstanceSnapshotDevice(id, device)
				if err != nil {
					return fmt.Errorf("Unable to fetch instance snapshot devices: %w", err)
				}
			}

			// Read back the snapshot, to get ID and creation time.
			s, err := tx.GetInstanceSnapshot(args.Project, instanceName, snapshotName)
			if err != nil {
				return fmt.Errorf("Fetch created snapshot from the database: %w", err)
			}

			instanceInfo, err = s.ToInstanceArgs(tx, instance)
			if err != nil {
				return fmt.Errorf("Failed to get snapshot instance info: %w", err)
			}

			return nil
		}

		// Create the instance entry.
		inst := db.Instance{
			Project:      args.Project,
			Name:         args.Name,
			Node:         node,
			Type:         args.Type,
			Snapshot:     args.Snapshot,
			Architecture: args.Architecture,
			Ephemeral:    args.Ephemeral,
			CreationDate: args.CreationDate,
			Stateful:     args.Stateful,
			LastUseDate:  sql.NullTime{Time: args.LastUsedDate, Valid: true},
			Description:  args.Description,
			ExpiryDate:   sql.NullTime{Time: args.ExpiryDate, Valid: true},
		}

		id, err := tx.CreateInstance(inst)
		if err != nil {
			return fmt.Errorf("Failed to add instance info to the database: %w", err)
		}

		inst.ID = int(id)
		err = tx.CreateInstanceConfig(id, args.Config)
		if err != nil {
			return fmt.Errorf("Failed to add instance config to the database: %w", err)
		}

		for _, device := range devices {
			err = tx.CreateInstanceDevice(id, device)
			if err != nil {
				return fmt.Errorf("Failed to add instance devices to the database: %w", err)
			}
		}

		err = tx.UpdateInstanceProfiles(inst, args.Profiles)
		if err != nil {
			return fmt.Errorf("Failed to add instance profiles to the database: %w", err)
		}

		// Read back the instance, to get ID and creation time.
		dbInst, err := tx.GetInstance(args.Project, args.Name)
		if err != nil {
			return fmt.Errorf("Failed to fetch created instance from the database: %w", err)
		}

		apiInst, _, err := dbInst.ToAPI(tx)
		if err != nil {
			return fmt.Errorf("Failed to get API Instance fields: %w", err)
		}

		instanceInfo, err = db.InstanceToArgs(*dbInst, *apiInst)
		if err != nil {
			return fmt.Errorf("Failed to get all instance information: %w", err)
		}

		if dbInst.ID < 1 {
			return fmt.Errorf("Unexpected instance database ID %d: %w", dbInst.ID, err)
		}

		op, err = operationlock.Create(dbInst.Project, dbInst.Name, "create", false, false)
		if err != nil {
			return err
		}

		revert.Add(func() { op.Done(err) })

		return nil
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			thing := "Instance"
			if shared.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}

			return nil, nil, fmt.Errorf("%s %q already exists", thing, args.Name)
		}

		return nil, nil, err
	}

	revert.Add(func() { s.Cluster.DeleteInstance(args.Project, instanceInfo.Name) })

	inst, err := Create(s, *instanceInfo, volumeConfig, revert)
	if err != nil {
		logger.Error("Failed initialising instance", log.Ctx{"project": instanceInfo.Project, "instance": instanceInfo.Name, "type": instanceInfo.Type, "err": err})
		return nil, nil, errors.Wrap(err, "Failed initialising instance")
	}

	// Wipe any existing log for this instance name.
	if clearLogDir {
		os.RemoveAll(inst.LogPath())
	}

	return inst, op, nil
}

// NextSnapshotName finds the next snapshot for an instance.
func NextSnapshotName(s *state.State, inst Instance, defaultPattern string) (string, error) {
	var err error

	pattern := inst.ExpandedConfig()["snapshots.pattern"]
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
		i := s.Cluster.GetNextInstanceSnapshotIndex(inst.Project(), inst.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := inst.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = fmt.Sprintf("%s-%%d", pattern)
		i := s.Cluster.GetNextInstanceSnapshotIndex(inst.Project(), inst.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}

// MoveTemporaryName returns a name derived from the instance's volatile.uuid, to use when moving an instance
// across pools or cluster members which can be used for the naming the temporary copy before deleting the original
// instance and renaming the copy to the original name.
func MoveTemporaryName(inst Instance) string {
	return fmt.Sprintf("lxd-move-of-%s", inst.LocalConfig()["volatile.uuid"])
}

// IsSameLogicalInstance returns true if the supplied Instance and db.Instance have the same project and name or
// if they have the same volatile.uuid values.
func IsSameLogicalInstance(inst Instance, dbInst *db.InstanceArgs) bool {
	// Instance name is unique within a project.
	if dbInst.Project == inst.Project() && dbInst.Name == inst.Name() {
		return true
	}

	// Instance UUID is expected to be globally unique (which then allows for the *temporary* existence of
	// duplicate instances of different names with the same volatile.uuid in order to accommodate moving
	// instances between projects and storage pools without triggering duplicate resource errors).
	if dbInst.Config["volatile.uuid"] == inst.LocalConfig()["volatile.uuid"] {
		return true
	}

	return false
}
