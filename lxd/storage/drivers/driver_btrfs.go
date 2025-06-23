package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

var btrfsVersion string
var btrfsLoaded bool
var btrfsPropertyForce bool

type btrfs struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *btrfs) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if btrfsLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"btrfs"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool %q is missing", tool)
		}
	}

	// Detect and record the version.
	if btrfsVersion == "" {
		out, err := shared.RunCommandContext(context.TODO(), "btrfs", "version")
		if err != nil {
			return err
		}

		count, err := fmt.Sscanf(strings.SplitN(out, " ", 2)[1], "v%s\n", &btrfsVersion)
		if err != nil || count != 1 {
			return errors.New("The 'btrfs' tool isn't working properly")
		}
	}

	// Check if we need --force to set properties.
	ver5142, err := version.Parse("5.14.2")
	if err != nil {
		return err
	}

	ourVer, err := version.Parse(btrfsVersion)
	if err != nil {
		return err
	}

	// If running 5.14.2 or older, we need --force.
	if ourVer.Compare(ver5142) > 0 {
		btrfsPropertyForce = true
	}

	btrfsLoaded = true
	return nil
}

// Info returns info about the driver and its environment.
func (d *btrfs) Info() Info {
	return Info{
		Name:                         "btrfs",
		Version:                      btrfsVersion,
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              true,
		OptimizedBackups:             true,
		OptimizedBackupHeader:        true,
		PreservesInodes:              !d.state.OS.RunningInUserNS,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeBucket, VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		VolumeMultiNode:              d.isRemote(),
		BlockBacking:                 false,
		RunningCopyFreeze:            false,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  true,
		Buckets:                      true,
		PopulateParentVolumeUUID:     false,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *btrfs) FillConfig() error {
	loopPath := loopFilePath(d.name)
	if d.config["source"] == "" || d.config["source"] == loopPath {
		// Pick a default size of the loop file if not specified.
		if d.config["size"] == "" {
			defaultSize, err := loopFileSizeDefault()
			if err != nil {
				return err
			}

			d.config["size"] = fmt.Sprint(defaultSize, "GiB")
		}
	} else {
		// Unset size property since it's irrelevant.
		d.config["size"] = ""
	}

	// Store the provided source as we are likely to be mangling it.
	d.config["volatile.initial_source"] = d.config["source"]

	// Set the block device's UUID in case it already has one.
	// This allows to recover the pools configuration without actually
	// creating the storage pool.
	// Downstream functions should use `volatile.initial_source` to ensure
	// they are using the path instead of the volume's UUID.
	if shared.IsBlockdevPath(d.config["source"]) {
		devUUID, err := fsUUID(d.config["source"])
		if err == nil {
			d.config["source"] = devUUID
		}
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *btrfs) Create() error {
	revert := revert.New()
	defer revert.Fail()

	err := d.FillConfig()
	if err != nil {
		return err
	}

	loopPath := loopFilePath(d.name)
	if d.config["volatile.initial_source"] == "" || d.config["volatile.initial_source"] == loopPath {
		// Create a loop based pool.
		d.config["source"] = loopPath

		// Create the loop file itself.
		size, err := units.ParseByteSizeString(d.config["size"])
		if err != nil {
			return err
		}

		err = ensureSparseFile(d.config["source"], size)
		if err != nil {
			return fmt.Errorf("Failed to create the sparse file: %w", err)
		}

		revert.Add(func() { _ = os.Remove(d.config["source"]) })

		// Format the file.
		_, err = makeFSType(d.config["source"], "btrfs", &mkfsOptions{Label: d.name})
		if err != nil {
			return fmt.Errorf("Failed to format sparse file: %w", err)
		}
	} else if shared.IsBlockdevPath(d.config["volatile.initial_source"]) {
		// Make sure to use the block volumes `volatile.initial_source` here
		// as an earlier call to the drivers FillConfig() might have set
		// the `source` property to the block volumes UUID in case it's not empty.

		// Wipe if requested.
		if shared.IsTrue(d.config["source.wipe"]) {
			err := wipeBlockHeaders(d.config["volatile.initial_source"])
			if err != nil {
				return fmt.Errorf("Failed to wipe headers from disk %q: %w", d.config["volatile.initial_source"], err)
			}

			d.config["source.wipe"] = ""
		}

		// Format the block device.
		_, err := makeFSType(d.config["volatile.initial_source"], "btrfs", &mkfsOptions{Label: d.name})
		if err != nil {
			return fmt.Errorf("Failed to format block device: %w", err)
		}

		// Record the UUID as the source.
		devUUID, err := fsUUID(d.config["volatile.initial_source"])
		if err != nil {
			return err
		}

		// Confirm that the symlink is appearing (give it 10s).
		// In case of timeout it falls back to using the volume's path
		// instead of its UUID.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 10*time.Second)
		defer cancel()

		if tryExists(ctx, "/dev/disk/by-uuid/"+devUUID) {
			// Override the config to use the UUID.
			d.config["source"] = devUUID
		} else {
			d.config["source"] = d.config["volatile.initial_source"]
		}
	} else if d.config["source"] != "" {
		hostPath := shared.HostPath(d.config["source"])
		if d.isSubvolume(hostPath) {
			// Existing btrfs subvolume.
			hasSubvolumes, err := d.hasSubvolumes(hostPath)
			if err != nil {
				return fmt.Errorf("Could not determine if existing btrfs subvolume is empty: %w", err)
			}

			// Check that the provided subvolume is empty.
			if hasSubvolumes {
				return errors.New("Requested btrfs subvolume exists but is not empty")
			}
		} else {
			// New btrfs subvolume on existing btrfs filesystem.
			cleanSource := filepath.Clean(hostPath)
			lxdDir := shared.VarPath()

			if shared.PathExists(hostPath) {
				hostPathFS, _ := filesystem.Detect(hostPath)
				if hostPathFS != "btrfs" {
					return fmt.Errorf("Provided path does not reside on a btrfs filesystem (detected %s)", hostPathFS)
				}
			}

			if strings.HasPrefix(cleanSource, lxdDir) {
				if cleanSource != GetPoolMountPath(d.name) {
					return fmt.Errorf("Only allowed source path under %q is %q", shared.VarPath(), GetPoolMountPath(d.name))
				}

				storagePoolDirFS, _ := filesystem.Detect(shared.VarPath("storage-pools"))
				if storagePoolDirFS != "btrfs" {
					return fmt.Errorf("Provided path does not reside on a btrfs filesystem (detected %s)", storagePoolDirFS)
				}

				// Delete the current directory to replace by subvolume.
				err := os.Remove(cleanSource)
				if err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("Failed to remove %q: %w", cleanSource, err)
				}
			}

			// Create the subvolume.
			_, err := shared.RunCommandContext(context.TODO(), "btrfs", "subvolume", "create", hostPath)
			if err != nil {
				return err
			}
		}
	} else {
		return errors.New(`Invalid "source" property`)
	}

	revert.Success()
	return nil
}

// Delete removes the storage pool from the storage device.
func (d *btrfs) Delete(op *operations.Operation) error {
	// If the user completely destroyed it, call it done.
	if !shared.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// Delete potential intermediate btrfs subvolumes.
	for _, volType := range d.Info().VolumeTypes {
		for _, dir := range BaseDirectories[volType] {
			path := filepath.Join(GetPoolMountPath(d.name), dir)
			if !shared.PathExists(path) {
				continue
			}

			if !d.isSubvolume(path) {
				continue
			}

			err := d.deleteSubvolume(path, true)
			if err != nil {
				return fmt.Errorf("Failed deleting btrfs subvolume %q", path)
			}
		}
	}

	// On delete, wipe everything in the directory.
	mountPath := GetPoolMountPath(d.name)
	err := wipeDirectory(mountPath)
	if err != nil {
		return fmt.Errorf("Failed removing mount path %q: %w", mountPath, err)
	}

	// Unmount the path.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	// If the pool path is a subvolume itself, delete it.
	if d.isSubvolume(mountPath) {
		err := d.deleteSubvolume(mountPath, false)
		if err != nil {
			return err
		}

		// And re-create as an empty directory to make the backend happy.
		err = os.Mkdir(mountPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed creating directory %q: %w", mountPath, err)
		}
	}

	// Delete any loop file we may have used.
	loopPath := loopFilePath(d.name)
	err = os.Remove(loopPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed removing loop file %q: %w", loopPath, err)
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *btrfs) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"size": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=storage-btrfs; group=pool-conf; key=btrfs.mount_options)
		//
		// ---
		//  type: string
		//  defaultdesc: `user_subvol_rm_allowed`
		//  shortdesc: Mount options for block devices
		//  scope: global
		"btrfs.mount_options": validate.IsAny,
	}

	return d.validatePool(config, rules, nil)
}

// Update applies any driver changes required from a configuration change.
func (d *btrfs) Update(changedConfig map[string]string) error {
	// We only care about btrfs.mount_options.
	val, ok := changedConfig["btrfs.mount_options"]
	if ok {
		// Custom mount options don't work inside containers
		if d.state.OS.RunningInUserNS {
			return nil
		}

		// Trigger a re-mount.
		d.config["btrfs.mount_options"] = val
		mntFlags, mntOptions := filesystem.ResolveMountOptions(strings.Split(d.getMountOptions(), ","))
		mntFlags |= unix.MS_REMOUNT

		err := TryMount(context.TODO(), "", GetPoolMountPath(d.name), "none", mntFlags, mntOptions)
		if err != nil {
			return err
		}
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

		defer func() { _ = loopDeviceAutoDetach(loopDevPath) }()

		err = loopDeviceSetCapacity(loopDevPath)
		if err != nil {
			return err
		}

		_, err = shared.RunCommandContext(context.TODO(), "btrfs", "filesystem", "resize", "max", GetPoolMountPath(d.name))
		if err != nil {
			return err
		}
	}

	return nil
}

// Mount mounts the storage pool.
func (d *btrfs) Mount() (bool, error) {
	// Check if already mounted.
	if filesystem.IsMountPoint(GetPoolMountPath(d.name)) {
		return false, nil
	}

	var err error

	// Setup mount options.
	loopPath := loopFilePath(d.name)
	mntSrc := ""
	mntDst := GetPoolMountPath(d.name)
	mntFilesystem := "btrfs"
	if d.config["source"] == loopPath {
		mntSrc, err = loopDeviceSetup(d.config["source"])
		if err != nil {
			return false, err
		}

		defer func() { _ = loopDeviceAutoDetach(mntSrc) }()
	} else if filepath.IsAbs(d.config["source"]) {
		// Bring up an existing device or path.
		mntSrc = shared.HostPath(d.config["source"])

		if !shared.IsBlockdevPath(mntSrc) {
			mntFilesystem = "none"

			mntSrcFS, _ := filesystem.Detect(mntSrc)
			if mntSrcFS != "btrfs" {
				return false, fmt.Errorf("Source path %q isn't btrfs (detected %s)", mntSrc, mntSrcFS)
			}
		}
	} else {
		// Mount using UUID.
		mntSrc = "/dev/disk/by-uuid/" + d.config["source"]
	}

	// Get the custom mount flags/options.
	mntFlags, mntOptions := filesystem.ResolveMountOptions(strings.Split(d.getMountOptions(), ","))

	// Handle bind-mounts first.
	if mntFilesystem == "none" {
		// Setup the bind-mount itself.
		err := TryMount(context.TODO(), mntSrc, mntDst, mntFilesystem, unix.MS_BIND, "")
		if err != nil {
			return false, err
		}

		// Custom mount options don't work inside containers
		if d.state.OS.RunningInUserNS {
			return true, nil
		}

		// Now apply the custom options.
		mntFlags |= unix.MS_REMOUNT
		err = TryMount(context.TODO(), "", mntDst, mntFilesystem, mntFlags, mntOptions)
		if err != nil {
			return false, err
		}

		return true, nil
	}

	// Handle traditional mounts.
	err = TryMount(context.TODO(), mntSrc, mntDst, mntFilesystem, mntFlags, mntOptions)
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *btrfs) Unmount() (bool, error) {
	// Unmount the pool.
	ourUnmount, err := forceUnmount(GetPoolMountPath(d.name))
	if err != nil {
		return false, err
	}

	return ourUnmount, nil
}

// GetResources returns the pool resource usage information.
func (d *btrfs) GetResources() (*api.ResourcesStoragePool, error) {
	return genericVFSGetResources(d)
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *btrfs) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	var rsyncFeatures []string
	btrfsFeatures := []string{migration.BTRFSFeatureMigrationHeader, migration.BTRFSFeatureSubvolumes, migration.BTRFSFeatureSubvolumeUUIDs}

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	// Only offer rsync if running in an unprivileged container.
	if d.state.OS.RunningInUserNS {
		var transportType migration.MigrationFSType

		if IsContentBlock(contentType) {
			transportType = migration.MigrationFSType_BLOCK_AND_RSYNC
		} else {
			transportType = migration.MigrationFSType_RSYNC
		}

		return []migration.Type{
			{
				FSType:   transportType,
				Features: rsyncFeatures,
			},
		}
	}

	if IsContentBlock(contentType) {
		return []migration.Type{
			{
				FSType:   migration.MigrationFSType_BTRFS,
				Features: btrfsFeatures,
			},
			{
				FSType:   migration.MigrationFSType_BLOCK_AND_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	if refresh && !copySnapshots {
		return []migration.Type{
			{
				FSType:   migration.MigrationFSType_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_BTRFS,
			Features: btrfsFeatures,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}
