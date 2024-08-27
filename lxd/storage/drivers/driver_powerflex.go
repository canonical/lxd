package drivers

import (
	"fmt"
	"strings"

	"github.com/dell/goscaleio"

	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

// powerFlexDefaultUser represents the default PowerFlex user name.
const powerFlexDefaultUser = "admin"

// powerFlexDefaultSize represents the default PowerFlex volume size.
const powerFlexDefaultSize = "8GiB"

const (
	powerFlexModeNVMe = "nvme"
	powerFlexModeSDC  = "sdc"
)

var powerFlexLoaded bool
var powerFlexVersion string

type powerflex struct {
	common

	// Holds the low level HTTP client for the PowerFlex API.
	// Use powerflex.client() to retrieve the client struct.
	httpClient *powerFlexClient

	// Holds the SDC GUID of this specific host.
	// Use powerflex.getHostGUID() to retrieve the actual value.
	sdcGUID string
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *powerflex) load() error {
	// Done if previously loaded.
	if powerFlexLoaded {
		return nil
	}

	// Detect and record the version.
	// The NVMe CLI is shipped with the snap.
	out, err := shared.RunCommand("nvme", "version")
	if err != nil {
		return fmt.Errorf("Failed to get nvme-cli version: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(out), " ")
	if strings.HasPrefix(out, "nvme version ") && len(fields) > 2 {
		powerFlexVersion = fmt.Sprintf("%s (nvme-cli)", fields[2])
	}

	// Load the NVMe/TCP kernel modules.
	// Ignore if the modules cannot be loaded.
	// Support for the NVMe/TCP mode is checked during pool creation.
	// When a LXD host gets rebooted this ensures that the kernel modules are still loaded.
	_ = d.loadNVMeModules()

	powerFlexLoaded = true
	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *powerflex) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *powerflex) Info() Info {
	return Info{
		Name:                         "powerflex",
		Version:                      powerFlexVersion,
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
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *powerflex) FillConfig() error {
	if d.config["powerflex.user.name"] == "" {
		d.config["powerflex.user.name"] = powerFlexDefaultUser
	}

	// Try to discover the PowerFlex operation mode.
	// First try if the NVMe/TCP kernel modules can be loaed.
	// Second try if the SDC kernel module is setup.
	if d.config["powerflex.mode"] == "" {
		if d.loadNVMeModules() {
			d.config["powerflex.mode"] = powerFlexModeNVMe
		} else if goscaleio.DrvCfgIsSDCInstalled() {
			d.config["powerflex.mode"] = powerFlexModeSDC
		}
	}

	// PowerFlex volumes have to be at least 8GiB in size.
	if d.config["volume.size"] == "" {
		d.config["volume.size"] = powerFlexDefaultSize
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *powerflex) Create() error {
	err := d.FillConfig()
	if err != nil {
		return err
	}

	// Validate both pool and gateway here and return an error if they are not set.
	// Since those aren't any cluster member specific keys the general validation
	// rules allow empty strings in order to create the pending storage pools.
	if d.config["powerflex.pool"] == "" {
		return fmt.Errorf("The powerflex.pool cannot be empty")
	}

	if d.config["powerflex.gateway"] == "" {
		return fmt.Errorf("The powerflex.gateway cannot be empty")
	}

	client := d.client()

	switch d.config["powerflex.mode"] {
	case powerFlexModeNVMe:
		// Discover one of the storage pools SDT services.
		if d.config["powerflex.sdt"] == "" {
			pool, err := d.resolvePool()
			if err != nil {
				return err
			}

			relations, err := client.getProtectionDomainSDTRelations(pool.ProtectionDomainID)
			if err != nil {
				return err
			}

			if len(relations) == 0 {
				return fmt.Errorf("Failed to retrieve at least one SDT for the given storage pool: %q", pool.ID)
			}

			if len(relations[0].IPList) == 0 {
				return fmt.Errorf("Failed to retrieve IP from SDT: %q", relations[0].Name)
			}

			d.config["powerflex.sdt"] = relations[0].IPList[0].IP
		}

	case powerFlexModeSDC:
		if d.config["powerflex.sdt"] != "" {
			return fmt.Errorf("The powerflex.sdt config key is specific to the NVMe/TCP mode")
		}

		if !goscaleio.DrvCfgIsSDCInstalled() {
			return fmt.Errorf("PowerFlex SDC is not available on the host")
		}

	default:
		// Fail if no PowerFlex mode can be discovered.
		return fmt.Errorf("Failed to discover PowerFlex mode")
	}

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *powerflex) Delete(op *operations.Operation) error {
	// If the user completely destroyed it, call it done.
	if !shared.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// On delete, wipe everything in the directory.
	return wipeDirectory(GetPoolMountPath(d.name))
}

// Validate checks that all provided keys are supported and that no conflicting or missing configuration is present.
func (d *powerflex) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.user.name)
		//
		// ---
		//  type: string
		//  defaultdesc: `admin`
		//  shortdesc: User for PowerFlex Gateway authentication
		"powerflex.user.name": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.user.password)
		//
		// ---
		//  type: string
		//  shortdesc: Password for PowerFlex Gateway authentication
		"powerflex.user.password": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.gateway)
		//
		// ---
		//  type: string
		//  shortdesc: Address of the PowerFlex Gateway
		"powerflex.gateway": validate.Optional(validate.IsRequestURL),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.gateway.verify)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to verify the PowerFlex Gateway's certificate
		"powerflex.gateway.verify": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.pool)
		// If you want to specify the storage pool via its name, also set {config:option}`storage-powerflex-pool-conf:powerflex.domain`.
		// ---
		//  type: string
		//  shortdesc: ID of the PowerFlex storage pool
		"powerflex.pool": validate.IsAny,
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.domain)
		// This option is required only if {config:option}`storage-powerflex-pool-conf:powerflex.pool` is specified using its name.
		// ---
		//  type: string
		//  shortdesc: Name of the PowerFlex protection domain
		"powerflex.domain": validate.Optional(validate.IsAny),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.mode)
		// The mode gets discovered automatically if the system provides the necessary kernel modules.
		// This can be either `nvme` or `sdc`.
		// ---
		//  type: string
		//  defaultdesc: the discovered mode
		//  shortdesc: How volumes are mapped to the local server
		"powerflex.mode": validate.Optional(validate.IsOneOf("nvme", "sdc")),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.sdt)
		//
		// ---
		//  type: string
		//  defaultdesc: one of the SDT
		//  shortdesc: PowerFlex NVMe/TCP SDT
		"powerflex.sdt": validate.Optional(validate.IsNetworkAddress),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=powerflex.clone_copy)
		// If this option is set to `true`, PowerFlex makes a non-sparse copy when creating a snapshot of an instance or custom volume.
		// See {ref}`storage-powerflex-limitations` for more information.
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to use non-sparse copies for snapshots
		"powerflex.clone_copy": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=storage-powerflex; group=pool-conf; key=volume.size)
		// The size must be in multiples of 8 GiB.
		// See {ref}`storage-powerflex-limitations` for more information.
		// ---
		//  type: string
		//  defaultdesc: `8GiB`
		//  shortdesc: Size/quota of the storage volume
		"volume.size": validate.Optional(validate.IsMultipleOfUnit("8GiB")),
	}

	err := d.validatePool(config, rules, d.commonVolumeRules())
	if err != nil {
		return err
	}

	// Check if the selected PowerFlex mode is supported on this node.
	// Also when forming the storage pool on a LXD cluster, the mode
	// that got discovered on the creating machine needs to be validated
	// on the other cluster members too. This can be done here since Validate
	// gets executed on every cluster member when receiving the cluster
	// notification to finally create the pool.
	if d.config["powerflex.mode"] == powerFlexModeNVMe && !d.loadNVMeModules() {
		return fmt.Errorf("NVMe/TCP is not supported")
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

	stats, err := d.client().getStoragePoolStatistics(pool.ID)
	if err != nil {
		return nil, err
	}

	res := &api.ResourcesStoragePool{}
	res.Space.Total = stats.MaxCapacityInKb * 1000
	res.Space.Used = stats.CapacityInUseInKb * 1000

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
