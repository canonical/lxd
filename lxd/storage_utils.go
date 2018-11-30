package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// Options for filesystem creation
type mkfsOptions struct {
	label string
}

// Export the mount options map since we might find it useful in other parts of
// LXD.
type mountOptions struct {
	capture bool
	flag    uintptr
}

var MountOptions = map[string]mountOptions{
	"async":         {false, syscall.MS_SYNCHRONOUS},
	"atime":         {false, syscall.MS_NOATIME},
	"bind":          {true, syscall.MS_BIND},
	"defaults":      {true, 0},
	"dev":           {false, syscall.MS_NODEV},
	"diratime":      {false, syscall.MS_NODIRATIME},
	"dirsync":       {true, syscall.MS_DIRSYNC},
	"exec":          {false, syscall.MS_NOEXEC},
	"lazytime":      {true, MS_LAZYTIME},
	"mand":          {true, syscall.MS_MANDLOCK},
	"noatime":       {true, syscall.MS_NOATIME},
	"nodev":         {true, syscall.MS_NODEV},
	"nodiratime":    {true, syscall.MS_NODIRATIME},
	"noexec":        {true, syscall.MS_NOEXEC},
	"nomand":        {false, syscall.MS_MANDLOCK},
	"norelatime":    {false, syscall.MS_RELATIME},
	"nostrictatime": {false, syscall.MS_STRICTATIME},
	"nosuid":        {true, syscall.MS_NOSUID},
	"rbind":         {true, syscall.MS_BIND | syscall.MS_REC},
	"relatime":      {true, syscall.MS_RELATIME},
	"remount":       {true, syscall.MS_REMOUNT},
	"ro":            {true, syscall.MS_RDONLY},
	"rw":            {false, syscall.MS_RDONLY},
	"strictatime":   {true, syscall.MS_STRICTATIME},
	"suid":          {false, syscall.MS_NOSUID},
	"sync":          {true, syscall.MS_SYNCHRONOUS},
}

func lxdResolveMountoptions(options string) (uintptr, string) {
	mountFlags := uintptr(0)
	tmp := strings.SplitN(options, ",", -1)
	for i := 0; i < len(tmp); i++ {
		opt := tmp[i]
		do, ok := MountOptions[opt]
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

// Useful functions for unreliable backends
func tryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func tryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Unmount(path, flags)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func storageValidName(value string) error {
	if strings.Contains(value, "/") {
		return fmt.Errorf("Invalid storage volume name \"%s\". Storage volumes cannot contain \"/\" in their name", value)
	}

	return nil
}

func storageConfigDiff(oldConfig map[string]string, newConfig map[string]string) ([]string, bool) {
	changedConfig := []string{}
	userOnly := true
	for key := range oldConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range newConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Skip on no change
	if len(changedConfig) == 0 {
		return nil, false
	}

	return changedConfig, userOnly
}

// Default permissions for folders in ${LXD_DIR}
const storagePoolsDirMode os.FileMode = 0711
const containersDirMode os.FileMode = 0711
const customDirMode os.FileMode = 0711
const imagesDirMode os.FileMode = 0700
const snapshotsDirMode os.FileMode = 0700

// Detect whether LXD already uses the given storage pool.
func lxdUsesPool(dbObj *db.Cluster, onDiskPoolName string, driver string, onDiskProperty string) (bool, string, error) {
	pools, err := dbObj.StoragePools()
	if err != nil && err != db.ErrNoSuchObject {
		return false, "", err
	}

	for _, pool := range pools {
		_, pl, err := dbObj.StoragePoolGet(pool)
		if err != nil {
			continue
		}

		if pl.Driver != driver {
			continue
		}

		if pl.Config[onDiskProperty] == onDiskPoolName {
			return true, pl.Name, nil
		}
	}

	return false, "", nil
}

func makeFSType(path string, fsType string, options *mkfsOptions) (string, error) {
	var err error
	var msg string

	fsOptions := options
	if fsOptions == nil {
		fsOptions = &mkfsOptions{}
	}

	cmd := []string{fmt.Sprintf("mkfs.%s", fsType), path}
	if fsOptions.label != "" {
		cmd = append(cmd, "-L", fsOptions.label)
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

func fsGenerateNewUUID(fstype string, lvpath string) (string, error) {
	switch fstype {
	case "btrfs":
		return btrfsGenerateNewUUID(lvpath)
	case "xfs":
		return xfsGenerateNewUUID(lvpath)
	}

	return "", nil
}

func xfsGenerateNewUUID(lvpath string) (string, error) {
	msg, err := shared.RunCommand(
		"xfs_admin",
		"-U", "generate",
		lvpath)
	if err != nil {
		return msg, err
	}

	return "", nil
}

func btrfsGenerateNewUUID(lvpath string) (string, error) {
	msg, err := shared.RunCommand(
		"btrfstune",
		"-f",
		"-u",
		lvpath)
	if err != nil {
		return msg, err
	}

	return "", nil
}

func growFileSystem(fsType string, devPath string, mntpoint string) error {
	var msg string
	var err error
	switch fsType {
	case "": // if not specified, default to ext4
		fallthrough
	case "ext4":
		msg, err = shared.TryRunCommand("resize2fs", devPath)
	case "xfs":
		msg, err = shared.TryRunCommand("xfs_growfs", devPath)
	case "btrfs":
		msg, err = shared.TryRunCommand("btrfs", "filesystem", "resize", "max", mntpoint)
	default:
		return fmt.Errorf(`Growing not supported for filesystem type "%s"`, fsType)
	}

	if err != nil {
		errorMsg := fmt.Sprintf(`Could not extend underlying %s filesystem for "%s": %s`, fsType, devPath, msg)
		logger.Errorf(errorMsg)
		return fmt.Errorf(errorMsg)
	}

	logger.Debugf(`extended underlying %s filesystem for "%s"`, fsType, devPath)
	return nil
}

func shrinkFileSystem(fsType string, devPath string, mntpoint string, byteSize int64) error {
	strSize := fmt.Sprintf("%dK", byteSize/1024)

	switch fsType {
	case "": // if not specified, default to ext4
		fallthrough
	case "ext4":
		_, err := shared.TryRunCommand("e2fsck", "-f", "-y", devPath)
		if err != nil {
			return err
		}

		_, err = shared.TryRunCommand("resize2fs", devPath, strSize)
		if err != nil {
			return err
		}
	case "btrfs":
		_, err := shared.TryRunCommand("btrfs", "filesystem", "resize", strSize, mntpoint)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf(`Shrinking not supported for filesystem type "%s"`, fsType)
	}

	return nil
}

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

	err := shrinkFileSystem(fsType, devPath, mntpoint, byteSize)
	return cleanupFunc, err
}

func storageResource(path string) (*api.ResourcesStoragePool, error) {
	st, err := shared.Statvfs(path)
	if err != nil {
		return nil, err
	}

	res := api.ResourcesStoragePool{}
	res.Space.Total = st.Blocks * uint64(st.Bsize)
	res.Space.Used = (st.Blocks - st.Bfree) * uint64(st.Bsize)

	// Some filesystems don't report inodes since they allocate them
	// dynamically e.g. btrfs.
	if st.Files > 0 {
		res.Inodes.Total = st.Files
		res.Inodes.Used = st.Files - st.Ffree
	}

	return &res, nil
}
