package drivers

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

// powerStoreDefaultUser represents the default PowerStore user name.
const powerStoreDefaultUser = "admin"

// powerStoreMinVolumeSizeBytes represents the minimal PowerStore volume size in bytes.
const powerStoreMinVolumeSizeBytes = 1 * 1024 * 1024 // 1MiB

// powerStoreMinVolumeSizeUnit represents the minimal PowerStore volume size expressed as unit string.
const powerStoreMinVolumeSizeUnit = "1MiB"

// powerStoreMaxVolumeSizeBytes represents the maximum PowerStore volume size in bytes.
const powerStoreMaxVolumeSizeBytes = 256 * 1024 * 1024 * 1024 * 1024 // 256TiB

// powerStoreMaxVolumeSizeUnit represents the maximum PowerStore volume size expressed as unit string.
const powerStoreMaxVolumeSizeUnit = "256TiB"

// powerStoreMinVolumeSizeAlignmentUnit represents the alignment unit for the PowerStore volume size (each volume size needs to be multiplicate of this value).
const powerStoreMinVolumeSizeAlignmentUnit = "1MiB"

// powerStoreSupportedConnectors list all connectors supported by the PowerStore driver.
var powerStoreSupportedConnectors = []string{
	connectors.TypeNVME,
	connectors.TypeISCSI,
}

// powerStoreSupportedModes list all mods supported by the PowerStore driver.
var powerStoreSupportedModes = []string{
	connectors.TypeNVME,
	connectors.TypeISCSI,
}

// powerStoreSupportedTransports list all transports supported by the PowerStore driver.
var powerStoreSupportedTransports = []string{
	"tcp",
}

// powerStoreModeAndTransportToConnectorType maps mode and transport of the PowerStore driver to matching connector type.
var powerStoreModeAndTransportToConnectorType = map[string]map[string]string{
	connectors.TypeNVME: {
		"tcp": connectors.TypeNVME,
	},
	connectors.TypeISCSI: {
		"tcp": connectors.TypeISCSI,
	},
}

var powerStoreLoaded bool
var powerStoreVersion string

type powerstore struct {
	common

	// Holds the low level HTTP client for the PowerStore API.
	// Use powerstore.client() to retrieve the initialized client struct.
	httpClient *powerStoreClient

	// Holds the low level connector for the PowerStore driver.
	// Use powerstore.connector() to retrieve the initialized connector.
	storageConnector connectors.Connector

	// Derived initiator resource associated with current host, mode and transport
	// Use powerstore.initiator() to retrieve the initialized initiator resource.
	initiatorResource *powerStoreHostInitiatorResource

	// Discovered target QN (qualified name)
	// Use powerstore.target() to retrieve the discovered PowerStore target and associated addresses.
	targetQualifiedName string
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

	versions := connectors.GetSupportedVersions(powerStoreSupportedConnectors)
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

// target return qualified name of a discovered PowerStore target and associated addresses.
func (d *powerstore) target() (string, []string, error) {
	targetAddress := shared.SplitNTrimSpace(d.config["powerstore.target"], ",", -1, true)
	if d.targetQualifiedName == "" {
		discovered, err := d.discoverTargetQualifiedName(targetAddress)
		if err != nil {
			return "", nil, err
		}
		d.targetQualifiedName = discovered
	}
	return d.targetQualifiedName, targetAddress, nil
}

// discoverTargetQualifiedName discovers target QN (qualified name).
func (d *powerstore) discoverTargetQualifiedName(targetAddress []string) (string, error) {
	connector, err := d.connector()
	if err != nil {
		return "", err
	}

	discoveryLogRecords, err := connector.Discover(d.state.ShutdownCtx, targetAddress...)
	if err != nil {
		return "", fmt.Errorf("discovering targets: %w", err)
	}

	for _, discoveryLogRecord := range discoveryLogRecords {
		switch record := discoveryLogRecord.(type) {
		case connectors.ISCSIDiscoveryLogRecord:
			return record.IQN, nil

		case connectors.NVMeDiscoveryLogRecord:
			if record.SubType != connectors.SubtypeNVMESubsys {
				continue
			}
			return record.SubNQN, nil

		default:
			return "", fmt.Errorf("unsupported discovery log record entry type %T", discoveryLogRecord)
		}
	}
	return "", fmt.Errorf("no discovery log record entries available for target addresses %q", targetAddress)
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

	// Try to discover the PowerStore operation mode and transport.
	switch {
	case d.config["powerstore.mode"] == "" || d.config["powerstore.transport"] == "":
		mode, transport, err := d.discoverModeAndTransport()
		if err != nil {
			return err
		}
		d.config["powerstore.mode"] = mode
		d.config["powerstore.transport"] = transport

	case d.config["powerstore.mode"] == "":
		mode, err := d.discoverMode(d.config["powerstore.transport"])
		if err != nil {
			return err
		}
		d.config["powerstore.mode"] = mode

	case d.config["powerstore.transport"] == "":
		transport, err := d.discoverTransport(d.config["powerstore.mode"])
		if err != nil {
			return err
		}
		d.config["powerstore.transport"] = transport
	}

	// Set default volume size if not provided.
	if d.config["volume.size"] == "" {
		d.config["volume.size"] = d.defaultBlockVolumeSize()
	}

	return nil
}

// discoverModeAndTransport attempts to discover operation mode and transport without using the storage pool's configuration.
func (d *powerstore) discoverModeAndTransport() (mode string, transport string, err error) {
	for _, transport = range powerStoreSupportedTransports {
		for _, mode = range powerStoreSupportedModes {
			connectorType := powerStoreModeAndTransportToConnectorType[mode][transport]
			connector, err := connectors.NewConnector(connectorType, "")
			if err != nil {
				return "", "", err
			}
			if connector.LoadModules() == nil {
				return mode, transport, nil
			}
		}
	}
	return "", "", errors.New("failed to discover PowerStore mode and transport")
}

// discoverMode attempts to discover operation mode for the provided transport without using the storage pool's configuration.
func (d *powerstore) discoverMode(transport string) (mode string, err error) {
	if !slices.Contains(powerStoreSupportedTransports, transport) {
		return "", fmt.Errorf("unsupported PowerStore transport %q", transport)
	}
	for _, mode := range powerStoreSupportedModes {
		connectorType := powerStoreModeAndTransportToConnectorType[mode][transport]
		connector, err := connectors.NewConnector(connectorType, "")
		if err != nil {
			return "", err
		}
		if connector.LoadModules() == nil {
			return mode, nil
		}
	}
	return "", errors.New("failed to discover PowerStore mode")
}

// discoverTransport attempts to discover operation transport for the provided mode without using the storage pool's configuration.
func (d *powerstore) discoverTransport(mode string) (transport string, err error) {
	if !slices.Contains(powerStoreSupportedModes, mode) {
		return "", fmt.Errorf("unsupported PowerStore mode %q", mode)
	}
	for _, transport := range powerStoreSupportedTransports {
		connectorType := powerStoreModeAndTransportToConnectorType[mode][transport]
		connector, err := connectors.NewConnector(connectorType, "")
		if err != nil {
			return "", err
		}
		if connector.LoadModules() == nil {
			return transport, nil
		}
	}
	return "", errors.New("failed to discover PowerStore transport")
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
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.mode)
		// The mode gets discovered automatically if the system provides the necessary kernel modules.
		// Supported values are `iscsi` and `nvme`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		//  scope: global
		"powerstore.mode": validate.Optional(validate.IsOneOf(powerStoreSupportedConnectors...)),
		// lxdmeta:generate(entities=storage-powerstore; group=pool-conf; key=powerstore.transport)
		// The transport gets discovered automatically if the system provides the necessary kernel modules.
		// Supported values are `tcp`.
		// ---
		//  type: string
		//  defaultdesc: the discovered transport
		//  shortdesc: Transport layer used when transferring volumes data to the local server.
		//  scope: global
		"powerstore.transport": validate.Optional(validate.IsOneOf(powerStoreSupportedTransports...)),
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

	newMode, oldMode := config["powerstore.mode"], d.config["powerstore.mode"]
	newTransport, oldTransport := config["powerstore.transport"], d.config["powerstore.transport"]

	// Ensure powerstore.mode and powerstore.transport cannot be changed to avoid
	// leaving volume mappings and to prevent disturbing running instances.
	if oldMode != "" && oldMode != newMode {
		return errors.New("PowerStore mode cannot be changed")
	}
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
		connectorType := powerStoreModeAndTransportToConnectorType[newMode][newTransport]
		connector, err := connectors.NewConnector(connectorType, "")
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

// SourceIdentifier returns a combined string consisting of the gateway address and pool name.
func (d *powerstore) SourceIdentifier() (string, error) {
	gateway := d.config["powerstore.gateway"]
	if gateway == "" {
		return "", errors.New("Cannot derive identifier from empty gateway address")
	}

	return fmt.Sprintf("%s-%s", gateway, d.Name()), nil
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
