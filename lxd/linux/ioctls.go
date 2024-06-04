package linux

/*
#include <linux/btrfs.h>
#include <linux/hidraw.h>
#include <linux/vhost.h>
*/
import "C"

// IoctlBtrfsSetReceivedSubvol is used to set information about a received subvolume.
const IoctlBtrfsSetReceivedSubvol = C.BTRFS_IOC_SET_RECEIVED_SUBVOL

// IoctlHIDIOCGrawInfo contains the bus type, the vendor ID (VID), and product ID (PID) of the device.
const IoctlHIDIOCGrawInfo = C.HIDIOCGRAWINFO

// IoctlVhostVsockSetGuestCid is used to set the vsock guest context ID.
const IoctlVhostVsockSetGuestCid = C.VHOST_VSOCK_SET_GUEST_CID
