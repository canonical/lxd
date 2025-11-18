package drivers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

var cephVersion string
var cephLoaded bool

type ceph struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *ceph) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if cephLoaded {
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
	if cephVersion == "" {
		out, err := shared.RunCommandContext(d.state.ShutdownCtx, "rbd", "--version")
		if err != nil {
			return err
		}

		out = strings.TrimSpace(out)

		fields := strings.Split(out, " ")
		if strings.HasPrefix(out, "ceph version ") && len(fields) > 2 {
			cephVersion = fields[2]
		} else {
			cephVersion = out
		}
	}

	cephLoaded = true
	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *ceph) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *ceph) Info() Info {
	return Info{
		Name:                         "ceph",
		Version:                      cephVersion,
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              true,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  false,
		PopulateParentVolumeUUID:     false,
	}
}

// getPlaceholderVolume returns the volume used to indicate if the pool is used by LXD.
func (d *ceph) getPlaceholderVolume() Volume {
	return NewVolume(d, d.name, VolumeType("lxd"), ContentTypeFS, d.config["ceph.osd.pool_name"], nil, nil)
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *ceph) FillConfig() error {
	if d.config["ceph.cluster_name"] == "" {
		d.config["ceph.cluster_name"] = CephDefaultCluster
	}

	if d.config["ceph.user.name"] == "" {
		d.config["ceph.user.name"] = CephDefaultUser
	}

	if d.config["ceph.osd.pg_num"] == "" {
		d.config["ceph.osd.pg_num"] = "32"
	}

	if d.config["ceph.osd.pool_size"] == "" {
		defaultSize, err := d.getOSDPoolDefaultSize()
		if err != nil {
			return err
		}

		d.config["ceph.osd.pool_size"] = strconv.Itoa(defaultSize)
	}

	// Use an existing OSD pool.
	if d.config["source"] != "" {
		d.config["ceph.osd.pool_name"] = d.config["source"]
	}

	if d.config["ceph.osd.pool_name"] == "" {
		d.config["ceph.osd.pool_name"] = d.name
		d.config["source"] = d.name
	}

	return nil
}

// SourceIdentifier returns a combined string consisting of the cluster and pool name.
func (d *ceph) SourceIdentifier() (string, error) {
	// Return an empty identifier in case the pool should be force reused.
	// This indicates the backend to skip further source verification.
	if shared.IsTrue(d.config["ceph.osd.force_reuse"]) {
		return "", nil
	}

	cluster := d.config["ceph.cluster_name"]
	if cluster == "" {
		return "", errors.New("Cannot derive identifier from empty cluster name")
	}

	pool := d.config["ceph.osd.pool_name"]
	if pool == "" {
		return "", errors.New("Cannot derive identifier from empty pool name")
	}

	return cluster + "-" + pool, nil
}

// ValidateSource checks whether the required config keys are set to access the remote source.
func (d *ceph) ValidateSource() error {
	if d.config["source"] != "" && d.config["ceph.osd.pool_name"] != "" && d.config["source"] != d.config["ceph.osd.pool_name"] {
		return errors.New(`The "source" and "ceph.osd.pool_name" property must not differ for Ceph OSD storage pools`)
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *ceph) Create() error {
	revert := revert.New()
	defer revert.Fail()

	d.config["volatile.initial_source"] = d.config["source"]

	// Validate.
	_, err := units.ParseByteSizeString(d.config["ceph.osd.pg_num"])
	if err != nil {
		return err
	}

	placeholderVol := d.getPlaceholderVolume()
	poolExists, err := d.osdPoolExists()
	if err != nil {
		return fmt.Errorf("Failed checking the existence of the ceph %q osd pool while attempting to create it because of an internal error: %w", d.config["ceph.osd.pool_name"], err)
	}

	if !poolExists {
		// Create new osd pool.
		_, err := shared.TryRunCommand("ceph",
			"--name", "client."+d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"osd",
			"pool",
			"create",
			d.config["ceph.osd.pool_name"],
			d.config["ceph.osd.pg_num"])
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.osdDeletePool() })

		// Fetch the default OSD pool size.
		defaultSize, err := d.getOSDPoolDefaultSize()
		if err != nil {
			return err
		}

		// If the OSD pool size in the config for this pool is different than the default OSD pool size, then set the pool size for the pool.
		if d.config["ceph.osd.pool_size"] != strconv.Itoa(defaultSize) {
			_, err = shared.TryRunCommand("ceph",
				"--name", "client."+d.config["ceph.user.name"],
				"--cluster", d.config["ceph.cluster_name"],
				"osd",
				"pool",
				"set",
				d.config["ceph.osd.pool_name"],
				"size",
				d.config["ceph.osd.pool_size"],
				"--yes-i-really-mean-it")
			if err != nil {
				return err
			}
		}

		// Initialize the pool. This is not necessary but allows the pool to be monitored.
		_, err = shared.TryRunCommand("rbd",
			"--id", d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"pool",
			"init",
			d.config["ceph.osd.pool_name"])
		if err != nil {
			d.logger.Warn("Failed to initialize pool", logger.Ctx{"pool": d.config["ceph.osd.pool_name"], "cluster": d.config["ceph.cluster_name"]})
		}

		// Create placeholder storage volume. Other LXD instances will use this to detect whether this osd
		// pool is already in use by another LXD instance.
		err = d.rbdCreateVolume(placeholderVol, "0")
		if err != nil {
			return err
		}
	} else {
		volExists, err := d.HasVolume(placeholderVol)
		if err != nil {
			return err
		}

		if volExists {
			return fmt.Errorf("Pool %q in cluster %q seems to be in use by another LXD instance", d.config["ceph.osd.pool_name"], d.config["ceph.cluster_name"])
		}

		// Create placeholder storage volume. Other LXD instances will use this to detect whether this osd
		// pool is already in use by another LXD instance.
		err = d.rbdCreateVolume(placeholderVol, "0")
		if err != nil {
			return err
		}

		// Use existing OSD pool.
		msg, err := shared.RunCommandContext(d.state.ShutdownCtx, "ceph",
			"--name", "client."+d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"osd",
			"pool",
			"get",
			d.config["ceph.osd.pool_name"],
			"pg_num")
		if err != nil {
			return err
		}

		idx := strings.Index(msg, "pg_num:")
		if idx == -1 {
			return fmt.Errorf("Failed to parse number of placement groups for pool: %s", msg)
		}

		msg = msg[(idx + len("pg_num:")):]
		msg = strings.TrimSpace(msg)

		// It is ok to update the pool configuration since storage pool
		// creation via API is implemented such that the storage pool is
		// checked for a changed config after this function returns and
		// if so the db for it is updated.
		d.config["ceph.osd.pg_num"] = msg
	}

	// After dropping the ceph.osd.force_reuse key, the volatile.pool.pristine
	// config key can only be true.
	// For backwards compatibility always set it to true when creating new pools.
	// This ensures that when deleting the pool we also always delete the respective OSD pool
	// but keep it for old storage pools which were created using ceph.osd.force_reuse=true.
	d.config["volatile.pool.pristine"] = "true"

	revert.Success()

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *ceph) Delete(op *operations.Operation) error {
	// Test if the pool exists.
	poolExists, err := d.osdPoolExists()
	if err != nil {
		return fmt.Errorf("Failed checking the existence of the ceph %q osd pool while attempting to delete it because of an internal error: %w", d.config["ceph.osd.pool_name"], err)
	}

	if !poolExists {
		d.logger.Warn("Pool does not exist", logger.Ctx{"pool": d.config["ceph.osd.pool_name"], "cluster": d.config["ceph.cluster_name"]})
	}

	// Check whether we own the pool and only remove in this case.
	if shared.IsTrue(d.config["volatile.pool.pristine"]) {
		// Delete the osd pool.
		if poolExists {
			err := d.osdDeletePool()
			if err != nil {
				return err
			}
		}
	}

	// If the user completely destroyed it, call it done.
	if !shared.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *ceph) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.cluster_name)
		//
		// ---
		//  type: string
		//  defaultdesc: `ceph`
		//  shortdesc: Name of the Ceph cluster in which to create new storage pools
		//  scope: global
		"ceph.cluster_name": validate.IsAny,
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.osd.pg_num)
		//
		// ---
		//  type: string
		//  defaultdesc: `32`
		//  shortdesc: Number of placement groups for the OSD storage pool
		//  scope: global
		"ceph.osd.pg_num": validate.IsAny,
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.osd.pool_size)
		// This option specifies the name for the file metadata OSD pool that should be used when
		// creating a file system automatically.
		// ---
		//  type: string
		//  defaultdesc: `3`
		//  shortdesc: Number of RADOS object replicas. Set to 1 for no replication.
		"ceph.osd.pool_size": validate.Optional(validate.IsInRange(1, 255)),
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.osd.pool_name)
		//
		// ---
		//  type: string
		//  defaultdesc: name of the pool
		//  shortdesc: Name of the OSD storage pool
		//  scope: global
		"ceph.osd.pool_name": validate.IsAny,
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.osd.data_pool_name)
		//
		// ---
		//  type: string
		//  shortdesc: Name of the OSD data pool
		//  scope: global
		"ceph.osd.data_pool_name": validate.IsAny,
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.rbd.clone_copy)
		// Enable this option to use RBD lightweight clones rather than full dataset copies.
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to use RBD lightweight clones
		//  scope: global
		"ceph.rbd.clone_copy": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.rbd.du)
		// This option specifies whether to use RBD `du` to obtain disk usage data for stopped instances.
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to use RBD `du`
		//  scope: global
		"ceph.rbd.du": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.rbd.features)
		//
		// ---
		//  type: string
		//  defaultdesc: `layering`
		//  shortdesc: Comma-separated list of RBD features to enable on the volumes
		//  scope: global
		"ceph.rbd.features": validate.IsAny,
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=ceph.user.name)
		//
		// ---
		//  type: string
		//  defaultdesc: `admin`
		//  shortdesc: The Ceph user to use when creating storage pools and volumes
		//  scope: global
		"ceph.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-ceph; group=pool-conf; key=volatile.pool.pristine)
		//
		// ---
		//  type: string
		//  defaultdesc: `true`
		//  shortdesc: Whether the pool was empty on creation time
		//  scope: global
		"volatile.pool.pristine": validate.IsAny,
	}

	immutableOptions := []string{
		// Changing the cluster name does not work as the volume's won't be moved to the new cluster.
		"ceph.cluster_name",
		// Changing the pool name whilst having active volumes does not work as the volumes won't be moved to the new pool.
		"ceph.osd.pool_name",
	}

	for configOption, configOptionValue := range config {
		oldValue, ok := d.config[configOption]

		// Skip config settings which weren't populated before.
		if !ok {
			continue
		}

		if oldValue != configOptionValue && slices.Contains(immutableOptions, configOption) {
			return fmt.Errorf("Cannot update %q", configOption)
		}
	}

	return d.validatePool(config, rules, d.commonVolumeRules())
}

// Update applies any driver changes required from a configuration change.
func (d *ceph) Update(changedConfig map[string]string) error {
	// applyPool applies a OSD pool level setting.
	applyPool := func(key string, value string) error {
		_, err := shared.TryRunCommand("ceph",
			"--name", "client."+d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"osd",
			"pool",
			"set",
			d.config["ceph.osd.pool_name"],
			key,
			value,
			// Not all settings require this flag but we can set it nonetheless.
			"--yes-i-really-mean-it")
		return err
	}

	newSize, changed := changedConfig["ceph.osd.pool_size"]
	if changed {
		err := applyPool("size", newSize)
		if err != nil {
			return err
		}
	}

	newPgNum, changed := changedConfig["ceph.osd.pg_num"]
	if changed {
		err := applyPool("pg_num", newPgNum)
		if err != nil {
			return err
		}
	}

	return nil
}

// Mount mounts the storage pool.
func (d *ceph) Mount() (bool, error) {
	placeholderVol := d.getPlaceholderVolume()
	volExists, err := d.HasVolume(placeholderVol)
	if err != nil {
		return false, err
	}

	if !volExists {
		return false, errors.New("Placeholder volume does not exist")
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *ceph) Unmount() (bool, error) {
	// Nothing to do here.
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *ceph) GetResources() (*api.ResourcesStoragePool, error) {
	var stdout bytes.Buffer

	err := shared.RunCommandWithFds(context.TODO(), nil, &stdout,
		"ceph",
		"--name", "client."+d.config["ceph.user.name"],
		"--cluster", d.config["ceph.cluster_name"],
		"df",
		"-f", "json")
	if err != nil {
		return nil, err
	}

	// Temporary structs for parsing.
	type cephDfPoolStats struct {
		BytesUsed      int64 `json:"bytes_used"`
		BytesAvailable int64 `json:"max_avail"`
	}

	type cephDfPool struct {
		Name  string          `json:"name"`
		Stats cephDfPoolStats `json:"stats"`
	}

	type cephDf struct {
		Pools []cephDfPool `json:"pools"`
	}

	// Parse the JSON output.
	df := cephDf{}
	err = json.NewDecoder(&stdout).Decode(&df)
	if err != nil {
		return nil, err
	}

	var pool *cephDfPool
	for _, entry := range df.Pools {
		if entry.Name == d.config["ceph.osd.pool_name"] {
			pool = &entry
			break
		}
	}

	if pool == nil {
		return nil, errors.New("OSD pool missing in df output")
	}

	spaceUsed := uint64(pool.Stats.BytesUsed)
	spaceAvailable := uint64(pool.Stats.BytesAvailable)

	res := api.ResourcesStoragePool{}
	res.Space.Total = spaceAvailable + spaceUsed
	res.Space.Used = spaceUsed

	return &res, nil
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *ceph) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if refresh {
		if IsContentBlock(contentType) {
			return []migration.Type{
				{
					FSType:   migration.MigrationFSType_RBD_AND_RSYNC,
					Features: rsyncFeatures,
				},
				{
					FSType:   migration.MigrationFSType_BLOCK_AND_RSYNC,
					Features: rsyncFeatures,
				},
			}
		}

		return []migration.Type{
			{
				FSType:   migration.MigrationFSType_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	if IsContentBlock(contentType) {
		return []migration.Type{
			// Prefer to use RBD_AND_RSYNC for the initial migration.
			{
				FSType:   migration.MigrationFSType_RBD_AND_RSYNC,
				Features: rsyncFeatures,
			},
			// If RBD_AND_RSYNC is not supported by the target it will fall back to BLOCK_AND_RSYNC
			// as RBD wasn't sent as the preferred method by the source.
			// If the source sends RBD as the preferred method the target will accept RBD
			// as it's in the list of supported migration types.
			{
				FSType: migration.MigrationFSType_RBD,
			},
			{
				FSType:   migration.MigrationFSType_BLOCK_AND_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	return []migration.Type{
		{
			FSType: migration.MigrationFSType_RBD,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}
