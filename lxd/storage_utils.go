package main

import (
	"fmt"

	"github.com/lxc/lxd/lxd/instance"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared/logger"
)

func shrinkVolumeFilesystem(s instance.Storage, volumeType int, fsType string, devPath string, mntpoint string, byteSize int64, data interface{}) (func() (bool, error), error) {
	var cleanupFunc func() (bool, error)
	switch fsType {
	case "xfs":
		logger.Errorf("XFS filesystems cannot be shrunk: dump, mkfs, and restore are required")
		return nil, fmt.Errorf("xfs filesystems cannot be shrunk: dump, mkfs, and restore are required")
	case "btrfs":
		fallthrough
	case "": // if not specified, default to ext4
		fallthrough
	case "ext4":
		switch volumeType {
		case storagePoolVolumeTypeContainer:
			c := data.(container)
			ourMount, err := c.StorageStop()
			if err != nil {
				return nil, err
			}
			if !ourMount {
				cleanupFunc = c.StorageStart
			}
		case storagePoolVolumeTypeCustom:
			ourMount, err := s.StoragePoolVolumeUmount()
			if err != nil {
				return nil, err
			}
			if !ourMount {
				cleanupFunc = s.StoragePoolVolumeMount
			}
		default:
			return nil, fmt.Errorf(`Resizing not implemented for storage volume type %d`, volumeType)
		}

	default:
		return nil, fmt.Errorf(`Shrinking not supported for filesystem type "%s"`, fsType)
	}

	err := driver.ShrinkFileSystem(fsType, devPath, mntpoint, byteSize)
	return cleanupFunc, err
}
