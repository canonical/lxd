package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

const lvmVgPoolMarker = "lxd_pool" // Indicator tag used to mark volume groups as in use by LXD.

var lvmLoaded bool
var lvmVersion string

type lvm struct {
	common
}

func (d *lvm) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         d.patchStorageSkipActivation,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
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
		output, err := shared.RunCommandContext(d.state.ShutdownCtx, "lvm", "version")
		if err != nil {
			return fmt.Errorf("Error getting LVM version: %w", err)
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
		Name:                         "lvm",
		Version:                      lvmVersion,
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              d.usesThinpool(), // Only thinpool pools support optimized images.
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeBucket, VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  false,
		Buckets:                      true,
		PopulateParentVolumeUUID:     false,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *lvm) FillConfig() error {
	// Set default thin pool name if not specified.
	if d.usesThinpool() && d.config["lvm.thinpool_name"] == "" {
		d.config["lvm.thinpool_name"] = lvmThinpoolDefaultName
	}

	return nil
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

	err = d.FillConfig()
	if err != nil {
		return err
	}

	var usingLoopFile bool

	if d.config["source"] == "" || d.config["source"] == defaultSource {
		usingLoopFile = true

		// We are using a LXD internal loopback file.
		d.config["source"] = defaultSource
		if d.config["lvm.vg_name"] == "" {
			d.config["lvm.vg_name"] = d.name
		}

		// Pick a default size of the loop file if not specified.
		if d.config["size"] == "" {
			defaultSize, err := loopFileSizeDefault()
			if err != nil {
				return err
			}

			d.config["size"] = strconv.FormatUint(defaultSize, 10) + "GiB"
		}

		size, err := units.ParseByteSizeString(d.config["size"])
		if err != nil {
			return err
		}

		if shared.PathExists(d.config["source"]) {
			return fmt.Errorf("Source file location %q already exists", d.config["source"])
		}

		err = ensureSparseFile(d.config["source"], size)
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %q: %w", d.config["source"], err)
		}

		revert.Add(func() { _ = os.Remove(d.config["source"]) })

		// Open the loop file.
		loopDevPath, err := d.openLoopFile(d.config["source"])
		if err != nil {
			return err
		}

		defer func() { _ = loopDeviceAutoDetach(loopDevPath) }()

		// Check if the physical volume already exists.
		pvName = loopDevPath
		pvExists, err = d.pysicalVolumeExists(pvName)
		if err != nil {
			return err
		}

		if pvExists {
			return fmt.Errorf("A physical volume already exists for %q", pvName)
		}

		// Check if the volume group already exists.
		vgExists, vgTags, err = d.volumeGroupExists(d.config["lvm.vg_name"])
		if err != nil {
			return err
		}

		if vgExists {
			return fmt.Errorf("A volume group already exists called %q", d.config["lvm.vg_name"])
		}
	} else if filepath.IsAbs(d.config["source"]) {
		// We are using an existing physical device.
		srcPath := shared.HostPath(d.config["source"])

		// Size is invalid as the physical device is already sized.
		if d.config["size"] != "" && !d.usesThinpool() {
			return errors.New("Cannot specify size when using an existing physical device for non-thin pool")
		}

		if d.config["lvm.vg_name"] == "" {
			d.config["lvm.vg_name"] = d.name
		}

		d.config["source"] = d.config["lvm.vg_name"]

		if !shared.IsBlockdevPath(srcPath) {
			return errors.New("Custom loop file locations are not supported")
		}

		// Wipe if requested.
		if shared.IsTrue(d.config["source.wipe"]) {
			err := wipeBlockHeaders(srcPath)
			if err != nil {
				return fmt.Errorf("Failed to wipe headers from disk %q: %w", d.config["source"], err)
			}

			d.config["source.wipe"] = ""
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

		// Size is invalid as the volume group is already sized.
		if d.config["size"] != "" && !d.usesThinpool() {
			return errors.New("Cannot specify size when using an existing volume group for non-thin pool")
		}

		if d.config["lvm.vg_name"] != "" && d.config["lvm.vg_name"] != d.config["source"] {
			return errors.New("Invalid combination of source and lvm.vg_name properties")
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
		return errors.New("Invalid source property")
	}

	// This is an internal error condition which should never be hit.
	if d.config["lvm.vg_name"] == "" {
		return errors.New("No name for volume group detected")
	}

	// Used to track the result of checking whether the thin pool exists during the existing volume group empty
	// checks to avoid having to do it twice.
	thinPoolExists := false

	if vgExists {
		// Check that the volume group is empty. Otherwise we will refuse to use it.
		// The LV count returned includes both normal volumes and thin volumes.
		lvCount, err := d.countLogicalVolumes(d.config["lvm.vg_name"])
		if err != nil {
			return fmt.Errorf("Failed to determine whether the volume group %q is empty: %w", d.config["lvm.vg_name"], err)
		}

		empty := false
		if lvCount > 0 {
			if d.usesThinpool() {
				// Always check if the thin pool exists as we may need to create it later.
				thinPoolExists, err = d.thinpoolExists(d.config["lvm.vg_name"], d.thinpoolName())
				if err != nil {
					return fmt.Errorf("Failed to determine whether thinpool %q exists in volume group %q: %w", d.config["lvm.vg_name"], d.thinpoolName(), err)
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
		if shared.IsFalseOrEmpty(d.config["lvm.vg.force_reuse"]) {
			if !empty {
				return fmt.Errorf("Volume group %q is not empty", d.config["lvm.vg_name"])
			}

			// Check the tags on the volume group to check it is not already being used by LXD.
			if slices.Contains(vgTags, lvmVgPoolMarker) {
				return fmt.Errorf("Volume group %q is already used by LXD", d.config["lvm.vg_name"])
			}
		}
	} else {
		// Create physical volume if doesn't exist.
		if !pvExists {
			// This is an internal error condition which should never be hit.
			if pvName == "" {
				return errors.New("No name for physical volume detected")
			}

			_, err := shared.TryRunCommand("pvcreate", pvName)
			if err != nil {
				return err
			}

			revert.Add(func() { _, _ = shared.TryRunCommand("pvremove", pvName) })
		}

		// Create volume group.
		_, err := shared.TryRunCommand("vgcreate", d.config["lvm.vg_name"], pvName)
		if err != nil {
			return err
		}

		d.logger.Debug("Volume group created", logger.Ctx{"pv_name": pvName, "vg_name": d.config["lvm.vg_name"]})
		revert.Add(func() { _, _ = shared.TryRunCommand("vgremove", d.config["lvm.vg_name"]) })
	}

	// Create thin pool if needed.
	if d.usesThinpool() {
		if !thinPoolExists {
			var thinpoolSizeBytes int64

			// If not using loop file then the size setting controls the size of the thinpool volume.
			if !usingLoopFile {
				thinpoolSizeBytes, err = d.roundedSizeBytesString(d.config["size"])
				if err != nil {
					return fmt.Errorf("Invalid size: %w", err)
				}
			}

			err = d.createDefaultThinPool(d.Info().Version, d.thinpoolName(), thinpoolSizeBytes)
			if err != nil {
				return err
			}

			d.logger.Debug("Thin pool created", logger.Ctx{"vg_name": d.config["lvm.vg_name"], "thinpool_name": d.thinpoolName()})

			revert.Add(func() {
				_ = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], "", "", d.thinpoolName()))
			})
		} else if d.config["size"] != "" {
			return errors.New("Cannot specify size when using an existing thin pool")
		}
	}

	// Mark the volume group with the lvmVgPoolMarker tag to indicate it is now in use by LXD.
	_, err = shared.TryRunCommand("vgchange", "--addtag", lvmVgPoolMarker, d.config["lvm.vg_name"])
	if err != nil {
		return err
	}

	d.logger.Debug("LXD marker tag added to volume group", logger.Ctx{"vg_name": d.config["lvm.vg_name"]})

	revert.Success()
	return nil
}

// Delete removes the storage pool from the storage device.
func (d *lvm) Delete(op *operations.Operation) error {
	var err error
	var loopDevPath string

	// Open the loop file if needed.
	if filepath.IsAbs(d.config["source"]) && !shared.IsBlockdevPath(d.config["source"]) {
		loopDevPath, err = d.openLoopFile(d.config["source"])
		if err != nil {
			return err
		}

		defer func() { _ = loopDeviceAutoDetach(loopDevPath) }()
	}

	vgExists, vgTags, err := d.volumeGroupExists(d.config["lvm.vg_name"])
	if err != nil {
		return err
	}

	removeVg := false
	if vgExists && shared.IsFalseOrEmpty(d.config["lvm.vg.force_reuse"]) {
		// Count normal and thin volumes.
		lvCount, err := d.countLogicalVolumes(d.config["lvm.vg_name"])
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusNotFound) {
				return err
			}
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
				if err != nil {
					if !api.StatusErrorCheck(err, http.StatusNotFound) {
						return err
					}
				}

				// Thin pool exists.
				if err == nil {
					// If thin pool is empty and the total VG volume count is 1 (our thin pool
					// volume) then just remove the entire volume group.
					if thinVolCount == 0 && lvCount == 1 {
						removeVg = true
					} else if thinVolCount == 0 && lvCount > 1 {
						// Otherwise, if the thin pool is empty but the volume group has
						// other volumes, then just remove the thin pool volume.
						err = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], "", "", d.thinpoolName()))
						if err != nil {
							return fmt.Errorf("Failed to delete thin pool %q from volume group %q: %w", d.thinpoolName(), d.config["lvm.vg_name"], err)
						}

						d.logger.Debug("Thin pool removed", logger.Ctx{"vg_name": d.config["lvm.vg_name"], "thinpool_name": d.thinpoolName()})
					}
				}
			}
		}

		// Remove volume group if needed.
		if removeVg {
			_, err := shared.TryRunCommand("vgremove", "-f", d.config["lvm.vg_name"])
			if err != nil {
				return fmt.Errorf("Failed to delete the volume group for the lvm storage pool: %w", err)
			}

			d.logger.Debug("Volume group removed", logger.Ctx{"vg_name": d.config["lvm.vg_name"]})
		} else {
			// Otherwise just remove the lvmVgPoolMarker tag to indicate LXD no longer uses this VG.
			if slices.Contains(vgTags, lvmVgPoolMarker) {
				_, err = shared.TryRunCommand("vgchange", "--deltag", lvmVgPoolMarker, d.config["lvm.vg_name"])
				if err != nil {
					return fmt.Errorf("Failed to remove marker tag on volume group for the lvm storage pool: %w", err)
				}

				d.logger.Debug("LXD marker tag removed from volume group", logger.Ctx{"vg_name": d.config["lvm.vg_name"]})
			}
		}
	}

	// If we have removed the volume group and this is a loop file, lets clean up the physical volume too.
	if removeVg && loopDevPath != "" {
		_, err := shared.TryRunCommand("pvremove", "-f", loopDevPath)
		if err != nil {
			d.logger.Warn("Failed to destroy the physical volume for the lvm storage pool", logger.Ctx{"err": err})
		}

		d.logger.Debug("Physical volume removed", logger.Ctx{"pv_name": loopDevPath})

		err = loopDeviceAutoDetach(loopDevPath)
		if err != nil {
			d.logger.Warn("Failed to set LO_FLAGS_AUTOCLEAR on loop device, manual cleanup needed", logger.Ctx{"dev": loopDevPath, "err": err})
		}

		// This is a loop file so deconfigure the associated loop device.
		err = os.Remove(d.config["source"])
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Error removing LVM pool loop file %q: %w", d.config["source"], err)
		}

		d.logger.Debug("Physical loop file removed", logger.Ctx{"file_name": d.config["source"]})
	}

	// Wipe everything in the storage pool directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting
// or missing configuration is present.
func (d *lvm) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"size": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=storage-lvm; group=pool-conf; key=lvm.vg_name)
		//
		// ---
		//  type: string
		//  defaultdesc: name of the pool
		//  shortdesc: Name of the volume group to create
		//  scope: local
		"lvm.vg_name": validate.Optional(func(value string) error { return ValidPoolName(value) }),
		// lxdmeta:generate(entities=storage-lvm; group=pool-conf; key=lvm.thinpool_name)
		//
		// ---
		//  type: string
		//  defaultdesc: `LXDThinPool`
		//  shortdesc: Thin pool where volumes are created
		//  scope: local
		"lvm.thinpool_name": validate.Optional(func(value string) error { return ValidVolumeName(value) }),
		// lxdmeta:generate(entities=storage-lvm; group=pool-conf; key=lvm.thinpool_metadata_size)
		// By default, LVM calculates an appropriate size.
		// ---
		//  type: string
		//  defaultdesc: `0` (auto)
		//  shortdesc: The size of the thin pool metadata volume
		//  scope: global
		"lvm.thinpool_metadata_size": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=storage-lvm; group=pool-conf; key=lvm.use_thinpool)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether the storage pool uses a thin pool for logical volumes
		//  scope: global
		"lvm.use_thinpool": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-lvm; group=pool-conf; key=lvm.vg.force_reuse)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Force using an existing non-empty volume group
		//  scope: global
		"lvm.vg.force_reuse": validate.Optional(validate.IsBool),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	if shared.IsFalse(config["lvm.use_thinpool"]) {
		if config["lvm.thinpool_name"] != "" {
			return errors.New("The key lvm.use_thinpool cannot be set to false when lvm.thinpool_name is set")
		}

		if config["lvm.thinpool_metadata_size"] != "" {
			return errors.New("The key lvm.use_thinpool cannot be set to false when lvm.thinpool_metadata_size is set")
		}
	}

	return nil
}

// Update updates the storage pool settings.
func (d *lvm) Update(changedConfig map[string]string) error {
	_, changed := changedConfig["lvm.use_thinpool"]
	if changed {
		return errors.New("lvm.use_thinpool cannot be changed")
	}

	_, changed = changedConfig["lvm.thinpool_metadata_size"]
	if changed {
		return errors.New("lvm.thinpool_metadata_size cannot be changed")
	}

	_, changed = changedConfig["volume.lvm.stripes"]
	if changed && d.usesThinpool() {
		return errors.New("volume.lvm.stripes cannot be changed when using thin pool")
	}

	_, changed = changedConfig["volume.lvm.stripes.size"]
	if changed && d.usesThinpool() {
		return errors.New("volume.lvm.stripes.size cannot be changed when using thin pool")
	}

	if changedConfig["lvm.vg_name"] != "" {
		_, err := shared.TryRunCommand("vgrename", d.config["lvm.vg_name"], changedConfig["lvm.vg_name"])
		if err != nil {
			return fmt.Errorf("Error renaming LVM volume group from %q to %q: %w", d.config["lvm.vg_name"], changedConfig["lvm.vg_name"], err)
		}

		d.logger.Debug("Volume group renamed", logger.Ctx{"vg_name": d.config["lvm.vg_name"], "new_vg_name": changedConfig["lvm.vg_name"]})
	}

	if changedConfig["lvm.thinpool_name"] != "" {
		_, err := shared.TryRunCommand("lvrename", d.config["lvm.vg_name"], d.thinpoolName(), changedConfig["lvm.thinpool_name"])
		if err != nil {
			return fmt.Errorf("Error renaming LVM thin pool from %q to %q: %w", d.thinpoolName(), changedConfig["lvm.thinpool_name"], err)
		}

		d.logger.Debug("Thin pool volume renamed", logger.Ctx{"vg_name": d.config["lvm.vg_name"], "thinpool": d.thinpoolName(), "new_thinpool": changedConfig["lvm.thinpool_name"]})
	}

	size, ok := changedConfig["size"]
	if ok {
		// Figure out loop path
		loopPath := loopFilePath(d.name)

		if d.config["source"] != loopPath {
			return errors.New("Cannot resize non-loopback pools")
		}

		// Resize loop file
		f, err := os.OpenFile(loopPath, os.O_RDWR, 0600)
		if err != nil {
			return err
		}

		defer func() { _ = f.Close() }()

		sizeBytes, _ := units.ParseByteSizeString(size)

		err = f.Truncate(sizeBytes)
		if err != nil {
			return err
		}

		loopDevPath, err := loopDeviceSetup(loopPath)
		if err != nil {
			return err
		}

		err = loopDeviceSetCapacity(loopDevPath)
		if err != nil {
			return err
		}

		// Resize physical volume so that lvresize is able to resize as well.
		_, err = shared.RunCommandContext(context.TODO(), "pvresize", "-y", loopDevPath)
		if err != nil {
			return err
		}

		if d.usesThinpool() {
			lvPath := d.lvmDevPath(d.config["lvm.vg_name"], "", "", d.thinpoolName())

			// Use the remaining space in the volume group.
			_, err = shared.RunCommandContext(context.TODO(), "lvresize", "-f", "-l", "+100%FREE", lvPath)
			if err != nil {
				return fmt.Errorf("Error resizing LV named %q: %w", lvPath, err)
			}
		}
	}

	return nil
}

// Mount mounts the storage pool (for loopback image pools this creates a loop device), and checks the volume group
// and thin pool volume (if used) exists.
func (d *lvm) Mount() (bool, error) {
	if d.config["lvm.vg_name"] == "" {
		return false, fmt.Errorf("Cannot mount pool as %q is not specified", "lvm.vg_name")
	}

	// Check if VG exists before we do anthing, this will indicate if its our mount or not.
	vgExists, _, _ := d.volumeGroupExists(d.config["lvm.vg_name"])
	ourMount := !vgExists

	waitDuration := time.Second * time.Duration(5)

	revert := revert.New()
	defer revert.Fail()

	// Open the loop file if the source points to a non-block device file.
	// This ensures that auto clear isn't enabled on the loop file.
	if filepath.IsAbs(d.config["source"]) && !shared.IsBlockdevPath(d.config["source"]) {
		loopDevPath, err := d.openLoopFile(d.config["source"])
		if err != nil {
			return false, err
		}

		revert.Add(func() { _ = loopDeviceAutoDetach(loopDevPath) })

		// Wait for volume group to be detected if wasn't detected before.
		if !vgExists {
			waitUntil := time.Now().Add(waitDuration)
			for {
				vgExists, _, _ = d.volumeGroupExists(d.config["lvm.vg_name"])
				if vgExists {
					break
				}

				if time.Now().After(waitUntil) {
					return false, fmt.Errorf("Volume group %q not found", d.config["lvm.vg_name"])
				}

				time.Sleep(1 * time.Second)
			}
		}
	} else if !vgExists {
		return false, fmt.Errorf("Volume group %s not found", d.config["lvm.vg_name"])
	}

	// Ensure thinpool exists if needed for storage pool.
	if d.usesThinpool() {
		waitUntil := time.Now().Add(waitDuration)
		for {
			thinpoolExists, _ := d.thinpoolExists(d.config["lvm.vg_name"], d.thinpoolName())
			if thinpoolExists {
				break
			}

			if time.Now().After(waitUntil) {
				return false, fmt.Errorf("Thin pool not found %q in volume group %q", d.thinpoolName(), d.config["lvm.vg_name"])
			}

			time.Sleep(1 * time.Second)
		}
	}

	revert.Success()
	return ourMount, nil
}

// Unmount unmounts the storage pool (this does nothing).
// LVM doesn't currently support unmounting, please see https://github.com/canonical/lxd/issues/9278
func (d *lvm) Unmount() (bool, error) {
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

		out, err := shared.RunCommandContext(d.state.ShutdownCtx, "vgs", args...)
		if err != nil {
			return nil, err
		}

		parts := strings.Split(strings.TrimSpace(out), ",")
		if len(parts) < 2 {
			return nil, errors.New("Unexpected output from vgs command")
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

// roundVolumeBlockSizeBytes returns sizeBytes rounded up to the next multiple
// of the volume group extent size.
func (d *lvm) roundVolumeBlockSizeBytes(vol Volume, sizeBytes int64) int64 {
	// Get the volume group's physical extent size, and use that as minimum size.
	vgExtentSize, _ := d.volumeGroupExtentSize(d.config["lvm.vg_name"])
	return roundAbove(vgExtentSize, sizeBytes)
}
