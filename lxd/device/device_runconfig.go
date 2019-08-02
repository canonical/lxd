package device

// RunConfigItem represents a single config item.
type RunConfigItem struct {
	Key   string
	Value string
}

// RunConfig represents LXD defined run-time config used for device setup.
type RunConfig struct {
	NetworkInterface []RunConfigItem
	PostStartHooks   []func() error
}
