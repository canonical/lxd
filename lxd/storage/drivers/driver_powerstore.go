package drivers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/drivers/powerstoreclient"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

// powerStoreDefaultUser represents the default PowerStore user name.
const powerStoreDefaultUser = "admin"

// powerStoreMinVolumeSizeBytes represents the minimal PowerStore volume size
// in bytes.
const powerStoreMinVolumeSizeBytes = 1 * 1024 * 1024 // 1MiB

// powerStoreMinVolumeSizeUnit represents the minimal PowerStore volume size
// expressed as unit string.
const powerStoreMinVolumeSizeUnit = "1MiB"

// powerStoreMaxVolumeSizeBytes represents the maximum PowerStore volume size
// in bytes.
const powerStoreMaxVolumeSizeBytes = 256 * 1024 * 1024 * 1024 * 1024 // 256TiB

// powerStoreMaxVolumeSizeUnit represents the maximum PowerStore volume size
// expressed as unit string.
const powerStoreMaxVolumeSizeUnit = "256TiB"

// powerStoreMinVolumeSizeAlignmentUnit represents the alignment unit for
// the PowerStore volume size (each volume size needs to be multiplicate of
// this value).
const powerStoreMinVolumeSizeAlignmentUnit = "1MiB"

const (
	powerStoreModeNVME  string = "nvme"
	powerStoreModeISCSI string = "iscsi"
)

const (
	powerStoreTransportTCP string = "tcp"
)

// powerStoreSupportedConnectorTypes list all connector types supported by
// the PowerStore driver.
var powerStoreSupportedModesAndTransports = driverModesAndTransports{
	{
		Mode:          powerStoreModeNVME,
		Transport:     powerStoreTransportTCP,
		ConnectorType: connectors.TypeNVME,
	},
	{
		Mode:          powerStoreModeISCSI,
		Transport:     powerStoreTransportTCP,
		ConnectorType: connectors.TypeISCSI,
	},
}

var powerStoreLoaded bool
var powerStoreVersion string

type powerstore struct {
	common

	// Holds the low level client for the PowerStore API.
	// Use powerstore.client() to retrieve the initialized client struct.
	apiClient *powerstoreclient.Client

	// Holds the low level connector for the PowerStore driver.
	// Use powerstore.connector() to retrieve the initialized connector.
	storageConnector connectors.Connector
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *powerstore) load() error {
	// Done if previously loaded.
	if powerStoreLoaded {
		return nil
	}

	versions := connectors.GetSupportedVersions(powerStoreSupportedModesAndTransports.ConnectorTypes())
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

// SourceIdentifier returns a combined string consisting of the gateway address and pool name.
func (d *powerstore) SourceIdentifier() (string, error) {
	gateway := d.config["powerstore.gateway"]
	if gateway == "" {
		return "", errors.New("Cannot derive identifier from empty gateway address")
	}

	return fmt.Sprintf("%s-%s", gateway, d.Name()), nil
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *powerstore) FillConfig() error {
	if d.config["powerstore.user.name"] == "" {
		d.config["powerstore.user.name"] = powerStoreDefaultUser
	}

	// Try to discover the PowerStore operation mode and transport.
	if d.config["powerstore.mode"] == "" || d.config["powerstore.transport"] == "" {
		discovered, err := discoverModeAndTransport(powerStoreSupportedModesAndTransports, d.config["powerstore.mode"], d.config["powerstore.transport"])
		if err != nil {
			return err
		}

		d.config["powerstore.mode"] = discovered.Mode
		d.config["powerstore.transport"] = discovered.Transport
	}

	// Set default volume size if not provided.
	if d.config["volume.size"] == "" {
		d.config["volume.size"] = d.defaultBlockVolumeSize()
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *powerstore) Create() error {
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
func (d *powerstore) Delete(op *operations.Operation) error {
	// If the user completely destroyed it, call it done.
	if !shared.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// On delete, wipe everything in the directory.
	return wipeDirectory(GetPoolMountPath(d.name))
}

// GetResources returns the pool resource usage information.
func (d *powerstore) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, ErrNotSupported
}

// commonVolumeRules returns validation rules which are common for pool and
// volume.
func (d *powerstore) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerstore; group=volume-conf; key=block.filesystem)
		// Valid options are: `btrfs`, `ext4`, `xfs`
		// If not set, `ext4` is assumed.
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.filesystem`
		//  shortdesc: File system of the storage volume
		//  scope: global
		"block.filesystem": validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		// lxdmeta:generate(entities=storage-powerstore; group=volume-conf; key=block.mount_options)
		//
		// ---
		//  type: string
		//  condition: block-based volume with content type `filesystem`
		//  defaultdesc: same as `volume.block.mount_options`
		//  shortdesc: Mount options for block-backed file system volumes
		//  scope: global
		"block.mount_options": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerstore; group=volume-conf; key=size)
		// The size must be in multiples of 1 MiB. The minimum size is 1 MiB and maximum is 256 TiB.
		// ---
		//  type: string
		//  defaultdesc: same as `volume.size`
		//  shortdesc: Size/quota of the storage volume
		//  scope: global
		"size": validate.Optional(
			validate.IsNoLessThanUnit(powerStoreMinVolumeSizeUnit),
			validate.IsNoGreaterThanUnit(powerStoreMaxVolumeSizeUnit),
			validate.IsMultipleOfUnit(powerStoreMinVolumeSizeAlignmentUnit),
		),
	}
}

// Validate checks that all provided keys are supported and that no conflicting or missing configuration is present.
func (d *powerstore) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.user.name)
		//
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
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.discovery)
		// A comma-separated list of NVMe or iSCSI discovery addresses.
		// ---
		//  type: string
		//  defaultdesc: the list of discovery addresses
		//  shortdesc: List of discovery addresses.
		"powerstore.discovery": validate.Optional(validate.IsListOf(validate.IsNetworkAddressWithOptionalPort)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.target)
		// A comma-separated list of NVMe or iSCSI target addresses. When empty targets are discovered via discovery endpoints.
		// ---
		//  type: string
		//  defaultdesc: the list of target addresses
		//  shortdesc: List of target addresses.
		"powerstore.target": validate.Optional(validate.IsListOf(validate.IsNetworkAddressWithOptionalPort)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.mode)
		// The mode gets discovered automatically if the system provides the necessary kernel modules.
		// Supported values are `iscsi` and `nvme`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		//  scope: global
		"powerstore.mode": validate.Optional(validate.IsOneOf(powerStoreSupportedModesAndTransports.Modes()...)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.transport)
		// The transport gets discovered automatically if the system provides the necessary kernel modules.
		// Supported values are `tcp`.
		// ---
		//  type: string
		//  defaultdesc: the discovered transport
		//  shortdesc: Transport layer used when transferring volumes data to the local server.
		//  scope: global
		"powerstore.transport": validate.Optional(validate.IsOneOf(powerStoreSupportedModesAndTransports.Transports()...)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=volume.size)
		// The size must be in multiples of 1 MiB. The minimum size is 1 MiB and maximum is 256 TiB.
		// ---
		//  type: string
		//  defaultdesc: `10GiB`
		//  shortdesc: Size/quota of the storage volume
		//  scope: global
		"volume.size": validate.Optional(
			validate.IsNoLessThanUnit(powerStoreMinVolumeSizeUnit),
			validate.IsNoGreaterThanUnit(powerStoreMaxVolumeSizeUnit),
			validate.IsMultipleOfUnit(powerStoreMinVolumeSizeAlignmentUnit),
		),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	// Ensure powerstore.mode cannot be changed to avoid leaving volume mappings
	// and to prevent disturbing running instances.
	newMode, oldMode := config["powerstore.mode"], d.config["powerstore.mode"]
	if oldMode != "" && oldMode != newMode {
		return errors.New("PowerStore mode cannot be changed")
	}

	// Ensure powerstore.transport cannot be changed to avoid leaving volume
	// mappings and to prevent disturbing running instances.
	newTransport, oldTransport := config["powerstore.transport"], d.config["powerstore.transport"]
	if oldTransport != "" && oldTransport != newTransport {
		return errors.New("PowerStore transport cannot be changed")
	}

	// Check if the selected PowerStore mode and transport is supported on this
	// node. Also when forming the storage pool on a LXD cluster, the mode
	// that got discovered on the creating machine needs to be validated
	// on the other cluster members too. This can be done here since Validate
	// gets executed on every cluster member when receiving the cluster
	// notification to finally create the pool.
	if newMode != "" && newTransport != "" {
		mt, err := powerStoreSupportedModesAndTransports.Find(newMode, newTransport)
		if err != nil {
			return err
		}

		connector, err := connectors.NewConnector(mt.ConnectorType, "")
		if err != nil {
			return fmt.Errorf("PowerStore mode %q with transport %q is not supported: %w", newMode, newTransport, err)
		}

		err = connector.LoadModules()
		if err != nil {
			return fmt.Errorf("PowerStore mode %q with transport %q is not supported due to missing kernel modules: %w", newMode, newTransport, err)
		}
	}

	return nil
}

// ValidateSource checks whether the required config keys are set to access the remote source.
func (d *powerstore) ValidateSource() error {
	if d.config["powerstore.gateway"] == "" {
		return errors.New("The powerstore.gateway cannot be empty")
	}

	return nil
}

// Update applies any driver changes required from a configuration change.
func (d *powerstore) Update(changedConfig map[string]string) error {
	return nil
}
