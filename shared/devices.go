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

func (old Devices) Update(newlist Devices) (map[string]Device, map[string]Device) {
	rmlist := map[string]Device{}
	addlist := map[string]Device{}

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

	return rmlist, addlist
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
