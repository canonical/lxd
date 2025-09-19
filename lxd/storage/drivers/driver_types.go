package drivers

// Info represents information about a storage driver.
type Info struct {
	// Name of the storage driver.
	Name string

	// Version of the storage driver.
	Version string

	// Supported volume types.
	VolumeTypes []VolumeType

	// Default block volume size.
	DefaultBlockSize string

	// Default volume size for VM block filesystems.
	DefaultVMBlockFilesystemSize string

	// Buckets supported.
	Buckets bool

	// Whether the driver uses a remote backing store.
	Remote bool

	// Whether volumes can be used on multiple nodes concurrently.
	VolumeMultiNode bool

	// Whether driver stores images as separate volume.
	OptimizedImages bool

	// Whether driver supports optimized volume backups.
	OptimizedBackups bool

	// Whether driver generates an optimised backup header file in backup.
	OptimizedBackupHeader bool

	// Whether driver preserves inodes when volumes are moved hosts.
	PreservesInodes bool

	// Whether driver uses block devices as backing store.
	BlockBacking bool

	// Whether instance should be frozen during snapshot if running.
	RunningCopyFreeze bool

	// Whether the driver supports direct I/O.
	DirectIO bool

	// Whether the driver supports io_uring.
	IOUring bool

	// Whether the pool directory itself is a mount.
	MountedRoot bool

	// Whether the volume should have parent UUID populated before any action.
	PopulateParentVolumeUUID bool

	// Whether the volume name is translated using its UUID.
	// If set to true, any volume returned by the driver's ListVolumes function
	// has to be handled with care because the name of the volume on storage does not
	// relate to the volume's actual name inside LXD's database.
	// Drivers usually take the volume's UUID and translate it to a base64 encoded string.
	UUIDVolumeNames bool
}

// VolumeFiller provides a struct for filling a volume.
type VolumeFiller struct {
	Fill func(vol Volume, rootBlockPath string, allowUnsafeResize bool) (int64, error) // Function to fill the volume.
	Size int64                                                                         // Size of the unpacked volume in bytes.

	Fingerprint string // If the Filler will unpack an image, it should be this fingerprint.
}
