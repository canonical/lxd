package drivers

import (
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/drivers/clients"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
)

// powerStoreLoaded indicates whether load() function was already called for the PowerStore driver.
var powerStoreLoaded bool

// powerStoreVersion holds the version of the PowerStore system.
var powerStoreVersion string

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
	return nil
}

// Validate checks that all provided keys are supported and that no conflicting or missing configuration is present.
func (d *powerstore) Validate(config map[string]string) error {
	return nil
}

// SourceIdentifier returns a combined string consisting of the gateway address and pool name.
func (d *powerstore) SourceIdentifier() (string, error) {
	return "", nil
}

// ValidateSource checks whether the required config keys are set to access the remote source.
func (d *powerstore) ValidateSource() error {
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
