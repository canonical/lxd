package drivers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
)

var btrfsVersion string
var btrfsLoaded bool

type btrfs struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *btrfs) load() error {
	if btrfsLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"btrfs"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool '%s' is missing", tool)
		}
	}

	// Detect and record the version.
	if btrfsVersion == "" {
		out, err := shared.RunCommand("btrfs", "version")
		if err != nil {
			return err
		}

		count, err := fmt.Sscanf(strings.SplitN(out, " ", 2)[1], "v%s\n", &btrfsVersion)
		if err != nil || count != 1 {
			return fmt.Errorf("The 'btrfs' tool isn't working properly")
		}
	}

	btrfsLoaded = true
	return nil
}

// Info returns info about the driver and its environment.
func (d *btrfs) Info() Info {
	return Info{
		Name:                  "btrfs",
		Version:               btrfsVersion,
		OptimizedImages:       true,
		PreservesInodes:       !d.state.OS.RunningInUserNS,
		Remote:                false,
		VolumeTypes:           []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:          false,
		RunningQuotaResize:    true,
		RunningSnapshotFreeze: false,
	}
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *btrfs) Create() error {
	// Store the provided source as we are likely to be mangling it.
	d.config["volatile.initial_source"] = d.config["source"]

	loopPath := filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", d.name))
	if d.config["source"] == "" || d.config["source"] == loopPath {
		// Create a loop based pool.
		d.config["source"] = loopPath

		// Create the loop file itself.
		size, err := units.ParseByteSizeString(d.config["size"])
		if err != nil {
			return err
		}

		err = createSparseFile(d.config["source"], size)
		if err != nil {
			return fmt.Errorf("Failed to create the sparse file: %v", err)
		}

		// Format the file.
		_, err = makeFSType(d.config["source"], "btrfs", &mkfsOptions{Label: d.name})
		if err != nil {
			return fmt.Errorf("Failed to format sparse file: %v", err)
		}
	} else if shared.IsBlockdevPath(d.config["source"]) {
		// Format the block device.
		_, err := makeFSType(d.config["source"], "btrfs", &mkfsOptions{Label: d.name})
		if err != nil {
			return fmt.Errorf("Failed to format block device: %v", err)
		}

		// Record the UUID as the source.
		devUUID, err := fsUUID(d.config["source"])
		if err != nil {
			return err
		}

		// Confirm that the symlink is appearing (give it 10s).
		if tryExists(fmt.Sprintf("/dev/disk/by-uuid/%s", devUUID)) {
			// Override the config to use the UUID.
			d.config["source"] = devUUID
		}
	} else if d.config["source"] != "" {
		hostPath := shared.HostPath(d.config["source"])
		if d.isSubvolume(hostPath) {
			// Existing btrfs subvolume.
			subvols, err := d.getSubvolumes(hostPath)
			if err != nil {
				return fmt.Errorf("Could not determine if existing btrfs subvolume is empty: %v", err)
			}

			// Check that the provided subvolume is empty.
			if len(subvols) > 0 {
				return fmt.Errorf("Requested btrfs subvolume exists but is not empty")
			}
		} else {
			// New btrfs subvolume on existing btrfs filesystem.
			cleanSource := filepath.Clean(hostPath)
			lxdDir := shared.VarPath()

			if shared.PathExists(hostPath) && !hasFilesystem(hostPath, util.FilesystemSuperMagicBtrfs) {
				return fmt.Errorf("Provided path does not reside on a btrfs filesystem")
			} else if strings.HasPrefix(cleanSource, lxdDir) {
				if cleanSource != GetPoolMountPath(d.name) {
					return fmt.Errorf("Only allowed source path under %s is %s", shared.VarPath(), GetPoolMountPath(d.name))
				} else if !hasFilesystem(shared.VarPath("storage-pools"), util.FilesystemSuperMagicBtrfs) {
					return fmt.Errorf("Provided path does not reside on a btrfs filesystem")
				}

				// Delete the current directory to replace by subvolume.
				err := os.Remove(cleanSource)
				if err != nil {
					return err
				}
			}

			// Create the subvolume.
			_, err := shared.RunCommand("btrfs", "subvolume", "create", hostPath)
			if err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("Invalid \"source\" property")
	}

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
				return fmt.Errorf("Could not delete btrfs subvolume: %s", path)
			}
		}
	}

	// On delete, wipe everything in the directory.
	err := wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Unmount the path.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	// If the pool path is a subvolume itself, delete it.
	if d.isSubvolume(GetPoolMountPath(d.name)) {
		err := d.deleteSubvolume(GetPoolMountPath(d.name), false)
		if err != nil {
			return err
		}

		// And re-create as an empty directory to make the backend happy.
		err = os.Mkdir(GetPoolMountPath(d.name), 0700)
		if err != nil {
			return err
		}
	}

	// Delete any loop file we may have used.
	loopPath := filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", d.name))
	if shared.PathExists(loopPath) {
		err = os.Remove(loopPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *btrfs) Validate(config map[string]string) error {
	return nil
}

// Update applies any driver changes required from a configuration change.
func (d *btrfs) Update(changedConfig map[string]string) error {
	// We only care about btrfs.mount_options.
	val, ok := changedConfig["btrfs.mount_options"]
	if !ok {
		return nil
	}

	// Trigger a re-mount.
	d.config["btrfs.mount_options"] = val
	mntFlags, mntOptions := resolveMountOptions(d.getMountOptions())
	mntFlags |= unix.MS_REMOUNT

	err := TryMount("", GetPoolMountPath(d.name), "none", mntFlags, mntOptions)
	if err != nil {
		return err
	}

	return nil
}

// Mount mounts the storage pool.
func (d *btrfs) Mount() (bool, error) {
	// Check if already mounted.
	if shared.IsMountPoint(GetPoolMountPath(d.name)) {
		logger.Errorf("here: %v", d.name)
		return false, nil
	}

	// Setup mount options.
	loopPath := filepath.Join(shared.VarPath("disks"), fmt.Sprintf("%s.img", d.name))
	mntSrc := ""
	mntDst := GetPoolMountPath(d.name)
	mntFilesystem := "btrfs"
	if d.config["source"] == loopPath {
		// Bring up the loop device.
		loopF, err := PrepareLoopDev(d.config["source"], LoFlagsAutoclear)
		if err != nil {
			return false, err
		}
		defer loopF.Close()

		mntSrc = loopF.Name()
	} else if filepath.IsAbs(d.config["source"]) {
		// Bring up an existing device or path.
		mntSrc = shared.HostPath(d.config["source"])

		if !shared.IsBlockdevPath(mntSrc) {
			mntFilesystem = "none"

			if !hasFilesystem(mntSrc, util.FilesystemSuperMagicBtrfs) {
				return false, fmt.Errorf("Source path '%s' isn't btrfs", mntSrc)
			}
		}
	} else {
		// Mount using UUID.
		mntSrc = fmt.Sprintf("/dev/disk/by-uuid/%s", d.config["source"])
	}

	// Get the custom mount flags/options.
	mntFlags, mntOptions := resolveMountOptions(d.getMountOptions())

	// Handle bind-mounts first.
	if mntFilesystem == "none" {
		// Setup the bind-mount itself.
		err := TryMount(mntSrc, mntDst, mntFilesystem, unix.MS_BIND, "")
		if err != nil {
			return false, err
		}

		// Now apply the custom options.
		mntFlags |= unix.MS_REMOUNT
		err = TryMount("", mntDst, mntFilesystem, mntFlags, mntOptions)
		if err != nil {
			return false, err
		}

		return true, nil
	}

	// Handle traditional mounts.
	err := TryMount(mntSrc, mntDst, mntFilesystem, mntFlags, mntOptions)
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *btrfs) Unmount() (bool, error) {
	return forceUnmount(GetPoolMountPath(d.name))
}

// GetResources returns the pool resource usage information.
func (d *btrfs) GetResources() (*api.ResourcesStoragePool, error) {
	return d.vfsGetResources()
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools in preference order.
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
