package cdi

const (
	// CDIHookCmdKey is used to reference a CDI hook command in a GPUDevice run config.
	CDIHookCmdKey = "cdiHookCmdKey"
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
