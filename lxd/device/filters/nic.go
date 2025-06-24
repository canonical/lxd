package filters

// IsNIC evaluates whether or not the given device is of type nic.
func IsNIC(device map[string]string) bool {
	return device["type"] == "nic"
}
