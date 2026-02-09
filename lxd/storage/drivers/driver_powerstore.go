package drivers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

// powerStoreDefaultUser represents the default PowerStore user name.
const powerStoreDefaultUser = "admin"

// powerStoreDefaultVolumeSize represents the default PowerStore volume size.
const powerStoreDefaultVolumeSize = "10GiB"

// powerStoreMinVolumeSizeBytes represents the minimal PowerStore volume size in bytes.
const powerStoreMinVolumeSizeBytes = 1 * 1024 * 1024 // 1MiB

// powerStoreMaxVolumeSizeBytes represents the maximum PowerStore volume size in bytes.
const powerStoreMaxVolumeSizeBytes = 256 * 1000 * 1000 * 1000 * 1000 // 256TB

var powerstoreSupportedConnectors = []string{
	connectors.TypeISCSI,
	connectors.TypeNVME,
}

var powerStoreLoaded bool
var powerStoreVersion string

type powerstore struct {
	common

	// Holds the low level connector for the PowerStore driver.
	// Use powerstore.connector() to retrieve the initialized connector.
	storageConnector connectors.Connector

	// Holds the low level HTTP client for the PowerStore API.
	// Use powerstore.client() to retrieve the initialized client struct.
	httpClient *powerStoreClient
}

// client returns the PowerStore API client.
// A new client gets created if one does not exists.
func (d *powerstore) client() *powerStoreClient {
	if d.httpClient == nil {
		d.httpClient = newPowerStoreClient(d)
	}
	return d.httpClient
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *powerstore) load() error {
	// Done if previously loaded.
	if powerStoreLoaded {
		return nil
	}

	versions := connectors.GetSupportedVersions(powerstoreSupportedConnectors)
	powerStoreVersion = strings.Join(versions, " / ")
	powerStoreLoaded = true

	// Load the kernel modules of the respective connector, ignoring those that cannot be loaded.
	// Support for a specific connector is checked during pool creation. However, this
	// ensures that the kernel modules are loaded, even if the host has been rebooted.
	connector, err := d.connector()
	if err == nil {
		_ = connector.LoadModules()
	}

	return nil
}

// connector retrieves an initialized storage connector based on the configured
// PowerStore mode. The connector is cached in the driver struct.
func (d *powerstore) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		connector, err := connectors.NewConnector(d.config["powerstore.mode"], d.state.OS.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
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
		OptimizedImages:              false,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeVM, VolumeTypeContainer, VolumeTypeImage},
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  false,
		PopulateParentVolumeUUID:     false,
		UUIDVolumeNames:              true,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *powerstore) FillConfig() error {
	if d.config["powerstore.user.name"] == "" {
		d.config["powerstore.user.name"] = powerStoreDefaultUser
	}

	// Try to discover the PowerStore operation mode.
	if d.config["powerstore.mode"] == "" {
		// Create temporary connector to check if NVMe/TCP kernel modules can be loaded.
		nvmeConnector, err := connectors.NewConnector(connectors.TypeNVME, "")
		if err != nil {
			return err
		}

		// Create temporary connector to check if ISCSI/TCP kernel modules can be loaded.
		iscsiConnector, err := connectors.NewConnector(connectors.TypeISCSI, "")
		if err != nil {
			return err
		}

		if nvmeConnector.LoadModules() == nil {
			d.config["powerstore.mode"] = connectors.TypeNVME
		} else if iscsiConnector.LoadModules() == nil {
			d.config["powerstore.mode"] = connectors.TypeISCSI
		} else {
			// Fail if no PowerStore mode can be discovered.
			return errors.New("Failed to discover PowerStore mode")
		}
	}

	// PowerStore set default volume size if not provided.
	if d.config["volume.size"] == "" {
		d.config["volume.size"] = powerStoreDefaultVolumeSize
	}

	return nil
}

// Validate checks that all provided keys are supported and that no conflicting or missing configuration is present.
func (d *powerstore) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.user.name)
		// Must have at least SystemAdmin role to give LXD full control over managed storage pools.
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
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.pool)
		// If you want to specify the storage pool via its name, also set {config:option}`storage-powerstore-pool-conf:powerstore.domain`.
		// ---
		//  type: string
		//  shortdesc: ID of the PowerStore storage pool
		//  scope: global
		"powerstore.pool": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.domain)
		// This option is required only if {config:option}`storage-powerstore-pool-conf:powerstore.pool` is specified using its name.
		// ---
		//  type: string
		//  shortdesc: Name of the PowerStore protection domain
		//  scope: global
		"powerstore.domain": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.mode)
		// The mode gets discovered automatically if the system provides the necessary kernel modules.
		// Supported values are `iscsi` and `nvme`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		//  scope: global
		"powerstore.mode": validate.Optional(validate.IsOneOf(powerstoreSupportedConnectors...)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=volume.size)
		// The size must be in multiples of 1 MiB. The minimum size is 1 MiB and maximum is 256 TB.
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
	// and to prevent disturbing running instances.
	if oldMode != "" && oldMode != newMode {
		return errors.New("PowerStore mode cannot be changed")
	}

	// Check if the selected PowerStore mode is supported on this node.
	// Also when forming the storage pool on a LXD cluster, the mode
	// that got discovered on the creating machine needs to be validated
	// on the other cluster members too. This can be done here since Validate
	// gets executed on every cluster member when receiving the cluster
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

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *powerstore) Create() error {
	return ErrNotSupported
}

// Delete removes the storage pool from the storage device.
func (d *powerstore) Delete(op *operations.Operation) error {
	return ErrNotSupported
}

// Update applies any driver changes required from a configuration change.
func (d *powerstore) Update(changedConfig map[string]string) error {
	return ErrNotSupported
}

// Mount mounts the storage pool.
func (d *powerstore) Mount() (bool, error) {
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *powerstore) Unmount() (bool, error) {
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *powerstore) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, ErrNotSupported
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *powerstore) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	return nil
}
