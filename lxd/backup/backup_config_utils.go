package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/osarch"
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

// ConvertFormat converts a backup config's metadata file format between versions.
// It returns the converted contents and doesn't modify the provided config.
// In case the requested format is already present it's a noop.
func ConvertFormat(backupConf *config.Config, version uint32) (*config.Config, error) {
	// Create a copy of the original config.
	copyBackupConf := config.NewConfig(backupConf.LastModified())
	err := shared.DeepCopy(backupConf, copyBackupConf)
	if err != nil {
		return nil, fmt.Errorf("Failed to deep copy backup config: %w", err)
	}

	if version <= api.BackupMetadataVersion1 {
		// Changes from the new to the old metadata file format.

		// Downgrading loses the information about any additional custom storage volumes
		// that might have been attached to the config.
		// For instances it only lists the root volume including its snapshots.
		if copyBackupConf.Instance != nil {
			copyBackupConf.Container = copyBackupConf.Instance //nolint:staticcheck

			if len(copyBackupConf.Pools) > 0 {
				copyBackupConf.Pool = copyBackupConf.Pools[0] //nolint:staticcheck
			}
		}

		if len(copyBackupConf.Volumes) > 0 {
			copyBackupConf.Volume = &copyBackupConf.Volumes[0].StorageVolume     //nolint:staticcheck
			copyBackupConf.VolumeSnapshots = copyBackupConf.Volumes[0].Snapshots //nolint:staticcheck
		}

		copyBackupConf.Version = 0
		copyBackupConf.Instance = nil
		copyBackupConf.Volumes = nil
		copyBackupConf.Pools = nil
	} else {
		// Changes from the old to the new metadata file format.

		// Rewrite the the instance and pools config keys only if observed in the old format.
		// Currently pools are only listed in the config files of instances.
		if copyBackupConf.Container != nil { //nolint:staticcheck
			copyBackupConf.Instance = copyBackupConf.Container             //nolint:staticcheck
			copyBackupConf.Pools = []*api.StoragePool{copyBackupConf.Pool} //nolint:staticcheck
		}

		// Rewrite the volumes only in case the old format is used.
		// We can indicate this by checking whether or not the .Volumes key is set.
		// This is applicable for both instances and custom storage volumes.
		// In case there is no volume set we also don't populate one in the new file format.
		if len(copyBackupConf.Volumes) == 0 && copyBackupConf.Volume != nil { //nolint:staticcheck
			copyBackupConf.Volumes = []*config.Volume{
				{
					StorageVolume: *copyBackupConf.Volume,         //nolint:staticcheck
					Snapshots:     copyBackupConf.VolumeSnapshots, //nolint:staticcheck
				},
			}
		}

		// Set the corresponding backup format version if not set.
		if copyBackupConf.Version == 0 {
			copyBackupConf.Version = api.BackupMetadataVersion2
		}

		// Unset the deprecated keys.
		copyBackupConf.Container = nil       //nolint:staticcheck
		copyBackupConf.Pool = nil            //nolint:staticcheck
		copyBackupConf.Volume = nil          //nolint:staticcheck
		copyBackupConf.VolumeSnapshots = nil //nolint:staticcheck
	}

	return copyBackupConf, nil
}

// ParseConfigYamlFile decodes the YAML file at path specified into a Config.
func ParseConfigYamlFile(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	backupConfInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to stat %q: %w", path, err)
	}

	backupConf := config.NewConfig(backupConfInfo.ModTime())
	err = yaml.Unmarshal(data, backupConf)
	if err != nil {
		return nil, err
	}

	// Rewrite from the old to the new format in case the metadata file hasn't been updated yet.
	backupConf, err = ConvertFormat(backupConf, api.BackupMetadataVersion2)
	if err != nil {
		return nil, fmt.Errorf("Failed to convert backup config to version %d: %w", api.BackupMetadataVersion2, err)
	}

	// Default to container if type not specified in backup config.
	if backupConf.Instance != nil && backupConf.Instance.Type == "" {
		backupConf.Instance.Type = string(api.InstanceTypeContainer)
	}

	return backupConf, nil
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

	// Update instance information in the backup.yaml.
	// Perform this after fetching the root vol as it's picked by the instance's name from the list of vols.
	if backup.Instance != nil {
		backup.Instance.Name = b.Name
		backup.Instance.Project = b.Project
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
	err = backup.UpdateRootVolumePool(pool)
	if err != nil {
		return fmt.Errorf("Failed to update the root volume's pool: %w", err)
	}

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
		return errors.New("No root device could be found")
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
