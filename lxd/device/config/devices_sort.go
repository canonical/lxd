package config

// DeviceNamed contains the name of a device and its config.
type DeviceNamed struct {
	Name   string
	Config Device
}

// DevicesSortable is a sortable slice of device names and config.
type DevicesSortable []DeviceNamed

func (devices DevicesSortable) Len() int {
	return len(devices)
}

func (devices DevicesSortable) Less(i, j int) bool {
	a := devices[i]
	b := devices[j]

	// First sort by types.
	if a.Config["type"] != b.Config["type"] {
		return a.Config["type"] < b.Config["type"]
	}

	// Special case disk paths.
	if a.Config["type"] == "disk" && b.Config["type"] == "disk" {
		if a.Config["path"] != b.Config["path"] {
			return a.Config["path"] < b.Config["path"]
		}
	}

	// Fallback to sorting by names.
	return a.Name < b.Name
}

func (devices DevicesSortable) Swap(i, j int) {
	devices[i], devices[j] = devices[j], devices[i]
}
