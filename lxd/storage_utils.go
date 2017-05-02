package main

import (
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
)

// Export the mount options map since we might find it useful in other parts of
// LXD.
type mountOptions struct {
	unset bool
	flag  uintptr
}

var MountOptions = map[string]mountOptions{
	"async":         {true, syscall.MS_SYNCHRONOUS},
	"atime":         {true, syscall.MS_NOATIME},
	"bind":          {false, syscall.MS_BIND},
	"defaults":      {false, 0},
	"dev":           {true, syscall.MS_NODEV},
	"diratime":      {true, syscall.MS_NODIRATIME},
	"dirsync":       {false, syscall.MS_DIRSYNC},
	"exec":          {true, syscall.MS_NOEXEC},
	"mand":          {false, syscall.MS_MANDLOCK},
	"noatime":       {false, syscall.MS_NOATIME},
	"nodev":         {false, syscall.MS_NODEV},
	"nodiratime":    {false, syscall.MS_NODIRATIME},
	"noexec":        {false, syscall.MS_NOEXEC},
	"nomand":        {true, syscall.MS_MANDLOCK},
	"norelatime":    {true, syscall.MS_RELATIME},
	"nostrictatime": {true, syscall.MS_STRICTATIME},
	"nosuid":        {false, syscall.MS_NOSUID},
	"rbind":         {false, syscall.MS_BIND | syscall.MS_REC},
	"relatime":      {false, syscall.MS_RELATIME},
	"remount":       {false, syscall.MS_REMOUNT},
	"ro":            {false, syscall.MS_RDONLY},
	"rw":            {true, syscall.MS_RDONLY},
	"strictatime":   {false, syscall.MS_STRICTATIME},
	"suid":          {true, syscall.MS_NOSUID},
	"sync":          {false, syscall.MS_SYNCHRONOUS},
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
