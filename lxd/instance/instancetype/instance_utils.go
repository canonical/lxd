package instancetype

import (
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/shared/api"
)

// ExpandInstanceConfig expands the given instance config with the config values of the given profiles.
func ExpandInstanceConfig(config map[string]string, profiles []api.Profile) map[string]string {
	expandedConfig := map[string]string{}

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
