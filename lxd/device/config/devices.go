package config

import (
	"fmt"
	"sort"
)

// Device represents a LXD container device
type Device map[string]string

// Clone returns a copy of the Device.
func (device Device) Clone() Device {
	copy := map[string]string{}

	for k, v := range device {
		copy[k] = v
	}

	return copy
}

// Validate accepts a map of field/validation functions to run against the device's config.
func (device Device) Validate(rules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(device[k])
		if err != nil {
			return fmt.Errorf("Invalid value for device option %s: %v", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range device {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// Skip type fields are these are validated by the presence of an implementation.
		if k == "type" {
			continue
		}

		if k == "nictype" && (device["type"] == "nic" || device["type"] == "infiniband") {
			continue
		}

		return fmt.Errorf("Invalid device option: %s", k)
	}

	return nil
}

// Devices represents a set of LXD container devices
type Devices map[string]Device

// NewDevices creates a new Devices set from a native map[string]map[string]string set.
func NewDevices(nativeSet map[string]map[string]string) Devices {
	newDevices := Devices{}

	for devName, devConfig := range nativeSet {
		newDev := Device{}
		for k, v := range devConfig {
			newDev[k] = v
		}
		newDevices[devName] = newDev
	}

	return newDevices
}

// Contains checks if a given device exists in the set and if it's
// identical to that provided
func (list Devices) Contains(k string, d Device) bool {
	// If it didn't exist, it's different
	if list[k] == nil {
		return false
	}

	old := list[k]

	return deviceEquals(old, d)
}

// Update returns the difference between two sets
func (list Devices) Update(newlist Devices, updateFields func(Device, Device) []string) (map[string]Device, map[string]Device, map[string]Device, []string) {
	rmlist := map[string]Device{}
	addlist := map[string]Device{}
	updatelist := map[string]Device{}

	for key, d := range list {
		if !newlist.Contains(key, d) {
			rmlist[key] = d
		}
	}

	for key, d := range newlist {
		if !list.Contains(key, d) {
			addlist[key] = d
		}
	}

	updateDiff := []string{}
	for key, d := range addlist {
		srcOldDevice := rmlist[key]
		oldDevice := srcOldDevice.Clone()

		srcNewDevice := newlist[key]
		newDevice := srcNewDevice.Clone()

		updateDiff = deviceEqualsDiffKeys(oldDevice, newDevice)
		for _, k := range updateFields(oldDevice, newDevice) {
			delete(oldDevice, k)
			delete(newDevice, k)
		}

		if deviceEquals(oldDevice, newDevice) {
			delete(rmlist, key)
			delete(addlist, key)
			updatelist[key] = d
		}
	}

	return rmlist, addlist, updatelist, updateDiff
}

// Clone returns a copy of the Devices set.
func (list Devices) Clone() Devices {
	copy := Devices{}

	for deviceName, device := range list {
		copy[deviceName] = device.Clone()
	}

	return copy
}

// CloneNative returns a copy of the Devices set as a native map[string]map[string]string type.
func (list Devices) CloneNative() map[string]map[string]string {
	copy := map[string]map[string]string{}

	for deviceName, device := range list {
		copy[deviceName] = device.Clone()
	}

	return copy
}

// Sorted returns the name of all devices in the set, sorted properly.
func (list Devices) Sorted() DevicesSortable {
	sortable := DevicesSortable{}
	for k, d := range list {
		sortable = append(sortable, DeviceNamed{k, d})
	}

	sort.Sort(sortable)
	return sortable
}

// Reversed returns the name of all devices in the set, sorted reversed.
func (list Devices) Reversed() DevicesSortable {
	sortable := DevicesSortable{}
	for k, d := range list {
		sortable = append(sortable, DeviceNamed{k, d})
	}

	sort.Sort(sort.Reverse(sortable))
	return sortable
}
