package util

import (
	"syscall"

	"github.com/lxc/lxd/shared/logger"
)

// Filesystem magic numbers
const (
	FilesystemSuperMagicTmpfs = 0x01021994
	FilesystemSuperMagicExt4  = 0xEF53
	FilesystemSuperMagicXfs   = 0x58465342
	FilesystemSuperMagicNfs   = 0x6969
	FilesystemSuperMagicZfs   = 0x2fc12fc1
)

// FilesystemDetect returns the filesystem on which the passed-in path sits.
func FilesystemDetect(path string) (string, error) {
	fs := syscall.Statfs_t{}

	err := syscall.Statfs(path, &fs)
	if err != nil {
		return "", err
	}

	switch fs.Type {
	case FilesystemSuperMagicBtrfs:
		return "btrfs", nil
	case FilesystemSuperMagicZfs:
		return "zfs", nil
	case FilesystemSuperMagicTmpfs:
		return "tmpfs", nil
	case FilesystemSuperMagicExt4:
		return "ext4", nil
	case FilesystemSuperMagicXfs:
		return "xfs", nil
	case FilesystemSuperMagicNfs:
		return "nfs", nil
	default:
		logger.Debugf("Unknown backing filesystem type: 0x%x", fs.Type)
		return string(fs.Type), nil
	}
}
