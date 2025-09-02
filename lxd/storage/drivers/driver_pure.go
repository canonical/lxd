package drivers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/validate"
)

// pureLoaded indicates whether load() function was already called for the Pure Storage driver.
var pureLoaded = false

// pureVersion indicates Pure Storage version.
var pureVersion = ""

// pureSupportedConnectors represents a list of storage connectors that can be used with Pure Storage.
var pureSupportedConnectors = []string{
	connectors.TypeISCSI,
	connectors.TypeNVME,
}

// pureMinVolumeSizeBytes defines the minimum size of a Pure Storage volume, which is 1MiB.
const pureMinVolumeSizeBytes = 1024 * 1024

type pure struct {
	common

	// Holds the low level connector for the Pure Storage driver.
	// Use pure.connector() to retrieve the initialized connector.
	storageConnector connectors.Connector

	// Holds the low level HTTP client for the Pure Storage API.
	// Use pure.client() to retrieve the client struct.
	httpClient *pureClient

	// apiVersion indicates the Pure Storage API version.
	apiVersion string
}

// load is used initialize the driver. It should be used only once.
func (d *pure) load() error {
	// Done if previously loaded.
	if pureLoaded {
		return nil
	}

	versions := connectors.GetSupportedVersions(pureSupportedConnectors)
	pureVersion = strings.Join(versions, " / ")
	pureLoaded = true

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
// Pure Storage mode. The connector is cached in the driver struct.
func (d *pure) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		connector, err := connectors.NewConnector(d.config["pure.mode"], d.state.OS.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
}

// client returns the drivers Pure Storage client. A new client is created only if it does not already exist.
func (d *pure) client() *pureClient {
	if d.httpClient == nil {
		d.httpClient = newPureClient(d)
	}

	return d.httpClient
}

// isRemote returns true indicating this driver uses remote storage.
func (d *pure) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *pure) Info() Info {
	return Info{
		Name:                         "pure",
		Version:                      pureVersion,
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
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *pure) FillConfig() error {
	// Use NVMe by default.
	if d.config["pure.mode"] == "" {
		d.config["pure.mode"] = connectors.TypeNVME
	}

	return nil
}

// Validate checks that all provided keys are supported and there is no conflicting or missing configuration.
func (d *pure) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"size": validate.Optional(validate.IsSize),
		// lxdmeta:generate(entities=storage-pure; group=pool-conf; key=pure.api.token)
		// API authorization token for Pure Storage gateway. Must have array_admin role to give LXD full control over managed storage pools (Pure Storage pods).
		// ---
		//  type: string
		//  shortdesc: API authorization token for Pure Storage gateway
		"pure.api.token": validate.Optional(),
		// lxdmeta:generate(entities=storage-pure; group=pool-conf; key=pure.gateway)
		//
		// ---
		//  type: string
		//  shortdesc: Address of the Pure Storage gateway
		"pure.gateway": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-pure; group=pool-conf; key=pure.gateway.verify)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to verify the Pure Storage gateway's certificate
		"pure.gateway.verify": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-pure; group=pool-conf; key=pure.target)
		// A comma-separated list of target addresses. If empty, LXD discovers and connects to all available targets. Otherwise, it only connects to the specified addresses.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: List of target addresses.
		"pure.target": validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),
		// lxdmeta:generate(entities=storage-pure; group=pool-conf; key=pure.mode)
		// The mode to use to map Pure Storage volumes to the local server.
		// Supported values are `iscsi` and `nvme`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		"pure.mode": validate.Optional(validate.IsOneOf(pureSupportedConnectors...)),
		// lxdmeta:generate(entities=storage-pure; group=pool-conf; key=volume.size)
		// Default Pure Storage volume size rounded to 512B. The minimum size is 1MiB.
		// ---
		//  type: string
		//  defaultdesc: `10GiB`
		//  shortdesc: Size/quota of the storage volume
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("512B")),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	newMode := config["pure.mode"]
	oldMode := d.config["pure.mode"]

	// Ensure pure.mode cannot be changed to avoid leaving volume mappings
	// and prevent disturbing running instances.
	if oldMode != "" && oldMode != newMode {
		return errors.New("Pure Storage mode cannot be changed")
	}

	// Check if the selected Pure Storage mode is supported on this node.
	// Also when forming the storage pool on a LXD cluster, the mode
	// that got discovered on the creating machine needs to be validated
	// on the other cluster members too. This can be done here since Validate
	// gets executed on every cluster member when receiving the cluster
	// notification to finally create the pool.
	if newMode != "" {
		connector, err := connectors.NewConnector(newMode, "")
		if err != nil {
			return fmt.Errorf("Pure Storage mode %q is not supported: %w", newMode, err)
		}

		err = connector.LoadModules()
		if err != nil {
			return fmt.Errorf("Pure Storage mode %q is not supported due to missing kernel modules: %w", newMode, err)
		}
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *pure) Create() error {
	err := d.FillConfig()
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	// Validate required Pure Storage configuration keys and return an error if they are
	// not set. Since those keys are not cluster member specific, the general validation
	// rules allow empty strings in order to create the pending storage pools.
	if d.config["pure.gateway"] == "" {
		return errors.New("The pure.gateway cannot be empty")
	}

	if d.config["pure.api.token"] == "" {
		return errors.New("The pure.api.token cannot be empty")
	}

	poolSizeBytes, err := units.ParseByteSizeString(d.config["size"])
	if err != nil {
		return fmt.Errorf("Failed to parse storage size: %w", err)
	}

	// Create the storage pool.
	err = d.client().createStoragePool(d.name, poolSizeBytes)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = d.client().deleteStoragePool(d.name) })

	revert.Success()

	return nil
}

// Update applies any driver changes required from a configuration change.
func (d *pure) Update(changedConfig map[string]string) error {
	newPoolSizeBytes, err := units.ParseByteSizeString(changedConfig["size"])
	if err != nil {
		return fmt.Errorf("Failed to parse storage size: %w", err)
	}

	oldPoolSizeBytes, err := units.ParseByteSizeString(d.config["size"])
	if err != nil {
		return fmt.Errorf("Failed to parse old storage size: %w", err)
	}

	if newPoolSizeBytes != oldPoolSizeBytes {
		err = d.client().updateStoragePool(d.name, newPoolSizeBytes)
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete removes the storage pool (Pure Storage pod).
func (d *pure) Delete(op *operations.Operation) error {
	// First delete the storage pool on Pure Storage.
	err := d.client().deleteStoragePool(d.name)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}

	// If the user completely destroyed it, call it done.
	if !shared.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// On delete, wipe everything in the directory.
	return wipeDirectory(GetPoolMountPath(d.name))
}

// Mount mounts the storage pool.
func (d *pure) Mount() (bool, error) {
	// Nothing to do here.
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *pure) Unmount() (bool, error) {
	// Nothing to do here.
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *pure) GetResources() (*api.ResourcesStoragePool, error) {
	pool, err := d.client().getStoragePool(d.name)
	if err != nil {
		return nil, err
	}

	res := &api.ResourcesStoragePool{}

	res.Space.Total = uint64(pool.Quota)
	res.Space.Used = uint64(pool.Space.UsedBytes)

	if pool.Quota == 0 {
		// If quota is set to 0, it means that the storage pool is unbounded. Therefore,
		// collect the total capacity of arrays where storage pool provisioned.
		arrayNames := make([]string, 0, len(pool.Arrays))
		for _, array := range pool.Arrays {
			arrayNames = append(arrayNames, array.Name)
		}

		arrays, err := d.client().getStorageArrays(arrayNames...)
		if err != nil {
			return nil, err
		}

		for _, array := range arrays {
			res.Space.Total += uint64(array.Capacity)
		}
	}

	return res, nil
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *pure) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type {
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
// multiple of 512 bytes, which is the minimum allocation unit on Pure Storage.
// It also enforces a minimum volume size of 1 MiB.
func (d *pure) roundVolumeBlockSizeBytes(_ Volume, sizeBytes int64) int64 {
	if sizeBytes < pureMinVolumeSizeBytes {
		return pureMinVolumeSizeBytes
	}

	return roundAbove(512, sizeBytes)
}
