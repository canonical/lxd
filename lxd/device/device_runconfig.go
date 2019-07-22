package device

// RunConfigItem represents a single config item.
type RunConfigItem struct {
	Key   string
	Value string
}

// RunConfig represents LXD defined run-time config used for device setup.
type RunConfig struct {
	NetworkInterfaces [][]RunConfigItem
	Mounts            []map[string]string
	Cgroups           []map[string]string
}
