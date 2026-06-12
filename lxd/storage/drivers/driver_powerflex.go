package drivers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

// powerFlexDefaultUser represents the default PowerFlex user name.
const powerFlexDefaultUser = "admin"

// powerFlex4DefaultSize represents the default PowerFlex volume size for PowerFlex 4.
const powerFlex4DefaultSize = "8GiB"

// powerFlex5DefaultSize represents the default PowerFlex volume size for PowerFlex 5.
const powerFlex5DefaultSize = "1GiB"

// powerFlex4MinVolumeSizeBytes represents the minimal PowerFlex volume size in bytes for PowerFlex 4.
// This translates to 8 GiB.
const powerFlex4MinVolumeSizeBytes = 8589934592

// powerFlex5MinVolumeSizeBytes represents the minimal PowerFlex volume size in bytes for PowerFlex 5.
// This translates to 1 GiB.
const powerFlex5MinVolumeSizeBytes = 1073741824

var powerflexSupportedConnectors = []string{
	connectors.TypeNVMeTCP,
	connectors.TypeSDC,
}

var powerFlexLoaded bool
var powerFlexVersion string

type powerflex struct {
	common

	// Holds the low level connector for the PowerFlex driver.
	// Use powerflex.connector() to retrieve the initialized connector.
	storageConnector connectors.Connector

	// Holds the low level HTTP client for the PowerFlex API.
	// Use powerflex.client() to retrieve the client struct.
	httpClient *powerFlexClient

	// Holds the SDC GUID of this specific host.
	// Use powerflex.getHostGUID() to retrieve the actual value.
	sdcGUID string

	// Holds the targetQN used by the SDTs.
	nvmeTargetQN string
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *powerflex) load() error {
	// Done if previously loaded.
	if powerFlexLoaded {
		return nil
	}

	versions := connectors.GetSupportedVersions(powerflexSupportedConnectors)
	powerFlexVersion = strings.Join(versions, " / ")
	powerFlexLoaded = true

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
// PowerFlex mode. The connector is cached in the driver struct.
func (d *powerflex) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		connector, err := connectors.NewConnector(d.config["powerflex.mode"], d.state.OS.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *powerflex) isRemote() bool {
	return true
}

// hasThinCloneSupport returns true if the PowerFlex system version supports thin clones.
// This is true for all PowerFlex version starting with 5.0.
func (d *powerflex) hasThinCloneSupport() bool {
	powerFlexVersion, err := version.NewDottedVersion(d.config["volatile.powerflex.version"])
	if err != nil {
		return false
	}

	thinCloneSupportVersion, err := version.NewDottedVersion("5.0")
	if err != nil {
		return false
	}

	return powerFlexVersion.Compare(thinCloneSupportVersion) >= 0
}

// Info returns info about the driver and its environment.
func (d *powerflex) Info() Info {
	return Info{
		Name:                         "powerflex",
		Version:                      powerFlexVersion,
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
		// If parent volume UUID is present we know a snapshot [Volume] is an actual snapshot.
		// In PowerFlex 5 when creating a thin clone of a snapshot, we unset the parent volume UUID to differentiate between snapshots and thin clones.
		PopulateParentVolumeUUID: true,
		UUIDVolumeNames:          true,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *powerflex) FillConfig() error {
	if d.config["powerflex.user.name"] == "" {
		d.config["powerflex.user.name"] = powerFlexDefaultUser
	}

	// Try to discover the PowerFlex operation mode.
	// First try if the NVMe/TCP kernel modules can be loaded.
	// Second try if the SDC kernel module is setup.
	if d.config["powerflex.mode"] == "" {
		// Create temporary connector to check if NVMe/TCP kernel modules can be loaded.
		nvmeConnector, err := connectors.NewConnector(connectors.TypeNVMeTCP, "")
		if err != nil {
			return err
		}

		// Create temporary connector to check if SDC kernel module is loaded.
		sdcConnector, err := connectors.NewConnector(connectors.TypeSDC, "")
		if err != nil {
			return err
		}

		if nvmeConnector.LoadModules() == nil {
			d.config["powerflex.mode"] = connectors.TypeNVMeTCP
		} else if sdcConnector.LoadModules() == nil {
			d.config["powerflex.mode"] = connectors.TypeSDC
		} else {
			// Fail if no PowerFlex mode can be discovered.
			return errors.New("Failed discovering PowerFlex mode")
		}
	}

	// Retrieve and store the PowerFlex system version.
	version, err := d.client().getVersion()
	if err != nil {
		return err
	}

	userProvidedVersion := d.config["volatile.powerflex.version"]

	// Exit if there already is a different PowerFlex version set by the user as this field gets auto-populated.
	// This ensures we don't overwrite a user-provided value.
	// We have to allow setting it to support creating pools in a cluster which will trigger the creation of each member
	// using the config which is provided in the final pool create call.
	if userProvidedVersion != "" && userProvidedVersion != version {
		return errors.New(`Cannot set "volatile.powerflex.version" manually`)
	}

	d.config["volatile.powerflex.version"] = version
	return nil
}

// SourceIdentifier returns a combined string consisting of the gateway address, protection domain and pool name.
// One PowerFlex cluster can have multiple protection domains with the same pool name.
// Therefore the identifier also contains the name of the protection domain.
func (d *powerflex) SourceIdentifier() (string, error) {
	gateway := d.config["powerflex.gateway"]
	if gateway == "" {
		return "", errors.New("Cannot derive identifier from empty gateway address")
	}

	// When creating the pool you can either specify powerflex.pool using the pool's ID
	// or by setting powerflex.domain and powerflex.pool to their names to allow the lookup.
	// In case of the latter we have to first resolve the pool.
	pool, err := d.resolvePool()
	if err != nil {
		return "", err
	}

	domain, err := d.client().getProtectionDomain(pool.ProtectionDomainID)
	if err != nil {
		return "", err
	}

	return strings.Join([]string{gateway, domain.Name, pool.Name}, "-"), nil
}

// ValidateSource checks whether the required config keys are set to access the remote source.
func (d *powerflex) ValidateSource() error {
	// Validate both pool and gateway here and return an error if they are not set.
	// Since those aren't any cluster member specific keys the general validation
	// rules allow empty strings in order to create the pending storage pools.
	if d.config["powerflex.pool"] == "" {
		return errors.New("The powerflex.pool cannot be empty")
	}

	if d.config["powerflex.gateway"] == "" {
		return errors.New("The powerflex.gateway cannot be empty")
	}

	if d.config["powerflex.mode"] == connectors.TypeSDC {
		// In case the SDC mode is used the SDTs cannot be set.
		if d.config["powerflex.sdt"] != "" {
			return fmt.Errorf("The %q config key is specific to the %q mode", "powerflex.sdt", connectors.TypeNVMeTCP)
		}
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *powerflex) Create() error {
	return nil
}

// Delete removes the storage pool from the storage device.
func (d *powerflex) Delete(progressReporter ioprogress.ProgressReporter) error {
	// On delete, wipe everything in the directory.
	return wipeDirectory(GetPoolMountPath(d.name))
}

// Validate checks that all provided keys are supported and that no conflicting or missing configuration is present.
func (d *powerflex) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.user.name)
		// Must have at least SystemAdmin role to give LXD full control over managed storage pools.
		// ---
		//  type: string
		//  defaultdesc: `admin`
		//  shortdesc: User for PowerFlex Gateway authentication
		//  scope: global
		"powerflex.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.user.password)
		//
		// ---
		//  type: string
		//  shortdesc: Password for PowerFlex Gateway authentication
		//  scope: global
		"powerflex.user.password": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.gateway)
		//
		// ---
		//  type: string
		//  shortdesc: Address of the PowerFlex Gateway
		//  scope: global
		"powerflex.gateway": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.gateway.verify)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to verify the PowerFlex Gateway's certificate
		//  scope: global
		"powerflex.gateway.verify": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.pool)
		// If you want to specify the storage pool via its name, also set {config:option}`storage-powerflex-pool-conf:powerflex.domain`.
		// ---
		//  type: string
		//  shortdesc: ID of the PowerFlex storage pool
		//  scope: global
		"powerflex.pool": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.domain)
		// This option is required only if {config:option}`storage-powerflex-pool-conf:powerflex.pool` is specified using its name.
		// ---
		//  type: string
		//  shortdesc: Name of the PowerFlex protection domain
		//  scope: global
		"powerflex.domain": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.mode)
		// The mode gets discovered automatically if the system provides the necessary kernel modules.
		// This can be either `nvme/tcp` or `sdc`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		//  scope: global
		"powerflex.mode": validate.Optional(validate.IsOneOf(connectors.TypeNVMeTCP, connectors.TypeSDC)),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.sdt)
		//
		// ---
		//  type: string
		//  shortdesc: Comma separated list of PowerFlex NVMe/TCP SDTs
		//  scope: global
		"powerflex.sdt": validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.snapshot_copy)
		// If this option is set to `true`, PowerFlex makes a sparse snapshot when copying an instance or custom volume.
		// See {ref}`storage-powerflex-limitations` for more information.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether to use sparse snapshots for copies
		//  scope: global
		"powerflex.snapshot_copy": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=volume.size)
		// The size must be in multiples of 8 GiB for PowerFlex 4.
		// Starting with PowerFlex 5, the size can be in multiples of 1 GiB.
		// See {ref}`storage-powerflex-limitations` for more information.
		// ---
		//  type: string
		//  shortdesc: Size/quota of the storage volume
		//  scope: global
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("1GiB")),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=volatile.powerflex.version)
		// This field is automatically populated after querying the PowerFlex version.
		// It cannot be set by the user.
		// ---
		//  type: string
		//  defaultdesc: Discovered version
		//  shortdesc: Software version of the PowerFlex array.
		//  scope: global
		"volatile.powerflex.version": validate.Optional(validate.IsDottedVersion),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	// Ensure powerflex.mode cannot be changed to avoid leaving volume mappings
	// and to prevent disturbing running instances.
	// Ensure volatile.powerflex.version cannot be changed.
	immutableKeys := []string{"powerflex.mode", "volatile.powerflex.version"}
	for _, key := range immutableKeys {
		newVal := config[key]
		oldVal := d.config[key]

		if oldVal != "" && oldVal != newVal {
			return fmt.Errorf("%q cannot be changed", key)
		}
	}

	newMode := config["powerflex.mode"]

	// Check if the selected PowerFlex mode is supported on this node.
	// Also when forming the storage pool on a LXD cluster, the mode
	// that got discovered on the creating machine needs to be validated
	// on the other cluster members too. This can be done here since Validate
	// gets executed on every cluster member when receiving the cluster
	// notification to finally create the pool.
	if newMode != "" {
		connector, err := connectors.NewConnector(newMode, "")
		if err != nil {
			return fmt.Errorf("PowerFlex mode %q is not supported: %w", newMode, err)
		}

		// In case of NVMe this will actually try to load the respective kernel modules.
		// In case of SDC it will check if the kernel module got loaded outside of LXD.
		err = connector.LoadModules()
		if err != nil {
			return fmt.Errorf("PowerFlex mode %q is not supported due to missing kernel modules: %w", newMode, err)
		}
	}

	return nil
}

// Update applies any driver changes required from a configuration change.
func (d *powerflex) Update(changedConfig map[string]string) error {
	return nil
}

// Mount mounts the storage pool.
func (d *powerflex) Mount() (bool, error) {
	// Nothing to do here.
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *powerflex) Unmount() (bool, error) {
	// Nothing to do here.
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *powerflex) GetResources() (*api.ResourcesStoragePool, error) {
	pool, err := d.resolvePool()
	if err != nil {
		return nil, err
	}

	client := d.client()
	res := &api.ResourcesStoragePool{}

	if !d.hasThinCloneSupport() {
		// PowerFlex 4 allows querying stats from the pool directly.
		stats, err := client.getStoragePoolStatistics(pool.ID)
		if err != nil {
			return nil, err
		}

		used := stats.NetCapacityInUseInKb * 1024

		res.Space.Total = stats.NetUnusedCapacityInKb*1024 + used
		res.Space.Used = used
	} else {
		// PowerFlex 5 requires using the new metrics endpoint.
		metrics, err := client.getStoragePoolMetrics(pool.ID)
		if err != nil {
			return nil, err
		}

		if len(metrics.Resources) != 1 {
			return nil, fmt.Errorf("Unexpected number of resources in metrics response for pool %q", pool.ID)
		}

		for _, metric := range metrics.Resources[0].Metrics {
			if len(metric.Values) != 1 {
				return nil, fmt.Errorf("Unexpected number of values in metrics response for pool %q and metric %q", pool.ID, metric.Name)
			}

			if metric.Name == "physical_total" {
				res.Space.Total = metric.Values[0]
				continue
			}

			if metric.Name == "physical_used" {
				res.Space.Used = metric.Values[0]
				continue
			}
		}
	}

	return res, nil
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *powerflex) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if refresh {
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

	if contentType == ContentTypeBlock {
		return []migration.Type{
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

// roundVolumeBlockSizeBytes rounds the given size (in bytes) up to the next
// multiple of 1 or 8 GiB, which is the minimum allocation unit on PowerFlex.
func (d *powerflex) roundVolumeBlockSizeBytes(_ Volume, sizeBytes int64) int64 {
	if d.hasThinCloneSupport() {
		return roundAbove(powerFlex5MinVolumeSizeBytes, sizeBytes)
	}

	return roundAbove(powerFlex4MinVolumeSizeBytes, sizeBytes)
}
