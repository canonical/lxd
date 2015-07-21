package shared

type Device map[string]string
type Devices map[string]Device

func (list Devices) ContainsName(k string) bool {
	if list[k] != nil {
		return true
	}
	return false
}

func nic_equal(d1 Device, d2 Device) bool {
	if d1.get("nictype") != d2.get("nictype") {
		return false
	}
	if d1.get("name") != d2.get("name") || d1.get("parent") != d2.get("parent") {
		return false
	}
	if d1.get("mtu") != d2.get("mtu") || d1.get("hwaddr") != d2.get("hwaddr") {
		return false
	}
	return true
}

func disk_equal(d1 Device, d2 Device) bool {
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
		if !nic_equal(ld, d) {
			return false
		}
	case "disk":
		if !disk_equal(ld, d) {
			return false
		}
	}
	return true
}

func (old Devices) Update(newlist Devices) (map[string]Device, map[string]Device) {
	rmlist := map[string]Device{}
	addlist := map[string]Device{}
	for key, d := range old {
		switch {
		case d["type"] != "nic":
			continue
		case !newlist.Contains(key, d):
			rmlist[key] = d
		}
	}
	for key, d := range newlist {
		switch {
		case d["type"] != "nic":
			continue
		case !old.Contains(key, d):
			addlist[key] = d
		}
	}
	return rmlist, addlist
}
