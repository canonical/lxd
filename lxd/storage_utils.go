package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/instance"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

func shrinkVolumeFilesystem(s storage, volumeType int, fsType string, devPath string, mntpoint string, byteSize int64, data interface{}) (func() (bool, error), error) {
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
			c := data.(instance.Instance)
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

// MkfsOptions represents options for filesystem creation.
type mkfsOptions struct {
	Label string
}

// MakeFSType creates the provided filesystem.
func makeFSType(path string, fsType string, options *mkfsOptions) (string, error) {
	var err error
	var msg string

	fsOptions := options
	if fsOptions == nil {
		fsOptions = &mkfsOptions{}
	}

	cmd := []string{fmt.Sprintf("mkfs.%s", fsType), path}
	if fsOptions.Label != "" {
		cmd = append(cmd, "-L", fsOptions.Label)
	}

	if fsType == "ext4" {
		cmd = append(cmd, "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0")
	}

	msg, err = shared.TryRunCommand(cmd[0], cmd[1:]...)
	if err != nil {
		return msg, err
	}

	return "", nil
}

// mountOption represents an individual mount option.
type mountOption struct {
	capture bool
	flag    uintptr
}

// mountOptions represents a list of possible mount options.
var mountOptions = map[string]mountOption{
	"async":         {false, unix.MS_SYNCHRONOUS},
	"atime":         {false, unix.MS_NOATIME},
	"bind":          {true, unix.MS_BIND},
	"defaults":      {true, 0},
	"dev":           {false, unix.MS_NODEV},
	"diratime":      {false, unix.MS_NODIRATIME},
	"dirsync":       {true, unix.MS_DIRSYNC},
	"exec":          {false, unix.MS_NOEXEC},
	"lazytime":      {true, unix.MS_LAZYTIME},
	"mand":          {true, unix.MS_MANDLOCK},
	"noatime":       {true, unix.MS_NOATIME},
	"nodev":         {true, unix.MS_NODEV},
	"nodiratime":    {true, unix.MS_NODIRATIME},
	"noexec":        {true, unix.MS_NOEXEC},
	"nomand":        {false, unix.MS_MANDLOCK},
	"norelatime":    {false, unix.MS_RELATIME},
	"nostrictatime": {false, unix.MS_STRICTATIME},
	"nosuid":        {true, unix.MS_NOSUID},
	"rbind":         {true, unix.MS_BIND | unix.MS_REC},
	"relatime":      {true, unix.MS_RELATIME},
	"remount":       {true, unix.MS_REMOUNT},
	"ro":            {true, unix.MS_RDONLY},
	"rw":            {false, unix.MS_RDONLY},
	"strictatime":   {true, unix.MS_STRICTATIME},
	"suid":          {false, unix.MS_NOSUID},
	"sync":          {true, unix.MS_SYNCHRONOUS},
}

// resolveMountOptions resolves the provided mount options.
func resolveMountOptions(options string) (uintptr, string) {
	mountFlags := uintptr(0)
	tmp := strings.SplitN(options, ",", -1)
	for i := 0; i < len(tmp); i++ {
		opt := tmp[i]
		do, ok := mountOptions[opt]
		if !ok {
			continue
		}

		if do.capture {
			mountFlags |= do.flag
		} else {
			mountFlags &= ^do.flag
		}

		copy(tmp[i:], tmp[i+1:])
		tmp[len(tmp)-1] = ""
		tmp = tmp[:len(tmp)-1]
		i--
	}

	return mountFlags, strings.Join(tmp, ",")
}
