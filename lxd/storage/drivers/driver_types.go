package drivers

// Info represents information about a storage driver.
type Info struct {
	Name                  string
	Version               string
	VolumeTypes           []VolumeType // Supported volume types.
	Remote                bool         // Whether the driver uses a remote backing store.
	OptimizedImages       bool         // Whether driver stores images as separate volume.
	PreservesInodes       bool         // Whether driver preserves inodes when volumes are moved hosts.
	BlockBacking          bool         // Whether driver uses block devices as backing store.
	RunningQuotaResize    bool         // Whether quota resize is supported whilst instance running.
	RunningSnapshotFreeze bool         // Whether instance should be frozen during snapshot if running.
}

// VolumeFiller provides a struct for filling a volume.
type VolumeFiller struct {
	Fill func(mountPath, rootBlockPath string) error // Function to fill the volume.

	Fingerprint string // If the Filler will unpack an image, it should be this fingerprint.
}
