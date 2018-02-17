package types

import (
	"sort"

	"github.com/lxc/lxd/shared"
)

// Device represents a LXD container device
type Device map[string]string

// Devices represents a set of LXD container devices
type Devices map[string]map[string]string

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
func (list Devices) Update(newlist Devices) (map[string]Device, map[string]Device, map[string]Device, []string) {
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

// DeviceNames returns the name of all devices in the set, sorted properly
func (list Devices) DeviceNames() []string {
	sortable := sortableDevices{}
	for k, d := range list {
		sortable = append(sortable, namedDevice{k, d})
	}

	sort.Sort(sortable)
	return sortable.Names()
}
