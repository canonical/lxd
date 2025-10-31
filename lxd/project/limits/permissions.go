package limits

import (
	"context"
	"errors"
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

const (
	// projectLimitDiskPool is the prefix used for pool-specific disk limits.
	projectLimitDiskPool string = "limits.disk.pool."
	// projectLimitNetworkUplinkIP4 is the prefix used for network-specific uplink IPv4 IP limits.
	projectLimitNetworkUplinkIP4 string = "limits.networks.uplink_ips.ipv4."
	// projectLimitNetworkUplinkIP6 is the prefix used for network-specific uplink IPv6 IP limits.
	projectLimitNetworkUplinkIP6 string = "limits.networks.uplink_ips.ipv6."
)

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
func AllowInstanceCreation(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string, req api.InstancesPost) error {
	info, err := fetchProject(ctx, tx, projectName, true)
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
	instance := api.Instance{
		Name:    req.Name,
		Project: projectName,
	}

	instance.SetWritable(req.InstancePut)
	info.Instances = append(info.Instances, instance)

	// Allow stripping volatile keys if dealing with a copy or migration.
	strip := slices.Contains([]string{api.SourceTypeCopy, api.SourceTypeMigration}, req.Source.Type)

	// Special case restriction checks on volatile.* keys.
	err = checkRestrictionsOnVolatileConfig(
		info.Project, instanceType, req.Name, req.Config, map[string]string{}, strip)
	if err != nil {
		return err
	}

	err = checkInstanceRestrictionsAndAggregateLimits(globalConfig, tx, info)
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
		if slices.Contains([]string{"volatile.apply_template", "volatile.base_image", "volatile.last_state.power"}, key) {
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
func AllowVolumeCreation(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string, poolName string, req api.StorageVolumesPost) error {
	info, err := fetchProject(ctx, tx, projectName, true)
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

	err = checkInstanceRestrictionsAndAggregateLimits(globalConfig, tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if volume creation allowed: %w", err)
	}

	return nil
}

// GetImageSpaceBudget returns how much disk space is left in the given project
// for writing images.
//
// If no limit is in place, return -1.
func GetImageSpaceBudget(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string) (int64, error) {
	var globalConfigDump map[string]string
	if globalConfig != nil {
		globalConfigDump = globalConfig.Dump()
	}

	info, err := fetchProject(ctx, tx, projectName, true)
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
// This function does not check the number of created instances. Limits associated
// to the project-wide instance count should be checked separately where needed.
func checkInstanceRestrictionsAndAggregateLimits(globalConfig *clusterConfig.Config, tx *db.ClusterTx, info *projectInfo) error {
	// List of config keys for which we need to check aggregate values
	// across all project instances.
	aggregateKeys := []string{}
	isRestricted := false

	for key, value := range info.Project.Config {
		// Check that the key is a known limits key and the value is not empty.
		if value != "" && (slices.Contains(allInstanceAggregateLimits, key) || strings.HasPrefix(key, projectLimitDiskPool)) {
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

	var globalConfigDump map[string]string
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
		err = checkInstanceRestrictions(info.Project, info.Instances, info.Profiles)
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
		maxValue := int64(-1)
		limit := info.Project.Config[key]
		if limit != "" {
			keyName := key

			// Handle pool-specific limits.
			if strings.HasPrefix(key, projectLimitDiskPool) {
				keyName = "limits.disk"
			}

			parser := aggregateLimitConfigValueParsers[keyName]
			maxValue, err = parser(info.Project.Config[key])
			if err != nil {
				return nil, err
			}
		}

		resource := api.ProjectStateResource{
			Usage: totals[key],
			Limit: maxValue,
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
		maxValue, err := parser(info.Project.Config[key])
		if err != nil {
			return err
		}

		if totals[key] > maxValue {
			return fmt.Errorf("Reached maximum aggregate value %q for %q in project %q", info.Project.Config[key], key, info.Project.Name)
		}
	}

	return nil
}

// parseHostIDMapRange parse the supplied list of host ID map ranges into a idmap.IdmapEntry slice.
func parseHostIDMapRange(isUID bool, isGID bool, listValue string) ([]idmap.IdmapEntry, error) {
	mapRanges := shared.SplitNTrimSpace(listValue, ",", -1, true)
	idmaps := make([]idmap.IdmapEntry, 0, len(mapRanges))

	for _, listItem := range mapRanges {
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
func checkInstanceRestrictions(proj api.Project, instances []api.Instance, profiles []api.Profile) error {
	containerConfigChecks := map[string]func(value string) error{}
	devicesChecks := map[string][]func(value map[string]string) error{}

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
						return errors.New("Container syscall interception is forbidden")
					}

					return nil
				}
			}
		case "restricted.containers.nesting":
			containerConfigChecks["security.nesting"] = func(instanceValue string) error {
				if restrictionValue == "block" && shared.IsTrue(instanceValue) {
					return errors.New("Container nesting is forbidden")
				}

				return nil
			}

		case "restricted.containers.lowlevel":
			if restrictionValue == "allow" {
				allowContainerLowLevel = true
			}

			// Add check for valid usage of acceleration.parent setting.
			devicesChecks["nic"] = append(devicesChecks["nic"], func(device map[string]string) error {
				_, accelerationParentUsed := device["acceleration.parent"]
				if accelerationParentUsed && !allowContainerLowLevel {
					return errors.New(`Use of low-level "acceleration.parent" NIC option forbidden`)
				}

				return nil
			})

		case "restricted.containers.privilege":
			containerConfigChecks["security.privileged"] = func(instanceValue string) error {
				if restrictionValue != "allow" && shared.IsTrue(instanceValue) {
					return errors.New("Privileged containers are forbidden")
				}

				return nil
			}

			containerConfigChecks["security.idmap.isolated"] = func(instanceValue string) error {
				if restrictionValue == "isolated" && shared.IsFalseOrEmpty(instanceValue) {
					return errors.New("Non-isolated containers are forbidden")
				}

				return nil
			}

		case "restricted.virtual-machines.lowlevel":
			if restrictionValue == "allow" {
				allowVMLowLevel = true
			}

			// Add check for valid usage of io.threads setting.
			devicesChecks["disk"] = append(devicesChecks["disk"], func(device map[string]string) error {
				_, ioThreadsUsed := device["io.threads"]
				if ioThreadsUsed && !allowVMLowLevel {
					return errors.New(`Use of low-level "io.threads" disk option forbidden`)
				}

				return nil
			})

			// Add check for valid usage of acceleration.parent setting.
			devicesChecks["nic"] = append(devicesChecks["nic"], func(device map[string]string) error {
				_, accelerationParentUsed := device["acceleration.parent"]
				if accelerationParentUsed && !allowVMLowLevel {
					return errors.New(`Use of low-level "acceleration.parent" NIC option forbidden`)
				}

				return nil
			})

		case "restricted.devices.unix-char":
			devicesChecks["unix-char"] = append(devicesChecks["unix-char"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("Unix character devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.unix-block":
			devicesChecks["unix-block"] = append(devicesChecks["unix-block"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("Unix block devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.unix-hotplug":
			devicesChecks["unix-hotplug"] = append(devicesChecks["unix-hotplug"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("Unix hotplug devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.infiniband":
			devicesChecks["infiniband"] = append(devicesChecks["infiniband"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("Infiniband devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.gpu":
			devicesChecks["gpu"] = append(devicesChecks["gpu"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("GPU devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.usb":
			devicesChecks["usb"] = append(devicesChecks["usb"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("USB devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.pci":
			devicesChecks["pci"] = append(devicesChecks["pci"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("PCI devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.proxy":
			devicesChecks["proxy"] = append(devicesChecks["proxy"], func(device map[string]string) error {
				if restrictionValue != "allow" {
					return errors.New("Proxy devices are forbidden")
				}

				return nil
			})

		case "restricted.devices.nic":
			devicesChecks["nic"] = append(devicesChecks["nic"], func(device map[string]string) error {
				// Check if the NICs are allowed at all.
				switch restrictionValue {
				case "block":
					return errors.New("Network devices are forbidden")
				case "managed":
					if device["network"] == "" {
						return errors.New("Only managed network devices are allowed")
					}
				}

				// Check if the NIC's parent/network setting is allowed based on the
				// restricted.devices.nic and restricted.networks.access settings.
				if device["network"] != "" {
					if !project.NetworkAllowed(proj.Config, device["network"], true) {
						return errors.New("Network not allowed in project")
					}
				} else if device["parent"] != "" {
					if !project.NetworkAllowed(proj.Config, device["parent"], false) {
						return errors.New("Network not allowed in project")
					}
				}

				return nil
			})

		case "restricted.devices.disk":
			devicesChecks["disk"] = append(devicesChecks["disk"], func(device map[string]string) error {
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
					return errors.New("Disk devices are forbidden")
				case "managed":
					if device["pool"] == "" {
						return errors.New("Attaching disks not backed by a pool is forbidden")
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
			})

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
			checks, ok := devicesChecks[device["type"]]
			if !ok {
				continue
			}

			for _, check := range checks {
				err := check(device)
				if err != nil {
					return fmt.Errorf("Invalid device %q on %s %q of project %q: %w", name, entityTypeLabel, entityName, proj.Name, err)
				}
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

// allInstanceAggregateLimits is the list of project limits that are affected by instance-level values.
// I.e., it does not include "limits.networks" because this limit is not affected by instances.
// This list also does not include "limits.virtual-machines", "limits.containers", and "limits.instances".
// These limits should be checked separately where needed.
var allInstanceAggregateLimits = []string{
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
	if strings.HasPrefix(key, "security.syscalls.intercept") && !slices.Contains(allowableIntercept, key) {
		return true
	}

	if strings.HasPrefix(key, "security.delegate_bpf") {
		return true
	}

	if slices.Contains([]string{"boot.host_shutdown_timeout", "linux.kernel_modules", "linux.kernel_modules.load", "raw.apparmor", "raw.idmap", "raw.lxc", "raw.seccomp", "security.devlxd.images", "security.idmap.base", "security.idmap.size"}, key) {
		return true
	}

	return false
}

// Return true if a low-level VM option is forbidden.
func isVMLowLevelOptionForbidden(key string) bool {
	return slices.Contains([]string{"boot.host_shutdown_timeout", "limits.memory.hugepages", "raw.idmap", "raw.qemu"}, key)
}

// AllowInstanceUpdate returns an error if any project-specific limit or
// restriction is violated when updating an existing instance.
func AllowInstanceUpdate(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName, instanceName string, req api.InstancePut, currentConfig map[string]string) error {
	var updatedInstance *api.Instance

	info, err := fetchProject(ctx, tx, projectName, true)
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

	err = checkInstanceRestrictionsAndAggregateLimits(globalConfig, tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if instance update allowed: %w", err)
	}

	return nil
}

// AllowVolumeUpdate returns an error if any project-specific limit or
// restriction is violated when updating an existing custom volume.
func AllowVolumeUpdate(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName, volumeName string, req api.StorageVolumePut, currentConfig map[string]string) error {
	info, err := fetchProject(ctx, tx, projectName, true)
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

	err = checkInstanceRestrictionsAndAggregateLimits(globalConfig, tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if volume update allowed: %w", err)
	}

	return nil
}

// AllowProfileUpdate checks that project limits and restrictions are not
// violated when changing a profile.
func AllowProfileUpdate(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName, profileName string, req api.ProfilePut) error {
	info, err := fetchProject(ctx, tx, projectName, true)
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

	err = checkInstanceRestrictionsAndAggregateLimits(globalConfig, tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if profile update allowed: %w", err)
	}

	return nil
}

// checkUplinkUse checks if an uplink that is not allowed by project restrictions is in use and
// if limits for uplink IP consumption are respected.
func checkUplinkUse(ctx context.Context, tx *db.ClusterTx, projectName string, config map[string]string) error {
	// If project does not have its own networks, no further checks are needed.
	if shared.IsFalseOrEmpty(config["features.networks"]) {
		return nil
	}

	projectNetworks, err := tx.GetCreatedNetworksByProject(ctx, projectName)
	if err != nil {
		return err
	}

	uplinksInUseSet := make(map[string]struct{})

	for _, network := range projectNetworks {
		// Check if uplink in use is allowed.
		uplinkInUse := network.Config["network"]
		if uplinkInUse != "" {
			uplinksInUseSet[uplinkInUse] = struct{}{}
		}
	}

	// Check uplink IP quota limits.
	for uplink := range uplinksInUseSet {
		ivp4LimitsRaw, hasIPV4Limits := config[projectLimitNetworkUplinkIP4+uplink]
		ivp6LimitsRaw, hasIPV6Limits := config[projectLimitNetworkUplinkIP6+uplink]

		ivp4Limits, err := strconv.Atoi(ivp4LimitsRaw)
		if err != nil {
			// Limit for this protocol is not defined
			hasIPV4Limits = false
			ivp4Limits = -1
		}

		ivp6Limits, err := strconv.Atoi(ivp6LimitsRaw)
		if err != nil {
			// Limit for this protocol is not defined
			hasIPV6Limits = false
			ivp6Limits = -1
		}

		// Check if the provided value is equal or lower to the number of uplink addresses currently in use
		// on the provided project and in the specified network.
		// We are only interested on the result for protocols with limits defined.
		invalidIPV4Quota, invalidIPV6Quota, err := UplinkAddressQuotasExceeded(ctx, tx, projectName, uplink, ivp4Limits, ivp6Limits, projectNetworks)
		if err != nil {
			return err
		}

		if hasIPV4Limits && invalidIPV4Quota || hasIPV6Limits && invalidIPV6Quota {
			return errors.New("Uplink IP limit is below current number of used uplink addresses")
		}
	}

	// If project is not restricted, no further checks are needed.
	if shared.IsFalseOrEmpty(config["restricted"]) {
		return nil
	}

	allowedNets := shared.SplitNTrimSpace(config["restricted.networks.uplinks"], ",", -1, false)

	for network := range uplinksInUseSet {
		if !slices.Contains(allowedNets, network) {
			return fmt.Errorf("Restrictions cannot be enforced as project is already using %q as uplink", network)
		}
	}

	return nil
}

// AllowProjectUpdate checks the new config to be set on a project is valid.
func AllowProjectUpdate(ctx context.Context, globalConfig *clusterConfig.Config, tx *db.ClusterTx, projectName string, config map[string]string, changed []string) error {
	info, err := fetchProject(ctx, tx, projectName, false)
	if err != nil {
		return err
	}

	// Set the fetched project's config to new config for validation.
	info.Project.Config = config

	// This checks if restricted uplinks are used within this project and if limits
	// for uplink IP usage are respected.
	// This is done separately because this may require getting network info from the database.
	err = checkUplinkUse(ctx, tx, projectName, info.Project.Config)
	if err != nil {
		return fmt.Errorf("Check project uplink network usage: %w", err)
	}

	// Check instance restrictions and aggregate limits affected by instance-level values.
	// This function will also set the "Instances" field for the fetched project.
	err = checkInstanceRestrictionsAndAggregateLimits(globalConfig, tx, info)
	if err != nil {
		return fmt.Errorf("Conflict detected when updating project %q: %w", projectName, err)
	}

	// Handle the changed project limits not yet checked.
	for _, key := range changed {
		switch key {
		case "limits.instances":
			err := validateTotalInstanceCountLimit(info.Instances, config[key], projectName)
			if err != nil {
				return fmt.Errorf("Can't change %q in project %q: %w", key, projectName, err)
			}

		case "limits.containers", "limits.virtual-machines":
			err := validateInstanceCountLimit(info.Instances, key, config[key], projectName)
			if err != nil {
				return fmt.Errorf("Can't change %q in project %q: %w", key, projectName, err)
			}

		case "limits.networks":
			// If project does not have its own networks, no need to validate limits.networks.
			if shared.IsTrue(config["features.networks"]) {
				err := validateNetworksCountLimit(info.Networks, config[key])
				if err != nil {
					return fmt.Errorf("Can't change %q in project %q: %w", key, projectName, err)
				}
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

// validateNetworksCountLimit checks that "limits.networks" is equal or above
// the current count of project networks.
func validateNetworksCountLimit(networks []string, limitValue string) error {
	if limitValue == "" {
		return nil
	}

	limit, err := strconv.Atoi(limitValue)
	if err != nil {
		return err
	}

	count := len(networks)

	if limit < count {
		return fmt.Errorf("Network limit exceeded: there currently are %d networks", count)
	}

	return nil
}

var countConfigInstanceType = map[string]api.InstanceType{
	"limits.containers":       api.InstanceTypeContainer,
	"limits.virtual-machines": api.InstanceTypeVM,
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
	Networks  []string

	// poolName: driverName
	StoragePoolDrivers map[string]string
}

// Fetch the given project from the database along with its profiles, instances,
// networks, and possibly custom volumes.
//
// If the skipIfNoLimits flag is true, then profiles, instances and volumes
// won't be loaded if the profile has no limits set on it, and nil will be
// returned.
func fetchProject(ctx context.Context, tx *db.ClusterTx, projectName string, skipIfNoLimits bool) (*projectInfo, error) {
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

	projectArgs, err := tx.GetProjectInstancesAndProfiles(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch instances from database: %w", err)
	}

	args := projectArgs[projectName]

	info := &projectInfo{
		Project:   *project,
		Profiles:  args.Profiles,
		Instances: args.Instances,
	}

	info.StoragePoolDrivers, err = tx.GetStoragePoolDrivers(ctx)
	if err != nil {
		return nil, fmt.Errorf("Fetch storage pools from database: %w", err)
	}

	info.Volumes, err = tx.GetCustomVolumesInProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("Fetch project custom volumes from database: %w", err)
	}

	info.Networks, err = tx.GetCreatedNetworkNamesByProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("Fetch project networks from database: %w", err)
	}

	return info, nil
}

// Expand the configuration and devices of the given instances, taking the give
// project profiles into account.
func expandInstancesConfigAndDevices(globalConfig map[string]string, instances []api.Instance, profiles []api.Profile) ([]api.Instance, error) {
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
			return -1, errors.New("Value can't be a percentage")
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
			return -1, errors.New("CPUs can't be pinned if project limits are used")
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
func CheckClusterTargetRestriction(ctx context.Context, authorizer auth.Authorizer, project *api.Project, targetFlag string) error {
	if projectHasRestriction(project, "restricted.cluster.target", "block") && targetFlag != "" {
		// Allow server administrators to move instances around even when restricted (node evacuation, ...)
		err := authorizer.CheckPermission(ctx, entity.ServerURL(), auth.EntitlementCanOverrideClusterTargetRestriction)
		if err != nil && auth.IsDeniedError(err) {
			return api.StatusErrorf(http.StatusForbidden, "This project doesn't allow cluster member targeting")
		} else if err != nil {
			return err
		}
	}

	return nil
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
			if slices.Contains(clusterGroupsAllowed, memberGroupName) {
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

	if len(clusterGroupsAllowed) > 0 && !slices.Contains(clusterGroupsAllowed, groupName) {
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

// TargetDetect returns either target node or group based on the provided prefix:
// An invocation with `target=h1` returns "h1", "" and `target=@g1` returns "", "g1".
func TargetDetect(target string) (targetNode string, targetGroup string) {
	after, found := strings.CutPrefix(target, instancetype.TargetClusterGroupPrefix)
	if found {
		targetGroup = after
	} else {
		targetNode = target
	}

	return targetNode, targetGroup
}

// CheckTarget checks if the given cluster target (member or group) is allowed.
// If target is a cluster member and is found in allMembers it returns the resolved node information object.
// If target is a cluster group it returns the cluster group name.
// In case of error, neither node information nor cluster group name gets returned.
func CheckTarget(ctx context.Context, authorizer auth.Authorizer, tx *db.ClusterTx, p *api.Project, target string, allMembers []db.NodeInfo) (*db.NodeInfo, string, error) {
	targetMemberName, targetGroupName := TargetDetect(target)

	// Check manual cluster member targeting restrictions.
	err := CheckClusterTargetRestriction(ctx, authorizer, p, target)
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

// uplinkIPLimits is a type used to help check uplink IP quota usage.
type uplinkIPLimits struct {
	quotaIPV4         int
	quotaIPV6         int
	usedIPV4Addresses int
	usedIPV6Addresses int
}

func (q *uplinkIPLimits) increment(incrementIPV4 bool, incrementIPV6 bool) {
	if incrementIPV4 {
		q.usedIPV4Addresses++
	}

	if incrementIPV6 {
		q.usedIPV6Addresses++
	}
}

func (q *uplinkIPLimits) hasExceeded() bool {
	return q.usedIPV4Addresses > q.quotaIPV4 && q.usedIPV6Addresses > q.quotaIPV6
}

// UplinkAddressQuotasExceeded checks whether the number of current uplink addresses used in project
// projectName on network networkName is higher than their provided quota for each IP protocol.
// Uplink addresses can be consumed by load balancers, network forwards and networks.
// For simplicity, this function assumes both limits are provided and returns early if both provided
// quotas are exceeded. So if one of the limits is not of the caller's interest, -1 should be provided
// and the result for that protocol should be ignored.
// The projectNetworks argument accepts cached networks for the provided project to avoid redundant DB queries.
func UplinkAddressQuotasExceeded(ctx context.Context, tx *db.ClusterTx, projectName string, networkName string, uplinkIPV4Quota int, uplinkIPV6Quota int, projectNetworks map[int64]api.Network) (V4QuotaExceeded bool, V6QuotaExceeded bool, err error) {
	quotas := uplinkIPLimits{
		quotaIPV4: uplinkIPV4Quota,
		quotaIPV6: uplinkIPV6Quota,
	}

	// If both provided quotas are below 0, return right away.
	if quotas.hasExceeded() {
		return true, true, nil
	}

	// If cached networks were not provided, retrieve them from the database.
	if projectNetworks == nil {
		// First count uplink addresses for other networks.
		projectNetworks, err = tx.GetCreatedNetworksByProject(ctx, projectName)
		if err != nil {
			return false, false, nil
		}
	}

	for _, network := range projectNetworks {
		// Check if each network is using our target network as an uplink.
		if network.Config["network"] == networkName {
			_, hasIPV6 := network.Config["volatile.network.ipv6.address"]
			_, hasIPV4 := network.Config["volatile.network.ipv4.address"]
			quotas.increment(hasIPV4, hasIPV6)
			if quotas.hasExceeded() {
				return true, true, nil
			}
		}
	}

	// Count listen addresses for network forwards.
	forwardListenAddressesMap, err := tx.GetProjectNetworkForwardListenAddressesByUplink(ctx, networkName, false)
	if err != nil {
		return false, false, err
	}

	// Iterate through each network on the provided project while counting the uplink addresses used by their
	// network forwards.
	for _, addresses := range forwardListenAddressesMap[projectName] {
		for _, address := range addresses {
			isIPV6 := validate.IsNetworkAddressV6(address) == nil
			quotas.increment(!isIPV6, isIPV6)
			if quotas.hasExceeded() {
				return true, true, nil
			}
		}
	}

	// Count listen addresses for load balancers.
	loadBalancerAddressesMap, err := tx.GetProjectNetworkLoadBalancerListenAddressesByUplink(ctx, networkName, false)
	if err != nil {
		return false, false, err
	}

	// Iterate through each network on the provided project while counting the uplink addresses used by their
	// load balancers.
	for _, addresses := range loadBalancerAddressesMap[projectName] {
		for _, address := range addresses {
			isIPV6 := validate.IsNetworkAddressV6(address) == nil
			quotas.increment(!isIPV6, isIPV6)
			if quotas.hasExceeded() {
				return true, true, nil
			}
		}
	}

	// At least one of the quotas were not exceeded.
	return quotas.usedIPV4Addresses > quotas.quotaIPV4, quotas.usedIPV6Addresses > quotas.quotaIPV6, err
}
