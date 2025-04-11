package config

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
)

// MetadataVersion represents the current version of the format used when writing a backup's metadata.
// The metadata is used both for exporting backups and for migration.
// Starting from LXD 6.x onwards version 2 of the format is used.
const MetadataVersion = api.BackupMetadataVersion2

// Volume represents the config of a volume including its snapshots.
type Volume struct {
	// Make sure to have the embedded structs fields inline to avoid nesting.
	api.StorageVolume `yaml:",inline"`
	Snapshots         []*api.StorageVolumeSnapshot `yaml:"snapshots,omitempty"`
}

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Version   uint32                  `yaml:"version,omitempty"`
	Instance  *api.Instance           `yaml:"instance,omitempty"`
	Snapshots []*api.InstanceSnapshot `yaml:"snapshots,omitempty"`
	Pools     []*api.StoragePool      `yaml:"pools,omitempty"`
	Profiles  []*api.Profile          `yaml:"profiles,omitempty"`
	Volumes   []*Volume               `yaml:"volumes,omitempty"`
	Bucket    *api.StorageBucket      `yaml:"bucket,omitempty"`
	// Deprecated: Use Instance instead.
	Container *api.Instance `yaml:"container,omitempty"`
	// Deprecated: Use Pools instead.
	Pool *api.StoragePool `yaml:"pool,omitempty"`
	// Deprecated: Use Volumes instead.
	Volume *api.StorageVolume `yaml:"volume,omitempty"`
	// Deprecated: Use the list of Snapshots under Volumes.
	VolumeSnapshots []*api.StorageVolumeSnapshot `yaml:"volume_snapshots,omitempty"`
}

func (c *Config) rootVolPoolName() (string, error) {
	if c.Instance == nil {
		return "", fmt.Errorf("Instance config is missing")
	}

	for deviceName, deviceConfig := range c.Instance.ExpandedDevices {
		if deviceName == "root" {
			poolName, ok := deviceConfig["pool"]
			if ok {
				return poolName, nil
			}
		}
	}

	return "", fmt.Errorf("Root volume pool does not exist")
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
		return nil, fmt.Errorf("Pool config of the root volume does not exist")
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
	return fmt.Errorf("Cannot apply invalid root volume pool")
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

		if volume.Name == c.Instance.Name {
			return volume, nil
		}
	}

	// Second try fetching the single volume for snapshot instances.
	// Snapshot instances don't have the Instance field populated.
	// A snapshot is always represented by a single volume.
	// Therefore reuse the same tooling as when retrieving a custom volume.
	volume, _ := c.CustomVolume()
	if volume != nil {
		return volume, nil
	}

	return nil, fmt.Errorf("Config of the root volume does not exist")
}

// CustomVolume returns the single custom volume.
// Unlike RootVolume, CustomVolume always returns the first and only volume in the list.
func (c *Config) CustomVolume() (*Volume, error) {
	if c.Instance != nil {
		return nil, fmt.Errorf("Instance config cannot be set for custom volumes")
	}

	if len(c.Volumes) == 0 {
		return nil, fmt.Errorf("No custom volumes are defined in backup config")
	}

	if len(c.Volumes) > 1 {
		return nil, fmt.Errorf("More than one custom volume is defined in backup config")
	}

	if c.Volumes[0] == nil {
		return nil, fmt.Errorf("Custom volume config does not exist")
	}

	return c.Volumes[0], nil
}
