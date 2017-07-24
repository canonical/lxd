package main

import (
	"database/sql"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/lxc/lxd/shared"
)

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

	if err != nil && err == syscall.EBUSY {
		return err
	}

	return nil
}

func storageValidName(value string) error {
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
const containersDirMode os.FileMode = 0755
const customDirMode os.FileMode = 0755
const imagesDirMode os.FileMode = 0700
const snapshotsDirMode os.FileMode = 0700

// Detect whether LXD already uses the given storage pool.
func lxdUsesPool(db *sql.DB, onDiskPoolName string, driver string, onDiskProperty string) (bool, string, error) {
	pools, err := dbStoragePools(db)
	if err != nil && err != NoSuchObjectError {
		return false, "", err
	}

	for _, pool := range pools {
		_, pl, err := dbStoragePoolGet(db, pool)
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

func makeFSType(path string, fsType string) (string, error) {
	var err error
	var msg string

	switch fsType {
	case "xfs":
		msg, err = shared.TryRunCommand("mkfs.xfs", path)
	default:
		msg, err = shared.TryRunCommand(
			"mkfs.ext4",
			"-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0",
			path)
	}
	if err != nil {
		return msg, err
	}

	return "", nil
}
