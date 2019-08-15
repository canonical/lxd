package storage

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// MkfsOptions represents options for filesystem creation.
type MkfsOptions struct {
	Label string
}

// Export the mount options map since we might find it useful in other parts of
// LXD.
type mountOptions struct {
	capture bool
	flag    uintptr
}

// MountOptions represents a list of possible mount options.
var MountOptions = map[string]mountOptions{
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

// LXDResolveMountoptions resolves the provided mount options.
func LXDResolveMountoptions(options string) (uintptr, string) {
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

// TryMount tries mounting a filesystem multiple times. This is useful for unreliable backends.
func TryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	for i := 0; i < 20; i++ {
		err = unix.Mount(src, dst, fs, flags, options)
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

// TryUnmount tries unmounting a filesystem multiple times. This is useful for unreliable backends.
func TryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = unix.Unmount(path, flags)
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

// ValidName validates the provided name, and returns an error if it's not a valid storage name.
func ValidName(value string) error {
	if strings.Contains(value, "/") {
		return fmt.Errorf("Invalid storage volume name \"%s\". Storage volumes cannot contain \"/\" in their name", value)
	}

	return nil
}

// ConfigDiff returns a diff of the provided configs. Additionally, it returns whether or not
// only user properties have been changed.
func ConfigDiff(oldConfig map[string]string, newConfig map[string]string) ([]string, bool) {
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

// StoragePoolsDirMode represents the default permissions for the storage pool directory.
const StoragePoolsDirMode os.FileMode = 0711

// ContainersDirMode represents the default permissions for the containers directory.
const ContainersDirMode os.FileMode = 0711

// CustomDirMode represents the default permissions for the custom directory.
const CustomDirMode os.FileMode = 0711

// ImagesDirMode represents the default permissions for the images directory.
const ImagesDirMode os.FileMode = 0700

// SnapshotsDirMode represents the default permissions for the snapshots directory.
const SnapshotsDirMode os.FileMode = 0700

// LXDUsesPool detect whether LXD already uses the given storage pool.
func LXDUsesPool(dbObj *db.Cluster, onDiskPoolName string, driver string, onDiskProperty string) (bool, string, error) {
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

// MakeFSType creates the provided filesystem.
func MakeFSType(path string, fsType string, options *MkfsOptions) (string, error) {
	var err error
	var msg string

	fsOptions := options
	if fsOptions == nil {
		fsOptions = &MkfsOptions{}
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

// FSGenerateNewUUID generates a UUID for the given path for btrfs and xfs filesystems.
func FSGenerateNewUUID(fstype string, lvpath string) (string, error) {
	switch fstype {
	case "btrfs":
		return btrfsGenerateNewUUID(lvpath)
	case "xfs":
		return xfsGenerateNewUUID(lvpath)
	}

	return "", nil
}

func xfsGenerateNewUUID(devPath string) (string, error) {
	// Attempt to generate a new UUID
	msg, err := shared.RunCommand("xfs_admin", "-U", "generate", devPath)
	if err != nil {
		return msg, err
	}

	if msg != "" {
		// Exit 0 with a msg usually means some log entry getting in the way
		msg, err = shared.RunCommand("xfs_repair", "-o", "force_geometry", "-L", devPath)
		if err != nil {
			return msg, err
		}

		// Attempt to generate a new UUID again
		msg, err = shared.RunCommand("xfs_admin", "-U", "generate", devPath)
		if err != nil {
			return msg, err
		}
	}

	return msg, nil
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

	return msg, nil
}

// GrowFileSystem grows a filesystem if it is supported.
func GrowFileSystem(fsType string, devPath string, mntpoint string) error {
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

// ShrinkFileSystem shrinks a filesystem if it is supported.
func ShrinkFileSystem(fsType string, devPath string, mntpoint string, byteSize int64) error {
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

// GetStorageResource returns the available resources of a given path.
func GetStorageResource(path string) (*api.ResourcesStoragePool, error) {
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
