package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
)

// DefaultMetadataVersion represents the current default version of the format used when writing a backup's metadata.
// The metadata is used both for exporting backups and for migration.
// Starting from LXD 6.x onwards version 2 of the format is used.
const DefaultMetadataVersion = api.BackupMetadataVersion2

// MaxMetadataVersion represents the latest supported metadata version.
const MaxMetadataVersion = api.BackupMetadataVersion2

// Type indicates the type of backup.
type Type string

// TypeUnknown defines the backup type value for unknown backups.
const TypeUnknown = Type("")

// TypeContainer defines the backup type value for a container.
const TypeContainer = Type("container")

// TypeVM defines the backup type value for a virtual-machine.
const TypeVM = Type("virtual-machine")

// TypeCustom defines the backup type value for a custom volume.
const TypeCustom = Type("custom")

// Volume represents the config of a volume including its snapshots.
type Volume struct {
	// Make sure to have the embedded structs fields inline to avoid nesting.
	api.StorageVolume `yaml:",inline"` //nolint:musttag

	// Use the uppercase representation of the field to follow the same format as the root Config struct.
	Snapshots []*api.StorageVolumeSnapshot `json:"Snapshots" yaml:"snapshots,omitempty"`
}

// Bucket represents the config of a bucket including its snapshots.
type Bucket struct {
	// Make sure to have the embedded structs fields inline to avoid nesting.
	*api.StorageBucket `yaml:",inline"` //nolint:musttag
}

// configMetadata represents internal fields which don't appear on the materialized backup config.
type configMetadata struct {
	// lastModified tracks the backup file's modification time.
	lastModified time.Time
}

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	// Unexported fields.
	// We cannot simply embed them as it will let the yaml marshaller panic.
	metadata configMetadata

	// The JSON representation of the fields does not use lowercase (and omitempty) to stay backwards compatible
	// across all versions of LXD as the Config struct is also used throughout the migration.
	Version   uint32                  `json:"Version" yaml:"version,omitempty"`
	Instance  *api.Instance           `json:"Instance" yaml:"instance,omitempty"`
	Snapshots []*api.InstanceSnapshot `json:"Snapshots" yaml:"snapshots,omitempty"`
	Pools     []*api.StoragePool      `json:"Pools" yaml:"pools,omitempty"`
	Profiles  []*api.Profile          `json:"Profiles" yaml:"profiles,omitempty"`
	Volumes   []*Volume               `json:"Volumes" yaml:"volumes,omitempty"`
	Bucket    *Bucket                 `json:"Bucket" yaml:"bucket,omitempty"`
	// Deprecated: Use Instance instead.
	Container *api.Instance `json:"Container" yaml:"container,omitempty"`
	// Deprecated: Use Pools instead.
	Pool *api.StoragePool `json:"Pool" yaml:"pool,omitempty"`
	// Deprecated: Use Volumes instead.
	Volume *api.StorageVolume `json:"Volume" yaml:"volume,omitempty"`
	// Deprecated: Use the list of Snapshots under Volumes.
	VolumeSnapshots []*api.StorageVolumeSnapshot `json:"VolumeSnapshots" yaml:"volume_snapshots,omitempty"`
}

// NewConfig returns a new Config instance initialized with an immutable last modified time.
func NewConfig(lastModified time.Time) *Config {
	return &Config{
		metadata: configMetadata{
			lastModified: lastModified,
		},
	}
}

// rootVolPoolName returns the pool name of an instance's root volume.
// The name is derived from the instance's expanded devices.
func (c *Config) rootVolPoolName() (string, error) {
	if c.Instance == nil {
		return "", errors.New("Instance config is missing")
	}

	_, deviceConfig, err := instancetype.GetRootDiskDevice(c.Instance.ExpandedDevices)
	if err != nil {
		return "", fmt.Errorf("Failed to get root disk device: %w", err)
	}

	poolName, ok := deviceConfig["pool"]
	if ok {
		return poolName, nil
	}

	return "", errors.New("Root volume pool does not exist")
}

// primaryVolume can be used to retrieve both custom storage volumes and the volume of instance snapshots.
// In both cases the backup config contains only a single volume.
func (c *Config) primaryVolume() (*Volume, error) {
	if len(c.Volumes) == 0 {
		return nil, errors.New("No primary volume is defined in backup config")
	}

	if len(c.Volumes) > 1 {
		return nil, errors.New("More than one primary volume is defined in backup config")
	}

	if c.Volumes[0] == nil {
		return nil, errors.New("Primary volume config does not exist")
	}

	return c.Volumes[0], nil
}

// RootVolumePool returns the pool of the root volume.
// The pool is derived from the volume whose name matches the one of the instance.
func (c *Config) RootVolumePool() (*api.StoragePool, error) {
	rootVolPoolName, err := c.rootVolPoolName()
	if err != nil {
		return nil, err
	}

	var rootVolPool *api.StoragePool
	for _, pool := range c.Pools {
		if pool.Name == rootVolPoolName {
			rootVolPool = pool
			break
		}
	}

	if rootVolPool == nil {
		return nil, errors.New("Pool config of the root volume does not exist")
	}

	return rootVolPool, nil
}

// UpdateRootVolumePool updates the root volume's storage pool.
func (c *Config) UpdateRootVolumePool(pool *api.StoragePool) error {
	rootVolPoolName, err := c.rootVolPoolName()
	if err != nil {
		return err
	}

	// Create the pool if it not yet exists.
	if c.Pools == nil {
		c.Pools = []*api.StoragePool{pool}
		return nil
	}

	for i, existingPool := range c.Pools {
		if existingPool.Name == rootVolPoolName {
			c.Pools[i] = pool
			return nil
		}
	}

	// There already exists a root volume pool and it's name doesn't match the given pool.
	return errors.New("Cannot apply invalid root volume pool")
}

// RootVolume returns an instance's root volume from the list of volumes.
// The volume's name matches the one of the instance.
func (c *Config) RootVolume() (*Volume, error) {
	// First try obtaining the root volume for non-snapshot instances.
	// In this case the Instance field is populated.
	for _, volume := range c.Volumes {
		if c.Instance == nil {
			continue
		}

		// An instance's root volume uses the same name as its parent instance.
		// In the list of volumes there might be a custom storage volume with an identical name.
		// Only return the volume if its type is either virtual-machine or container.
		if volume.Name == c.Instance.Name && Type(volume.Type) != TypeCustom {
			return volume, nil
		}
	}

	// Second try fetching the single volume for snapshot instances.
	// Snapshot instances don't have the Instance field populated.
	// A snapshot is always represented by a single volume.
	// Therefore reuse the same tooling as when retrieving a custom volume.
	volume, err := c.primaryVolume()
	if err != nil {
		return nil, fmt.Errorf("Failed to get the snapshot instance's volume: %w", err)
	}

	return volume, nil
}

// CustomVolume returns the single custom volume.
// Unlike RootVolume, CustomVolume always returns the first and only volume in the list.
func (c *Config) CustomVolume() (*Volume, error) {
	if c.Instance != nil {
		return nil, errors.New("Instance config cannot be set for custom volumes")
	}

	volume, err := c.primaryVolume()
	if err != nil {
		return nil, fmt.Errorf("Failed to get primary volume: %w", err)
	}

	return volume, nil
}

// LastModified returns the backup config's immutable last modification time.
func (c *Config) LastModified() time.Time {
	return c.metadata.lastModified
}
