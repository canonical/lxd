package backup

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/osarch"
)

// ConfigToInstanceDBArgs converts the instance config in the backup config to DB InstanceArgs.
func ConfigToInstanceDBArgs(state *state.State, c *config.Config, projectName string, applyProfiles bool) (*db.InstanceArgs, error) {
	if c.Container == nil {
		return nil, nil
	}

	arch, _ := osarch.ArchitectureId(c.Container.Architecture)
	instanceType, _ := instancetype.New(c.Container.Type)

	inst := &db.InstanceArgs{
		Project:      projectName,
		Architecture: arch,
		BaseImage:    c.Container.Config["volatile.base_image"],
		Config:       c.Container.Config,
		CreationDate: c.Container.CreatedAt,
		Type:         instanceType,
		Description:  c.Container.Description,
		Devices:      deviceConfig.NewDevices(c.Container.Devices),
		Ephemeral:    c.Container.Ephemeral,
		LastUsedDate: c.Container.LastUsedAt,
		Name:         c.Container.Name,
		Stateful:     c.Container.Stateful,
	}

	if applyProfiles {
		err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			inst.Profiles = make([]api.Profile, 0, len(c.Container.Profiles))
			profiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, c.Container.Profiles)
			if err != nil {
				return err
			}

			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				inst.Profiles = append(inst.Profiles, *apiProfile)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return inst, nil
}

// ParseConfigYamlFile decodes the YAML file at path specified into a Config.
func ParseConfigYamlFile(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	backupConf := config.Config{}
	err = yaml.Unmarshal(data, &backupConf)
	if err != nil {
		return nil, err
	}

	// Default to container if type not specified in backup config.
	if backupConf.Container != nil && backupConf.Container.Type == "" {
		backupConf.Container.Type = string(api.InstanceTypeContainer)
	}

	return &backupConf, nil
}

// updateRootDevicePool updates the root disk device in the supplied list of devices to the pool
// specified. Returns true if a root disk device has been found and updated otherwise false.
func updateRootDevicePool(devices map[string]map[string]string, poolName string) bool {
	if devices != nil {
		devName, _, err := instancetype.GetRootDiskDevice(devices)
		if err == nil {
			devices[devName]["pool"] = poolName
			return true
		}
	}

	return false
}

// UpdateInstanceConfigInPlace updates the instance's backup index in place.
func UpdateInstanceConfigInPlace(c *db.Cluster, b *Info) error {
	// Load the storage pool.
	_, pool, _, err := c.GetStoragePool(b.Pool)
	if err != nil {
		return err
	}

	rootDiskDeviceFound := false

	// Change the pool in case it doesn't match the one of the original instance.
	b.Config.Pool = pool

	if updateRootDevicePool(b.Config.Container.Devices, pool.Name) {
		rootDiskDeviceFound = true
	}

	if updateRootDevicePool(b.Config.Container.ExpandedDevices, pool.Name) {
		rootDiskDeviceFound = true
	}

	for _, snapshot := range b.Config.Snapshots {
		updateRootDevicePool(snapshot.Devices, pool.Name)
		updateRootDevicePool(snapshot.ExpandedDevices, pool.Name)
	}

	if !rootDiskDeviceFound {
		return fmt.Errorf("No root device could be found")
	}

	return nil
}
