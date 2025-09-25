package instance

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2"
	"github.com/google/uuid"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instance/operationlock"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/seccomp"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

// ValidDevices is linked from instance/drivers.validDevices to validate device config.
var ValidDevices func(state *state.State, p api.Project, instanceType instancetype.Type, localDevices deviceConfig.Devices, expandedDevices deviceConfig.Devices) error

// Load is linked from instance/drivers.load to allow different instance types to be loaded.
var Load func(s *state.State, args db.InstanceArgs, p api.Project) (Instance, error)

// Create is linked from instance/drivers.create to allow difference instance types to be created.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
var Create func(s *state.State, args db.InstanceArgs, p api.Project) (Instance, revert.Hook, error)

// ValidConfig validates an instance's config.
func ValidConfig(sysOS *sys.OS, config map[string]string, expanded bool, instanceType instancetype.Type, isSnapshot bool) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if instanceType == instancetype.Any && !expanded && strings.HasPrefix(k, instancetype.ConfigVolatilePrefix) {
			if !isSnapshot {
				return errors.New("Volatile keys can only be set on instances")
			} else if isSnapshot && !shared.StringHasPrefix(k, instancetype.ConfigVolatileSnapshotKeys...) {
				return fmt.Errorf("Invalid volatile key %q in snapshot config", k)
			}
		}

		if instanceType == instancetype.Any && !expanded && strings.HasPrefix(k, "image.") {
			return errors.New("Image keys can only be set on instances")
		}

		err := validConfigKey(sysOS, k, v, instanceType)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, isAllow := config["security.syscalls.allow"]
	_, isDeny := config["security.syscalls.deny"]
	isDenyDefault := shared.IsTrue(config["security.syscalls.deny_default"])
	isDenyCompat := shared.IsTrue(config["security.syscalls.deny_compat"])

	if rawSeccomp && (isAllow || isDeny || isDenyDefault || isDenyCompat) {
		return errors.New("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if isAllow && (isDeny || isDenyDefault || isDenyCompat) {
		return errors.New("security.syscalls.allow is mutually exclusive with security.syscalls.deny*")
	}

	_, err := seccomp.SyscallInterceptMountFilter(config)
	if err != nil {
		return err
	}

	if expanded && (shared.IsFalseOrEmpty(config["security.privileged"])) && sysOS.IdmapSet == nil {
		return errors.New("LXD doesn't have a uid/gid allocation. In this mode, only privileged containers are supported")
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
			return errors.New("LXD was configured to only allow unprivileged containers")
		}
	}

	if shared.IsTrue(config["security.privileged"]) && shared.IsTrue(config["nvidia.runtime"]) {
		return errors.New("nvidia.runtime is incompatible with privileged containers")
	}

	if sysOS.InUbuntuCore() && shared.IsTrue(config["nvidia.runtime"]) {
		return errors.New("nvidia.runtime is incompatible with Ubuntu Core")
	}

	// Validate pinning strategy when limits.cpu specifies static pinning.
	cpuPinStrategy := config["limits.cpu.pin_strategy"]
	cpuLimit := config["limits.cpu"]
	err = validate.IsStaticCPUPinning(cpuLimit)
	if err == nil && !expanded && cpuPinStrategy == "auto" {
		return errors.New(`CPU pinning specified, but pinning strategy is set to "auto"`)
	}

	return nil
}

func validConfigKey(os *sys.OS, key string, value string, instanceType instancetype.Type) error {
	// Disallow keys with container-specific prefixes such as "linux.sysctl." and "limits.kernel." for VMs.
	if instanceType == instancetype.VM && shared.StringHasPrefix(key, instancetype.ConfigKeyPrefixesContainer...) {
		return fmt.Errorf("%q isn't supported for %q", key, instanceType)
	}

	// Check if the key is a valid prefix and whether or not it requires a subkey.
	knownPrefixes := append(instancetype.ConfigKeyPrefixesAny, instancetype.ConfigKeyPrefixesContainer...)
	if strings.HasSuffix(key, ".") {
		if key != instancetype.ConfigVolatilePrefix && !slices.Contains(knownPrefixes, key) {
			// Not a known prefix.
			return fmt.Errorf("Unknown configuration key: %q", key)
		}

		return fmt.Errorf("%q requires a subkey", key)
	}

	// Validate the configuration key against instance type for containers and VMs.
	// Ignore configuration keys with known prefixes since usage has already been validated, and ConfigKeyChecker validates keys syntactically.
	if instanceType != instancetype.Any && !shared.StringHasPrefix(key, knownPrefixes...) && !strings.HasPrefix(key, instancetype.ConfigVolatilePrefix) {
		// Ensure key is present in instance config key map based on type.
		exists := false
		switch instanceType {
		case instancetype.VM:
			_, exists = instancetype.InstanceConfigKeysVM[key]
		case instancetype.Container:
			_, exists = instancetype.InstanceConfigKeysContainer[key]
		}

		_, existsAny := instancetype.InstanceConfigKeysAny[key]
		if !exists && !existsAny {
			return fmt.Errorf("%q isn't supported for %q", key, instanceType)
		}
	}

	f, err := instancetype.ConfigKeyChecker(key, instanceType)
	if err != nil {
		return err
	}

	if err = f(value); err != nil {
		return err
	}

	if key == "raw.lxc" {
		return lxcValidConfig(value)
	}

	if key == "security.syscalls.deny_compat" {
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

func lxcParseRawLXC(line string) (key string, val string, err error) {
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
	key, val, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", fmt.Errorf("Invalid raw.lxc line: %s", line)
	}

	key = strings.ToLower(strings.Trim(key, " \t"))
	val = strings.Trim(val, " \t")
	return key, val, nil
}

func lxcValidConfig(rawLxc string) error {
	for line := range strings.SplitSeq(rawLxc, "\n") {
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
			return errors.New("Setting lxc.logfile is not allowed")
		}

		if key == "lxc.syslog" || key == "lxc.log.syslog" {
			return errors.New("Setting lxc.log.syslog is not allowed")
		}

		if key == "lxc.ephemeral" {
			return errors.New("Setting lxc.ephemeral is not allowed")
		}

		if strings.HasPrefix(key, "lxc.prlimit.") {
			return errors.New(`Process limits should be set via ` +
				`"limits.kernel.[limit name]" and not ` +
				`directly via "lxc.prlimit.[limit name]"`)
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
			return errors.New("Cannot map root user into container as LXD was configured to only allow unprivileged containers")
		}
	}

	return nil
}

// LoadByID loads an instance by ID.
func LoadByID(s *state.State, id int) (Instance, error) {
	var project string
	var name string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get the DB record
		project, name, err = tx.GetInstanceProjectAndName(ctx, id)
		if err != nil {
			return fmt.Errorf("Failed getting instance project and name: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return LoadByProjectAndName(s, project, name)
}

// LoadInstanceDatabaseObject loads a db.Instance object, accounting for snapshots.
func LoadInstanceDatabaseObject(ctx context.Context, tx *db.ClusterTx, project, name string) (*cluster.Instance, error) {
	var container *cluster.Instance
	var err error

	if strings.Contains(name, shared.SnapshotDelimiter) {
		parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		instanceName := parts[0]
		snapshotName := parts[1]

		instance, err := cluster.GetInstance(ctx, tx.Tx(), project, instanceName)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch instance %q in project %q: %w", name, project, err)
		}

		snapshot, err := cluster.GetInstanceSnapshot(ctx, tx.Tx(), project, instanceName, snapshotName)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch snapshot %q of instance %q in project %q: %w", snapshotName, instanceName, project, err)
		}

		c := snapshot.ToInstance(instance.Name, instance.Node, instance.Type, instance.Architecture)
		container = &c
	} else {
		container, err = cluster.GetInstance(ctx, tx.Tx(), project, name)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch instance %q in project %q: %w", name, project, err)
		}
	}

	return container, nil
}

// LoadByProjectAndName loads an instance by project and name.
func LoadByProjectAndName(s *state.State, projectName string, instanceName string) (Instance, error) {
	// Get the DB record
	var args db.InstanceArgs
	var p *api.Project
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		proj, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = proj.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		inst, err := LoadInstanceDatabaseObject(ctx, tx, projectName, instanceName)
		if err != nil {
			return err
		}

		instArgs, err := tx.InstancesToInstanceArgs(ctx, true, *inst)
		if err != nil {
			return err
		}

		args = instArgs[inst.ID]

		return nil
	})
	if err != nil {
		return nil, err
	}

	inst, err := Load(s, args, *p)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance: %w", err)
	}

	return inst, nil
}

// LoadNodeAll loads all instances on this server.
func LoadNodeAll(s *state.State, instanceType instancetype.Type) ([]Instance, error) {
	var err error
	var instances []Instance

	filter := cluster.InstanceFilter{Type: instanceType.Filter()}
	if s.ServerName != "" {
		filter.Node = &s.ServerName
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
			}

			instances = append(instances, inst)

			return nil
		}, filter)
	})
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// LoadFromBackup loads from a mounted instance's backup file without needing the database.
// Project config is not populated (as not in the backup file), however expanded config from backup file is applied
// to avoid needing to expand config by loading profiles from database.
func LoadFromBackup(s *state.State, projectName string, instancePath string) (Instance, error) {
	backupYamlPath := filepath.Join(instancePath, "backup.yaml")
	backupConf, err := backup.ParseConfigYamlFile(backupYamlPath)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing instance backup file from %q: %w", backupYamlPath, err)
	}

	// Specify applyProfiles arg as false to avoid DB query.
	instDBArgs, err := backup.ConfigToInstanceDBArgs(s, backupConf, projectName, false)
	if err != nil {
		return nil, err
	}

	// Stop instance.Load() from expanding profile config from DB, and apply expanded config from
	// backup file to local config. This way we can still see the devices even if DB not available.
	instDBArgs.Config = backupConf.Instance.ExpandedConfig
	instDBArgs.Devices = deviceConfig.NewDevices(backupConf.Instance.ExpandedDevices)

	// Set Node field to local node.
	instDBArgs.Node = s.ServerName

	p := api.Project{
		Name: backupConf.Instance.Project,
	}

	inst, err := Load(s, *instDBArgs, p)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance from backup file %q: %w", backupYamlPath, err)
	}

	return inst, nil
}

// DeviceNextInterfaceHWAddr generates a random MAC address.
func DeviceNextInterfaceHWAddr() (string, error) {
	const prefix = "00:16:3e"
	buf := make([]byte, 0, 17)

	// Add the fixed prefix
	buf = append(buf, prefix...)

	// Append 3 random bytes
	for range 3 {
		rb, err := rand.Int(rand.Reader, big.NewInt(256))
		if err != nil {
			return "", err
		}

		buf = append(buf, ':')
		buf = append(buf, fmt.Sprintf("%02x", rb.Int64())...)
	}

	return string(buf), nil
}

// BackupLoadByName load an instance backup from the database.
func BackupLoadByName(s *state.State, project, name string) (*backup.InstanceBackup, error) {
	var args db.InstanceBackup

	// Get the backup database record
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		args, err = tx.GetInstanceBackup(ctx, project, name)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Load the instance it belongs to
	instance, err := LoadByID(s, args.InstanceID)
	if err != nil {
		return nil, err
	}

	return backup.NewInstanceBackup(s, instance, args.ID, name, args.CreationDate, args.ExpiryDate, args.InstanceOnly, args.OptimizedStorage), nil
}

// ResolveImage takes an instance source and returns a hash suitable for instance creation or download.
func ResolveImage(ctx context.Context, tx *db.ClusterTx, projectName string, source api.InstanceSource) (string, error) {
	if source.Fingerprint != "" {
		return source.Fingerprint, nil
	}

	if source.Alias != "" {
		if source.Server != "" {
			return source.Alias, nil
		}

		_, alias, err := tx.GetImageAlias(ctx, projectName, source.Alias, true)
		if err != nil {
			return "", err
		}

		return alias.Target, nil
	}

	if source.Properties != nil {
		if source.Server != "" {
			return "", errors.New("Property match is only supported for local images")
		}

		hashes, err := tx.GetImagesFingerprints(ctx, projectName, false)
		if err != nil {
			return "", err
		}

		var image *api.Image
		for _, imageHash := range hashes {
			_, img, err := tx.GetImageByFingerprintPrefix(ctx, imageHash, cluster.ImageFilter{Project: &projectName})
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

		return "", errors.New("No matching image could be found")
	}

	return "", errors.New("Must specify one of alias, fingerprint or properties for init from image")
}

// SuitableArchitectures returns a slice of architecture ids based on an instance create request.
//
// An empty list indicates that the request may be handled by any architecture.
// A nil list indicates that we can't tell at this stage, typically for private images.
func SuitableArchitectures(ctx context.Context, s *state.State, tx *db.ClusterTx, projectName string, sourceInst *cluster.Instance, sourceImageRef string, req api.InstancesPost) ([]int, error) {
	// Handle cases where the architecture is already provided.
	if slices.Contains([]string{api.SourceTypeConversion, api.SourceTypeMigration, api.SourceTypeNone}, req.Source.Type) && req.Architecture != "" {
		id, err := osarch.ArchitectureId(req.Architecture)
		if err != nil {
			return nil, err
		}

		return []int{id}, nil
	}

	// For migration and conversion, an architecture must be specified in the req.
	if slices.Contains([]string{api.SourceTypeConversion, api.SourceTypeMigration}, req.Source.Type) && req.Architecture == "" {
		return nil, api.StatusErrorf(http.StatusBadRequest, "An architecture must be specified in migration or conversion requests")
	}

	// For none, allow any architecture.
	if req.Source.Type == api.SourceTypeNone {
		return []int{}, nil
	}

	// For copy, always use the source architecture.
	if req.Source.Type == api.SourceTypeCopy {
		return []int{sourceInst.Architecture}, nil
	}

	// For image, things get a bit more complicated.
	if req.Source.Type == api.SourceTypeImage {
		// Handle local images.
		if req.Source.Server == "" {
			_, img, err := tx.GetImageByFingerprintPrefix(ctx, sourceImageRef, cluster.ImageFilter{Project: &projectName})
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
			if req.Source.Secret != "" {
				// We can't retrieve a private image, defer to later processing.
				return nil, nil
			}

			var err error
			var remote lxd.ImageServer
			if slices.Contains([]string{"", "lxd"}, req.Source.Protocol) {
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
				return nil, api.StatusErrorf(http.StatusBadRequest, "Unsupported remote image server protocol %q", req.Source.Protocol)
			}

			// Look for a matching alias.
			entries, err := remote.GetImageAliasArchitectures(string(req.Type), sourceImageRef)
			if err != nil {
				// Look for a matching image by fingerprint.
				img, _, err := remote.GetImage(sourceImageRef)
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
	return nil, api.StatusErrorf(http.StatusBadRequest, "Unknown instance source type %q", req.Source.Type)
}

// CreateInternal creates an instance record and storage volume record in the database and sets up devices.
// Accepts a reverter that revert steps this function does will be added to. It is up to the caller to
// call the revert's Fail() or Success() function as needed.
// Returns the created instance, along with a "create" operation lock that needs to be marked as Done once the
// instance is fully completed, and a revert fail function that can be used to undo this function if a subsequent
// step fails.
func CreateInternal(s *state.State, args db.InstanceArgs, clearLogDir bool) (Instance, *operationlock.InstanceOperation, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Check instance type requested is supported by this machine.
	err := s.InstanceTypes[args.Type]
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Instance type %q is not supported on this server: %w", args.Type, err)
	}

	// Set default values.
	if args.Project == "" {
		args.Project = api.ProjectDefaultName
	}

	if args.Profiles == nil {
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			args.Profiles, err = tx.GetProfiles(ctx, args.Project, []string{"default"})

			return err
		})
		if err != nil {
			return nil, nil, nil, errors.New("Failed to get default profile for new instance")
		}
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
	}

	if args.Config["volatile.uuid"] == "" {
		args.Config["volatile.uuid"] = uuid.New().String()
	}

	args.Config["volatile.uuid.generation"] = args.Config["volatile.uuid"]

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = s.OS.Architectures[0]
	}

	err = instancetype.ValidName(args.Name, args.Snapshot)
	if err != nil {
		return nil, nil, nil, err
	}

	if !args.Snapshot {
		// Unset expiry date since instances don't expire.
		args.ExpiryDate = time.Time{}

		// Generate a cloud-init instance-id if not provided.
		//
		// This is generated here rather than in startCommon as only new
		// instances or those which get modified should receive an instance-id.
		// Existing instances will keep using their instance name as instance-id to
		// avoid triggering cloud-init on upgrade.
		if args.Config["volatile.cloud-init.instance-id"] == "" {
			args.Config["volatile.cloud-init.instance-id"] = uuid.New().String()
		}
	}

	// Validate instance config.
	err = ValidConfig(s.OS, args.Config, false, args.Type)
	if err != nil {
		return nil, nil, nil, err
	}

	// Leave validating devices to Create function call below.

	// Validate architecture.
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, nil, nil, err
	}

	if !slices.Contains(s.OS.Architectures, args.Architecture) {
		return nil, nil, nil, errors.New("Requested architecture isn't supported by this host")
	}

	var profiles []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Validate profiles.
		profiles, err = tx.GetProfileNames(ctx, args.Project)

		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}

	checkedProfiles := map[string]bool{}
	for _, profile := range args.Profiles {
		if !slices.Contains(profiles, profile.Name) {
			return nil, nil, nil, fmt.Errorf("Requested profile %q doesn't exist", profile.Name)
		}

		if checkedProfiles[profile.Name] {
			return nil, nil, nil, errors.New("Duplicate profile found in request")
		}

		checkedProfiles[profile.Name] = true
	}

	if args.CreationDate.IsZero() {
		args.CreationDate = time.Now().UTC()
	}

	if args.LastUsedDate.IsZero() {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	// Prevent concurrent create requests for same instance.
	op, err := operationlock.Create(args.Project, args.Name, operationlock.ActionCreate, false, false)
	if err != nil {
		return nil, nil, nil, err
	}

	revert.Add(func() { op.Done(err) })

	var dbInst cluster.Instance
	var p *api.Project

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		proj, err := cluster.GetProject(ctx, tx.Tx(), args.Project)
		if err != nil {
			return err
		}

		p, err = proj.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		devices, err := cluster.APIToDevices(args.Devices.CloneNative())
		if err != nil {
			return err
		}

		if args.Snapshot {
			parts := strings.SplitN(args.Name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]
			instance, err := cluster.GetInstance(ctx, tx.Tx(), args.Project, instanceName)
			if err != nil {
				return fmt.Errorf("Get instance %q in project %q", instanceName, args.Project)
			}

			snapshot := cluster.InstanceSnapshot{
				Project:      args.Project,
				Instance:     instanceName,
				Name:         snapshotName,
				CreationDate: args.CreationDate,
				Stateful:     args.Stateful,
				Description:  args.Description,
				ExpiryDate:   sql.NullTime{Time: args.ExpiryDate, Valid: true},
			}

			id, err := cluster.CreateInstanceSnapshot(ctx, tx.Tx(), snapshot)
			if err != nil {
				return fmt.Errorf("Add snapshot info to the database: %w", err)
			}

			err = cluster.CreateInstanceSnapshotConfig(ctx, tx.Tx(), id, args.Config)
			if err != nil {
				return err
			}

			err = cluster.CreateInstanceSnapshotDevices(ctx, tx.Tx(), id, devices)
			if err != nil {
				return err
			}

			// Read back the snapshot, to get ID and creation time.
			s, err := cluster.GetInstanceSnapshot(ctx, tx.Tx(), args.Project, instanceName, snapshotName)
			if err != nil {
				return fmt.Errorf("Fetch created snapshot from the database: %w", err)
			}

			dbInst = s.ToInstance(instance.Name, instance.Node, instance.Type, instance.Architecture)

			newArgs, err := tx.InstancesToInstanceArgs(ctx, false, dbInst)
			if err != nil {
				return err
			}

			// Populate profile info that was already loaded.
			newInstArgs := newArgs[dbInst.ID]
			newInstArgs.Profiles = args.Profiles
			args = newInstArgs

			return nil
		}

		// Create the instance entry.
		dbInst = cluster.Instance{
			Project:      args.Project,
			Name:         args.Name,
			Node:         s.ServerName,
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

		instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), dbInst)
		if err != nil {
			return fmt.Errorf("Add instance info to the database: %w", err)
		}

		err = cluster.CreateInstanceDevices(ctx, tx.Tx(), instanceID, devices)
		if err != nil {
			return err
		}

		err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, args.Config)
		if err != nil {
			return err
		}

		profileNames := make([]string, 0, len(args.Profiles))
		for _, profile := range args.Profiles {
			profileNames = append(profileNames, profile.Name)
		}

		err = cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instanceID), dbInst.Project, profileNames)
		if err != nil {
			return err
		}

		// Read back the instance, to get ID and creation time.
		dbRow, err := cluster.GetInstance(ctx, tx.Tx(), args.Project, args.Name)
		if err != nil {
			return fmt.Errorf("Fetch created instance from the database: %w", err)
		}

		dbInst = *dbRow

		if dbInst.ID < 1 {
			return fmt.Errorf("Unexpected instance database ID %d: %w", dbInst.ID, err)
		}

		newArgs, err := tx.InstancesToInstanceArgs(ctx, false, dbInst)
		if err != nil {
			return err
		}

		// Populate profile info that was already loaded.
		newInstArgs := newArgs[dbInst.ID]
		newInstArgs.Profiles = args.Profiles
		args = newInstArgs

		return nil
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			thing := "Instance"
			if shared.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}

			return nil, nil, nil, api.StatusErrorf(http.StatusConflict, "%s %q already exists", thing, args.Name)
		}

		return nil, nil, nil, err
	}

	revert.Add(func() {
		_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteInstance(ctx, dbInst.Project, dbInst.Name)
		})
	})
	inst, cleanup, err := Create(s, args, *p)
	if err != nil {
		logger.Error("Failed initialising instance", logger.Ctx{"project": args.Project, "instance": args.Name, "type": args.Type, "err": err})
		return nil, nil, nil, fmt.Errorf("Failed initialising instance: %w", err)
	}

	revert.Add(cleanup)

	// Wipe any existing log for this instance name.
	if clearLogDir {
		_ = os.RemoveAll(inst.LogPath())
	}

	cleanup = revert.Clone().Fail
	revert.Success()
	return inst, op, cleanup, err
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
		return "", errors.New("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		var i int

		_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			i = tx.GetNextInstanceSnapshotIndex(ctx, inst.Project().Name, inst.Name(), pattern)

			return nil
		})

		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := inst.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := api.GetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = pattern + "-%d"

		var i int

		_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			i = tx.GetNextInstanceSnapshotIndex(ctx, inst.Project().Name, inst.Name(), pattern)

			return nil
		})
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}

// temporaryName returns the temporary instance name using a stable random generator.
// The returned string is a valid DNS name.
func temporaryName(instUUID string) (string, error) {
	r, err := util.GetStableRandomGenerator(instUUID)
	if err != nil {
		return "", err
	}

	// The longest temporary name is lxd-move-18446744073709551615 which has a length
	// of 30 characters since 18446744073709551615 is the biggest value for an uint64.
	// The prefix is attached to have a valid DNS name that doesn't start with numbers.
	return fmt.Sprintf("lxd-move-%d", r.Uint64()), nil
}

// MoveTemporaryName returns a name derived from the instance's volatile.uuid, to use when moving an instance
// across pools or cluster members which can be used for the naming the temporary copy before deleting the original
// instance and renaming the copy to the original name.
// If volatile.uuid is not set, a new UUID is generated and stored in the instance's config.
func MoveTemporaryName(inst Instance) (string, error) {
	instUUID := inst.LocalConfig()["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New().String()
		err := inst.VolatileSet(map[string]string{"volatile.uuid": instUUID})
		if err != nil {
			return "", fmt.Errorf("Failed setting volatile.uuid to %s: %w", instUUID, err)
		}
	}

	return temporaryName(instUUID)
}

// IsSameLogicalInstance returns true if the supplied Instance and db.Instance have the same project and name or
// if they have the same volatile.uuid values.
func IsSameLogicalInstance(inst Instance, dbInst *db.InstanceArgs) bool {
	// Instance name is unique within a project.
	if dbInst.Project == inst.Project().Name && dbInst.Name == inst.Name() {
		return true
	}

	// Don't trigger duplicate resource errors for temporary copies.
	if dbInst.Config["volatile.uuid"] == inst.LocalConfig()["volatile.uuid"] {
		// Accommodate moving instances between storage pools.
		// Check temporary copy against source.
		tempName, err := temporaryName(inst.LocalConfig()["volatile.uuid"])
		if err != nil {
			return false
		}

		if dbInst.Name == tempName {
			return true
		}

		// Check source against temporary copy.
		tempName, err = temporaryName(dbInst.Config["volatile.uuid"])
		if err != nil {
			return false
		}

		if inst.Name() == tempName {
			return true
		}

		// Accommodate moving instances between projects.
		if dbInst.Project != inst.Project().Name {
			return true
		}
	}

	return false
}

// SnapshotToProtobuf converts a snapshot record to a migration snapshot record.
func SnapshotToProtobuf(snap *api.InstanceSnapshot) *migration.Snapshot {
	config := make([]*migration.Config, 0, len(snap.Config))
	for k, v := range snap.Config {
		kCopy := string(k)
		vCopy := string(v)
		config = append(config, &migration.Config{Key: &kCopy, Value: &vCopy})
	}

	devices := make([]*migration.Device, 0, len(snap.Devices))
	for name, d := range snap.Devices {
		props := make([]*migration.Config, 0, len(snap.Devices))
		for k, v := range d {
			// Local loop vars.
			kCopy := string(k)
			vCopy := string(v)
			props = append(props, &migration.Config{Key: &kCopy, Value: &vCopy})
		}

		nameCopy := name // Local loop var.
		devices = append(devices, &migration.Device{Name: &nameCopy, Config: props})
	}

	isEphemeral := snap.Ephemeral
	archID, _ := osarch.ArchitectureId(snap.Architecture)
	arch := int32(archID)
	stateful := snap.Stateful
	creationDate := snap.CreatedAt.UTC().Unix()
	lastUsedDate := snap.LastUsedAt.UTC().Unix()
	expiryDate := snap.ExpiresAt.UTC().Unix()

	return &migration.Snapshot{
		Name:         &snap.Name,
		LocalConfig:  config,
		Profiles:     snap.Profiles,
		Ephemeral:    &isEphemeral,
		LocalDevices: devices,
		Architecture: &arch,
		Stateful:     &stateful,
		CreationDate: &creationDate,
		LastUsedDate: &lastUsedDate,
		ExpiryDate:   &expiryDate,
	}
}

// SnapshotProtobufToInstanceArgs converts a migration snapshot record to DB instance record format.
func SnapshotProtobufToInstanceArgs(s *state.State, inst Instance, snap *migration.Snapshot) (*db.InstanceArgs, error) {
	snapConfig := snap.GetLocalConfig()
	config := make(map[string]string, len(snapConfig))
	for _, ent := range snapConfig {
		config[ent.GetKey()] = ent.GetValue()
	}

	snapDevices := snap.GetLocalDevices()
	devices := make(deviceConfig.Devices, len(snapDevices))
	for _, ent := range snap.GetLocalDevices() {
		entConfig := ent.GetConfig()
		props := make(map[string]string, len(entConfig))
		for _, prop := range entConfig {
			props[prop.GetKey()] = prop.GetValue()
		}

		devices[ent.GetName()] = props
	}

	var profiles []api.Profile

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		profiles, err = tx.GetProfiles(ctx, inst.Project().Name, snap.Profiles)

		return err
	})
	if err != nil {
		return nil, err
	}

	args := db.InstanceArgs{
		Architecture: int(snap.GetArchitecture()),
		Config:       config,
		Type:         inst.Type(),
		Snapshot:     true,
		Devices:      devices,
		Ephemeral:    snap.GetEphemeral(),
		Name:         inst.Name() + shared.SnapshotDelimiter + snap.GetName(),
		Profiles:     profiles,
		Stateful:     snap.GetStateful(),
		Project:      inst.Project().Name,
	}

	if snap.GetCreationDate() != 0 {
		args.CreationDate = time.Unix(snap.GetCreationDate(), 0)
	}

	if snap.GetLastUsedDate() != 0 {
		args.LastUsedDate = time.Unix(snap.GetLastUsedDate(), 0)
	}

	if snap.GetExpiryDate() != 0 {
		args.ExpiryDate = time.Unix(snap.GetExpiryDate(), 0)
	}

	return &args, nil
}
