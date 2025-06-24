package filters

// IsDisk evaluates whether or not the given device is of type disk.
func IsDisk(device map[string]string) bool {
	return device["type"] == "disk"
}

// IsFilesystemDisk evaluates whether or not the given device contains a filesystem.
// A filesystem disk device always holds a target path.
// It returns true for custom volumes of type filesystem, root volumes and host's filesystem shares.
// One exception are VM root volumes for which IsFilesystemDisk also returns true even though
// they are not mountable because the IsRootDisk filter also applies for them.
func IsFilesystemDisk(device map[string]string) bool {
	return Or(IsCustomVolumeFilesystemDisk, IsRootDisk, IsHostFilesystemShareDisk)(device)
}

// IsRootDisk evaluates whether or not the given device is a root volume.
// Root disk devices also need a non-empty "pool" property, but we can't check that here
// because this function is used with clients talking to older servers where there was no
// concept of a storage pool, and also it is used for migrating from old to new servers.
// The validation of the non-empty "pool" property is done inside the disk device itself.
func IsRootDisk(device map[string]string) bool {
	return IsDisk(device) && device["path"] == "/" && device["source"] == ""
}

// IsCustomVolumeDisk evaluates whether or not the given device is a custom volume disk.
// It returns true for both custom volumes of type block and filesystem.
func IsCustomVolumeDisk(device map[string]string) bool {
	return Or(IsCustomVolumeBlockDisk, IsCustomVolumeFilesystemDisk)(device)
}

// IsCustomVolumeBlockDisk evaluates whether or not the given device is a custom volume of type block.
func IsCustomVolumeBlockDisk(device map[string]string) bool {
	return IsDisk(device) && device["path"] == "" && device["source"] != "" && device["pool"] != ""
}

// IsCustomVolumeFilesystemDisk evaluates whether or not the given device is a custom volume of type filesystem.
func IsCustomVolumeFilesystemDisk(device map[string]string) bool {
	return IsDisk(device) && device["path"] != "/" && device["path"] != "" && device["source"] != "" && device["pool"] != ""
}

// IsHostFilesystemShareDisk evaluates whether or not the given device is a host's filesystem share.
func IsHostFilesystemShareDisk(device map[string]string) bool {
	return IsDisk(device) && device["path"] != "/" && device["path"] != "" && device["source"] != "" && device["pool"] == ""
}
