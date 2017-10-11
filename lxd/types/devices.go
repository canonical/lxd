package types

import (
	"sort"

	"github.com/lxc/lxd/shared"
)

type Device map[string]string
type Devices map[string]map[string]string

func (list Devices) ContainsName(k string) bool {
	if list[k] != nil {
		return true
	}
	return false
}

func (d Device) get(key string) string {
	return d[key]
}

func (list Devices) Contains(k string, d Device) bool {
	// If it didn't exist, it's different
	if list[k] == nil {
		return false
	}

	old := list[k]

	return deviceEquals(old, d)
}

func deviceEquals(old Device, d Device) bool {
	// Check for any difference and addition/removal of properties
	for k := range d {
		if d[k] != old[k] {
			return false
		}
	}

	for k := range old {
		if d[k] != old[k] {
			return false
		}
	}

	return true
}

func deviceEqualsDiffKeys(old Device, d Device) []string {
	keys := []string{}

	// Check for any difference and addition/removal of properties
	for k := range d {
		if d[k] != old[k] {
			keys = append(keys, k)
		}
	}

	for k := range old {
		if d[k] != old[k] {
			keys = append(keys, k)
		}
	}

	return keys
}

func (old Devices) Update(newlist Devices) (map[string]Device, map[string]Device, map[string]Device, []string) {
	rmlist := map[string]Device{}
	addlist := map[string]Device{}
	updatelist := map[string]Device{}

	for key, d := range old {
		if !newlist.Contains(key, d) {
			rmlist[key] = d
		}
	}

	for key, d := range newlist {
		if !old.Contains(key, d) {
			addlist[key] = d
		}
	}

	updateDiff := []string{}
	for key, d := range addlist {
		srcOldDevice := rmlist[key]
		var oldDevice Device
		err := shared.DeepCopy(&srcOldDevice, &oldDevice)
		if err != nil {
			continue
		}

		srcNewDevice := newlist[key]
		var newDevice Device
		err = shared.DeepCopy(&srcNewDevice, &newDevice)
		if err != nil {
			continue
		}

		updateDiff = deviceEqualsDiffKeys(oldDevice, newDevice)

		for _, k := range []string{"limits.max", "limits.read", "limits.write", "limits.egress", "limits.ingress", "ipv4.address", "ipv6.address"} {
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

func (newBaseDevices Devices) ExtendFromProfile(currentFullDevices Devices, newDevicesFromProfile Devices) error {
	// For any entry which exists in a profile and doesn't in the container config, add it

	for name, newDev := range newDevicesFromProfile {
		if curDev, ok := currentFullDevices[name]; ok {
			newBaseDevices[name] = curDev
		} else {
			newBaseDevices[name] = newDev
		}
	}

	return nil
}

type namedDevice struct {
	name   string
	device Device
}
type sortableDevices []namedDevice

func (devices Devices) toSortable() sortableDevices {
	named := []namedDevice{}
	for k, d := range devices {
		named = append(named, namedDevice{k, d})
	}

	return named
}

func (devices sortableDevices) Len() int {
	return len(devices)
}

func (devices sortableDevices) Less(i, j int) bool {
	a := devices[i]
	b := devices[j]

	// First sort by types
	if a.device["type"] != b.device["type"] {
		return a.device["type"] < b.device["type"]
	}

	// Special case disk paths
	if a.device["type"] == "disk" && b.device["type"] == "disk" {
		if a.device["path"] != b.device["path"] {
			return a.device["path"] < b.device["path"]
		}
	}

	// Fallback to sorting by names
	return a.name < b.name
}

func (devices sortableDevices) Swap(i, j int) {
	tmp := devices[i]
	devices[i] = devices[j]
	devices[j] = tmp
}

func (devices sortableDevices) Names() []string {
	result := []string{}
	for _, d := range devices {
		result = append(result, d.name)
	}

	return result
}

/* DeviceNames returns the device names for this Devices in sorted order */
func (devices Devices) DeviceNames() []string {
	sortable := devices.toSortable()
	sort.Sort(sortable)
	return sortable.Names()
}
