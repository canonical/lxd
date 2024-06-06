package cdi

const (
	// CDIHookDefinitionKey is used to reference a CDI hook definition in a run config as a file path.
	// A CDI hook definition is a simple way to represent the symlinks to be created and the folder entries to add to the ld cache.
	// This resource file is to be read and processed by LXD's `callhook` program.
	CDIHookDefinitionKey = "cdiHookDefinitionKey"
	// CDIHooksFileSuffix is the suffix for the file that contains the CDI hooks.
	CDIHooksFileSuffix = "_cdi_hooks.json"
	// CDIConfigDevicesFileSuffix is the suffix for the file that contains the CDI config devices.
	CDIConfigDevicesFileSuffix = "_cdi_config_devices.json"
	// CDIUnixPrefix is the prefix used for creating unix char devices
	// (e.g. cdi.unix.<device_name>.<encoded_dest_path>).
	CDIUnixPrefix = "cdi.unix"
	// CDIDiskPrefix is the prefix used for creating bind mounts (or 'disk' devices)
	// representing user space files required for a CDI passthrough
	// (e.g. cdi.disk.<device_name>.<encoded_dest_path>).
	CDIDiskPrefix = "cdi.disk"
)

// SymlinkEntry represents a symlink entry.
type SymlinkEntry struct {
	Target string `json:"target" yaml:"target"`
	Link   string `json:"link" yaml:"link"`
}

// Hooks represents all the hook instructions that can be executed by
// `lxd-cdi-hook`.
type Hooks struct {
	// ContainerRootFS is the path to the container's root filesystem.
	ContainerRootFS string `json:"container_rootfs" yaml:"container_rootfs"`
	// LdCacheUpdates is a list of entries to update the ld cache.
	LDCacheUpdates []string `json:"ld_cache_updates" yaml:"ld_cache_updates"`
	// SymLinks is a list of entries to create a symlink.
	Symlinks []SymlinkEntry `json:"symlinks" yaml:"symlinks"`
}

// ConfigDevices represents devices and mounts that need to be configured from a CDI specification.
type ConfigDevices struct {
	// UnixCharDevs is a slice of unix-char device configuration.
	UnixCharDevs []map[string]string `json:"unix_char_devs" yaml:"unix_char_devs"`
	// BindMounts is a slice of mount configuration.
	BindMounts []map[string]string `json:"bind_mounts" yaml:"bind_mounts"`
}
