package shared

type Device map[string]string
type Devices map[string]Device

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
	for k, _ := range d {
		if d[k] != old[k] {
			return false
		}
	}

	for k, _ := range old {
		if d[k] != old[k] {
			return false
		}
	}

	return true
}

func (old Devices) Update(newlist Devices) (map[string]Device, map[string]Device, map[string]Device) {
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

	for key, d := range addlist {
		srcOldDevice := rmlist[key]
		var oldDevice Device
		err := DeepCopy(&srcOldDevice, &oldDevice)
		if err != nil {
			continue
		}

		srcNewDevice := newlist[key]
		var newDevice Device
		err = DeepCopy(&srcNewDevice, &newDevice)
		if err != nil {
			continue
		}

		for _, k := range []string{"limits.max", "limits.read", "limits.write", "limits.egress", "limits.ingress"} {
			delete(oldDevice, k)
			delete(newDevice, k)
		}

		if deviceEquals(oldDevice, newDevice) {
			delete(rmlist, key)
			delete(addlist, key)
			updatelist[key] = d
		}
	}

	return rmlist, addlist, updatelist
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
