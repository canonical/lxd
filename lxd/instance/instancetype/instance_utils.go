package instancetype

import (
	"maps"
	"strconv"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/shared/api"
)

// ExpandInstanceConfig expands the given instance config with the config values of the given profiles.
func ExpandInstanceConfig(globalConfig map[string]string, config map[string]string, profiles []api.Profile) map[string]string {
	expandedConfig := map[string]string{}

	// Apply global config overriding
	if globalConfig != nil {
		globalInstancesMigrationStatefulStr, ok := globalConfig["instances.migration.stateful"]
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
		maps.Copy(expandedConfig, profileConfigs[i])
	}

	// Stick the given config on top.
	maps.Copy(expandedConfig, config)

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
		maps.Copy(expandedDevices, profileDevices[i])
	}

	// Stick the given devices on top.
	maps.Copy(expandedDevices, devices)

	return expandedDevices
}
