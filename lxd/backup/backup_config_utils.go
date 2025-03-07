package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

const (
	// BackupFileName represents the original (old) file name.
	BackupFileName = "backup.yaml"

	// BackupFileNameNew represents the new file name.
	BackupFileNameNew = "backup2.yaml"
)

// ConfigToInstanceDBArgs converts the instance config in the backup config to DB InstanceArgs.
func ConfigToInstanceDBArgs(state *state.State, c *config.Config, projectName string, applyProfiles bool) (*db.InstanceArgs, error) {
	if c.Instance == nil {
		return nil, nil
	}

	arch, _ := osarch.ArchitectureId(c.Instance.Architecture)
	instanceType, _ := instancetype.New(c.Instance.Type)

	inst := &db.InstanceArgs{
		Project:      projectName,
		Architecture: arch,
		BaseImage:    c.Instance.Config["volatile.base_image"],
		Config:       c.Instance.Config,
		CreationDate: c.Instance.CreatedAt,
		Type:         instanceType,
		Description:  c.Instance.Description,
		Devices:      deviceConfig.NewDevices(c.Instance.Devices),
		Ephemeral:    c.Instance.Ephemeral,
		LastUsedDate: c.Instance.LastUsedAt,
		Name:         c.Instance.Name,
		Stateful:     c.Instance.Stateful,
	}

	if applyProfiles {
		err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			inst.Profiles = make([]api.Profile, 0, len(c.Instance.Profiles))
			profiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, c.Instance.Profiles)
			if err != nil {
				return err
			}

			// Get all the profile configs.
			profileConfigs, err := cluster.GetConfig(ctx, tx.Tx(), "profile")
			if err != nil {
				return err
			}

			// Get all the profile devices.
			profileDevices, err := cluster.GetDevices(ctx, tx.Tx(), "profile")
			if err != nil {
				return err
			}

			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
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

// UpgradeConfigFile changes from the old to the new metadata file format.
// It's a noop in case the config is already using the new format.
func UpgradeConfigFile(backupConf *config.Config) {
	// Rewrite the the instance and pools config keys only if observed in the old format.
	// Currently pools are only listed in the config files of instances.
	if backupConf.Container != nil {
		backupConf.Instance = backupConf.Container
		backupConf.Pools = []*api.StoragePool{backupConf.Pool}
	}

	// Rewrite the volumes only in case the old format is used.
	// We can indicate this by checking whether or not the .Volumes key is set.
	// This is applicable for both instances and custom storage volumes.
	if len(backupConf.Volumes) == 0 {
		backupConf.Volumes = []*config.VolumeConfig{
			{
				StorageVolume: *backupConf.Volume,
				Snapshots:     backupConf.VolumeSnapshots,
			},
		}
	}

	// Unset the deprecated keys.
	backupConf.Container = nil
	backupConf.Pool = nil
	backupConf.Volume = nil
	backupConf.VolumeSnapshots = nil
}

// DowngradeConfigFile changes from the new to the old metadata file format.
// It's a noop in case the config is already using the old format.
// Downgrading looses the information about any additional custom storage volumes
// that might have been attached to the config.
// For instances it only lists the root volume including its snapshots.
func DowngradeConfigFile(backupConf *config.Config) {
	if backupConf.Instance != nil {
		backupConf.Container = backupConf.Instance

		if len(backupConf.Pools) > 0 {
			backupConf.Pool = backupConf.Pools[0]
		}
	}

	if len(backupConf.Volumes) > 0 {
		backupConf.Volume = &backupConf.Volumes[0].StorageVolume
		backupConf.VolumeSnapshots = backupConf.Volumes[0].Snapshots
	}

	backupConf.Instance = nil
	backupConf.Volumes = nil
	backupConf.Pools = nil
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

	// Rewrite from the old to the new format in case the metadata file hasn't been updated yet.
	UpgradeConfigFile(&backupConf)

	// Default to container if type not specified in backup config.
	if backupConf.Instance != nil && backupConf.Instance.Type == "" {
		backupConf.Instance.Type = string(api.InstanceTypeContainer)
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

// UpdateInstanceConfig updates the instance's backup.yaml configuration file.
func UpdateInstanceConfig(c *db.Cluster, b Info, mountPath string) error {
	backupFilePath := filepath.Join(mountPath, "backup.yaml")

	// Read in the backup.yaml file.
	backup, err := ParseConfigYamlFile(backupFilePath)
	if err != nil {
		return err
	}

	// Update instance information in the backup.yaml.
	if backup.Instance != nil {
		backup.Instance.Name = b.Name
		backup.Instance.Project = b.Project
	}

	// Update volume information in the backup.yaml.
	if backup.Volumes != nil {
		rootVol, err := backup.RootVolume()
		if err != nil {
			return fmt.Errorf("Failed getting the root volume: %w", err)
		}

		rootVol.Name = b.Name
		rootVol.Project = b.Project

		updateRootVol, err := b.Config.RootVolume()
		if err != nil {
			return fmt.Errorf("Failed getting the root volume: %w", err)
		}

		// Ensure the most recent volume UUIDs get updated.
		rootVol.Config = updateRootVol.Config
		rootVol.Snapshots = updateRootVol.Snapshots
	}

	var pool *api.StoragePool

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load the storage pool.
		_, pool, _, err = tx.GetStoragePool(ctx, b.Pool)

		return err
	})
	if err != nil {
		return err
	}

	rootDiskDeviceFound := false

	// Change the pool in the backup.yaml.
	backup.UpdateRootVolumePool(pool)

	if updateRootDevicePool(backup.Instance.Devices, pool.Name) {
		rootDiskDeviceFound = true
	}

	if updateRootDevicePool(backup.Instance.ExpandedDevices, pool.Name) {
		rootDiskDeviceFound = true
	}

	for _, snapshot := range backup.Snapshots {
		updateRootDevicePool(snapshot.Devices, pool.Name)
		updateRootDevicePool(snapshot.ExpandedDevices, pool.Name)
	}

	if !rootDiskDeviceFound {
		return fmt.Errorf("No root device could be found")
	}

	// Write updated backup.yaml file.

	file, err := os.Create(backupFilePath)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	data, err := yaml.Marshal(&backup)
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	if err != nil {
		return err
	}

	return file.Close()
}
