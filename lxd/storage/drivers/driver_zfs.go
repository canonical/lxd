package drivers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/validate"
	"github.com/lxc/lxd/shared/version"
)

var zfsVersion string
var zfsLoaded bool
var zfsDirectIO bool
var zfsTrim bool
var zfsRaw bool

var zfsDefaultSettings = map[string]string{
	"relatime":   "on",
	"mountpoint": "legacy",
	"setuid":     "on",
	"exec":       "on",
	"devices":    "on",
	"acltype":    "posixacl",
	"xattr":      "sa",
}

type zfs struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *zfs) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":          nil,
		"storage_missing_snapshot_records":    nil,
		"storage_delete_old_snapshot_records": nil,
	}

	// Done if previously loaded.
	if zfsLoaded {
		return nil
	}

	// Load the kernel module.
	err := util.LoadModule("zfs")
	if err != nil {
		return fmt.Errorf("Error loading %q module: %w", "zfs", err)
	}

	// Validate the needed tools are present.
	for _, tool := range []string{"zpool", "zfs"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool '%s' is missing", tool)
		}
	}

	// Get the version information.
	if zfsVersion == "" {
		version, err := d.version()
		if err != nil {
			return err
		}

		zfsVersion = version
	}

	// Decide whether we can use features added by 0.8.0.
	ver080, err := version.Parse("0.8.0")
	if err != nil {
		return err
	}

	ourVer, err := version.Parse(zfsVersion)
	if err != nil {
		return err
	}

	// If running 0.8.0 or newer, we can use direct I/O, trim and raw.
	if ourVer.Compare(ver080) >= 0 {
		zfsDirectIO = true
		zfsTrim = true
		zfsRaw = true
	}

	zfsLoaded = true
	return nil
}

// Info returns info about the driver and its environment.
func (d *zfs) Info() Info {
	info := Info{
		Name:              "zfs",
		Version:           zfsVersion,
		OptimizedImages:   true,
		OptimizedBackups:  true,
		PreservesInodes:   true,
		Remote:            d.isRemote(),
		VolumeTypes:       []VolumeType{VolumeTypeBucket, VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:      shared.IsTrue(d.config["volume.zfs.block_mode"]),
		RunningCopyFreeze: false,
		DirectIO:          zfsDirectIO,
		MountedRoot:       false,
		Buckets:           true,
	}

	return info
}

// ensureInitialDatasets creates missing initial datasets or configures existing ones with current policy.
// Accepts warnOnExistingPolicyApplyError argument, if true will warn rather than fail if applying current policy
// to an existing dataset fails.
func (d zfs) ensureInitialDatasets(warnOnExistingPolicyApplyError bool) error {
	args := make([]string, 0, len(zfsDefaultSettings))
	for k, v := range zfsDefaultSettings {
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}

	err := d.setDatasetProperties(d.config["zfs.pool_name"], args...)
	if err != nil {
		if warnOnExistingPolicyApplyError {
			d.logger.Warn("Failed applying policy to existing dataset", logger.Ctx{"dataset": d.config["zfs.pool_name"], "err": err})
		} else {
			return fmt.Errorf("Failed applying policy to existing dataset %q: %w", d.config["zfs.pool_name"], err)
		}
	}

	for _, dataset := range d.initialDatasets() {
		properties := []string{"mountpoint=legacy"}
		if shared.StringInSlice(dataset, []string{"virtual-machines", "deleted/virtual-machines"}) {
			if len(zfsVersion) >= 3 && zfsVersion[0:3] == "0.6" {
				d.logger.Warn("Unable to set volmode on parent virtual-machines datasets due to ZFS being too old")
			} else {
				properties = append(properties, "volmode=none")
			}
		}

		datasetPath := filepath.Join(d.config["zfs.pool_name"], dataset)
		exists, err := d.datasetExists(datasetPath)
		if err != nil {
			return err
		}

		if exists {
			err = d.setDatasetProperties(datasetPath, properties...)
			if err != nil {
				if warnOnExistingPolicyApplyError {
					d.logger.Warn("Failed applying policy to existing dataset", logger.Ctx{"dataset": datasetPath, "err": err})
				} else {
					return fmt.Errorf("Failed applying policy to existing dataset %q: %w", datasetPath, err)
				}
			}
		} else {
			err = d.createDataset(datasetPath, properties...)
			if err != nil {
				return fmt.Errorf("Failed creating dataset %q: %w", datasetPath, err)
			}
		}
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *zfs) Create() error {
	// Store the provided source as we are likely to be mangling it.
	d.config["volatile.initial_source"] = d.config["source"]

	loopPath := loopFilePath(d.name)
	if d.config["source"] == "" || d.config["source"] == loopPath {
		// Create a loop based pool.
		d.config["source"] = loopPath

		// Set default pool_name.
		if d.config["zfs.pool_name"] == "" {
			d.config["zfs.pool_name"] = d.name
		}

		// Validate pool_name.
		if strings.Contains(d.config["zfs.pool_name"], "/") {
			return fmt.Errorf("zfs.pool_name can't point to a dataset when source isn't set")
		}

		// Pick a default size of the loop file if not specified.
		if d.config["size"] == "" {
			defaultSize, err := loopFileSizeDefault()
			if err != nil {
				return err
			}

			d.config["size"] = fmt.Sprintf("%dGiB", defaultSize)
		}

		// Create the loop file itself.
		size, err := units.ParseByteSizeString(d.config["size"])
		if err != nil {
			return err
		}

		err = ensureSparseFile(loopPath, size)
		if err != nil {
			return err
		}

		// Create the zpool.
		_, err = shared.RunCommand("zpool", "create", "-m", "none", "-O", "compression=on", d.config["zfs.pool_name"], loopPath)
		if err != nil {
			return err
		}

		// Apply auto-trim if supported.
		if zfsTrim {
			_, err := shared.RunCommand("zpool", "set", "autotrim=on", d.config["zfs.pool_name"])
			if err != nil {
				return err
			}
		}
	} else if filepath.IsAbs(d.config["source"]) {
		// Handle existing block devices.
		if !shared.IsBlockdevPath(d.config["source"]) {
			return fmt.Errorf("Custom loop file locations are not supported")
		}

		// Unset size property since it's irrelevant.
		d.config["size"] = ""

		// Set default pool_name.
		if d.config["zfs.pool_name"] == "" {
			d.config["zfs.pool_name"] = d.name
		}

		// Validate pool_name.
		if strings.Contains(d.config["zfs.pool_name"], "/") {
			return fmt.Errorf("zfs.pool_name can't point to a dataset when source isn't set")
		}

		// Wipe if requested.
		if shared.IsTrue(d.config["source.wipe"]) {
			err := wipeBlockHeaders(d.config["source"])
			if err != nil {
				return fmt.Errorf("Failed to wipe headers from disk %q: %w", d.config["source"], err)
			}

			d.config["source.wipe"] = ""

			// Create the zpool.
			_, err = shared.RunCommand("zpool", "create", "-f", "-m", "none", "-O", "compression=on", d.config["zfs.pool_name"], d.config["source"])
			if err != nil {
				return err
			}
		} else {
			// Create the zpool.
			_, err := shared.RunCommand("zpool", "create", "-m", "none", "-O", "compression=on", d.config["zfs.pool_name"], d.config["source"])
			if err != nil {
				return err
			}
		}

		// Apply auto-trim if supported.
		if zfsTrim {
			_, err := shared.RunCommand("zpool", "set", "autotrim=on", d.config["zfs.pool_name"])
			if err != nil {
				return err
			}
		}

		// We don't need to keep the original source path around for import.
		d.config["source"] = d.config["zfs.pool_name"]
	} else {
		// Handle an existing zpool.
		if d.config["zfs.pool_name"] == "" {
			d.config["zfs.pool_name"] = d.config["source"]
		}

		// Unset size property since it's irrelevant.
		d.config["size"] = ""

		// Validate pool_name.
		if d.config["zfs.pool_name"] != d.config["source"] {
			return fmt.Errorf("The source must match zfs.pool_name if specified")
		}

		if strings.Contains(d.config["zfs.pool_name"], "/") {
			// Handle a dataset.
			exists, err := d.datasetExists(d.config["zfs.pool_name"])
			if err != nil {
				return err
			}

			if !exists {
				err := d.createDataset(d.config["zfs.pool_name"], "mountpoint=legacy")
				if err != nil {
					return err
				}
			}
		} else {
			// Ensure that the pool is available.
			_, err := d.importPool()
			if err != nil {
				return err
			}
		}

		// Confirm that the existing pool/dataset is all empty.
		datasets, err := d.getDatasets(d.config["zfs.pool_name"])
		if err != nil {
			return err
		}

		if len(datasets) > 0 {
			return fmt.Errorf(`Provided ZFS pool (or dataset) isn't empty, run "sudo zfs list -r %s" to see existing entries`, d.config["zfs.pool_name"])
		}
	}

	// Setup revert in case of problems
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { _ = d.Delete(nil) })

	// Apply our default configuration.
	err := d.ensureInitialDatasets(false)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Delete removes the storage pool from the storage device.
func (d *zfs) Delete(op *operations.Operation) error {
	// Check if the dataset/pool is already gone.
	exists, err := d.datasetExists(d.config["zfs.pool_name"])
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	// Confirm that nothing's been left behind
	datasets, err := d.getDatasets(d.config["zfs.pool_name"])
	if err != nil {
		return err
	}

	initialDatasets := d.initialDatasets()
	for _, dataset := range datasets {
		if shared.StringInSlice(dataset, initialDatasets) {
			continue
		}

		fields := strings.Split(dataset, "/")
		if len(fields) > 1 {
			return fmt.Errorf("ZFS pool has leftover datasets: %s", dataset)
		}
	}

	if strings.Contains(d.config["zfs.pool_name"], "/") {
		// Delete the dataset.
		_, err := shared.RunCommand("zfs", "destroy", "-r", d.config["zfs.pool_name"])
		if err != nil {
			return err
		}
	} else {
		// Delete the pool.
		_, err := shared.RunCommand("zpool", "destroy", d.config["zfs.pool_name"])
		if err != nil {
			return err
		}
	}

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Delete any loop file we may have used
	loopPath := loopFilePath(d.name)
	err = os.Remove(loopPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", loopPath, err)
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *zfs) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"size":          validate.Optional(validate.IsSize),
		"zfs.pool_name": validate.IsAny,
		"zfs.clone_copy": validate.Optional(func(value string) error {
			if value == "rebase" {
				return nil
			}

			return validate.IsBool(value)
		}),
		"zfs.export": validate.Optional(validate.IsBool),
	}

	return d.validatePool(config, rules, d.commonVolumeRules())
}

// Update applies any driver changes required from a configuration change.
func (d *zfs) Update(changedConfig map[string]string) error {
	_, ok := changedConfig["zfs.pool_name"]
	if ok {
		return fmt.Errorf("zfs.pool_name cannot be modified")
	}

	size, ok := changedConfig["size"]
	if ok {
		// Figure out loop path
		loopPath := loopFilePath(d.name)

		if d.config["source"] != loopPath {
			return fmt.Errorf("Cannot resize non-loopback pools")
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

		_, err = shared.RunCommand("zpool", "online", "-e", d.name, loopPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// importPool the storage pool.
func (d *zfs) importPool() (bool, error) {
	if d.config["zfs.pool_name"] == "" {
		return false, fmt.Errorf("Cannot mount pool as %q is not specified", "zfs.pool_name")
	}

	// Check if already setup.
	exists, err := d.datasetExists(d.config["zfs.pool_name"])
	if err != nil {
		return false, err
	}

	if exists {
		return false, nil
	}

	// Check if the pool exists.
	poolName := strings.Split(d.config["zfs.pool_name"], "/")[0]
	exists, err = d.datasetExists(poolName)
	if err != nil {
		return false, err
	}

	if exists {
		return false, fmt.Errorf("ZFS zpool exists but dataset is missing")
	}

	// Import the pool.
	if filepath.IsAbs(d.config["source"]) {
		disksPath := shared.VarPath("disks")
		_, err := shared.RunCommand("zpool", "import", "-f", "-d", disksPath, poolName)
		if err != nil {
			return false, err
		}
	} else {
		_, err := shared.RunCommand("zpool", "import", poolName)
		if err != nil {
			return false, err
		}
	}

	// Check that the dataset now exists.
	exists, err = d.datasetExists(d.config["zfs.pool_name"])
	if err != nil {
		return false, err
	}

	if exists {
		return true, nil
	}

	return false, fmt.Errorf("ZFS zpool exists but dataset is missing")
}

// Mount mounts the storage pool.
func (d *zfs) Mount() (bool, error) {
	// Import the pool if not already imported.
	imported, err := d.importPool()
	if err != nil {
		return false, err
	}

	// Apply our default configuration.
	err = d.ensureInitialDatasets(true)
	if err != nil {
		return false, err
	}

	return imported, nil
}

// Unmount unmounts the storage pool.
func (d *zfs) Unmount() (bool, error) {
	// Skip if zfs.export config is set to false
	if shared.IsFalse(d.config["zfs.export"]) {
		return false, nil
	}

	// Skip if using a dataset and not a full pool.
	if strings.Contains(d.config["zfs.pool_name"], "/") {
		return false, nil
	}

	// Check if already unmounted.
	exists, err := d.datasetExists(d.config["zfs.pool_name"])
	if err != nil {
		return false, err
	}

	if !exists {
		return false, nil
	}

	// Export the pool.
	poolName := strings.Split(d.config["zfs.pool_name"], "/")[0]
	_, err = shared.RunCommand("zpool", "export", poolName)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (d *zfs) GetResources() (*api.ResourcesStoragePool, error) {
	// Get the total amount of space.
	availableStr, err := d.getDatasetProperty(d.config["zfs.pool_name"], "available")
	if err != nil {
		return nil, err
	}

	available, err := strconv.ParseUint(strings.TrimSpace(availableStr), 10, 64)
	if err != nil {
		return nil, err
	}

	// Get the used amount of space.
	usedStr, err := d.getDatasetProperty(d.config["zfs.pool_name"], "used")
	if err != nil {
		return nil, err
	}

	used, err := strconv.ParseUint(strings.TrimSpace(usedStr), 10, 64)
	if err != nil {
		return nil, err
	}

	// Build the struct.
	// Inode allocation is dynamic so no use in reporting them.
	res := api.ResourcesStoragePool{}
	res.Space.Total = used + available
	res.Space.Used = used

	return &res, nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *zfs) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	// Detect ZFS features.
	features := []string{migration.ZFSFeatureMigrationHeader}

	if contentType == ContentTypeFS {
		features = append(features, migration.ZFSFeatureZvolFilesystems)
	}

	if len(zfsVersion) >= 3 && zfsVersion[0:3] != "0.6" {
		features = append(features, "compress")
	}

	if contentType == ContentTypeBlock {
		return []migration.Type{
			{
				FSType:   migration.MigrationFSType_ZFS,
				Features: features,
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
			FSType:   migration.MigrationFSType_ZFS,
			Features: features,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}
