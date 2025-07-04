package drivers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
)

// hpeAlletraLoaded indicates whether load() function was already called for this storage driver.
var hpeAlletraLoaded = false

// hpeAlletraVersion indicates storage driver version.
var hpeAlletraVersion = ""

// hpeAlletraSupportedConnectors represents a list of storage connectors that can be used.
var hpeAlletraSupportedConnectors = []string{
	connectors.TypeISCSI,
	connectors.TypeNVME,
}

type hpeAlletra struct {
	common

	// Holds the low level connector (iSCSI, NVMEoTCP).
	// Use .connector() method to retrieve the initialized connector.
	storageConnector connectors.Connector

	// Holds the targetQN of NVMe target.
	nvmeTargetQN string

	// Holds the low level HTTP client for the HPE Alletra Storage API.
	// Use .client() method to retrieve the client struct.
	httpClient *hpeAlletraClient
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *hpeAlletra) load() error {
	// Done if previously loaded.
	if hpeAlletraLoaded {
		return nil
	}

	versions := connectors.GetSupportedVersions(hpeAlletraSupportedConnectors)
	hpeAlletraVersion = strings.Join(versions, " / ")
	hpeAlletraLoaded = true

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
// storage driver mode (iSCSI, NVMEoTCP). The connector is cached in the driver struct.
func (d *hpeAlletra) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		connector, err := connectors.NewConnector(d.config["hpe_alletra.mode"], d.state.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
}

// client returns the drivers HPE Alletra Storage client. A new client is created only if it does not already exist.
func (d *hpeAlletra) client() *hpeAlletraClient {
	if d.httpClient == nil {
		d.httpClient = newHPEAlletraClient(d)
	}

	return d.httpClient
}

// isRemote returns true indicating this driver uses remote storage.
func (d *hpeAlletra) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *hpeAlletra) Info() Info {
	return Info{
		Name:                         "hpeAlletra",
		Version:                      "1",
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              false,
		PreservesInodes:              true,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		MountedRoot:                  false,
		PopulateParentVolumeUUID:     true,
	}
}

// FillConfig populates the driver's config with default values.
func (d *hpeAlletra) FillConfig() error {
	// Use NVMe by default.
	if d.config["hpe_alletra.mode"] == "" {
		d.config["hpe_alletra.mode"] = connectors.TypeNVME
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *hpeAlletra) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.wsapi.url)
		//
		// ---
		//  type: string
		//  shortdesc: Address of the HPE Alletra Storage UI/WSAPI
		"hpe_alletra.wsapi.url": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.wsapi.verify)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to verify the HPE Alletra Storage UI/WSAPI certificate
		"hpe_alletra.wsapi.verify": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.wsapi.user.name)
		//
		// ---
		//  type: string
		//  shortdesc: HPE Alletra storage admin username
		"hpe_alletra.wsapi.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.wsapi.user.password)
		//
		// ---
		//  type: string
		//  shortdesc: HPE Alletra storage admin password
		"hpe_alletra.wsapi.user.password": validate.IsAny,
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.wsapi.cpg)
		//
		// ---
		//  type: string
		//  shortdesc: HPE Alletra CPG name
		"hpe_alletra.wsapi.cpg": validate.IsAny,
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.target)
		// A comma-separated list of target addresses. If empty, LXD discovers and connects to all available targets. Otherwise, it only connects to the specified addresses.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: List of target addresses.
		"hpe_alletra.target": validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=hpe_alletra.mode)
		// The mode to use to map Pure Storage volumes to the local server.
		// Supported values are `iscsi` and `nvme`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		"hpe_alletra.mode": validate.Optional(validate.IsOneOf(pureSupportedConnectors...)),
		// lxdmeta:generate(entities=storage-hpe-alletra; group=pool-conf; key=volume.size)
		// Default storage volume size rounded to 256MiB. The minimum size is 256MiB.
		// ---
		//  type: string
		//  defaultdesc: `10GiB`
		//  shortdesc: Size/quota of the storage volume
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("256MiB")),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	newMode := config["hpe_alletra.mode"]
	oldMode := d.config["hpe_alletra.mode"]

	// Ensure hpe_alletra.mode cannot be changed to avoid leaving volume mappings
	// and prevent disturbing running instances.
	if oldMode != "" && oldMode != newMode {
		return errors.New("HPE Alletra Storage mode cannot be changed")
	}

	// Check if the selected HPE Alletra Storage mode is supported on this node.
	// Also when forming the storage pool on a LXD cluster, the mode
	// that got discovered on the creating machine needs to be validated
	// on the other cluster members too. This can be done here since Validate
	// gets executed on every cluster member when receiving the cluster
	// notification to finally create the pool.
	if newMode != "" {
		connector, err := connectors.NewConnector(newMode, "")
		if err != nil {
			return fmt.Errorf("HPE Alletra Storage mode %q is not supported: %w", newMode, err)
		}

		err = connector.LoadModules()
		if err != nil {
			return fmt.Errorf("HPE Alletra Storage mode %q is not supported due to missing kernel modules: %w", newMode, err)
		}
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *hpeAlletra) Create() error {
	err := d.FillConfig()
	if err != nil {
		return err
	}

	// Validate both pool and gateway here and return an error if they are not set.
	// Since those aren't any cluster member specific keys the general validation
	// rules allow empty strings in order to create the pending storage pools.
	if d.config["hpe_alletra.wsapi.url"] == "" {
		return errors.New("The hpe_alletra.wsapi.url cannot be empty")
	}

	if d.config["hpe_alletra.wsapi.user.name"] == "" {
		return errors.New("The hpe_alletra.wsapi.user.name cannot be empty")
	}

	if d.config["hpe_alletra.wsapi.user.password"] == "" {
		return errors.New("The hpe_alletra.wsapi.user.password cannot be empty")
	}

	// TODO: do we need to *actually* go to WSAPI and create something?
	// Volumeset? Hostset? something else?
	logger.Warn("Going to createVolumeSet")
	err = d.client().createVolumeSet()
	if err != nil {
		logger.Warn("createVolumeSet failed", logger.Ctx{"err": err})
		return err
	}

	return nil
}

// Delete removes a storage pool.
func (d *hpeAlletra) Delete(op *operations.Operation) error {
	// FIXME: recursive?
	err := d.client().deleteVolumeSet()
	if err != nil {
		return err
	}

	// If the user completely destroyed it, call it done.
	if !shared.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// On delete, wipe everything in the directory.
	return wipeDirectory(GetPoolMountPath(d.name))
}

// Update applies any driver changes required from a configuration change.
func (d *hpeAlletra) Update(changedConfig map[string]string) error {
	return nil
}

// Mount mounts the storage pool.
func (d *hpeAlletra) Mount() (bool, error) {
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *hpeAlletra) Unmount() (bool, error) {
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *hpeAlletra) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, nil
}
