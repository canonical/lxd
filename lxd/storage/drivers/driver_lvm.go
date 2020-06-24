package drivers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/units"
)

const lvmVgPoolMarker = "lxd_pool" // Indicator tag used to mark volume groups as in use by LXD.

var lvmLoaded bool
var lvmVersion string

var lvmAllowedFilesystems = []string{"btrfs", "ext4", "xfs"}

type lvm struct {
	common
}

func (d *lvm) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_create_vm":                        nil,
		"storage_zfs_mount":                        nil,
		"storage_create_vm_again":                  nil,
		"storage_zfs_volmode":                      nil,
		"storage_rename_custom_volume_add_project": nil,
		"storage_lvm_skipactivation":               d.patchStorageSkipActivation,
	}

	// Done if previously loaded.
	if lvmLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"lvm"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool %q is missing", tool)
		}
	}

	// Detect and record the version.
	if lvmVersion == "" {
		output, err := shared.RunCommand("lvm", "version")
		if err != nil {
			return errors.Wrapf(err, "Error getting LVM version")
		}

		lines := strings.Split(output, "\n")
		for idx, line := range lines {
			fields := strings.SplitAfterN(line, ":", 2)
			if len(fields) < 2 {
				continue
			}

			if !strings.Contains(line, "version:") {
				continue
			}

			if idx > 0 {
				lvmVersion += " / "
			}

			lvmVersion += strings.TrimSpace(fields[1])
		}
	}

	lvmLoaded = true
	return nil
}

// Info returns info about the driver and its environment.
func (d *lvm) Info() Info {
	return Info{
		Name:                  "lvm",
		Version:               lvmVersion,
		OptimizedImages:       d.usesThinpool(), // Only thinpool pools support optimized images.
		PreservesInodes:       false,
		Remote:                false,
		VolumeTypes:           []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:          true,
		RunningQuotaResize:    false,
		RunningSnapshotFreeze: false,
		DirectIO:              true,
		MountedRoot:           false,
	}
}

// Create creates the storage pool on the storage device.
func (d *lvm) Create() error {
	d.config["volatile.initial_source"] = d.config["source"]

	defaultSource := loopFilePath(d.name)
	var err error
	var pvExists, vgExists bool
	var pvName string
	var vgTags []string

	revert := revert.New()
	defer revert.Fail()

	if d.config["source"] == "" || d.config["source"] == defaultSource {
		// We are using an LXD internal loopback file.
		d.config["source"] = defaultSource
		if d.config["lvm.vg_name"] == "" {
			d.config["lvm.vg_name"] = d.name
		}

		size, err := units.ParseByteSizeString(d.config["size"])
		if err != nil {
			return err
		}

		if shared.PathExists(d.config["source"]) {
			return fmt.Errorf("Source file location already exists")
		}

		err = ensureSparseFile(d.config["source"], size)
		if err != nil {
			return errors.Wrapf(err, "Failed to create sparse file %q", d.config["source"])
		}

		revert.Add(func() { os.Remove(d.config["source"]) })

		// Open the loop file.
		loopFile, err := d.openLoopFile(d.config["source"])
		if err != nil {
			return err
		}
		defer loopFile.Close()

		// Check if the physical volume already exists.
		pvName = loopFile.Name()
		pvExists, err = d.pysicalVolumeExists(pvName)
		if err != nil {
			return err
		}

		// Check if the volume group already exists.
		vgExists, vgTags, err = d.volumeGroupExists(d.config["lvm.vg_name"])
		if err != nil {
			return err
		}
	} else if filepath.IsAbs(d.config["source"]) {
		// We are using an existing physical device.
		srcPath := shared.HostPath(d.config["source"])

		// Size is ignored as the physical device is a fixed size.
		d.config["size"] = ""

		if d.config["lvm.vg_name"] == "" {
			d.config["lvm.vg_name"] = d.name
		}
		d.config["source"] = d.config["lvm.vg_name"]

		if !shared.IsBlockdevPath(srcPath) {
			return fmt.Errorf("Custom loop file locations are not supported")
		}

		// Check if the volume group already exists.
		vgExists, vgTags, err = d.volumeGroupExists(d.config["lvm.vg_name"])
		if err != nil {
			return err
		}

		if vgExists {
			return fmt.Errorf("Volume group already exists, cannot use new physical device at %q", srcPath)
		}

		// Check if the physical volume already exists.
		pvName = srcPath
		pvExists, err = d.pysicalVolumeExists(pvName)
		if err != nil {
			return err
		}
	} else if d.config["source"] != "" {
		// We are using an existing volume group, so physical must exist already.
		pvExists = true

		// Size is ignored as the existing device is a fixed size.
		d.config["size"] = ""

		if d.config["lvm.vg_name"] != "" && d.config["lvm.vg_name"] != d.config["source"] {
			return fmt.Errorf("Invalid combination of source and lvm.vg_name properties")
		}

		d.config["lvm.vg_name"] = d.config["source"]

		// Check the volume group already exists.
		vgExists, vgTags, err = d.volumeGroupExists(d.config["lvm.vg_name"])
		if err != nil {
			return err
		}

		if !vgExists {
			return fmt.Errorf("The requested volume group %q does not exist", d.config["lvm.vg_name"])
		}
	} else {
		return fmt.Errorf("Invalid source property")
	}

	// This is an internal error condition which should never be hit.
	if d.config["lvm.vg_name"] == "" {
		return fmt.Errorf("No name for volume group detected")
	}

	// Used to track the result of checking whether the thin pool exists during the existing volume group empty
	// checks to avoid having to do it twice.
	thinPoolExists := false

	if vgExists {
		// Check that the volume group is empty. Otherwise we will refuse to use it.
		// The LV count returned includes both normal volumes and thin volumes.
		lvCount, err := d.countLogicalVolumes(d.config["lvm.vg_name"])
		if err != nil {
			return errors.Wrapf(err, "Failed to determine whether the volume group %q is empty", d.config["lvm.vg_name"])
		}

		empty := false
		if lvCount > 0 {
			if d.usesThinpool() {
				// Always check if the thin pool exists as we may need to create it later.
				thinPoolExists, err = d.thinpoolExists(d.config["lvm.vg_name"], d.thinpoolName())
				if err != nil {
					return errors.Wrapf(err, "Failed to determine whether thinpool %q exists in volume group %q", d.config["lvm.vg_name"], d.thinpoolName())
				}

				// If the single volume is the storage pool's thin pool LV then we still consider
				// this an empty volume group.
				if thinPoolExists && lvCount == 1 {
					empty = true
				}
			}
		} else {
			empty = true
		}

		// Skip the in use checks if the force reuse option is enabled. This allows a storage pool to be
		// backed by an existing non-empty volume group. Note: This option should be used with care, as LXD
		// can then not guarantee that volume name conflicts won't occur with non-LXD created volumes in
		// the same volume group. This could also potentially lead to LXD deleting a non-LXD volume should
		// name conflicts occur.
		if !shared.IsTrue(d.config["lvm.vg.force_reuse"]) {
			if !empty {
				return fmt.Errorf("Volume group %q is not empty", d.config["lvm.vg_name"])
			}

			// Check the tags on the volume group to check it is not already being used by LXD.
			if shared.StringInSlice(lvmVgPoolMarker, vgTags) {
				return fmt.Errorf("Volume group %q is already used by LXD", d.config["lvm.vg_name"])
			}
		}
	} else {
		// Create physical volume if doesn't exist.
		if !pvExists {
			// This is an internal error condition which should never be hit.
			if pvName == "" {
				return fmt.Errorf("No name for physical volume detected")
			}

			_, err := shared.TryRunCommand("pvcreate", pvName)
			if err != nil {
				return err
			}
			revert.Add(func() { shared.TryRunCommand("pvremove", pvName) })
		}

		// Create volume group.
		_, err := shared.TryRunCommand("vgcreate", d.config["lvm.vg_name"], pvName)
		if err != nil {
			return err
		}
		d.logger.Debug("Volume group created", log.Ctx{"pv_name": pvName, "vg_name": d.config["lvm.vg_name"]})
		revert.Add(func() { shared.TryRunCommand("vgremove", d.config["lvm.vg_name"]) })
	}

	// Create thin pool if needed.
	if d.usesThinpool() && !thinPoolExists {
		err = d.createDefaultThinPool(d.Info().Version, d.config["lvm.vg_name"], d.thinpoolName())
		if err != nil {
			return err
		}
		d.logger.Debug("Thin pool created", log.Ctx{"vg_name": d.config["lvm.vg_name"], "thinpool_name": d.thinpoolName()})

		revert.Add(func() {
			d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], "", "", d.thinpoolName()))
		})
	}

	// Mark the volume group with the lvmVgPoolMarker tag to indicate it is now in use by LXD.
	_, err = shared.TryRunCommand("vgchange", "--addtag", lvmVgPoolMarker, d.config["lvm.vg_name"])
	if err != nil {
		return err
	}
	d.logger.Debug("LXD marker tag added to volume group", log.Ctx{"vg_name": d.config["lvm.vg_name"]})

	revert.Success()
	return nil
}

// Delete removes the storage pool from the storage device.
func (d *lvm) Delete(op *operations.Operation) error {
	var err error
	var loopFile *os.File

	// Open the loop file if needed.
	if filepath.IsAbs(d.config["source"]) && !shared.IsBlockdevPath(d.config["source"]) {
		loopFile, err = d.openLoopFile(d.config["source"])
		if err != nil {
			return err
		}
		defer loopFile.Close()
	}

	vgExists, vgTags, err := d.volumeGroupExists(d.config["lvm.vg_name"])
	if err != nil {
		return err
	}

	removeVg := false
	if vgExists {
		// Count normal and thin volumes.
		lvCount, err := d.countLogicalVolumes(d.config["lvm.vg_name"])
		if err != nil && err != errLVMNotFound {
			return err
		}

		// Check that volume group is not in use. If it is we need to assume that other users are using
		// the volume group, so don't remove it. This actually goes against policy since we explicitly
		// state: our pool, and nothing but our pool, but still, let's not hurt users.
		if err == nil {
			if lvCount == 0 {
				removeVg = true // Volume group is totally empty, safe to remove.
			} else if d.usesThinpool() && lvCount > 0 {
				// Lets see if the lv count is just our thin pool, or whether we can only remove
				// the thin pool itself and not the volume group.
				thinVolCount, err := d.countThinVolumes(d.config["lvm.vg_name"], d.thinpoolName())
				if err != nil && err != errLVMNotFound {
					return err
				}

				// Thin pool exists.
				if err == nil {
					// If thin pool is empty and the total VG volume count is 1 (our thin pool
					// volume) then just remote the entire volume group.
					if thinVolCount == 0 && lvCount == 1 {
						removeVg = true
					} else if thinVolCount == 0 && lvCount > 1 {
						// Otherwise, if the thin pool is empty but the volume group has
						// other volumes, then just remove the thin pool volume.
						err = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], "", "", d.thinpoolName()))
						if err != nil {
							return errors.Wrapf(err, "Failed to delete thin pool %q from volume group %q", d.thinpoolName(), d.config["lvm.vg_name"])
						}
						d.logger.Debug("Thin pool removed", log.Ctx{"vg_name": d.config["lvm.vg_name"], "thinpool_name": d.thinpoolName()})
					}
				}
			}
		}

		// Remove volume group if needed.
		if removeVg {
			_, err := shared.TryRunCommand("vgremove", "-f", d.config["lvm.vg_name"])
			if err != nil {
				return errors.Wrapf(err, "Failed to delete the volume group for the lvm storage pool")
			}
			d.logger.Debug("Volume group removed", log.Ctx{"vg_name": d.config["lvm.vg_name"]})
		} else {
			// Otherwise just remove the lvmVgPoolMarker tag to indicate LXD no longer uses this VG.
			if shared.StringInSlice(lvmVgPoolMarker, vgTags) {
				_, err = shared.TryRunCommand("vgchange", "--deltag", lvmVgPoolMarker, d.config["lvm.vg_name"])
				if err != nil {
					return errors.Wrapf(err, "Failed to remove marker tag on volume group for the lvm storage pool")
				}
				d.logger.Debug("LXD marker tag removed from volume group", log.Ctx{"vg_name": d.config["lvm.vg_name"]})
			}
		}
	}

	// If we have removed the volume group and this is a loop file, lets clean up the physical volume too.
	if removeVg && loopFile != nil {
		err = SetAutoclearOnLoopDev(int(loopFile.Fd()))
		if err != nil {
			d.logger.Warn("Failed to set LO_FLAGS_AUTOCLEAR on loop device, manual cleanup needed", log.Ctx{"dev": loopFile.Name(), "err": err})
		}

		_, err := shared.TryRunCommand("pvremove", "-f", loopFile.Name())
		if err != nil {
			d.logger.Warn("Failed to destroy the physical volume for the lvm storage pool", log.Ctx{"err": err})
		}
		d.logger.Debug("Physical volume removed", log.Ctx{"pv_name": loopFile.Name()})

		err = loopFile.Close()
		if err != nil {
			return err
		}

		// This is a loop file so deconfigure the associated loop device.
		err = os.Remove(d.config["source"])
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "Error removing LVM pool loop file %q", d.config["source"])
		}
		d.logger.Debug("Physical loop file removed", log.Ctx{"file_name": d.config["source"]})
	}

	// Wipe everything in the storage pool directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	return nil
}

func (d *lvm) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"lvm.vg_name":                shared.IsAny,
		"lvm.thinpool_name":          shared.IsAny,
		"lvm.use_thinpool":           shared.IsBool,
		"volume.block.mount_options": shared.IsAny,
		"volume.block.filesystem": func(value string) error {
			if value == "" {
				return nil
			}
			return shared.IsOneOf(value, lvmAllowedFilesystems)
		},
		"volume.lvm.stripes":      shared.IsUint32,
		"volume.lvm.stripes.size": shared.IsSize,
		"lvm.vg.force_reuse":      shared.IsBool,
	}

	err := d.validatePool(config, rules)
	if err != nil {
		return err
	}

	if v, found := config["lvm.use_thinpool"]; found && !shared.IsTrue(v) && config["lvm.thinpool_name"] != "" {
		return fmt.Errorf("The key lvm.use_thinpool cannot be set to false when lvm.thinpool_name is set")
	}

	return nil
}

// Update updates the storage pool settings.
func (d *lvm) Update(changedConfig map[string]string) error {
	if _, changed := changedConfig["lvm.use_thinpool"]; changed {
		return fmt.Errorf("lvm.use_thinpool cannot be changed")
	}

	if _, changed := changedConfig["volume.lvm.stripes"]; changed && d.usesThinpool() {
		return fmt.Errorf("volume.lvm.stripes cannot be changed when using thin pool")
	}

	if _, changed := changedConfig["volume.lvm.stripes.size"]; changed && d.usesThinpool() {
		return fmt.Errorf("volume.lvm.stripes.size cannot be changed when using thin pool")
	}

	if changedConfig["lvm.vg_name"] != "" {
		_, err := shared.TryRunCommand("vgrename", d.config["lvm.vg_name"], changedConfig["lvm.vg_name"])
		if err != nil {
			return errors.Wrapf(err, "Error renaming LVM volume group from %q to %q", d.config["lvm.vg_name"], changedConfig["lvm.vg_name"])
		}
		d.logger.Debug("Volume group renamed", log.Ctx{"vg_name": d.config["lvm.vg_name"], "new_vg_name": changedConfig["lvm.vg_name"]})
	}

	if changedConfig["lvm.thinpool_name"] != "" {
		_, err := shared.TryRunCommand("lvrename", d.config["lvm.vg_name"], d.thinpoolName(), changedConfig["lvm.thinpool_name"])
		if err != nil {
			return errors.Wrapf(err, "Error renaming LVM thin pool from %q to %q", d.thinpoolName(), changedConfig["lvm.thinpool_name"])
		}
		d.logger.Debug("Thin pool volume renamed", log.Ctx{"vg_name": d.config["lvm.vg_name"], "thinpool": d.thinpoolName(), "new_thinpool": changedConfig["lvm.thinpool_name"]})
	}

	return nil
}

// Mount mounts the storage pool (this does nothing for external LVM pools, but for loopback image
// LVM pools this creates a loop device).
func (d *lvm) Mount() (bool, error) {
	if d.config["lvm.vg_name"] == "" {
		return false, fmt.Errorf("Cannot mount pool as %q is not specified", "lvm.vg_name")
	}

	// Open the loop file if the LVM device doesn't exist yet and the source points to a file.
	if !shared.IsDir(fmt.Sprintf("/dev/%s", d.config["lvm.vg_name"])) && filepath.IsAbs(d.config["source"]) && !shared.IsBlockdevPath(d.config["source"]) {
		loopFile, err := d.openLoopFile(d.config["source"])
		if err != nil {
			return false, err
		}
		defer loopFile.Close()
		return true, nil
	}

	return false, nil
}

// Unmount unmounts the storage pool (this does nothing for external LVM pools, but for loopback
// image LVM pools this closes the loop device handle if needed).
func (d *lvm) Unmount() (bool, error) {
	// If loop backed, force release the loop device.
	if filepath.IsAbs(d.config["source"]) && !shared.IsBlockdevPath(d.config["source"]) {
		vgExists, _, _ := d.volumeGroupExists(d.config["lvm.vg_name"])
		if vgExists {
			// Deactivate volume group so that it's device is removed from /dev.
			_, err := shared.TryRunCommand("vgchange", "-an", d.config["lvm.vg_name"])
			if err != nil {
				return false, err
			}
		}

		err := releaseLoopDev(d.config["source"])
		if err != nil {
			return false, errors.Wrapf(err, "Failed releasing loop file device %q", d.config["source"])
		}

		return true, nil // We closed the file.
	}

	// No loop device was opened, so nothing to close.
	return false, nil
}

// GetResources returns utilisation and space info about the pool.
func (d *lvm) GetResources() (*api.ResourcesStoragePool, error) {
	res := api.ResourcesStoragePool{}

	// Thinpools will always report zero free space on the volume group, so calculate approx
	// used space using the thinpool logical volume allocated (data and meta) percentages.
	if d.usesThinpool() {
		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], "", "", d.thinpoolName())
		totalSize, usedSize, err := d.thinPoolVolumeUsage(volDevPath)
		if err != nil {
			return nil, err
		}

		res.Space.Total = totalSize
		res.Space.Used = usedSize
	} else {
		// If thinpools are not in use, calculate used space in volume group.
		args := []string{
			d.config["lvm.vg_name"],
			"--noheadings",
			"--units", "b",
			"--nosuffix",
			"--separator", ",",
			"-o", "vg_size,vg_free",
		}

		out, err := shared.RunCommand("vgs", args...)
		if err != nil {
			return nil, err
		}

		parts := strings.Split(strings.TrimSpace(out), ",")
		if len(parts) < 2 {
			return nil, fmt.Errorf("Unexpected output from vgs command")
		}

		total, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return nil, err
		}

		res.Space.Total = total

		free, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return nil, err
		}
		res.Space.Used = total - free
	}

	return &res, nil
}
