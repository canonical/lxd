package types

type namedDevice struct {
	name   string
	device Device
}

type sortableDevices []namedDevice

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
