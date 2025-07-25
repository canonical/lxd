package config

import (
	"github.com/canonical/lxd/shared/revert"
)

// MountOwnerShiftNone do not use owner shifting.
const MountOwnerShiftNone = ""

// MountOwnerShiftDynamic use shifted mounts for dynamic owner shifting.
const MountOwnerShiftDynamic = "dynamic"

// MountOwnerShiftStatic statically modify ownership.
const MountOwnerShiftStatic = "static"

// RunConfigItem represents a single config item.
type RunConfigItem struct {
	Key   string
	Value string
}

// DevSource is either:
// - A path on the LXD host.
// - A file descriptor held by the LXD process.
// - A Ceph RBD Image description.
type DevSource any

// DevSourcePath is a path on the LXD host.
// See `deviceConfig.DevSource`.
type DevSourcePath struct {
	Path string
}

// DevSourceFD is a file descriptor held by the LXD process.
// See `deviceConfig.DevSource`.
type DevSourceFD struct {
	FD   uintptr
	Path string
}

// DevSourceRBD describes an RBD image.
// See `deviceConfig.DevSource`.
//
// This structure roughly corresponds to a qmp BlockdevOptionsRbd:
// https://www.qemu.org/docs/master/interop/qemu-storage-daemon-qmp-ref.html#qapidoc-708
type DevSourceRBD struct {
	ClusterName string
	UserName    string
	PoolName    string
	ImageName   string
	Snapshot    string
}

// MountEntryItem represents a single mount entry item.
type MountEntryItem struct {
	DevName    string      // The internal name for the device.
	DevSource  DevSource   // Describes the block special device or remote filesystem to be mounted.
	TargetPath string      // Describes the mount point (target) for the filesystem.
	FSType     string      // Describes the type of the filesystem.
	Opts       []string    // Describes the mount options associated with the filesystem.
	Freq       int         // Used by dump(8) to determine which filesystems need to be dumped. Defaults to zero (don't dump) if not present.
	PassNo     int         // Used by fsck(8) to determine the order in which filesystem checks are done at boot time. Defaults to zero (don't fsck) if not present.
	OwnerShift string      // Ownership shifting mode, use constants MountOwnerShiftNone, MountOwnerShiftStatic or MountOwnerShiftDynamic.
	Limits     *DiskLimits // Disk limits.
}

// RootFSEntryItem represents the root filesystem options for an Instance.
type RootFSEntryItem struct {
	Path string   // Describes the root file system source.
	Opts []string // Describes the mount options associated with the filesystem.
}

// USBDeviceItem represents a single USB device matched from LXD USB device specification.
type USBDeviceItem struct {
	DeviceName     string
	HostDevicePath string
}

// DiskLimits represents a set of I/O disk limits.
type DiskLimits struct {
	ReadBytes  int64
	ReadIOps   int64
	WriteBytes int64
	WriteIOps  int64
}

// RunConfig represents run-time config used for device setup/cleanup.
type RunConfig struct {
	BusNum           uint8            // Allocated bus number for the device.
	RootFS           RootFSEntryItem  // RootFS to setup.
	NetworkInterface []RunConfigItem  // Network interface configuration settings.
	CGroups          []RunConfigItem  // Cgroup rules to setup.
	Mounts           []MountEntryItem // Mounts to setup/remove.
	Uevents          [][]string       // Uevents to inject.
	PostHooks        []func() error   // Functions to be run after device attach/detach.
	GPUDevice        []RunConfigItem  // GPU device configuration settings.
	USBDevice        []USBDeviceItem  // USB device configuration settings.
	TPMDevice        []RunConfigItem  // TPM device configuration settings.
	PCIDevice        []RunConfigItem  // PCI device configuration settings.
	Revert           revert.Hook      // Revert setup of device on post-setup error.
}

// NICConfigDir shared constant used to indicate where NIC config is stored.
const NICConfigDir = "nics"

// NICConfig contains network interface configuration to be passed into a VM and applied by the agent.
type NICConfig struct {
	DeviceName string `json:"device_name"`
	NICName    string `json:"nic_name"`
	MACAddress string `json:"mac_address"`
	MTU        uint32 `json:"mtu"`
}
