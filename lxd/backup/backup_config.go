package backup

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Container *api.Instance           `yaml:"container"`
	Snapshots []*api.InstanceSnapshot `yaml:"snapshots"`
	Pool      *api.StoragePool        `yaml:"pool"`
	Volume    *api.StorageVolume      `yaml:"volume"`
}

// ParseConfigYamlFile decodes the YAML file at path specified into a Config.
func ParseConfigYamlFile(path string) (*Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	backup := Config{}
	if err := yaml.Unmarshal(data, &backup); err != nil {
		return nil, err
	}

	return &backup, nil
}

// updateRootDevicePool updates the root disk device in the supplied list of devices to the pool
// specified. Returns true if a root disk device has been found and updated otherwise false.
func updateRootDevicePool(devices map[string]map[string]string, poolName string) bool {
	if devices != nil {
		devName, _, err := shared.GetRootDiskDevice(devices)
		if err == nil {
			devices[devName]["pool"] = poolName
			return true
		}
	}

	return false
}

// UpdateInstanceConfigStoragePool changes the pool information in the backup.yaml to the pool specified in b.Pool.
func UpdateInstanceConfigStoragePool(c *db.Cluster, b Info, mountPath string) error {
	// Load the storage pool.
	_, pool, err := c.GetStoragePool(b.Pool)
	if err != nil {
		return err
	}

	f := func(path string) error {
		// Read in the backup.yaml file.
		backup, err := ParseConfigYamlFile(path)
		if err != nil {
			return err
		}

		rootDiskDeviceFound := false

		// Change the pool in the backup.yaml.
		backup.Pool = pool

		if updateRootDevicePool(backup.Container.Devices, pool.Name) {
			rootDiskDeviceFound = true
		}

		if updateRootDevicePool(backup.Container.ExpandedDevices, pool.Name) {
			rootDiskDeviceFound = true
		}

		for _, snapshot := range backup.Snapshots {
			updateRootDevicePool(snapshot.Devices, pool.Name)
			updateRootDevicePool(snapshot.ExpandedDevices, pool.Name)
		}

		if !rootDiskDeviceFound {
			return fmt.Errorf("No root device could be found")
		}

		file, err := os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()

		data, err := yaml.Marshal(&backup)
		if err != nil {
			return err
		}

		_, err = file.Write(data)
		if err != nil {
			return err
		}

		return nil
	}

	err = f(filepath.Join(mountPath, "backup.yaml"))
	if err != nil {
		return err
	}

	return nil
}
