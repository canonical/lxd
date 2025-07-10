package main

import (
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/instance"
)

type devLXDDeviceAccessValidator func(device map[string]string) bool

// getDevLXDOwnedDevices extracts instance devices that are owned by the provided identity.
func getDevLXDOwnedDevices(devices map[string]map[string]string, config map[string]string, identityID string) map[string]map[string]string {
	ownedDevices := make(map[string]map[string]string)

	for name, device := range devices {
		if config["volatile."+name+".devlxd.owner"] == identityID {
			ownedDevices[name] = device
		}
	}

	return ownedDevices
}

// newDevLXDDeviceAccessValidator returns a device validator function that
// checks if the given device is accessible by the devLXD.
//
// For example, disk device is accessible if the appropriate security flag
// is enabled on the instance and the device represents a custom volume.
func newDevLXDDeviceAccessValidator(inst instance.Instance) devLXDDeviceAccessValidator {
	diskDeviceAllowed := hasInstanceSecurityFeatures(inst.ExpandedConfig(), devLXDSecurityManagementVolumesKey)

	return func(device map[string]string) bool {
		return diskDeviceAllowed && filters.IsCustomVolumeDisk(device)
	}
}
