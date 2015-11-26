package shared

type Device map[string]string
type Devices map[string]Device

func (list Devices) ContainsName(k string) bool {
	if list[k] != nil {
		return true
	}
	return false
}

func nicEqual(d1 Device, d2 Device) bool {
	if !nicSettingsEqual(d1, d2) {
		return false
	}
	if d1.get("hwaddr") != d2.get("hwaddr") {
		return false
	}
	return true
}

func nicSettingsEqual(d1 Device, d2 Device) bool {
	for _, prop := range []string{"nictype", "name", "parent", "mtu"} {
		if d1.get(prop) != d2.get(prop) {
			return false
		}
	}
	return true
}

func diskEqual(d1 Device, d2 Device) bool {
	if d1.get("path") != d2.get("path") || d1.get("source") != d2.get("source") {
		return false
	}
	if d1.get("optional") != d2.get("optional") || d1.get("readonly") != d2.get("readonly") {
		return false
	}
	return true
}

func (d Device) get(key string) string {
	return d[key]
}

func (list Devices) Contains(k string, d Device) bool {
	if list[k] == nil {
		return false
	}
	ld := list[k]
	if ld.get("type") != d.get("type") {
		return false
	}
	switch ld.get("type") {
	case "nic":
		if !nicEqual(ld, d) {
			return false
		}
	case "disk":
		if !diskEqual(ld, d) {
			return false
		}
	}
	return true
}

func liveUpdateable(devtype string) bool {
	switch devtype {
	case "nic":
		return true
	case "disk":
		return true
	case "unix-block":
		return true
	case "unix-char":
		return true
	default:
		return false
	}
}

func (old Devices) Update(newlist Devices) (map[string]Device, map[string]Device) {
	rmlist := map[string]Device{}
	addlist := map[string]Device{}
	for key, d := range old {
		switch {
		case !liveUpdateable(d["type"]):
			continue
		case !newlist.Contains(key, d):
			rmlist[key] = d
		}
	}
	for key, d := range newlist {
		switch {
		case !liveUpdateable(d["type"]):
			continue
		case !old.Contains(key, d):
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
