package main

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/units"
)

func storagePoolFillDefault(name string, driver string, config map[string]string) error {
	if driver == "dir" || driver == "ceph" || driver == "cephfs" {
		if config["size"] != "" {
			return fmt.Errorf(`The "size" property does not apply to %q storage pools`, driver)
		}
	} else {
		if config["size"] == "" {
			st := unix.Statfs_t{}
			err := unix.Statfs(shared.VarPath(), &st)
			if err != nil {
				return fmt.Errorf("Couldn't statfs %s: %w", shared.VarPath(), err)
			}

			/* choose 5 GB < x < 30GB, where x is 20% of the disk size */
			defaultSize := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
			if defaultSize > 30 {
				defaultSize = 30
			}
			if defaultSize < 5 {
				defaultSize = 5
			}

			config["size"] = strconv.FormatUint(uint64(defaultSize), 10) + "GB"
		} else {
			_, err := units.ParseByteSizeString(config["size"])
			if err != nil {
				return err
			}
		}
	}

	if driver == "lvm" {
		// We use thin pools by default.
		useThinpool := true
		if config["lvm.use_thinpool"] != "" {
			useThinpool = shared.IsTrue(config["lvm.use_thinpool"])
		}

		if useThinpool && config["lvm.thinpool_name"] == "" {
			// Unchangeable pool property: Set unconditionally.
			config["lvm.thinpool_name"] = "LXDThinPool"
		}
	}

	if config["volume.size"] != "" {
		_, err := units.ParseByteSizeString(config["volume.size"])
		if err != nil {
			return err
		}
	}

	return nil
}
