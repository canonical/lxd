package instance

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/client"
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
	"github.com/lxc/lxd/shared/version"
)

// ValidDevices is linked from instance/drivers.validDevices to validate device config.
var ValidDevices func(state *state.State, cluster *db.Cluster, instanceType instancetype.Type, devices deviceConfig.Devices, expanded bool) error

// Load is linked from instance/drivers.load to allow different instance types to be loaded.
var Load func(s *state.State, args db.InstanceArgs, profiles []api.Profile) (Instance, error)

// Create is linked from instance/drivers.create to allow difference instance types to be created.
var Create func(s *state.State, args db.InstanceArgs) (Instance, error)

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
			return fmt.Errorf("Volatile keys can only be set on instances")
		}

		if profile && strings.HasPrefix(k, "image.") {
			return fmt.Errorf("Image keys can only be set on instances")
		}

		err := validConfigKey(sysOS, k, v)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, isAllow := config["security.syscalls.whitelist"]
	_, isDeny := config["security.syscalls.blacklist"]
	isDenyDefault := shared.IsTrue(config["security.syscalls.blacklist_default"])
	isDenyCompat := shared.IsTrue(config["security.syscalls.blacklist_compat"])

	if rawSeccomp && (isAllow || isDeny || isDenyDefault || isDenyCompat) {
		return fmt.Errorf("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if isAllow && (isDeny || isDenyDefault || isDenyCompat) {
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

		c := db.InstanceSnapshotToInstance(instance, snapshot)
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
	container, err := fetchInstanceDatabaseObject(s, project, name)
	if err != nil {
		return nil, err
	}

	args := db.InstanceToArgs(container)
	inst, err := Load(s, args, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load instance")
	}

	return inst, nil
}

// LoadAllInternal loads a list of db instances into a list of instances.
func LoadAllInternal(s *state.State, dbInstances []db.Instance) ([]Instance, error) {
	// Figure out what profiles are in use
	profiles := map[string]map[string]api.Profile{}
	for _, instArgs := range dbInstances {
		projectProfiles, ok := profiles[instArgs.Project]
		if !ok {
			projectProfiles = map[string]api.Profile{}
			profiles[instArgs.Project] = projectProfiles
		}
		for _, profile := range instArgs.Profiles {
			_, ok := projectProfiles[profile]
			if !ok {
				projectProfiles[profile] = api.Profile{}
			}
		}
	}

	// Get the profile data
	for project, projectProfiles := range profiles {
		for name := range projectProfiles {
			_, profile, err := s.Cluster.GetProfile(project, name)
			if err != nil {
				return nil, err
			}

			projectProfiles[name] = *profile
		}
	}

	// Load the instances structs
	instances := []Instance{}
	for _, dbInstance := range dbInstances {
		// Figure out the instances's profiles
		cProfiles := []api.Profile{}
		for _, name := range dbInstance.Profiles {
			cProfiles = append(cProfiles, profiles[dbInstance.Project][name])
		}

		args := db.InstanceToArgs(&dbInstance)
		inst, err := Load(s, args, cProfiles)
		if err != nil {
			return nil, err
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

// LoadByProject loads all instances in a project.
func LoadByProject(s *state.State, project string) ([]Instance, error) {
	// Get all the instances.
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.InstanceFilter{
			Project: project,
			Type:    instancetype.Any,
		}
		var err error
		cts, err = tx.GetInstances(filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return LoadAllInternal(s, cts)
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
	var insts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		insts, err = tx.GetLocalInstancesInProject("", instanceType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return LoadAllInternal(s, insts)
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
	args, err := s.Cluster.GetInstanceBackup(project, name)
	if err != nil {
		return nil, errors.Wrap(err, "Load backup from database")
	}

	// Load the instance it belongs to
	instance, err := LoadByID(s, args.InstanceID)
	if err != nil {
		return nil, errors.Wrap(err, "Load instance from database")
	}

	return backup.New(s, instance, args.ID, name, args.CreationDate, args.ExpiryDate, args.InstanceOnly, args.OptimizedStorage), nil
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
			_, img, err := s.Cluster.GetImage(project, imageHash, false)
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
			_, img, err := s.Cluster.GetImage(project, hash, false)
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
