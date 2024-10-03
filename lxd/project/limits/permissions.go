package limits

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceconfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

// projectLimitDiskPool is the prefix used for pool-specific disk limits.
var projectLimitDiskPool = "limits.disk.pool."

// HiddenStoragePools returns a list of storage pools that should be hidden from users of the project.
func HiddenStoragePools(ctx context.Context, tx *db.ClusterTx, projectName string) ([]string, error) {
	dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return nil, fmt.Errorf("Failed getting project: %w", err)
	}

	project, err := dbProject.ToAPI(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	hiddenPools := []string{}
	for k, v := range project.Config {
		if !strings.HasPrefix(k, projectLimitDiskPool) || v != "0" {
			continue
		}

		fields := strings.SplitN(k, projectLimitDiskPool, 2)
		if len(fields) == 2 {
			hiddenPools = append(hiddenPools, fields[1])
		}
	}

	return hiddenPools, nil
}

// AllowInstanceCreation returns an error if any project-specific limit or
// restriction is violated when creating a new instance.
func AllowInstanceCreation(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string, clusterMemberName string, sysinfo *api.ClusterMemberSysInfo, req api.InstancesPost) error {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	instance := api.Instance{
		Name:    req.Name,
		Project: projectName,
	}

	instance.SetWritable(req.InstancePut)

	errors, err := CheckReservationsWithInstance(context.TODO(), tx, globalConfigDump, &instance, map[string]api.ClusterMemberSysInfo{clusterMemberName: *sysinfo})
	if err != nil {
		return fmt.Errorf("Failed validating resource reservations: %w", err)
	}

	err = errors[clusterMemberName]
	if err != nil {
		return fmt.Errorf("Reservation exceeded on cluster member %q: %w", clusterMemberName, err)
	}

	info, err := fetchProject(globalConfigDump, tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	var instanceType instancetype.Type
	switch req.Type {
	case api.InstanceTypeContainer:
		instanceType = instancetype.Container
	case api.InstanceTypeVM:
		instanceType = instancetype.VM
	default:
		return fmt.Errorf("Unexpected instance type %q", req.Type)
	}

	if req.Profiles == nil {
		req.Profiles = []string{"default"}
	}

	err = checkInstanceCountLimit(info, instanceType)
	if err != nil {
		return err
	}

	err = checkTotalInstanceCountLimit(info)
	if err != nil {
		return err
	}

	// Add the instance being created.
	info.Instances = append(info.Instances, instance)

	// Special case restriction checks on volatile.* keys.
	strip := false

	if shared.ValueInSlice(req.Source.Type, []string{"copy", "migration"}) {
		// Allow stripping volatile keys if dealing with a copy or migration.
		strip = true
	}

	err = checkRestrictionsOnVolatileConfig(
		info.Project, instanceType, req.Name, req.Config, map[string]string{}, strip)
	if err != nil {
		return err
	}

	err = checkRestrictionsAndAggregateLimits(globalConfig, info)
	if err != nil {
		return fmt.Errorf("Failed checking if instance creation allowed: %w", err)
	}

	return nil
}

// Check that we have not exceeded the maximum total allotted number of instances for both containers and vms.
func checkTotalInstanceCountLimit(info *projectInfo) error {
	count, limit, err := getTotalInstanceCountLimit(info)
	if err != nil {
		return err
	}

	if limit >= 0 && count >= limit {
		return fmt.Errorf("Reached maximum number of instances in project %q", info.Project.Name)
	}

	return nil
}

func getTotalInstanceCountLimit(info *projectInfo) (instanceCount int, limit int, err error) {
	overallValue, ok := info.Project.Config["limits.instances"]
	if ok {
		limit, err := strconv.Atoi(overallValue)
		if err != nil {
			return -1, -1, err
		}

		return len(info.Instances), limit, nil
	}

	return len(info.Instances), -1, nil
}

// Check that we have not reached the maximum number of instances for this type.
func checkInstanceCountLimit(info *projectInfo, instanceType instancetype.Type) error {
	count, limit, err := getInstanceCountLimit(info, instanceType)
	if err != nil {
		return err
	}

	if limit >= 0 && count >= limit {
		return fmt.Errorf("Reached maximum number of instances of type %q in project %q", instanceType, info.Project.Name)
	}

	return nil
}

func getInstanceCountLimit(info *projectInfo, instanceType instancetype.Type) (instanceCount int, limit int, err error) {
	var key string
	switch instanceType {
	case instancetype.Container:
		key = "limits.containers"
	case instancetype.VM:
		key = "limits.virtual-machines"
	default:
		return -1, -1, fmt.Errorf("Unexpected instance type %q", instanceType)
	}

	for _, inst := range info.Instances {
		if inst.Type == instanceType.String() {
			instanceCount++
		}
	}

	value, ok := info.Project.Config[key]
	if ok {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 0 {
			return -1, -1, fmt.Errorf("Unexpected %q value: %q", key, value)
		}

		return instanceCount, limit, nil
	}

	return instanceCount, -1, nil
}

// Check restrictions on setting volatile.* keys.
func checkRestrictionsOnVolatileConfig(project api.Project, instanceType instancetype.Type, instanceName string, config, currentConfig map[string]string, strip bool) error {
	if project.Config["restrict"] == "false" {
		return nil
	}

	var restrictedLowLevel string
	switch instanceType {
	case instancetype.Container:
		restrictedLowLevel = "restricted.containers.lowlevel"
	case instancetype.VM:
		restrictedLowLevel = "restricted.virtual-machines.lowlevel"
	}

	if project.Config[restrictedLowLevel] == "allow" {
		return nil
	}

	// Checker for safe volatile keys.
	isSafeKey := func(key string) bool {
		if shared.ValueInSlice(key, []string{"volatile.apply_template", "volatile.base_image", "volatile.last_state.power"}) {
			return true
		}

		if strings.HasPrefix(key, instancetype.ConfigVolatilePrefix) {
			if strings.HasSuffix(key, ".apply_quota") {
				return true
			}

			if strings.HasSuffix(key, ".hwaddr") {
				return true
			}
		}

		return false
	}

	for key, value := range config {
		if !strings.HasPrefix(key, instancetype.ConfigVolatilePrefix) {
			continue
		}

		// Allow given safe volatile keys to be set
		if isSafeKey(key) {
			continue
		}

		if strip {
			delete(config, key)
			continue
		}

		currentValue, ok := currentConfig[key]
		if !ok {
			return fmt.Errorf("Setting %q on %s %q in project %q is forbidden", key, instanceType, instanceName, project.Name)
		}

		if currentValue != value {
			return fmt.Errorf("Changing %q on %s %q in project %q is forbidden", key, instanceType, instanceName, project.Name)
		}
	}

	return nil
}

// AllowVolumeCreation returns an error if any project-specific limit or
// restriction is violated when creating a new custom volume in a project.
func AllowVolumeCreation(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string, poolName string, req api.StorageVolumesPost) error {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	info, err := fetchProject(globalConfigDump, tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// If "limits.disk" is not set, there's nothing to do.
	if info.Project.Config["limits.disk"] == "" {
		return nil
	}

	// Add the volume being created.
	info.Volumes = append(info.Volumes, db.StorageVolumeArgs{
		Name:     req.Name,
		Config:   req.Config,
		PoolName: poolName,
	})

	err = checkRestrictionsAndAggregateLimits(globalConfig, info)
	if err != nil {
		return fmt.Errorf("Failed checking if volume creation allowed: %w", err)
	}

	return nil
}

// GetImageSpaceBudget returns how much disk space is left in the given project
// for writing images.
//
// If no limit is in place, return -1.
func GetImageSpaceBudget(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string) (int64, error) {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	info, err := fetchProject(globalConfigDump, tx, projectName, true)
	if err != nil {
		return -1, err
	}

	if info == nil {
		return -1, nil
	}

	// If "features.images" is not enabled, the budget is unlimited.
	if shared.IsFalse(info.Project.Config["features.images"]) {
		return -1, nil
	}

	// If "limits.disk" is not set, the budget is unlimited.
	if info.Project.Config["limits.disk"] == "" {
		return -1, nil
	}

	parser := aggregateLimitConfigValueParsers["limits.disk"]
	quota, err := parser(info.Project.Config["limits.disk"])
	if err != nil {
		return -1, err
	}

	instances, err := expandInstancesConfigAndDevices(globalConfigDump, info.Instances, info.Profiles)
	if err != nil {
		return -1, err
	}

	info.Instances = instances

	totals, err := getTotalsAcrossProjectEntities(info, []string{"limits.disk"}, false)
	if err != nil {
		return -1, err
	}

	if totals["limits.disk"] < quota {
		return quota - totals["limits.disk"], nil
	}

	return 0, nil
}

// Check that we would not violate the project limits or restrictions if we
// were to commit the given instances and profiles.
func checkRestrictionsAndAggregateLimits(globalConfig *clusterConfig.Config, info *projectInfo) error {
	// List of config keys for which we need to check aggregate values
	// across all project instances.
	aggregateKeys := []string{}
	isRestricted := false

	for key, value := range info.Project.Config {
		if slices.Contains(allAggregateLimits, key) || strings.HasPrefix(key, projectLimitDiskPool) {
			aggregateKeys = append(aggregateKeys, key)
			continue
		}

		if key == "restricted" && shared.IsTrue(value) {
			isRestricted = true
			continue
		}
	}

	if len(aggregateKeys) == 0 && !isRestricted {
		return nil
	}

	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	instances, err := expandInstancesConfigAndDevices(globalConfigDump, info.Instances, info.Profiles)
	if err != nil {
		return err
	}

	info.Instances = instances

	err = checkAggregateLimits(info, aggregateKeys)
	if err != nil {
		return err
	}

	if isRestricted {
		err = checkRestrictions(info.Project, info.Instances, info.Profiles)
		if err != nil {
			return err
		}
	}

	return nil
}

func getAggregateLimits(info *projectInfo, aggregateKeys []string) (map[string]api.ProjectStateResource, error) {
	result := map[string]api.ProjectStateResource{}

	if len(aggregateKeys) == 0 {
		return result, nil
	}

	totals, err := getTotalsAcrossProjectEntities(info, aggregateKeys, true)
	if err != nil {
		return nil, err
	}

	for _, key := range aggregateKeys {
		max := int64(-1)
		limit := info.Project.Config[key]
		if limit != "" {
			keyName := key

			// Handle pool-specific limits.
			if strings.HasPrefix(key, projectLimitDiskPool) {
				keyName = "limits.disk"
			}

			parser := aggregateLimitConfigValueParsers[keyName]
			max, err = parser(info.Project.Config[key])
			if err != nil {
				return nil, err
			}
		}

		resource := api.ProjectStateResource{
			Usage: totals[key],
			Limit: max,
		}

		result[key] = resource
	}

	return result, nil
}

func checkAggregateLimits(info *projectInfo, aggregateKeys []string) error {
	if len(aggregateKeys) == 0 {
		return nil
	}

	totals, err := getTotalsAcrossProjectEntities(info, aggregateKeys, false)
	if err != nil {
		return fmt.Errorf("Failed getting usage of project entities: %w", err)
	}

	for _, key := range aggregateKeys {
		keyName := key

		// Handle pool-specific limits.
		if strings.HasPrefix(key, projectLimitDiskPool) {
			keyName = "limits.disk"
		}

		parser := aggregateLimitConfigValueParsers[keyName]
		max, err := parser(info.Project.Config[key])
		if err != nil {
			return err
		}

		if totals[key] > max {
			return fmt.Errorf("Reached maximum aggregate value %q for %q in project %q", info.Project.Config[key], key, info.Project.Name)
		}
	}

	return nil
}

// parseHostIDMapRange parse the supplied list of host ID map ranges into a idmap.IdmapEntry slice.
func parseHostIDMapRange(isUID bool, isGID bool, listValue string) ([]idmap.IdmapEntry, error) {
	var idmaps []idmap.IdmapEntry

	for _, listItem := range shared.SplitNTrimSpace(listValue, ",", -1, true) {
		rangeStart, rangeSize, err := validate.ParseUint32Range(listItem)
		if err != nil {
			return nil, err
		}

		idmaps = append(idmaps, idmap.IdmapEntry{
			Hostid:   int64(rangeStart),
			Maprange: int64(rangeSize),
			Isuid:    isUID,
			Isgid:    isGID,
			Nsid:     -1, // We don't have this as we are just parsing host IDs.
		})
	}

	return idmaps, nil
}

// Check that the project's restrictions are not violated across the given
// instances and profiles.
func checkRestrictions(proj api.Project, instances []api.Instance, profiles []api.Profile) error {
	containerConfigChecks := map[string]func(value string) error{}
	devicesChecks := map[string]func(value map[string]string) error{}

	allowContainerLowLevel := false
	allowVMLowLevel := false
	var allowedIDMapHostUIDs, allowedIDMapHostGIDs []idmap.IdmapEntry

	for i := range allRestrictions {
		// Check if this particular restriction is defined explicitly in the project config.
		// If not, use the default value. Assign to local var so it doesn't change to the default value of
		// another restriction by time check functions run.
		restrictionKey := i
		restrictionValue, ok := proj.Config[restrictionKey]
		if !ok {
			restrictionValue = allRestrictions[restrictionKey]
		}

		switch restrictionKey {
		case "restricted.containers.interception":
			for _, key := range allowableIntercept {
				containerConfigChecks[key] = func(instanceValue string) error {
					disabled := shared.IsFalseOrEmpty(instanceValue)

					if restrictionValue != "allow" && !disabled {
						return fmt.Errorf("Container syscall interception is forbidden")
					}

					return nil
				}
			}
		case "restricted.containers.nesting":
			containerConfigChecks["security.nesting"] = func(instanceValue string) error {
				if restrictionValue == "block" && shared.IsTrue(instanceValue) {
					return fmt.Errorf("Container nesting is forbidden")
				}

				return nil
			}

		case "restricted.containers.lowlevel":
			if restrictionValue == "allow" {
				allowContainerLowLevel = true
			}

		case "restricted.containers.privilege":
			containerConfigChecks["security.privileged"] = func(instanceValue string) error {
				if restrictionValue != "allow" && shared.IsTrue(instanceValue) {
					return fmt.Errorf("Privileged containers are forbidden")
				}

				return nil
			}

			containerConfigChecks["security.idmap.isolated"] = func(instanceValue string) error {
				if restrictionValue == "isolated" && shared.IsFalseOrEmpty(instanceValue) {
					return fmt.Errorf("Non-isolated containers are forbidden")
				}

				return nil
			}

		case "restricted.virtual-machines.lowlevel":
			if restrictionValue == "allow" {
				allowVMLowLevel = true
			}

		case "restricted.devices.unix-char":
			devicesChecks["unix-char"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Unix character devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.unix-block":
			devicesChecks["unix-block"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Unix block devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.unix-hotplug":
			devicesChecks["unix-hotplug"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Unix hotplug devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.infiniband":
			devicesChecks["infiniband"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Infiniband devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.gpu":
			devicesChecks["gpu"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("GPU devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.usb":
			devicesChecks["usb"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("USB devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.pci":
			devicesChecks["pci"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("PCI devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.proxy":
			devicesChecks["proxy"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Proxy devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.nic":
			devicesChecks["nic"] = func(device map[string]string) error {
				// Check if the NICs are allowed at all.
				switch restrictionValue {
				case "block":
					return fmt.Errorf("Network devices are forbidden")
				case "managed":
					if device["network"] == "" {
						return fmt.Errorf("Only managed network devices are allowed")
					}
				}

				// Check if the NIC's parent/network setting is allowed based on the
				// restricted.devices.nic and restricted.networks.access settings.
				if device["network"] != "" {
					if !project.NetworkAllowed(proj.Config, device["network"], true) {
						return fmt.Errorf("Network not allowed in project")
					}
				} else if device["parent"] != "" {
					if !project.NetworkAllowed(proj.Config, device["parent"], false) {
						return fmt.Errorf("Network not allowed in project")
					}
				}

				return nil
			}

		case "restricted.devices.disk":
			devicesChecks["disk"] = func(device map[string]string) error {
				// The root device is always allowed.
				if device["path"] == "/" && device["pool"] != "" {
					return nil
				}

				// Always allow the cloud-init config drive.
				if device["path"] == "" && device["source"] == "cloud-init:config" {
					return nil
				}

				switch restrictionValue {
				case "block":
					return fmt.Errorf("Disk devices are forbidden")
				case "managed":
					if device["pool"] == "" {
						return fmt.Errorf("Attaching disks not backed by a pool is forbidden")
					}

				case "allow":
					if device["pool"] == "" {
						allowed, _ := project.CheckRestrictedDevicesDiskPaths(proj.Config, device["source"])
						if !allowed {
							return fmt.Errorf("Disk source path %q not allowed", device["source"])
						}
					}
				}

				return nil
			}

		case "restricted.idmap.uid":
			var err error
			allowedIDMapHostUIDs, err = parseHostIDMapRange(true, false, restrictionValue)
			if err != nil {
				return fmt.Errorf("Failed parsing %q: %w", "restricted.idmap.uid", err)
			}

		case "restricted.idmap.gid":
			var err error
			allowedIDMapHostGIDs, err = parseHostIDMapRange(false, true, restrictionValue)
			if err != nil {
				return fmt.Errorf("Failed parsing %q: %w", "restricted.idmap.uid", err)
			}
		}
	}

	// Common config check logic between instances and profiles.
	entityConfigChecker := func(instType instancetype.Type, entityName string, config map[string]string) error {
		entityTypeLabel := instType.String()
		if instType == instancetype.Any {
			entityTypeLabel = "profile"
		}

		isContainerOrProfile := instType == instancetype.Container || instType == instancetype.Any
		isVMOrProfile := instType == instancetype.VM || instType == instancetype.Any

		for key, value := range config {
			if ((isContainerOrProfile && !allowContainerLowLevel) || (isVMOrProfile && !allowVMLowLevel)) && key == "raw.idmap" {
				// If the low-level raw.idmap is used check whether the raw.idmap host IDs
				// are allowed based on the project's allowed ID map Host UIDs and GIDs.
				idmaps, err := idmap.ParseRawIdmap(value)
				if err != nil {
					return err
				}

				for i, entry := range idmaps {
					if !entry.HostIDsCoveredBy(allowedIDMapHostUIDs, allowedIDMapHostGIDs) {
						return fmt.Errorf(`Use of low-level "raw.idmap" element %d on %s %q of project %q is forbidden`, i, entityTypeLabel, entityName, proj.Name)
					}
				}

				// Skip the other checks.
				continue
			}

			if isContainerOrProfile && !allowContainerLowLevel && isContainerLowLevelOptionForbidden(key) {
				return fmt.Errorf("Use of low-level config %q on %s %q of project %q is forbidden", key, entityTypeLabel, entityName, proj.Name)
			}

			if isVMOrProfile && !allowVMLowLevel && isVMLowLevelOptionForbidden(key) {
				return fmt.Errorf("Use of low-level config %q on %s %q of project %q is forbidden", key, entityTypeLabel, entityName, proj.Name)
			}

			var checker func(value string) error
			if isContainerOrProfile {
				checker = containerConfigChecks[key]
			}

			if checker == nil {
				continue
			}

			err := checker(value)
			if err != nil {
				return fmt.Errorf("Invalid value %q for config %q on %s %q of project %q: %w", value, key, instType, entityName, proj.Name, err)
			}
		}

		return nil
	}

	// Common devices check logic between instances and profiles.
	entityDevicesChecker := func(instType instancetype.Type, entityName string, devices map[string]map[string]string) error {
		entityTypeLabel := instType.String()
		if instType == instancetype.Any {
			entityTypeLabel = "profile"
		}

		for name, device := range devices {
			check, ok := devicesChecks[device["type"]]
			if !ok {
				continue
			}

			err := check(device)
			if err != nil {
				return fmt.Errorf("Invalid device %q on %s %q of project %q: %w", name, entityTypeLabel, entityName, proj.Name, err)
			}
		}
		return nil
	}

	for _, instance := range instances {
		instType, err := instancetype.New(instance.Type)
		if err != nil {
			return err
		}

		err = entityConfigChecker(instType, instance.Name, instance.Config)
		if err != nil {
			return err
		}

		err = entityDevicesChecker(instType, instance.Name, instance.Devices)
		if err != nil {
			return err
		}
	}

	for _, profile := range profiles {
		err := entityConfigChecker(instancetype.Any, profile.Name, profile.Config)
		if err != nil {
			return err
		}

		err = entityDevicesChecker(instancetype.Any, profile.Name, profile.Devices)
		if err != nil {
			return err
		}
	}

	return nil
}

var allAggregateLimits = []string{
	"limits.cpu",
	"limits.disk",
	"limits.memory",
	"limits.processes",
}

// allRestrictions lists all available 'restrict.*' config keys along with their default setting.
var allRestrictions = map[string]string{
	"restricted.backups":                   "block",
	"restricted.cluster.groups":            "",
	"restricted.cluster.target":            "block",
	"restricted.containers.nesting":        "block",
	"restricted.containers.interception":   "block",
	"restricted.containers.lowlevel":       "block",
	"restricted.containers.privilege":      "unprivileged",
	"restricted.virtual-machines.lowlevel": "block",
	"restricted.devices.unix-char":         "block",
	"restricted.devices.unix-block":        "block",
	"restricted.devices.unix-hotplug":      "block",
	"restricted.devices.infiniband":        "block",
	"restricted.devices.gpu":               "block",
	"restricted.devices.usb":               "block",
	"restricted.devices.pci":               "block",
	"restricted.devices.proxy":             "block",
	"restricted.devices.nic":               "managed",
	"restricted.devices.disk":              "managed",
	"restricted.devices.disk.paths":        "",
	"restricted.idmap.uid":                 "",
	"restricted.idmap.gid":                 "",
	"restricted.networks.access":           "",
	"restricted.snapshots":                 "block",
}

// allowableIntercept lists all syscall interception keys which may be allowed.
var allowableIntercept = []string{
	"security.syscalls.intercept.bpf",
	"security.syscalls.intercept.bpf.devices",
	"security.syscalls.intercept.mknod",
	"security.syscalls.intercept.mount",
	"security.syscalls.intercept.mount.fuse",
	"security.syscalls.intercept.setxattr",
	"security.syscalls.intercept.sysinfo",
}

// Return true if a low-level container option is forbidden.
func isContainerLowLevelOptionForbidden(key string) bool {
	if strings.HasPrefix(key, "security.syscalls.intercept") && !shared.ValueInSlice(key, allowableIntercept) {
		return true
	}

	if shared.ValueInSlice(key, []string{
		"boot.host_shutdown_timeout",
		"linux.kernel_modules",
		"linux.kernel_modules.load",
		"raw.apparmor",
		"raw.idmap",
		"raw.lxc",
		"raw.seccomp",
		"security.devlxd.images",
		"security.idmap.base",
		"security.idmap.size",
	}) {
		return true
	}

	return false
}

// Return true if a low-level VM option is forbidden.
func isVMLowLevelOptionForbidden(key string) bool {
	return shared.ValueInSlice(key, []string{
		"boot.host_shutdown_timeout",
		"limits.memory.hugepages",
		"raw.idmap",
		"raw.qemu",
	})
}

// AllowInstanceUpdate returns an error if any project-specific limit or
// restriction is violated when updating an existing instance.
func AllowInstanceUpdate(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName, instanceName string, clusterMemberName string, sysinfo *api.ClusterMemberSysInfo, req api.InstancePut, currentConfig map[string]string) error {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	inst := api.Instance{
		Name:   instanceName,
		Config: req.Config,
	}

	errors, err := CheckReservationsWithInstance(context.TODO(), tx, globalConfigDump, &inst, map[string]api.ClusterMemberSysInfo{clusterMemberName: *sysinfo})
	if err != nil {
		return err
	}

	err = errors[clusterMemberName]
	if err != nil {
		return fmt.Errorf("Reservation exceeded on cluster member %q: %w", clusterMemberName, err)
	}

	var updatedInstance *api.Instance

	info, err := fetchProject(globalConfigDump, tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// Change the instance being updated.
	for i, instance := range info.Instances {
		if instance.Name != instanceName {
			continue
		}

		info.Instances[i].Profiles = req.Profiles
		info.Instances[i].Config = req.Config
		info.Instances[i].Devices = req.Devices
		updatedInstance = &info.Instances[i]
	}

	instType, err := instancetype.New(updatedInstance.Type)
	if err != nil {
		return err
	}

	// Special case restriction checks on volatile.* keys, since we want to
	// detect if they were changed or added.
	err = checkRestrictionsOnVolatileConfig(
		info.Project, instType, updatedInstance.Name, req.Config, currentConfig, false)
	if err != nil {
		return err
	}

	err = checkRestrictionsAndAggregateLimits(globalConfig, info)
	if err != nil {
		return fmt.Errorf("Failed checking if instance update allowed: %w", err)
	}

	return nil
}

// AllowVolumeUpdate returns an error if any project-specific limit or
// restriction is violated when updating an existing custom volume.
func AllowVolumeUpdate(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName, volumeName string, req api.StorageVolumePut, currentConfig map[string]string) error {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	info, err := fetchProject(globalConfigDump, tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// If "limits.disk" is not set, there's nothing to do.
	if info.Project.Config["limits.disk"] == "" {
		return nil
	}

	// Change the volume being updated.
	for i, volume := range info.Volumes {
		if volume.Name != volumeName {
			continue
		}

		info.Volumes[i].Config = req.Config
	}

	err = checkRestrictionsAndAggregateLimits(globalConfig, info)
	if err != nil {
		return fmt.Errorf("Failed checking if volume update allowed: %w", err)
	}

	return nil
}

// AllowProfileUpdate checks that project limits and restrictions are not
// violated when changing a profile.
func AllowProfileUpdate(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName, profileName string, req api.ProfilePut) error {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	info, err := fetchProject(globalConfigDump, tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// Change the profile being updated.
	for i, profile := range info.Profiles {
		if profile.Name != profileName {
			continue
		}

		info.Profiles[i].Config = req.Config
		info.Profiles[i].Devices = req.Devices
	}

	err = checkRestrictionsAndAggregateLimits(globalConfig, info)
	if err != nil {
		return fmt.Errorf("Failed checking if profile update allowed: %w", err)
	}

	return nil
}

// AllowProjectUpdate checks the new config to be set on a project is valid.
func AllowProjectUpdate(globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string, config map[string]string, changed []string) error {
	var globalConfigDump map[string]any
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	info, err := fetchProject(globalConfigDump, tx, projectName, false)
	if err != nil {
		return err
	}

	info.Instances, err = expandInstancesConfigAndDevices(globalConfigDump, info.Instances, info.Profiles)
	if err != nil {
		return err
	}

	// List of keys that need to check aggregate values across all project
	// instances.
	aggregateKeys := []string{}

	for _, key := range changed {
		if strings.HasPrefix(key, "restricted.") {
			project := api.Project{
				Name:   projectName,
				Config: config,
			}

			err := checkRestrictions(project, info.Instances, info.Profiles)
			if err != nil {
				return fmt.Errorf("Conflict detected when changing %q in project %q: %w", key, projectName, err)
			}

			continue
		}

		switch key {
		case "limits.instances":
			err := validateTotalInstanceCountLimit(info.Instances, config[key], projectName)
			if err != nil {
				return fmt.Errorf("Can't change limits.instances in project %q: %w", projectName, err)
			}

		case "limits.containers":
			fallthrough
		case "limits.virtual-machines":
			err := validateInstanceCountLimit(info.Instances, key, config[key], projectName)
			if err != nil {
				return fmt.Errorf("Can't change %q in project %q: %w", key, projectName, err)
			}

		case "limits.processes":
			fallthrough
		case "limits.cpu":
			fallthrough
		case "limits.memory":
			fallthrough
		case "limits.disk":
			aggregateKeys = append(aggregateKeys, key)
		}
	}

	if len(aggregateKeys) > 0 {
		totals, err := getTotalsAcrossProjectEntities(info, aggregateKeys, false)
		if err != nil {
			return err
		}

		for _, key := range aggregateKeys {
			err := validateAggregateLimit(totals, key, config[key])
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Check that limits.instances, i.e. the total limit of containers/virtual machines allocated
// to the user is equal to or above the current count.
func validateTotalInstanceCountLimit(instances []api.Instance, value, project string) error {
	if value == "" {
		return nil
	}

	limit, err := strconv.Atoi(value)
	if err != nil {
		return err
	}

	count := len(instances)

	if limit < count {
		return fmt.Errorf(`"limits.instances" is too low: there currently are %d total instances in project %q`, count, project)
	}

	return nil
}

// Check that limits.containers or limits.virtual-machines is equal or above
// the current count.
func validateInstanceCountLimit(instances []api.Instance, key, value, project string) error {
	if value == "" {
		return nil
	}

	instanceType := countConfigInstanceType[key]
	limit, err := strconv.Atoi(value)
	if err != nil {
		return err
	}

	dbType, err := instancetype.New(string(instanceType))
	if err != nil {
		return err
	}

	count := 0
	for _, instance := range instances {
		if instance.Type == dbType.String() {
			count++
		}
	}

	if limit < count {
		return fmt.Errorf(`%q is too low: there currently are %d instances of type %s in project %q`, key, count, instanceType, project)
	}

	return nil
}

var countConfigInstanceType = map[string]api.InstanceType{
	"limits.containers":       api.InstanceTypeContainer,
	"limits.virtual-machines": api.InstanceTypeVM,
}

// Validates an aggregate limit, checking that the new value is not below the
// current total amount.
func validateAggregateLimit(totals map[string]int64, key, value string) error {
	if value == "" {
		return nil
	}

	keyName := key

	// Handle pool-specific limits.
	if strings.HasPrefix(key, projectLimitDiskPool) {
		keyName = "limits.disk"
	}

	parser := aggregateLimitConfigValueParsers[keyName]
	limit, err := parser(value)
	if err != nil {
		return fmt.Errorf("Invalid value %q for limit %q: %w", value, key, err)
	}

	total := totals[key]
	if limit < total {
		keyName := key

		// Handle pool-specific limits.
		if strings.HasPrefix(key, projectLimitDiskPool) {
			keyName = "limits.disk"
		}

		printer := aggregateLimitConfigValuePrinters[keyName]
		return fmt.Errorf("%q is too low: current total is %q", key, printer(total))
	}

	return nil
}

// Return true if the project has some limits or restrictions set.
func projectHasLimitsOrRestrictions(project api.Project) bool {
	for k, v := range project.Config {
		if strings.HasPrefix(k, "limits.") {
			return true
		}

		if k == "restricted" && shared.IsTrue(v) {
			return true
		}
	}

	return false
}

// Hold information associated with the project, such as profiles and
// instances.
type projectInfo struct {
	Project   api.Project
	Profiles  []api.Profile
	Instances []api.Instance
	Volumes   []db.StorageVolumeArgs

	// poolName: driverName
	StoragePoolDrivers map[string]string
}

// Fetch the given project from the database along with its profiles, instances
// and possibly custom volumes.
//
// If the skipIfNoLimits flag is true, then profiles, instances and volumes
// won't be loaded if the profile has no limits set on it, and nil will be
// returned.
func fetchProject(globalConfig map[string]any, tx *db.ClusterTx, projectName string, skipIfNoLimits bool) (*projectInfo, error) {
	ctx := context.Background()
	dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return nil, fmt.Errorf("Fetch project database object: %w", err)
	}

	project, err := dbProject.ToAPI(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	if skipIfNoLimits && !projectHasLimitsOrRestrictions(*project) {
		return nil, nil
	}

	profilesFilter := cluster.ProfileFilter{}

	// If the project has the profiles feature enabled, we use its own
	// profiles to expand the instances configs, otherwise we use the
	// profiles from the default project.
	defaultProject := api.ProjectDefaultName
	if projectName == api.ProjectDefaultName || shared.IsTrue(project.Config["features.profiles"]) {
		profilesFilter.Project = &projectName
	} else {
		profilesFilter.Project = &defaultProject
	}

	dbProfiles, err := cluster.GetProfiles(ctx, tx.Tx(), profilesFilter)
	if err != nil {
		return nil, fmt.Errorf("Fetch profiles from database: %w", err)
	}

	profiles := make([]api.Profile, 0, len(dbProfiles))
	for _, profile := range dbProfiles {
		apiProfile, err := profile.ToAPI(ctx, tx.Tx())
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, *apiProfile)
	}

	drivers, err := tx.GetStoragePoolDrivers(ctx)
	if err != nil {
		return nil, fmt.Errorf("Fetch storage pools from database: %w", err)
	}

	dbInstances, err := cluster.GetInstances(ctx, tx.Tx(), cluster.InstanceFilter{Project: &projectName})
	if err != nil {
		return nil, fmt.Errorf("Fetch project instances from database: %w", err)
	}

	instances := make([]api.Instance, 0, len(dbInstances))
	for _, instance := range dbInstances {
		apiInstance, err := instance.ToAPI(ctx, tx.Tx(), globalConfig)
		if err != nil {
			return nil, fmt.Errorf("Failed to get API data for instance %q in project %q: %w", instance.Name, instance.Project, err)
		}

		instances = append(instances, *apiInstance)
	}

	volumes, err := tx.GetCustomVolumesInProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("Fetch project custom volumes from database: %w", err)
	}

	info := &projectInfo{
		Project:   *project,
		Profiles:  profiles,
		Instances: instances,
		Volumes:   volumes,

		StoragePoolDrivers: drivers,
	}

	return info, nil
}

// Expand the configuration and devices of the given instances, taking the give
// project profiles into account.
func expandInstancesConfigAndDevices(globalConfig map[string]any, instances []api.Instance, profiles []api.Profile) ([]api.Instance, error) {
	expandedInstances := make([]api.Instance, len(instances))

	// Index of all profiles by name.
	profilesByName := map[string]api.Profile{}
	for _, profile := range profiles {
		profilesByName[profile.Name] = profile
	}

	for i, instance := range instances {
		apiProfiles := make([]api.Profile, len(instance.Profiles))

		for j, name := range instance.Profiles {
			profile := profilesByName[name]
			apiProfiles[j] = profile
		}

		expandedInstances[i] = instance
		expandedInstances[i].Config = instancetype.ExpandInstanceConfig(globalConfig, instance.Config, apiProfiles)
		expandedInstances[i].Devices = instancetype.ExpandInstanceDevices(deviceconfig.NewDevices(instance.Devices), apiProfiles).CloneNative()
	}

	return expandedInstances, nil
}

// Sum of the effective values for the given limits across all project
// entities (instances and custom volumes).
func getTotalsAcrossProjectEntities(info *projectInfo, keys []string, skipUnset bool) (map[string]int64, error) {
	totals := map[string]int64{}

	for _, key := range keys {
		totals[key] = 0
		if key == "limits.disk" || strings.HasPrefix(key, projectLimitDiskPool) {
			poolName := ""
			fields := strings.SplitN(key, projectLimitDiskPool, 2)
			if len(fields) == 2 {
				poolName = fields[1]
			}

			for _, volume := range info.Volumes {
				if poolName != "" && volume.PoolName != poolName {
					continue
				}

				value, ok := volume.Config["size"]
				if !ok {
					if skipUnset {
						continue
					}

					return nil, fmt.Errorf(`Custom volume %q in project %q has no "size" config set`, volume.Name, info.Project.Name)
				}

				limit, err := units.ParseByteSizeString(value)
				if err != nil {
					return nil, fmt.Errorf(`Parse "size" for custom volume %q in project %q: %w`, volume.Name, info.Project.Name, err)
				}

				totals[key] += limit
			}
		}
	}

	for _, instance := range info.Instances {
		limits, err := getInstanceLimits(instance, keys, skipUnset, info.StoragePoolDrivers)
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			totals[key] += limits[key]
		}
	}

	return totals, nil
}

// Return the effective instance-level values for the limits with the given keys.
func getInstanceLimits(instance api.Instance, keys []string, skipUnset bool, storagePoolDrivers map[string]string) (map[string]int64, error) {
	var err error
	limits := map[string]int64{}

	for _, key := range keys {
		var limit int64
		keyName := key

		// Handle pool-specific limits.
		if strings.HasPrefix(key, projectLimitDiskPool) {
			keyName = "limits.disk"
		}

		parser := aggregateLimitConfigValueParsers[keyName]

		if key == "limits.disk" || strings.HasPrefix(key, projectLimitDiskPool) {
			poolName := ""
			fields := strings.SplitN(key, projectLimitDiskPool, 2)
			if len(fields) == 2 {
				poolName = fields[1]
			}

			_, device, err := instancetype.GetRootDiskDevice(instance.Devices)
			if err != nil {
				return nil, fmt.Errorf("Failed getting root disk device for instance %q in project %q: %w", instance.Name, instance.Project, err)
			}

			if poolName != "" && device["pool"] != poolName {
				continue
			}

			value, ok := device["size"]
			if !ok || value == "" {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf(`Instance %q in project %q has no "size" config set on the root device either directly or via a profile`, instance.Name, instance.Project)
			}

			limit, err = parser(value)
			if err != nil {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf("Failed parsing %q for instance %q in project %q", key, instance.Name, instance.Project)
			}

			// Add size.state accounting for VM root disks.
			if instance.Type == instancetype.VM.String() {
				sizeStateValue, ok := device["size.state"]
				if !ok {
					poolName, ok := device["pool"]
					if !ok {
						return nil, fmt.Errorf("Root disk device for %q missing pool", instance.Name)
					}

					driverName, ok := storagePoolDrivers[poolName]
					if !ok {
						return nil, fmt.Errorf("No driver found for pool %q", poolName)
					}

					sizeStateValue, err = drivers.DefaultVMBlockFilesystemSize(driverName)
					if err != nil {
						return nil, err
					}
				}

				sizeStateLimit, err := parser(sizeStateValue)
				if err != nil {
					if skipUnset {
						continue
					}

					return nil, fmt.Errorf("Failed parsing %q for instance %q in project %q", "size.state", instance.Name, instance.Project)
				}

				limit += sizeStateLimit
			}
		} else {
			value, ok := instance.Config[key]
			if !ok || value == "" {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf("Instance %q in project %q has no %q config, either directly or via a profile", instance.Name, instance.Project, key)
			}

			limit, err = parser(value)
			if err != nil {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf("Failed parsing %q for instance %q in project %q", key, instance.Name, instance.Project)
			}
		}

		limits[key] = limit
	}

	return limits, nil
}

var aggregateLimitConfigValueParsers = map[string]func(string) (int64, error){
	"limits.memory": func(value string) (int64, error) {
		if strings.HasSuffix(value, "%") {
			return -1, fmt.Errorf("Value can't be a percentage")
		}

		return units.ParseByteSizeString(value)
	},
	"limits.processes": func(value string) (int64, error) {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return -1, err
		}

		return int64(limit), nil
	},
	"limits.cpu": func(value string) (int64, error) {
		if strings.Contains(value, ",") || strings.Contains(value, "-") {
			return -1, fmt.Errorf("CPUs can't be pinned if project limits are used")
		}

		limit, err := strconv.Atoi(value)
		if err != nil {
			return -1, err
		}

		return int64(limit), nil
	},
	"limits.disk": func(value string) (int64, error) {
		return units.ParseByteSizeString(value)
	},
}

var aggregateLimitConfigValuePrinters = map[string]func(int64) string{
	"limits.memory": func(limit int64) string {
		return units.GetByteSizeStringIEC(limit, 1)
	},
	"limits.processes": func(limit int64) string {
		return fmt.Sprintf("%d", limit)
	},
	"limits.cpu": func(limit int64) string {
		return fmt.Sprintf("%d", limit)
	},
	"limits.disk": func(limit int64) string {
		return units.GetByteSizeStringIEC(limit, 1)
	},
}

// Return true if particular restriction in project is violated.
func projectHasRestriction(project *api.Project, restrictionKey string, blockValue string) bool {
	if shared.IsFalseOrEmpty(project.Config["restricted"]) {
		return false
	}

	restrictionValue, ok := project.Config[restrictionKey]
	if !ok {
		restrictionValue = allRestrictions[restrictionKey]
	}

	if restrictionValue == blockValue {
		return true
	}

	return false
}

// CheckClusterTargetRestriction check if user is allowed to use cluster member targeting.
func CheckClusterTargetRestriction(authorizer auth.Authorizer, r *http.Request, project *api.Project, targetFlag string) error {
	if projectHasRestriction(project, "restricted.cluster.target", "block") && targetFlag != "" {
		// Allow server administrators to move instances around even when restricted (node evacuation, ...)
		err := authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanOverrideClusterTargetRestriction)
		if err != nil && auth.IsDeniedError(err) {
			return api.StatusErrorf(http.StatusForbidden, "This project doesn't allow cluster member targeting")
		} else if err != nil {
			return err
		}
	}

	return nil
}

func coalesceOverrides(config map[string]db.OverridableConfig) map[string]string {
	overridden := make(map[string]string)
	for key, cfg := range config {
		if cfg.ClusterValue.Valid {
			overridden[key] = cfg.ClusterValue.String
		}

		if cfg.MemberValue.Valid {
			overridden[key] = cfg.MemberValue.String
		}
	}

	return overridden
}

// AllowClusterUpdate returns err if replacing the cluster configuration with
// newConfig would violate any limits.reserve.* configuration.
func AllowClusterUpdate(ctx context.Context, tx *db.ClusterTx, sysinfo map[string]api.ClusterMemberSysInfo, newConfig map[string]any) error {
	membersConfig, err := tx.GetMemberConfigWithGlobalDefault(ctx, []string{}, allReservations)
	if err != nil {
		return err
	}

	requiredLimits := make(map[string][]string, len(sysinfo))

	// Modify membersConfig with any new global config options
	for reservationKey, newValue := range newConfig {
		if !slices.Contains(allReservations, reservationKey) {
			continue
		}

		newValue, ok := newValue.(string)
		if !ok {
			return fmt.Errorf("Invalid type for %q", reservationKey)
		}

		for memberName := range sysinfo {
			// Ensure that every member has an entry in membersConfig
			memberConfig, hasMember := membersConfig[memberName]
			if !hasMember {
				memberConfig = make(map[string]db.OverridableConfig)
			}

			overridable := memberConfig[reservationKey]
			if newValue != "" {
				overridable.ClusterValue.String = newValue
				overridable.ClusterValue.Valid = true
			}

			if !overridable.ClusterValue.Valid && !overridable.MemberValue.Valid {
				delete(memberConfig, reservationKey)
			} else {
				limitKey, err := reservationLimit(reservationKey)
				if err != nil {
					return err
				}

				requiredLimits[memberName] = append(requiredLimits[memberName], limitKey)
				memberConfig[reservationKey] = overridable
			}

			membersConfig[memberName] = memberConfig
		}
	}

	if len(requiredLimits) == 0 {
		return nil
	}

	aggregates, err := getClusterMemberAggregateLimits(ctx, tx, newConfig, requiredLimits)
	if err != nil {
		return err
	}

	// Validate each cluster member's new config
	for clusterMemberName, config := range membersConfig {
		memberInstanceLimits, hasClusterMember := instanceLimits[clusterMemberName]
		if !hasClusterMember {
			// No instances on this cluster member
			continue
		}

		aggregateLimits, err := getClusterMemberAggregateLimits(memberInstanceLimits, clusterMemberName, config)
		if err != nil {
			return err
		}

		reservations := coalesceOverrides(config)

		memberSysInfo, hasSysInfo := sysinfo[clusterMemberName]
		if !hasSysInfo {
			return fmt.Errorf("Missing sysinfo for cluster member %q", clusterMemberName)
		}

		err = checkClusterMemberReservations(reservations, aggregateLimits, &memberSysInfo)
		if err != nil {
			return err
		}
	}

	return nil
}

// AllowClusterMemberUpdate returns err if replacing the cluster member's
// configuration with newConfig would violate any limits.reserve.* configuration.
func AllowClusterMemberUpdate(ctx context.Context, tx *db.ClusterTx, globalConfig map[string]any, clusterMemberName string, sysinfo *api.ClusterMemberSysInfo, newConfig map[string]string) error {
	membersConfig, err := tx.GetMemberConfigWithGlobalDefault(ctx, []string{clusterMemberName}, allReservations)
	if err != nil {
		return err
	}

	memberConfig := membersConfig[clusterMemberName]
	if memberConfig == nil {
		memberConfig = make(map[string]db.OverridableConfig)
	}

	// update the existing config
	for reservationKey, newValue := range newConfig {
		if !slices.Contains(allReservations, reservationKey) {
			continue
		}

		overridable := memberConfig[reservationKey]
		if newValue != "" {
			overridable.MemberValue.String = newValue
			overridable.MemberValue.Valid = true
		}

		if !overridable.MemberValue.Valid && !overridable.ClusterValue.Valid {
			delete(memberConfig, reservationKey)
		} else {
			memberConfig[reservationKey] = overridable
		}
	}

	reservations := coalesceOverrides(memberConfig)

	// No reservations are set for this cluster member so skip checking them
	if len(reservations) == 0 {
		return nil
	}

	membersConfig[clusterMemberName] = memberConfig
	requiredLimits, err := getLimits(membersConfig)
	if err != nil {
		return err
	}

	clusterAggregateLimits, err := getClusterMemberAggregateLimits(ctx, tx, globalConfig, requiredLimits)
	if err != nil {
		return err
	}

	aggregateLimits, hasClusterMember := clusterAggregateLimits[clusterMemberName]
	if !hasClusterMember {
		// No instances on this cluster member
		return nil
	}

	return checkClusterMemberReservations(reservations, aggregateLimits, sysinfo)
}

// AllowBackupCreation returns an error if any project-specific restriction is violated
// when creating a new backup in a project.
func AllowBackupCreation(tx *db.ClusterTx, projectName string) error {
	ctx := context.Background()
	dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return err
	}

	project, err := dbProject.ToAPI(ctx, tx.Tx())
	if err != nil {
		return err
	}

	if projectHasRestriction(project, "restricted.backups", "block") {
		return fmt.Errorf("Project %q doesn't allow for backup creation", projectName)
	}

	return nil
}

// AllowSnapshotCreation returns an error if any project-specific restriction is violated
// when creating a new snapshot in a project.
func AllowSnapshotCreation(p *api.Project) error {
	if projectHasRestriction(p, "restricted.snapshots", "block") {
		return fmt.Errorf("Project %q doesn't allow for snapshot creation", p.Name)
	}

	return nil
}

// GetRestrictedClusterGroups returns a slice of restricted cluster groups for the given project.
func GetRestrictedClusterGroups(p *api.Project) []string {
	return shared.SplitNTrimSpace(p.Config["restricted.cluster.groups"], ",", -1, true)
}

// AllowClusterMember returns nil if the given project is allowed to use the cluster member.
func AllowClusterMember(p *api.Project, member *db.NodeInfo) error {
	clusterGroupsAllowed := GetRestrictedClusterGroups(p)

	if shared.IsTrue(p.Config["restricted"]) && len(clusterGroupsAllowed) > 0 {
		for _, memberGroupName := range member.Groups {
			if shared.ValueInSlice(memberGroupName, clusterGroupsAllowed) {
				return nil
			}
		}

		return fmt.Errorf("Project isn't allowed to use this cluster member: %q", member.Name)
	}

	return nil
}

// AllowClusterGroup returns nil if the given project is allowed to use the cluster groupName.
func AllowClusterGroup(p *api.Project, groupName string) error {
	clusterGroupsAllowed := GetRestrictedClusterGroups(p)

	// Skip the check if the project is not restricted
	if shared.IsFalseOrEmpty(p.Config["restricted"]) {
		return nil
	}

	if len(clusterGroupsAllowed) > 0 && !shared.ValueInSlice(groupName, clusterGroupsAllowed) {
		return fmt.Errorf("Project isn't allowed to use this cluster group: %q", groupName)
	}

	return nil
}

// CheckTargetMember checks if the given targetMemberName is present in allMembers
// and is allowed for the project.
// If the target member is allowed it returns the resolved node information.
func CheckTargetMember(p *api.Project, targetMemberName string, allMembers []db.NodeInfo) (*db.NodeInfo, error) {
	// Find target member.
	for _, potentialMember := range allMembers {
		if potentialMember.Name == targetMemberName {
			// If restricted groups are specified then check member is in at least one of them.
			err := AllowClusterMember(p, &potentialMember)
			if err != nil {
				return nil, api.StatusErrorf(http.StatusForbidden, "%w", err)
			}

			return &potentialMember, nil
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Cluster member %q not found", targetMemberName)
}

// CheckTargetGroup checks if the given groupName is allowed for the project.
func CheckTargetGroup(ctx context.Context, tx *db.ClusterTx, p *api.Project, groupName string) error {
	// If restricted groups are specified then check the requested group is in the list.
	err := AllowClusterGroup(p, groupName)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "%w", err)
	}

	// Check if the target group exists.
	targetGroupExists, err := cluster.ClusterGroupExists(ctx, tx.Tx(), groupName)
	if err != nil {
		return err
	}

	if !targetGroupExists {
		return api.StatusErrorf(http.StatusBadRequest, "Cluster group %q doesn't exist", groupName)
	}

	return nil
}

// CheckTarget checks if the given cluster target (member or group) is allowed.
// If target is a cluster member and is found in allMembers it returns the resolved node information object.
// If target is a cluster group it returns the cluster group name.
// In case of error, neither node information nor cluster group name gets returned.
func CheckTarget(ctx context.Context, authorizer auth.Authorizer, r *http.Request, tx *db.ClusterTx, p *api.Project, target string, allMembers []db.NodeInfo) (*db.NodeInfo, string, error) {
	targetMemberName, targetGroupName := shared.TargetDetect(target)

	// Check manual cluster member targeting restrictions.
	err := CheckClusterTargetRestriction(authorizer, r, p, target)
	if err != nil {
		return nil, "", err
	}

	if targetMemberName != "" {
		member, err := CheckTargetMember(p, targetMemberName, allMembers)
		if err != nil {
			return nil, "", err
		}

		return member, "", nil
	} else if targetGroupName != "" {
		err := CheckTargetGroup(ctx, tx, p, targetGroupName)
		if err != nil {
			return nil, "", err
		}

		return nil, targetGroupName, nil
	}

	return nil, "", nil
}

func parseLimit(limit string, val string) (int64, error) {
	if val == "" {
		return 0, fmt.Errorf("%q was not configured for instance or profile", limit)
	}

	parser, hasParser := aggregateLimitConfigValueParsers[limit]
	if !hasParser {
		return 0, fmt.Errorf("Missing parser for %q", limit)
	}

	value, err := parser(val)
	if err != nil {
		return 0, fmt.Errorf("Parse %q: %w", limit, err)
	}

	return value, nil
}

// requiredLimits is a map from clusterMemberName -> limits where limits are the
// limit keys (limits.{cpu,memory}) that are required for that cluster member.
func getClusterMemberAggregateLimits(ctx context.Context, tx *db.ClusterTx, globalConfig map[string]any, requiredLimits map[string][]string) (map[string]map[string]int64, error) {
	filters := []cluster.InstanceFilter{}
	for clusterMemberName := range requiredLimits {
		filters = append(filters, cluster.InstanceFilter{Node: &clusterMemberName})
	}

	aggregates := make(map[string]map[string]int64)

	instanceLoad := func(inst db.InstanceArgs, project api.Project) error {
		_, clusterMemberSeen := aggregates[inst.Node]
		if !clusterMemberSeen {
			aggregates[inst.Node] = make(map[string]int64)
		}

		instConfig := instancetype.ExpandInstanceConfig(globalConfig, inst.Config, inst.Profiles)

		for _, limit := range requiredLimits[inst.Node] {
			value, err := parseLimit(limit, instConfig[limit])
			if err != nil {
				return fmt.Errorf("Parse %q for instance %q: %w", limit, inst.Name, err)
			}

			aggregates[inst.Node][limit] += value
		}

		return nil
	}

	err := tx.InstanceList(ctx, instanceLoad, filters...)
	return aggregates, err
}

var allReservations = []string{
	"limits.reserve.cpu",
	"limits.reserve.memory",
}

func reservationLimit(reservation string) (string, error) {
	switch reservation {
	case "limits.reserve.cpu":
		return "limits.cpu", nil
	case "limits.reserve.memory":
		return "limits.memory", nil
	default:
		return "", fmt.Errorf("Invalid reservation key %q", reservation)
	}
}

func getLimits(reservations map[string]map[string]db.OverridableConfig) (map[string][]string, error) {
	limits := map[string][]string{}
	for clusterMemberName, clusterMemberReservations := range reservations {
		for reservationKey := range clusterMemberReservations {
			limit, err := reservationLimit(reservationKey)
			if err != nil {
				return nil, err
			}

			limits[clusterMemberName] = append(limits[clusterMemberName], limit)
		}
	}

	return limits, nil
}

// CheckReservationsWithInstance returns a map of clusterMemberName -> error.
// sysinfo is a map of clusterMemberName -> sysinfo.
// An entry will be present in the returned map for every cluster member whose
// limits.reserve.* would prevent `instance` from being created.
func CheckReservationsWithInstance(ctx context.Context, tx *db.ClusterTx, globalConfig map[string]any, instance *api.Instance, sysinfo map[string]api.ClusterMemberSysInfo) (map[string]error, error) {
	clusterMembers := make([]string, 0, len(sysinfo))
	for clusterMemberName := range sysinfo {
		clusterMembers = append(clusterMembers, clusterMemberName)
	}

	clusterOverrides, err := tx.GetMemberConfigWithGlobalDefault(ctx, clusterMembers, allReservations)
	if err != nil {
		return nil, err
	}

	// No reservations are set for any cluster member so skip the aggregates query
	if len(clusterOverrides) == 0 {
		return nil, nil
	}

	requiredLimits, err := getLimits(clusterOverrides)
	if err != nil {
		return nil, err
	}

	clusterAggregateLimits, err := getClusterMemberAggregateLimits(ctx, tx, globalConfig, requiredLimits)
	if err != nil {
		return nil, err
	}

	errors := make(map[string]error)
	for clusterMemberName, overrides := range clusterOverrides {
		aggregateLimits, hasClusterMember := clusterAggregateLimits[clusterMemberName]
		if !hasClusterMember {
			aggregateLimits = make(map[string]int64, len(requiredLimits[clusterMemberName]))
		}

		for _, limit := range requiredLimits[clusterMemberName] {
			value, err := parseLimit(limit, instance.Config[limit])
			if err != nil {
				errors[clusterMemberName] = err
				continue
			}

			aggregateLimits[limit] += value
		}

		reservations := coalesceOverrides(overrides)

		memberSysInfo := sysinfo[clusterMemberName]
		err = checkClusterMemberReservations(reservations, aggregateLimits, &memberSysInfo)
		if err != nil {
			errors[clusterMemberName] = err
		}
	}

	return errors, nil
}

// effectiveReservations is reservationKey -> effectiveReservation
// aggregateLimits is limitKey -> aggregate value for cluster member
func checkClusterMemberReservations(effectiveReservations map[string]string, aggregateLimits map[string]int64, sysinfo *api.ClusterMemberSysInfo) error {
	available := map[string]uint64{
		"limits.reserve.cpu":    sysinfo.CPUThreads,
		"limits.reserve.memory": sysinfo.TotalRAM,
	}

	for reservationKey, reservation := range effectiveReservations {
		limitKey, err := reservationLimit(reservationKey)
		if err != nil {
			return err
		}

		reservation, err := aggregateLimitConfigValueParsers[limitKey](reservation)
		if err != nil {
			return err
		}

		if int64(available[reservationKey]) < reservation+aggregateLimits[limitKey] {
			return fmt.Errorf("%s exceeded", reservationKey)
		}
	}

	return nil
}
