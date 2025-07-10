package main

import (
	"net/http"

	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/shared/api"
)

type devLXDDeviceAccessValidator func(device map[string]string) bool

// patchDevLXDInstanceDevices updates an existing instance (api.Instance) with devices from the
// request instance (api.DevLXDInstancePut), and adjusts the device ownership configuration
// accordingly.
//
// Device actions are determined as follows:
// - Add:
//   - Condition: New device that devLXD can manage and is not present in the existing devices.
//   - Action:    Adds new device to the instance and set its owner in the instance config.
//
// - Update:
//   - Condition: Existing device that devLXD can manage and is present in the new devices.
//   - Action:    Updates existing device in the instance.
//
// - Remove:
//   - Condition: Existing device that devLXD can manage is set to "null" in the request.
//   - Action:    Removes existing device from the instance and removes its owner from the instance config.
func patchDevLXDInstanceDevices(inst *api.Instance, req api.DevLXDInstancePut, identityID string, isDeviceAccessible devLXDDeviceAccessValidator) error {
	newDevices := make(map[string]map[string]string)
	newConfig := make(map[string]string)

	// Pass local devices, as non-local devices cannot be owned.
	ownedDevices := getDevLXDOwnedDevices(*inst, identityID)

	// Merge new devices into existing ones.
	for name, device := range req.Devices {
		ownedDevice, isOwned := ownedDevices[name]

		if device == nil {
			// Device is being removed. Check if the device is owned.
			// For consistency with LXD API, we do not error out if
			// the device is not found.
			if !isOwned {
				continue
			}

			// Ensure devLXD has sufficient permissions to manage the device.
			// Pass old device to the validator, as new device is nil.
			if isDeviceAccessible != nil && !isDeviceAccessible(ownedDevice) {
				return api.StatusErrorf(http.StatusForbidden, "Not authorized to delete device %q", name)
			}

			// Device is removed, so remove the owner config key.
			newConfig["volatile."+name+".devlxd.owner"] = ""
		} else {
			_, exists := inst.ExpandedDevices[name]

			// Device is being either added or updated.
			// Ensure devLXD has sufficient permissions to manage the device.
			// If the device already exists (update), ensure that it is owned.
			if (exists && !isOwned) || (isDeviceAccessible != nil && !isDeviceAccessible(device)) {
				return api.StatusErrorf(http.StatusForbidden, "Not authorized to manage device %q", name)
			}

			// At this point we know that the ownership is correct (there is
			// no existing unowned device), so we can safely set the device owner
			// in instance configuration.
			newConfig["volatile."+name+".devlxd.owner"] = identityID
		}

		newDevices[name] = device
	}

	inst.Devices = newDevices
	inst.Config = newConfig
	return nil
}

// getDevLXDOwnedDevices extracts instance devices that are owned by the provided identity.
func getDevLXDOwnedDevices(inst api.Instance, identityID string) map[string]map[string]string {
	ownedDevices := make(map[string]map[string]string)

	for name, device := range inst.Devices {
		if inst.Config["volatile."+name+".devlxd.owner"] == identityID {
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
	diskDeviceAllowed := hasInstanceSecurityFeatures(inst, devLXDSecurityManagementVolumesKey)

	return func(device map[string]string) bool {
		return diskDeviceAllowed && filters.IsCustomVolumeDisk(device)
	}
}
