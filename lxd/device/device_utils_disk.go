package device

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// StorageVolumeMount checks if storage volume is mounted and if not tries to mount it.
var StorageVolumeMount func(s *state.State, poolName string, volumeName string, volumeTypeName string, instance InstanceIdentifier) error

// StorageVolumeUmount unmounts a storage volume.
var StorageVolumeUmount func(s *state.State, poolName string, volumeName string, volumeType int) error

// BlockFsDetect detects the type of block device.
func BlockFsDetect(dev string) (string, error) {
	out, err := shared.RunCommand("blkid", "-s", "TYPE", "-o", "value", dev)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// IsBlockdev returns boolean indicating whether device is block type.
func IsBlockdev(path string) bool {
	// Get a stat struct from the provided path
	stat := unix.Stat_t{}
	err := unix.Stat(path, &stat)
	if err != nil {
		return false
	}

	// Check if it's a block device
	if stat.Mode&unix.S_IFMT == unix.S_IFBLK {
		return true
	}

	// Not a device
	return false
}

// DiskMount mounts a disk device.
func DiskMount(srcPath string, dstPath string, readonly bool, recursive bool, propagation string) error {
	var err error

	// Prepare the mount flags
	flags := 0
	if readonly {
		flags |= unix.MS_RDONLY
	}

	// Detect the filesystem
	fstype := "none"
	if IsBlockdev(srcPath) {
		fstype, err = BlockFsDetect(srcPath)
		if err != nil {
			return err
		}
	} else {
		flags |= unix.MS_BIND
		if propagation != "" {
			switch propagation {
			case "private":
				flags |= unix.MS_PRIVATE
			case "shared":
				flags |= unix.MS_SHARED
			case "slave":
				flags |= unix.MS_SLAVE
			case "unbindable":
				flags |= unix.MS_UNBINDABLE
			case "rprivate":
				flags |= unix.MS_PRIVATE | unix.MS_REC
			case "rshared":
				flags |= unix.MS_SHARED | unix.MS_REC
			case "rslave":
				flags |= unix.MS_SLAVE | unix.MS_REC
			case "runbindable":
				flags |= unix.MS_UNBINDABLE | unix.MS_REC
			default:
				return fmt.Errorf("Invalid propagation mode '%s'", propagation)
			}
		}

		if recursive {
			flags |= unix.MS_REC
		}
	}

	// Mount the filesystem
	err = unix.Mount(srcPath, dstPath, fstype, uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("Unable to mount %s at %s: %s", srcPath, dstPath, err)
	}

	// Remount bind mounts in readonly mode if requested
	if readonly == true && flags&unix.MS_BIND == unix.MS_BIND {
		flags = unix.MS_RDONLY | unix.MS_BIND | unix.MS_REMOUNT
		err = unix.Mount("", dstPath, fstype, uintptr(flags), "")
		if err != nil {
			return fmt.Errorf("Unable to mount %s in readonly mode: %s", dstPath, err)
		}
	}

	flags = unix.MS_REC | unix.MS_SLAVE
	err = unix.Mount("", dstPath, "", uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("unable to make mount %s private: %s", dstPath, err)
	}

	return nil
}
