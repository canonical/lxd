package drivers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/drivers/clients"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/validate"
)

// powerStoreLoaded indicates whether load() function was already called for the PowerStore driver.
var powerStoreLoaded bool

// powerStoreVersion holds the version of the PowerStore system.
var powerStoreVersion string

// powerStoreSupportedConnectors represents a list of storage connectors that can be used with PowerStore.
var powerStoreSupportedConnectors = []string{
	connectors.TypeISCSI,
}

// powerStoreDefaultUser represents the default PowerStore user name.
const powerStoreDefaultUser = "admin"

// Common prefix for resource names in PowerStore.
const powerStoreResourcePrefix = "lxd-"

type powerstore struct {
	common

	// Holds the low level client for the PowerStore API.
	// Use [powerstore.client] to retrieve the initialized client struct.
	httpClient *clients.PowerStoreClient

	// Holds the low level connector for the PowerStore driver.
	// Use [powerstore.connector] to retrieve the initialized connector.
	storageConnector connectors.Connector
}

// load initializes the PowerStore driver.
func (d *powerstore) load() error {
	// Done if previously loaded.
	if powerStoreLoaded {
		return nil
	}

	powerStoreLoaded = true
	return nil
}

// connector retrieves an initialized storage connector based on the configured
// PowerStore mode. The connector is cached in the driver struct.
func (d *powerstore) connector() (connectors.Connector, error) {
	return d.storageConnector, nil
}

// client returns the PowerStore API client.
func (d *powerstore) client() *clients.PowerStoreClient {
	return d.httpClient
}

// isRemote returns true indicating this driver uses remote storage.
func (d *powerstore) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *powerstore) Info() Info {
	return Info{
		Name:                         "powerstore",
		Version:                      powerStoreVersion,
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              true,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeVM, VolumeTypeContainer, VolumeTypeImage},
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  false,
		PopulateParentVolumeUUID:     true,
		UUIDVolumeNames:              true,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *powerstore) FillConfig() error {
	if d.config["powerstore.user.name"] == "" {
		d.config["powerstore.user.name"] = powerStoreDefaultUser
	}

	return nil
}

// Validate checks that all provided keys are supported and that no conflicting or missing configuration is present.
func (d *powerstore) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.user.name)
		// Name of the PowerStore user with an admin role that gives LXD full control over managed storage pools.
		// ---
		//  type: string
		//  defaultdesc: `admin`
		//  shortdesc: User for PowerStore Gateway authentication
		//  scope: global
		"powerstore.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.user.password)
		//
		// ---
		//  type: string
		//  shortdesc: Password for PowerStore Gateway authentication
		//  scope: global
		"powerstore.user.password": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.gateway)
		//
		// ---
		//  type: string
		//  shortdesc: Address of the PowerStore Gateway
		//  scope: global
		"powerstore.gateway": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.gateway.verify)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to verify the PowerStore Gateway's certificate
		//  scope: global
		"powerstore.gateway.verify": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.mode)
		// The mode to use to map PowerStore volumes to the local server.
		// Supported value is `iscsi`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		//  scope: global
		"powerstore.mode": validate.Optional(validate.IsOneOf(powerStoreSupportedConnectors...)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.target)
		// A comma-separated list of target addresses. If empty, LXD discovers and connects to all available targets. Otherwise, it only connects to the specified addresses.
		// ---
		//  type: string
		//  defaultdesc: target addresses
		//  shortdesc: List of target addresses the LXD connects to.
		"powerstore.target": validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=volume.size)
		// The size must be in multiples of 1 MiB. The minimum size is 1 MiB and maximum is 256 TiB.
		// ---
		//  type: string
		//  defaultdesc: `10GiB`
		//  shortdesc: Size/quota of the storage volume
		//  scope: global
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("1MiB")),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	newMode := config["powerstore.mode"]
	oldMode := d.config["powerstore.mode"]

	// Ensure powerstore.mode cannot be changed to avoid leaving volume mappings
	// and prevent disturbing running instances.
	if oldMode != "" && oldMode != newMode {
		return errors.New("PowerStore mode cannot be changed")
	}

	// Check if the selected PowerStore mode is supported on this node.
	// Also when forming the storage pool on a LXD cluster, the mode that got discovered on this
	// host needs to be validated on the other cluster members as well. This can be done here
	// since Validate gets executed on every cluster member when receiving the cluster
	// notification to finally create the pool.
	if newMode != "" {
		connector, err := connectors.NewConnector(newMode, "")
		if err != nil {
			return fmt.Errorf("PowerStore mode %q is not supported: %w", newMode, err)
		}

		err = connector.LoadModules()
		if err != nil {
			return fmt.Errorf("PowerStore mode %q is not supported due to missing kernel modules: %w", newMode, err)
		}
	}

	return nil
}

// SourceIdentifier returns a combined string consisting of the gateway address and pool name.
func (d *powerstore) SourceIdentifier() (string, error) {
	gateway := d.config["powerstore.gateway"]
	if gateway == "" {
		return "", errors.New("Cannot derive identifier from empty gateway address")
	}

	if d.name == "" {
		return "", errors.New("Cannot derive identifier from empty pool name")
	}

	return gateway + "-" + d.name, nil
}

// ValidateSource checks whether the required config keys are set to access the remote source.
func (d *powerstore) ValidateSource() error {
	if d.config["powerstore.gateway"] == "" {
		return errors.New("The powerstore.gateway cannot be empty")
	}

	if d.config["powerstore.user.name"] == "" {
		return errors.New("The powerstore.user.name cannot be empty")
	}

	if d.config["powerstore.user.password"] == "" {
		return errors.New("The powerstore.user.password cannot be empty")
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *powerstore) Create() error {
	return nil
}

// Update applies any driver changes required from a configuration change.
func (d *powerstore) Update(changedConfig map[string]string) error {
	return nil
}

// Mount mounts the storage pool.
func (d *powerstore) Mount() (bool, error) {
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *powerstore) Unmount() (bool, error) {
	return true, nil
}

// Delete removes the storage pool from the storage device.
func (d *powerstore) Delete(progressReporter ioprogress.ProgressReporter) error {
	return wipeDirectory(GetPoolMountPath(d.name))
}

// GetResources returns the pool resource usage information.
func (d *powerstore) GetResources() (*api.ResourcesStoragePool, error) {
	res := &api.ResourcesStoragePool{}
	return res, nil
}

// storagePoolScopePrefix returns the prefix used to scope PowerStore resource
// names to an LXD storage pool. This prevents conflicts in PowerStore with
// resources created for other LXD storage pools or at the root level.
func (d *powerstore) storagePoolScopePrefix(poolName string) string {
	poolID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(poolName))
	prefix := strings.ReplaceAll(poolID.String(), "-", "")
	return powerStoreResourcePrefix + prefix + "-"
}
