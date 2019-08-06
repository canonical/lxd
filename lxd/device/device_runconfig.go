package device

// RunConfigItem represents a single config item.
type RunConfigItem struct {
	Key   string
	Value string
}

// RunConfig represents LXD defined run-time config used for device setup/cleanup.
type RunConfig struct {
	NetworkInterface []RunConfigItem // Network interface configuration settings.
	PostHooks        []func() error  // Functions to be run after device attach/detach.
}
