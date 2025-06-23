package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
)

var cephfsVersion string
var cephfsLoaded bool

type cephfs struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *cephfs) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if cephfsLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"ceph", "rbd"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool %q is missing", tool)
		}
	}

	// Detect and record the version.
	if cephfsVersion == "" {
		out, err := shared.RunCommandContext(d.state.ShutdownCtx, "rbd", "--version")
		if err != nil {
			return err
		}

		out = strings.TrimSpace(out)

		fields := strings.Split(out, " ")
		if strings.HasPrefix(out, "ceph version ") && len(fields) > 2 {
			cephfsVersion = fields[2]
		} else {
			cephfsVersion = out
		}
	}

	cephfsLoaded = true
	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *cephfs) isRemote() bool {
	return true
}

// Info returns the pool driver information.
func (d *cephfs) Info() Info {
	return Info{
		Name:                         "cephfs",
		Version:                      cephfsVersion,
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              false,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom},
		VolumeMultiNode:              d.isRemote(),
		BlockBacking:                 false,
		RunningCopyFreeze:            false,
		DirectIO:                     true,
		MountedRoot:                  true,
		PopulateParentVolumeUUID:     false,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *cephfs) FillConfig() error {
	if d.config["cephfs.cluster_name"] == "" {
		d.config["cephfs.cluster_name"] = CephDefaultCluster
	}

	if d.config["cephfs.user.name"] == "" {
		d.config["cephfs.user.name"] = CephDefaultUser
	}

	if d.config["cephfs.osd_pool_size"] == "" {
		defaultSize, err := d.getOSDPoolDefaultSize()
		if err != nil {
			return err
		}

		d.config["cephfs.osd_pool_size"] = strconv.Itoa(defaultSize)
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *cephfs) Create() error {
	revert := revert.New()
	defer revert.Fail()

	err := d.FillConfig()
	if err != nil {
		return err
	}

	// Config validation.
	if d.config["source"] == "" {
		return errors.New("Missing required source name/path")
	}

	if d.config["cephfs.path"] != "" && d.config["cephfs.path"] != d.config["source"] {
		return errors.New("cephfs.path must match the source")
	}

	d.config["cephfs.path"] = d.config["source"]

	// Parse the namespace / path.
	fields := strings.SplitN(d.config["cephfs.path"], "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// If the filesystem already exists, disallow keys associated to creating the filesystem.
	fsExists, err := d.fsExists(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"], fsName)
	if err != nil {
		return fmt.Errorf("Failed to check if %q CephFS exists: %w", fsName, err)
	}

	if fsExists {
		for _, key := range []string{"create_missing", "osd_pg_num", "meta_pool", "data_pool"} {
			cephfsSourceKey := "cephfs." + key
			if d.config[cephfsSourceKey] != "" {
				return fmt.Errorf("Invalid config key %q: CephFS filesystem already exists", cephfsSourceKey)
			}
		}
	} else {
		createMissing := shared.IsTrue(d.config["cephfs.create_missing"])
		if !createMissing {
			return fmt.Errorf("The requested %q CephFS doesn't exist", fsName)
		}

		// Set the pg_num to 32 because we need to specify something, but ceph will automatically change it if necessary.
		pgNum := d.config["cephfs.osd_pg_num"]
		if pgNum == "" {
			d.config["cephfs.osd_pg_num"] = "32"
		}

		// Create the meta and data pools if necessary.
		for _, key := range []string{"cephfs.meta_pool", "cephfs.data_pool"} {
			pool := d.config[key]

			if pool == "" {
				return fmt.Errorf("Missing required key %q for creating cephfs osd pool", key)
			}

			osdPoolExists, err := d.osdPoolExists(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"], pool)
			if err != nil {
				return fmt.Errorf("Failed to check if %q OSD Pool exists: %w", pool, err)
			}

			if !osdPoolExists {
				// Create new osd pool.
				_, err := shared.RunCommandContext(d.state.ShutdownCtx, "ceph",
					"--name", "client."+d.config["cephfs.user.name"],
					"--cluster", d.config["cephfs.cluster_name"],
					"osd",
					"pool",
					"create",
					pool,
					d.config["cephfs.osd_pg_num"],
				)
				if err != nil {
					return fmt.Errorf("Failed to create ceph OSD pool %q: %w", pool, err)
				}

				revert.Add(func() {
					// Delete the OSD pool.
					_, _ = shared.RunCommandContext(d.state.ShutdownCtx, "ceph",
						"--name", "client."+d.config["cephfs.user.name"],
						"--cluster", d.config["cephfs.cluster_name"],
						"osd",
						"pool",
						"delete",
						pool,
						pool,
						"--yes-i-really-really-mean-it",
					)
				})

				defaultSize, err := d.getOSDPoolDefaultSize()
				if err != nil {
					return err
				}

				if strconv.Itoa(defaultSize) != d.config["cephfs.osd_pool_size"] {
					_, err = shared.TryRunCommand("ceph",
						"--name", "client."+d.config["cephfs.user.name"],
						"--cluster", d.config["cephfs.cluster_name"],
						"osd",
						"pool",
						"set",
						pool,
						"size",
						d.config["cephfs.osd_pool_size"],
						"--yes-i-really-mean-it")
					if err != nil {
						return err
					}
				}
			}
		}

		// Create the filesystem.
		_, err := shared.RunCommandContext(d.state.ShutdownCtx, "ceph",
			"--name", "client."+d.config["cephfs.user.name"],
			"--cluster", d.config["cephfs.cluster_name"],
			"fs",
			"new",
			fsName,
			d.config["cephfs.meta_pool"],
			d.config["cephfs.data_pool"],
		)
		if err != nil {
			return fmt.Errorf("Failed to create CephFS %q: %w", fsName, err)
		}

		revert.Add(func() {
			// Set the FS to fail so that we can remove it.
			_, _ = shared.RunCommandContext(d.state.ShutdownCtx, "ceph",
				"--name", "client."+d.config["cephfs.user.name"],
				"--cluster", d.config["cephfs.cluster_name"],
				"fs",
				"fail",
				fsName,
			)

			// Delete the FS.
			_, _ = shared.RunCommandContext(d.state.ShutdownCtx, "ceph",
				"--name", "client."+d.config["cephfs.user.name"],
				"--cluster", d.config["cephfs.cluster_name"],
				"fs",
				"rm",
				fsName,
				"--yes-i-really-mean-it",
			)
		})
	}

	// Create a temporary mountpoint.
	mountPath, err := os.MkdirTemp("", "lxd_cephfs_")
	if err != nil {
		return fmt.Errorf("Failed to create temporary directory under: %w", err)
	}

	defer func() { _ = os.RemoveAll(mountPath) }()

	err = os.Chmod(mountPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to chmod '%s': %w", mountPath, err)
	}

	mountPoint := filepath.Join(mountPath, "mount")

	err = os.Mkdir(mountPoint, 0700)
	if err != nil {
		return fmt.Errorf("Failed to create directory '%s': %w", mountPoint, err)
	}

	// Get the credentials and host.
	monAddresses, userSecret, err := d.getConfig(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"])
	if err != nil {
		return err
	}

	// Mount the pool.
	srcPath := strings.Join(monAddresses, ",") + ":/"
	err = TryMount(context.TODO(), srcPath, mountPoint, "ceph", 0, d.getMountOptions(d.config["cephfs.user.name"], userSecret, fsName))
	if err != nil {
		return err
	}

	defer func() { _, _ = forceUnmount(mountPoint) }()

	// Create the path if missing.
	err = os.MkdirAll(filepath.Join(mountPoint, fsPath), 0755)
	if err != nil {
		return fmt.Errorf("Failed to create directory '%s': %w", filepath.Join(mountPoint, fsPath), err)
	}

	// Check that the existing path is empty.
	ok, _ := shared.PathIsEmpty(filepath.Join(mountPoint, fsPath))
	if !ok {
		return errors.New("Only empty CephFS paths can be used as a LXD storage pool")
	}

	revert.Success()

	return nil
}

// Delete clears any local and remote data related to this driver instance.
func (d *cephfs) Delete(op *operations.Operation) error {
	// Parse the namespace / path.
	fields := strings.SplitN(d.config["cephfs.path"], "/", 2)
	fsName := fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Create a temporary mountpoint.
	mountPath, err := os.MkdirTemp("", "lxd_cephfs_")
	if err != nil {
		return fmt.Errorf("Failed to create temporary directory under: %w", err)
	}

	defer func() { _ = os.RemoveAll(mountPath) }()

	err = os.Chmod(mountPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to chmod '%s': %w", mountPath, err)
	}

	mountPoint := filepath.Join(mountPath, "mount")
	err = os.Mkdir(mountPoint, 0700)
	if err != nil {
		return fmt.Errorf("Failed to create directory '%s': %w", mountPoint, err)
	}

	// Get the credentials and host.
	monAddresses, userSecret, err := d.getConfig(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"])
	if err != nil {
		return err
	}

	// Mount the pool.
	srcPath := strings.Join(monAddresses, ",") + ":/"
	err = TryMount(context.TODO(), srcPath, mountPoint, "ceph", 0, d.getMountOptions(d.config["cephfs.user.name"], userSecret, fsName))
	if err != nil {
		return err
	}

	defer func() { _, _ = forceUnmount(mountPoint) }()

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Delete the pool from the parent.
	if shared.PathExists(filepath.Join(mountPoint, fsPath)) {
		// Delete the path itself.
		if fsPath != "" && fsPath != "/" {
			err = os.Remove(filepath.Join(mountPoint, fsPath))
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("Failed to remove directory '%s': %w", filepath.Join(mountPoint, fsPath), err)
			}
		}
	}

	// Make sure the existing pool is unmounted.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *cephfs) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.cluster_name)
		//
		// ---
		//  type: string
		//  defaultdesc: `ceph`
		//  shortdesc: Name of the Ceph cluster that contains the CephFS file system
		//  scope: global
		"cephfs.cluster_name": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.fscache)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Enable use of kernel `fscache` and `cachefilesd`
		//  scope: global
		"cephfs.fscache": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.path)
		//
		// ---
		//  type: string
		//  defaultdesc: `/`
		//  shortdesc: The base path for the CephFS mount
		//  scope: global
		"cephfs.path": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.user.name)
		//
		// ---
		//  type: string
		//  defaultdesc: `admin`
		//  shortdesc: The Ceph user to use
		//  scope: global
		"cephfs.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.create_missing)
		// Use this option if the CephFS file system does not exist yet.
		// LXD will then automatically create the file system and the missing data and metadata OSD pools.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Automatically create the CephFS file system
		//  scope: global
		"cephfs.create_missing": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.osd_pg_num)
		// This option specifies the number of OSD pool placement groups (`pg_num`) to use
		// when creating a missing OSD pool.
		// ---
		//  type: string
		//  shortdesc: Number of placement groups when creating missing OSD pools
		//  scope: global
		"cephfs.osd_pg_num": validate.Optional(validate.IsInt64),
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.osd_pool_size)
		// This option specifies the number of OSD pool replicas to use
		// when creating an OSD pool.
		// ---
		//  type: string
		//  defaultdesc: `3`
		//  shortdesc: Number of RADOS object replicas. Set to 1 for no replication.
		"cephfs.osd_pool_size": validate.Optional(validate.IsInRange(1, 255)),
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.meta_pool)
		// This option specifies the name for the file metadata OSD pool that should be used when
		// creating a file system automatically.
		// ---
		//  type: string
		//  shortdesc: Metadata OSD pool name
		//  scope: global
		"cephfs.meta_pool": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=cephfs.data_pool)
		// This option specifies the name for the data OSD pool that should be used when creating
		// a file system automatically.
		// ---
		//  type: string
		//  shortdesc: Data OSD pool name
		//  scope: global
		"cephfs.data_pool": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephfs; group=pool-conf; key=volatile.pool.pristine)
		//
		// ---
		//  type: string
		//  defaultdesc: `true`
		//  shortdesc: Whether the CephFS file system was empty on creation time
		//  scope: global
		"volatile.pool.pristine": validate.IsAny,
	}

	// This overrides the common driver rule for security.shared.
	volumeRules := map[string]func(value string) error{
		"security.shared": func(value string) error {
			if value != "" {
				return errors.New(`Setting "security.shared" is not allowed for cephfs as it doesn't support block volumes`)
			}

			return nil
		},
	}

	return d.validatePool(config, rules, volumeRules)
}

// Update applies any driver changes required from a configuration change.
func (d *cephfs) Update(changedConfig map[string]string) error {
	newSize, changed := changedConfig["cephfs.osd_pool_size"]
	if changed {
		for _, poolName := range []string{d.config["cephfs.meta_pool"], d.config["cephfs.data_pool"]} {
			if poolName == "" {
				continue
			}

			_, err := shared.TryRunCommand("ceph",
				"--name", "client."+d.config["cephfs.user.name"],
				"--cluster", d.config["cephfs.cluster_name"],
				"osd",
				"pool",
				"set",
				poolName,
				"size",
				newSize,
				"--yes-i-really-mean-it")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Mount brings up the driver and sets it up to be used.
func (d *cephfs) Mount() (bool, error) {
	// Check if already mounted.
	if filesystem.IsMountPoint(GetPoolMountPath(d.name)) {
		return false, nil
	}

	// Parse the namespace / path.
	fields := strings.SplitN(d.config["cephfs.path"], "/", 2)
	fsName := fields[0]
	fsPath := ""
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Get the credentials and host.
	monAddresses, userSecret, err := d.getConfig(d.config["cephfs.cluster_name"], d.config["cephfs.user.name"])
	if err != nil {
		return false, err
	}

	// Mount options.
	options := d.getMountOptions(d.config["cephfs.user.name"], userSecret, fsName)
	if shared.IsTrue(d.config["cephfs.fscache"]) {
		options += ",fsc"
	}

	// Mount the pool.
	srcPath := strings.Join(monAddresses, ",") + ":/" + fsPath
	err = TryMount(context.TODO(), srcPath, GetPoolMountPath(d.name), "ceph", 0, options)
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount clears any of the runtime state of the driver.
func (d *cephfs) Unmount() (bool, error) {
	return forceUnmount(GetPoolMountPath(d.name))
}

// GetResources returns the pool resource usage information.
func (d *cephfs) GetResources() (*api.ResourcesStoragePool, error) {
	return genericVFSGetResources(d)
}

// MigrationTypes returns the supported migration types and options supported by the driver.
func (d *cephfs) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"delete", "compress", "bidirectional"}
	}

	if contentType != ContentTypeFS {
		return nil
	}

	// Do not support xattr transfer on cephfs
	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}
