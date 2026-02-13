package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

var cephobjectVersion string
var cephobjectLoaded bool

// cephobjectRadosgwAdminUser admin user in radosgw.
const cephobjectRadosgwAdminUser = "lxd-admin"

type cephobject struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *cephobject) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if cephobjectLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"radosgw-admin"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool %q is missing", tool)
		}
	}

	// Detect and record the version.
	if cephobjectVersion == "" {
		ver, err := radosgwVersion()
		if err != nil {
			return err
		}

		cephobjectVersion = ver
	}

	cephobjectLoaded = true

	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *cephobject) isRemote() bool {
	return true
}

// Info returns the pool driver information.
func (d *cephobject) Info() Info {
	return Info{
		Name:                     "cephobject",
		Version:                  cephobjectVersion,
		OptimizedImages:          false,
		PreservesInodes:          false,
		Remote:                   d.isRemote(),
		Buckets:                  true,
		VolumeTypes:              []VolumeType{},
		VolumeMultiNode:          false,
		BlockBacking:             false,
		RunningCopyFreeze:        false,
		DirectIO:                 false,
		MountedRoot:              false,
		PopulateParentVolumeUUID: false,
	}
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *cephobject) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-cephobject; group=pool-conf; key=cephobject.cluster_name)
		//
		// ---
		//  type: string
		//  shortdesc: The Ceph cluster to use
		//  scope: global
		"cephobject.cluster_name": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephobject; group=pool-conf; key=cephobject.user.name)
		//
		// ---
		//  type: string
		//  defaultdesc: `admin`
		//  shortdesc: The Ceph user to use
		//  scope: global
		"cephobject.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephobject; group=pool-conf; key=cephobject.radosgw.endpoint)
		//
		// ---
		//  type: string
		//  shortdesc: URL of the `radosgw` gateway process
		//  scope: global
		"cephobject.radosgw.endpoint": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-cephobject; group=pool-conf; key=cephobject.radosgw.endpoint_cert_file)
		// Specify the path to the file that contains the TLS client certificate.
		// ---
		//  type: string
		//  shortdesc: TLS client certificate to use for endpoint communication
		//  scope: global
		"cephobject.radosgw.endpoint_cert_file": validate.Optional(validate.IsAbsFilePath),
		// lxdmeta:generate(entities=storage-cephobject; group=pool-conf; key=cephobject.bucket.name_prefix)
		//
		// ---
		//  type: string
		//  shortdesc: Prefix to add to bucket names in Ceph
		//  scope: global
		"cephobject.bucket.name_prefix": validate.IsAny,
		// lxdmeta:generate(entities=storage-cephobject; group=pool-conf; key=volatile.pool.pristine)
		//
		// ---
		//  type: string
		//  defaultdesc: `true`
		//  shortdesc: Whether the `radosgw` `lxd-admin` user existed at creation time
		//  scope: global
		"volatile.pool.pristine": validate.Optional(validate.IsBool),
	}

	return d.validatePool(config, rules, nil)
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *cephobject) FillConfig() error {
	if d.config["cephobject.cluster_name"] == "" {
		d.config["cephobject.cluster_name"] = CephDefaultCluster
	}

	if d.config["cephobject.user.name"] == "" {
		d.config["cephobject.user.name"] = CephDefaultUser
	}

	return nil
}

// SourceIdentifier returns a string consisting of the RadosGW endpoint.
func (d *cephobject) SourceIdentifier() (string, error) {
	endpoint := d.config["cephobject.radosgw.endpoint"]
	if endpoint != "" {
		return endpoint, nil
	}

	return "", errors.New("Cannot derive identifier from empty endpoint")
}

// ValidateSource checks whether the required config keys are set to access the remote source.
func (d *cephobject) ValidateSource() error {
	if d.config["cephobject.radosgw.endpoint"] == "" {
		return errors.New(`"cephobject.radosgw.endpoint" option is required`)
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *cephobject) Create() error {
	// Check if there is an existing cephobjectRadosgwAdminUser user.
	adminUserInfo, _, err := d.radosgwadminGetUser(context.TODO(), cephobjectRadosgwAdminUser)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed getting admin user %q: %w", cephobjectRadosgwAdminUser, err)
	}

	// Create missing cephobjectRadosgwAdminUser user.
	if adminUserInfo == nil {
		_, err = d.radosgwadminUserAdd(context.TODO(), cephobjectRadosgwAdminUser, 0)
		if err != nil {
			return fmt.Errorf("Failed added admin user %q: %w", cephobjectRadosgwAdminUser, err)
		}

		d.config["volatile.pool.pristine"] = "true" // Remove admin user on pool delete.
	}

	return nil
}

// Delete clears any local and remote data related to this driver instance.
func (d *cephobject) Delete(op *operations.Operation) error {
	if shared.IsTrue(d.config["volatile.pool.pristine"]) {
		err := d.radosgwadminUserDelete(context.TODO(), cephobjectRadosgwAdminUser)
		if err != nil {
			return fmt.Errorf("Failed deleting admin user %q: %w", cephobjectRadosgwAdminUser, err)
		}
	}

	return nil
}

// Update applies any driver changes required from a configuration change.
func (d *cephobject) Update(changedConfig map[string]string) error {
	_, prefixChanged := changedConfig["cephobject.bucket.name_prefix"]
	if prefixChanged {
		buckets, err := d.radosgwadminBucketList(context.TODO())
		if err != nil {
			return err
		}

		for _, bucketName := range buckets {
			if strings.HasPrefix(bucketName, d.config["cephobject.bucket.name_prefix"]) {
				return errors.New(`Cannot change "cephobject.bucket.name_prefix" when there are existing buckets`)
			}
		}
	}

	return nil
}

// Mount brings up the driver and sets it up to be used.
func (d *cephobject) Mount() (bool, error) {
	return false, nil
}

// Unmount clears any of the runtime state of the driver.
func (d *cephobject) Unmount() (bool, error) {
	return false, nil
}

// GetResources returns the pool resource usage information.
func (d *cephobject) GetResources() (*api.ResourcesStoragePool, error) {
	return &api.ResourcesStoragePool{}, nil
}

// MigrationTypes returns the supported migration types and options supported by the driver.
func (d *cephobject) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	return nil
}
