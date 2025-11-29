package linux

/*
#include <linux/btrfs.h>
#include <linux/hidraw.h>
#include <linux/vhost.h>

#define ZFS_MAX_DATASET_NAME_LEN 256
#define BLKZNAME _IOR(0x12, 125, char[ZFS_MAX_DATASET_NAME_LEN])
*/
import "C"

// IoctlBtrfsSetReceivedSubvol is used to set information about a received subvolume.
const IoctlBtrfsSetReceivedSubvol = C.BTRFS_IOC_SET_RECEIVED_SUBVOL

// IoctlHIDIOCGrawInfo contains the bus type, the vendor ID (VID), and product ID (PID) of the device.
const IoctlHIDIOCGrawInfo = C.HIDIOCGRAWINFO

// IoctlVhostVsockSetGuestCid is used to set the vsock guest context ID.
const IoctlVhostVsockSetGuestCid = C.VHOST_VSOCK_SET_GUEST_CID

// IoctlBlkZname matches BLKZNAME (ZFS specific).
const IoctlBlkZname = C.BLKZNAME

// ZFSMaxDatasetNameLen is the maximum length of a ZFS dataset name.
const ZFSMaxDatasetNameLen = C.ZFS_MAX_DATASET_NAME_LEN
