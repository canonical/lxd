package config

// MountOwnerShiftNone do not use owner shifting.
const MountOwnerShiftNone = ""

// MountOwnerShiftDynamic use shiftfs for dynamic owner shifting.
const MountOwnerShiftDynamic = "dynamic"

// MountOwnerShiftStatic statically modify ownership.
const MountOwnerShiftStatic = "static"

// RunConfigItem represents a single config item.
type RunConfigItem struct {
	Key   string
	Value string
}

// MountEntryItem represents a single mount entry item.
type MountEntryItem struct {
	DevName    string   // The internal name for the device.
	DevPath    string   // Describes the block special device or remote filesystem to be mounted.
	TargetPath string   // Describes the mount point (target) for the filesystem.
	FSType     string   // Describes the type of the filesystem.
	Opts       []string // Describes the mount options associated with the filesystem.
	Freq       int      // Used by dump(8) to determine which filesystems need to be dumped. Defaults to zero (don't dump) if not present.
	PassNo     int      // Used by fsck(8) to determine the order in which filesystem checks are done at boot time. Defaults to zero (don't fsck) if not present.
	OwnerShift string   // Ownership shifting mode, use constants MountOwnerShiftNone, MountOwnerShiftStatic or MountOwnerShiftDynamic.
}

// RootFSEntryItem represents the root filesystem options for an Instance.
type RootFSEntryItem struct {
	Path string   // Describes the root file system source.
	Opts []string // Describes the mount options associated with the filesystem.
}

// RunConfig represents LXD defined run-time config used for device setup/cleanup.
type RunConfig struct {
	RootFS           RootFSEntryItem  // RootFS to setup.
	NetworkInterface []RunConfigItem  // Network interface configuration settings.
	CGroups          []RunConfigItem  // Cgroup rules to setup.
	Mounts           []MountEntryItem // Mounts to setup/remove.
	Uevents          [][]string       // Uevents to inject.
	PostHooks        []func() error   // Functions to be run after device attach/detach.
	GPUDevice        []RunConfigItem  // GPU device configuration settings.
	USBDevice        []RunConfigItem  // USB device configuration settings.
	TPMDevice        []RunConfigItem  // TPM device configuration settings.
}
