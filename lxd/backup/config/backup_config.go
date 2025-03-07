package config

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
)

// VolumeConfig represents the config of a volume including its snapshots.
type VolumeConfig struct {
	// Make sure to have the embedded structs fields inline to avoid nesting.
	api.StorageVolume `yaml:",inline"`
	Snapshots         []*api.StorageVolumeSnapshot `yaml:"snapshots,omitempty"`
}

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Version api.BackupMetadataVersion `yaml:"version,omitempty"`
	// Deprecated.
	// Use Instance instead.
	Container *api.Instance           `yaml:"container,omitempty"`
	Instance  *api.Instance           `yaml:"instance,omitempty"`
	Snapshots []*api.InstanceSnapshot `yaml:"snapshots,omitempty"`
	// Deprecated.
	// Use Pools instead.
	Pool     *api.StoragePool   `yaml:"pool,omitempty"`
	Pools    []*api.StoragePool `yaml:"pools,omitempty"`
	Profiles []*api.Profile     `yaml:"profiles,omitempty"`
	// Deprecated.
	// Use Volumes instead.
	Volume  *api.StorageVolume `yaml:"volume,omitempty"`
	Volumes []*VolumeConfig    `yaml:"volumes,omitempty"`
	// Deprecated.
	// Use the list of Snapshots under Volumes.
	VolumeSnapshots []*api.StorageVolumeSnapshot `yaml:"volume_snapshots,omitempty"`
	Bucket          *api.StorageBucket           `yaml:"bucket,omitempty"`
}

// RootVolumePool returns the pool of the root volume.
// This is the first pool in the list of pools.
func (c *Config) RootVolumePool() (*api.StoragePool, error) {
	if len(c.Pools) == 0 {
		return nil, fmt.Errorf("No pools are defined in backup config")
	}

	if c.Pools[0] == nil {
		return nil, fmt.Errorf("Pool config is empty")
	}

	return c.Pools[0], nil
}

// UpdateRootVolumePool updates the root volume's storage pool.
func (c *Config) UpdateRootVolumePool(pool *api.StoragePool) {
	if c.Pools == nil {
		c.Pools = make([]*api.StoragePool, 0, 1)
	}

	c.Pools[0] = pool
}

// RootVolume returns the root volume.
// This is the first volume in the list of volumes.
func (c *Config) RootVolume() (*VolumeConfig, error) {
	if len(c.Volumes) == 0 {
		return nil, fmt.Errorf("No volumes are defined in backup config")
	}

	if c.Volumes[0] == nil {
		return nil, fmt.Errorf("Volume config is empty")
	}

	return c.Volumes[0], nil
}
