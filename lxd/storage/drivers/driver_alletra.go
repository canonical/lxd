package drivers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/drivers/clients"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

// alletraLoaded indicates whether load() function was already called for this storage driver.
var alletraLoaded = false

// alletraVersion indicates storage driver version.
var alletraVersion = ""

// alletraSupportedConnectors represents a list of storage connectors that can be used.
var alletraSupportedConnectors = []string{
	connectors.TypeNVME,
	connectors.TypeISCSI,
}

type alletra struct {
	common

	// Holds the low level connector (iSCSI, NVMe/TCP).
	// Use .connector() method to retrieve the initialized connector.
	storageConnector connectors.Connector

	// Holds the targetQN of NVMe target.
	targetQN string

	// Holds the low level HTTP client for the HPE Alletra Storage API.
	// Use .client() method to retrieve the client struct.
	httpClient *clients.AlletraClient
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *alletra) load() error {
	// Done if previously loaded.
	if alletraLoaded {
		return nil
	}

	versions := connectors.GetSupportedVersions(alletraSupportedConnectors)
	alletraVersion = strings.Join(versions, " / ")
	alletraLoaded = true

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
// storage driver mode (iSCSI, NVMe/TCP). The connector is cached in the driver struct.
func (d *alletra) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		connector, err := connectors.NewConnector(d.config["alletra.mode"], d.state.OS.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
}

// client returns the drivers HPE Alletra Storage client. A new client is created only if it does not already exist.
func (d *alletra) client() *clients.AlletraClient {
	if d.httpClient == nil {
		d.httpClient = clients.NewAlletraClient(
			d.logger,
			d.config["alletra.wsapi"],
			d.config["alletra.user.name"],
			d.config["alletra.user.password"],
			shared.IsFalse(d.config["alletra.wsapi.verify"]),
			d.config["alletra.cpg"])
	}

	return d.httpClient
}

// isRemote returns true indicating this driver uses remote storage.
func (d *alletra) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *alletra) Info() Info {
	return Info{
		Name:                         "alletra",
		Version:                      alletraVersion,
		DefaultBlockSize:             d.defaultBlockVolumeSize(),
		DefaultVMBlockFilesystemSize: d.defaultVMBlockFilesystemSize(),
		OptimizedImages:              true,
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
func (d *alletra) FillConfig() error {
	// Use NVMe by default.
	if d.config["alletra.mode"] == "" {
		d.config["alletra.mode"] = connectors.TypeNVME
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *alletra) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.wsapi)
		//
		// ---
		//  type: string
		//  shortdesc: Address of the HPE Alletra Storage UI/WSAPI
		"alletra.wsapi": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.wsapi.verify)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to verify the HPE Alletra Storage UI/WSAPI certificate
		"alletra.wsapi.verify": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.user.name)
		//
		// ---
		//  type: string
		//  shortdesc: HPE Alletra storage admin username
		"alletra.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.user.password)
		//
		// ---
		//  type: string
		//  shortdesc: HPE Alletra storage admin password
		"alletra.user.password": validate.IsAny,
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.cpg)
		//
		// ---
		//  type: string
		//  shortdesc: HPE Alletra Common Provisioning Group (CPG) name
		"alletra.cpg": validate.IsAny,
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.target)
		// A comma-separated list of target addresses. If empty, LXD discovers and connects to all available targets. Otherwise, it only connects to the specified addresses.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: List of target addresses.
		"alletra.target": validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=alletra.mode)
		// The mode to use to map storage volumes to the local server.
		// Supported values are `iscsi` and `nvme`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		"alletra.mode": validate.Optional(validate.IsOneOf(alletraSupportedConnectors...)),
		// lxdmeta:generate(entities=storage-alletra; group=pool-conf; key=volume.size)
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

	newMode := config["alletra.mode"]
	oldMode := d.config["alletra.mode"]

	// Ensure alletra.mode cannot be changed to avoid leaving volume mappings
	// and prevent disturbing running instances.
	if oldMode != "" && oldMode != newMode {
		return errors.New("Alletra Storage mode cannot be changed")
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
			return fmt.Errorf("Alletra Storage mode %q is not supported: %w", newMode, err)
		}

		err = connector.LoadModules()
		if err != nil {
			return fmt.Errorf("Alletra Storage mode %q is not supported due to missing kernel modules: %w", newMode, err)
		}
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *alletra) Create() error {
	err := d.FillConfig()
	if err != nil {
		return err
	}

	// Validate both pool and gateway here and return an error if they are not set.
	// Since those aren't any cluster member specific keys the general validation
	// rules allow empty strings in order to create the pending storage pools.
	if d.config["alletra.wsapi"] == "" {
		return errors.New("The alletra.wsapi cannot be empty")
	}

	if d.config["alletra.user.name"] == "" {
		return errors.New("The alletra.user.name cannot be empty")
	}

	if d.config["alletra.user.password"] == "" {
		return errors.New("The alletra.user.password cannot be empty")
	}

	err = d.client().CreateVolumeSet(d.name)
	if err != nil {
		return err
	}

	return nil
}

// Delete removes a storage pool.
func (d *alletra) Delete(op *operations.Operation) error {
	err := d.client().DeleteVolumeSet(d.name)
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
func (d *alletra) Update(changedConfig map[string]string) error {
	// We don't need to do anything here, and just return success,
	// because we have no driver-level config options we want to allow
	// for an update.
	return nil
}

// Mount mounts the storage pool.
func (d *alletra) Mount() (bool, error) {
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *alletra) Unmount() (bool, error) {
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *alletra) GetResources() (*api.ResourcesStoragePool, error) {
	// We have to keep in mind, that CPG is a shared resource, and it can be
	// that the same CPG is used by many other LXD storage pools or even for
	// another workloads (unrelated to LXD). So this used/free information
	// can be inacurate for this reason.
	spaceReport, err := d.client().GetCPGSpaceReport()
	if err != nil {
		return nil, err
	}

	res := api.ResourcesStoragePool{}
	res.Space.Used = uint64(spaceReport.OverprovisionedUsedMiB) * 1024 * 1024
	res.Space.Total = res.Space.Used + uint64(spaceReport.OverProvisionedFreeMiB)*1024*1024
	return &res, nil
}

// getNVMeTargetQN discovers the targetQN used for the given addresses.
func (d *alletra) getNVMeTargetQN(targetAddresses ...string) (targetQN string, err error) {
	connector, err := d.connector()
	if err != nil {
		return "", err
	}

	// The discovery log from the first reachable target address is returned.
	discoveryLogRecords, err := connector.Discover(d.state.ShutdownCtx, targetAddresses...)
	if err != nil {
		return "", fmt.Errorf("Failed to discover array NVMe subsystem NQN: %w", err)
	}

	for _, recordAny := range discoveryLogRecords {
		record, ok := recordAny.(connectors.NVMeDiscoveryLogRecord)
		if !ok {
			return "", fmt.Errorf("Invalid discovery log record entry type %T is not connectors.NVMeDiscoveryLogRecord", recordAny)
		}

		if record.SubType != connectors.SubtypeNVMESubsys {
			continue
		}

		// The targetQN is listed together with every log record of type SubtypeNVMESubsys.
		targetQN = record.SubNQN
		break
	}

	return targetQN, nil
}

// getISCSITargetQN discovers the targetQN used for the given addresses.
func (d *alletra) getISCSITargetQN(targetAddresses ...string) (targetQN string, err error) {
	connector, err := d.connector()
	if err != nil {
		return "", err
	}

	// The discovery log from the first reachable target address is returned.
	discoveryLogRecords, err := connector.Discover(d.state.ShutdownCtx, targetAddresses...)
	if err != nil {
		return "", fmt.Errorf("Failed to discover array ISCSI subsystem IQN: %w", err)
	}

	record, ok := discoveryLogRecords[0].(connectors.ISCSIDiscoveryLogRecord)
	if !ok {
		return "", fmt.Errorf("Invalid discovery log record entry type %T is not connectors.ISCSIDiscoveryLogRecord", discoveryLogRecords[0])
	}

	// The targetQN is listed together with every log record.
	targetQN = record.IQN

	return targetQN, nil
}

// getTargetQN discovers the targetQN used for the given addresses.
func (d *alletra) getTargetQN(targetAddresses ...string) (string, error) {
	// The targetQN is unqiue per HPE Alletra storage pool.
	// Cache the targetQN as it doesn't change throughout the lifetime of the storage pool,
	// if there are volumes mapped and NVMe/TCP or ISCSI session is active.
	targetQN := d.targetQN
	if targetQN != "" {
		return targetQN, nil
	}

	connector, err := d.connector()
	if err != nil {
		return "", err
	}

	mode := connector.Type()

	switch mode {
	case connectors.TypeISCSI:
		targetQN, err = d.getISCSITargetQN(targetAddresses...)
		if err != nil {
			return "", err
		}

	case connectors.TypeNVME:
		targetQN, err = d.getNVMeTargetQN(targetAddresses...)
		if err != nil {
			return "", err
		}

	default:
		return "", fmt.Errorf("Storage target QN discovery is not supported for mode %q", mode)
	}

	if targetQN == "" {
		return "", errors.New("Couldn't discover target QN")
	}

	d.targetQN = targetQN
	return targetQN, nil
}

// getTarget discovers the targetQN and target's IP addresses list.
func (d *alletra) getTarget() (targetQN string, targetAddrs []string, err error) {
	// First check if target addresses are configured, otherwise, use the discovered ones.
	var configAddrs = shared.SplitNTrimSpace(d.config["alletra.target"], ",", -1, true)
	if len(configAddrs) > 0 {
		targetAddrs = configAddrs
	}

	connector, err := d.connector()
	if err != nil {
		return "", nil, err
	}

	mode := connector.Type()

	if len(targetAddrs) == 0 {
		targetAddrs, err = d.client().GetTargetAddrs(mode)
		if err != nil {
			return "", nil, err
		}
	}

	if len(targetAddrs) == 0 {
		return "", nil, fmt.Errorf("No usable target found for mode %q", mode)
	}

	// Discover the targetQN from any of the addresses.
	targetQN, err = d.getTargetQN(targetAddrs...)
	if err != nil {
		return "", nil, err
	}

	return targetQN, targetAddrs, nil
}
