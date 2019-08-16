package device

// RunConfigItem represents a single config item.
type RunConfigItem struct {
	Key   string
	Value string
}

// MountEntryItem represents a single mount entry item.
type MountEntryItem struct {
	DevPath    string   // Describes the block special device or remote filesystem to be mounted.
	TargetPath string   // Describes the mount point (target) for the filesystem.
	FSType     string   // Describes the type of the filesystem.
	Opts       []string // Describes the mount options associated with the filesystem.
	Freq       int      // Used by dump(8) to determine which filesystems need to be dumped. Defaults to zero (don't dump) if not present.
	PassNo     int      // Used by fsck(8) to determine the order in which filesystem checks are done at boot time. Defaults to zero (don't fsck) if not present.
	Shift      bool     // Whether or not to use shiftfs with this mount.
}

// RunConfig represents LXD defined run-time config used for device setup/cleanup.
type RunConfig struct {
	NetworkInterface []RunConfigItem  // Network interface configuration settings.
	CGroups          []RunConfigItem  // Cgroup rules to setup.
	Mounts           []MountEntryItem // Mounts to setup/remove.
	PostHooks        []func() error   // Functions to be run after device attach/detach.
}
