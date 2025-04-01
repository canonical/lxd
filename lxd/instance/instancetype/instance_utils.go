package instancetype

import (
	"strconv"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/shared/api"
)

// ExpandInstanceConfig expands the given instance config with the config values of the given profiles.
func ExpandInstanceConfig(globalConfig map[string]any, config map[string]string, profiles []api.Profile) map[string]string {
	expandedConfig := map[string]string{}

	// Apply global config overriding
	if globalConfig != nil {
		globalInstancesMigrationStatefulStr, ok := globalConfig["instances.migration.stateful"].(string)
		if ok {
			globalInstancesMigrationStateful, _ := strconv.ParseBool(globalInstancesMigrationStatefulStr)
			if globalInstancesMigrationStateful {
				expandedConfig["migration.stateful"] = globalInstancesMigrationStatefulStr
			}
		}
	}

	// Apply all the profiles.
	profileConfigs := make([]map[string]string, len(profiles))
	for i, profile := range profiles {
		profileConfigs[i] = profile.Config
	}

	for i := range profileConfigs {
		for k, v := range profileConfigs[i] {
			expandedConfig[k] = v
		}
	}

	// Stick the given config on top.
	for k, v := range config {
		expandedConfig[k] = v
	}

	return expandedConfig
}

// ExpandInstanceDevices expands the given instance devices with the devices defined in the given profiles.
func ExpandInstanceDevices(devices deviceConfig.Devices, profiles []api.Profile) deviceConfig.Devices {
	expandedDevices := deviceConfig.Devices{}

	// Apply all the profiles.
	profileDevices := make([]deviceConfig.Devices, len(profiles))
	for i, profile := range profiles {
		profileDevices[i] = deviceConfig.NewDevices(profile.Devices)
	}

	for i := range profileDevices {
		for k, v := range profileDevices[i] {
			expandedDevices[k] = v
		}
	}

	// Stick the given devices on top.
	for k, v := range devices {
		expandedDevices[k] = v
	}

	return expandedDevices
}

// ExpandInstancePlacementRules expands the given instance placement rules with the placement rules defined in the given profiles.
func ExpandInstancePlacementRules(localRules map[string]api.InstancePlacementRule, profiles []api.Profile) map[string]api.InstancePlacementRule {
	if localRules == nil {
		localRules = make(map[string]api.InstancePlacementRule)
	}

	expandedRules := make(map[string]api.InstancePlacementRule)
	for _, profile := range profiles {
		for name, rule := range profile.PlacementRules {
			expandedRules[name] = rule
		}
	}

	// Stick the given devices on top.
	for k, v := range localRules {
		expandedRules[k] = v
	}

	return expandedRules
}
