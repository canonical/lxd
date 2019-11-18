package drivers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/units"
)

var btrfsVersion string
var btrfsLoaded bool

type btrfs struct {
	common

	remount uintptr
}

// Info returns info about the driver and its environment.
func (d *btrfs) Info() Info {
	return Info{
		Name:               "btrfs",
		Version:            btrfsVersion,
		OptimizedImages:    true,
		PreservesInodes:    !d.state.OS.RunningInUserNS,
		Remote:             false,
		VolumeTypes:        []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:       false,
		RunningQuotaResize: true,
	}
}

func (d *btrfs) Create() error {
	// WARNING: The Create() function cannot rely on any of the struct attributes being set.

	// Set default source if missing.
	defaultSource := filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", d.name))
	source := d.config["source"]

	if source == "" {
		source = defaultSource
		d.config["source"] = source
	} else if strings.HasPrefix(source, "/") {
		source = shared.HostPath(source)
	} else {
		return fmt.Errorf("Invalid \"source\" property")
	}

	poolMntPoint := GetPoolMountPath(d.name)
	isBlockDev := false

	if source == defaultSource {
		size, err := units.ParseByteSizeString(d.config["size"])
		if err != nil {
			return err
		}

		err = createSparseFile(source, size)
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %q: %s", source, err)
		}

		output, err := makeFSType(source, "btrfs", &mkfsOptions{Label: d.name})
		if err != nil {
			return fmt.Errorf("Failed to create btrfs: %v (%s)", err, output)
		}
	} else {
		isBlockDev = shared.IsBlockdevPath(source)

		if isBlockDev {
			output, err := makeFSType(source, "btrfs", &mkfsOptions{Label: d.name})
			if err != nil {
				return fmt.Errorf("Failed to create btrfs: %v (%s)", err, output)
			}
		} else {
			if d.isSubvolume(source) {
				subvols, err := d.getSubvolume(source)
				if err != nil {
					return fmt.Errorf("Could not determine if existing btrfs subvolume ist empty: %s", err)
				}

				if len(subvols) > 0 {
					return fmt.Errorf("Requested btrfs subvolume exists but is not empty")
				}
			} else {
				cleanSource := filepath.Clean(source)
				lxdDir := shared.VarPath()

				if shared.PathExists(source) && !hasFilesystem(source, util.FilesystemSuperMagicBtrfs) {
					return fmt.Errorf("Existing path is neither a btrfs subvolume nor does it reside on a btrfs filesystem")
				} else if strings.HasPrefix(cleanSource, lxdDir) {
					if cleanSource != poolMntPoint {
						return fmt.Errorf("btrfs subvolumes requests in LXD directory %q are only valid under %q\n(e.g. source=%s)", shared.VarPath(), shared.VarPath("storage-pools"), poolMntPoint)
					} else if d.state.OS.BackingFS != "btrfs" {
						return fmt.Errorf("Creation of btrfs subvolume requested but %q does not reside on btrfs filesystem", source)
					}
				}

				err := d.createSubvolume(source)
				if err != nil {
					return err
				}
			}
		}
	}

	var err error
	var devUUID string
	mountFlags, mountOptions := resolveMountOptions(d.getMountOptions())
	mountFlags |= d.remount

	if isBlockDev {
		devUUID, _ = shared.LookupUUIDByBlockDevPath(source)
		// The symlink might not have been created even with the delay
		// we granted it above. So try to call btrfs filesystem show and
		// parse it out. (I __hate__ this!)
		if devUUID == "" {
			devUUID, err = d.lookupFsUUID(source)
			if err != nil {
				return err
			}
		}
		d.config["source"] = devUUID

		// If the symlink in /dev/disk/by-uuid hasn't been created yet
		// aka we only detected it by parsing btrfs filesystem show, we
		// cannot call StoragePoolMount() since it will try to do the
		// reverse operation. So instead we shamelessly mount using the
		// block device path at the time of pool creation.
		err = TryMount(source, GetPoolMountPath(d.name), "btrfs", mountFlags, mountOptions)
	} else {
		_, err = d.Mount()
	}
	if err != nil {
		return err
	}

	// Create default subvolumes.
	subvolumes := []string{
		filepath.Join(poolMntPoint, "containers"),
		filepath.Join(poolMntPoint, "containers-snapshots"),
		filepath.Join(poolMntPoint, "custom"),
		filepath.Join(poolMntPoint, "custom-snapshots"),
		filepath.Join(poolMntPoint, "images"),
		filepath.Join(poolMntPoint, "virtual-machines"),
		filepath.Join(poolMntPoint, "virtual-machines-snapshots"),
	}

	for _, subvol := range subvolumes {
		err := d.createSubvolume(subvol)
		if err != nil {
			return fmt.Errorf("Could not create btrfs subvolume: %s", subvol)
		}
	}

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *btrfs) Delete(op *operations.Operation) error {
	source := d.config["source"]

	if source == "" {
		return fmt.Errorf("no \"source\" property found for the storage pool")
	}

	if strings.HasPrefix(source, "/") {
		source = shared.HostPath(d.config["source"])
	}

	poolMntPoint := GetPoolMountPath(d.name)

	// Delete default subvolumes.
	subvolumes := []string{
		filepath.Join(poolMntPoint, "containers"),
		filepath.Join(poolMntPoint, "containers-snapshots"),
		filepath.Join(poolMntPoint, "custom"),
		filepath.Join(poolMntPoint, "custom-snapshots"),
		filepath.Join(poolMntPoint, "images"),
		filepath.Join(poolMntPoint, "virtual-machines"),
		filepath.Join(poolMntPoint, "virtual-machines-snapshots"),
	}

	for _, subvol := range subvolumes {
		err := d.deleteSubvolumes(subvol)
		if err != nil {
			return fmt.Errorf("Could not delete btrfs subvolume: %s", subvol)
		}
	}

	_, err := d.Unmount()
	if err != nil {
		return err
	}

	if filepath.IsAbs(source) {
		var err error
		cleanSource := filepath.Clean(source)
		sourcePath := shared.VarPath("disks", d.name)
		loopFilePath := sourcePath + ".img"

		if cleanSource == loopFilePath {
			// This is a loop file so simply remove it.
			err = os.Remove(source)
		} else {
			if !d.isFilesystem(source) && d.isSubvolume(source) {
				err = d.deleteSubvolumes(source)
			}
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return wipeDirectory(poolMntPoint)
}

// Mount mounts the storage pool.
func (d *btrfs) Mount() (bool, error) {
	source := d.config["source"]

	if source == "" {
		return false, fmt.Errorf("no \"source\" property found for the storage pool")
	}

	if strings.HasPrefix(source, "/") {
		source = shared.HostPath(d.config["source"])
	}

	path := GetPoolMountPath(d.name)

	if shared.IsMountPoint(path) && (d.remount&unix.MS_REMOUNT) == 0 {
		return false, nil
	}

	mountFlags, mountOptions := resolveMountOptions(d.getMountOptions())
	mountSource := source
	isBlockDev := shared.IsBlockdevPath(source)

	if filepath.IsAbs(source) {
		cleanSource := filepath.Clean(source)
		loopFilePath := shared.VarPath("disks", d.name+".img")

		if !isBlockDev && cleanSource == loopFilePath {
			// If source == "${LXD_DIR}"/disks/{pool_name} it is a
			// loop file we're dealing with.
			//
			// Since we mount the loop device LO_FLAGS_AUTOCLEAR is
			// fine since the loop device will be kept around for as
			// long as the mount exists.
			loopF, loopErr := PrepareLoopDev(source, LoFlagsAutoclear)
			if loopErr != nil {
				return false, loopErr
			}
			mountSource = loopF.Name()
			defer loopF.Close()
		} else if !isBlockDev && cleanSource != path {
			mountSource = source
			mountFlags |= unix.MS_BIND
		} else if !isBlockDev && cleanSource == path && d.state.OS.BackingFS == "btrfs" {
			return false, nil
		}
	} else {
		// User is using block device path.
		// Try to lookup the disk device by UUID but don't fail. If we
		// don't find one this might just mean we have been given the
		// UUID of a subvolume.
		byUUID := fmt.Sprintf("/dev/disk/by-uuid/%s", source)
		diskPath, err := os.Readlink(byUUID)
		if err == nil {
			mountSource = fmt.Sprintf("/dev/%s", strings.Trim(diskPath, "../../"))
		} else {
			// We have very likely been given a subvolume UUID. In
			// this case we should simply assume that the user has
			// mounted the parent of the subvolume or the subvolume
			// itself. Otherwise this becomes a really messy
			// detection task.
			return false, nil
		}
	}

	mountFlags |= d.remount
	err := TryMount(mountSource, path, "btrfs", mountFlags, mountOptions)
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *btrfs) Unmount() (bool, error) {
	path := GetPoolMountPath(d.name)
	return forceUnmount(path)
}

func (d *btrfs) GetResources() (*api.ResourcesStoragePool, error) {
	// Use the generic VFS resources.
	return d.vfsGetResources()
}

func (d *btrfs) Validate(config map[string]string) error {
	return nil
}

func (d *btrfs) Update(changedConfig map[string]string) error {
	val, ok := changedConfig["btrfs.mount_options"]
	if !ok {
		return nil
	}

	d.config["btrfs.mount_options"] = val
	d.remount |= unix.MS_REMOUNT

	_, err := d.Mount()
	if err != nil {
		return err
	}

	return nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools
// in preference order.
func (d *btrfs) MigrationTypes(contentType ContentType, refresh bool) []migration.Type {
	if contentType != ContentTypeFS {
		return nil
	}

	// When performing a refresh, always use rsync. Using btrfs send/receive
	// here doesn't make sense since it would need to send everything again
	// which defeats the purpose of a refresh.
	if refresh {
		return []migration.Type{
			{
				FSType:   migration.MigrationFSType_RSYNC,
				Features: []string{"xattrs", "delete", "compress", "bidirectional"},
			},
		}
	}

	return []migration.Type{
		{
			FSType: migration.MigrationFSType_BTRFS,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}
