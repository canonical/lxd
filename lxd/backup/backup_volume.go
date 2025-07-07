package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
)

// VolumeBackup represents a custom volume backup.
type VolumeBackup struct {
	CommonBackup

	projectName string
	poolName    string
	volumeName  string
	volumeOnly  bool
}

// NewVolumeBackup instantiates a new VolumeBackup struct.
func NewVolumeBackup(state *state.State, projectName, poolName, volumeName string, ID int, name string, creationDate, expiryDate time.Time, volumeOnly, optimizedStorage bool) *VolumeBackup {
	return &VolumeBackup{
		CommonBackup: CommonBackup{
			state:            state,
			id:               ID,
			name:             name,
			creationDate:     creationDate,
			expiryDate:       expiryDate,
			optimizedStorage: optimizedStorage,
		},
		projectName: projectName,
		poolName:    poolName,
		volumeName:  volumeName,
		volumeOnly:  volumeOnly,
	}
}

// VolumeOnly returns whether only the volume itself is to be backed up.
func (b *VolumeBackup) VolumeOnly() bool {
	return b.volumeOnly
}

// OptimizedStorage returns whether the backup is to be performed using optimization format of the storage driver.
func (b *VolumeBackup) OptimizedStorage() bool {
	return b.optimizedStorage
}

// Rename renames a volume backup.
func (b *VolumeBackup) Rename(newName string) error {
	backupsPath := b.state.BackupsStoragePath("")
	oldBackupPath := filepath.Join(backupsPath, "custom", b.poolName, project.StorageVolume(b.projectName, b.name))
	newBackupPath := filepath.Join(backupsPath, "custom", b.poolName, project.StorageVolume(b.projectName, newName))

	// Extract the old and new parent backup paths from the old and new backup names rather than use
	// instance.Name() as this may be in flux if the instance itself is being renamed, whereas the relevant
	// instance name is encoded into the backup names.
	oldParentName, _, _ := api.GetParentAndSnapshotName(b.name)
	oldParentBackupsPath := filepath.Join(backupsPath, "custom", b.poolName, project.StorageVolume(b.projectName, oldParentName))
	newParentName, _, _ := api.GetParentAndSnapshotName(newName)
	newParentBackupsPath := filepath.Join(backupsPath, "custom", b.poolName, project.StorageVolume(b.projectName, newParentName))

	revert := revert.New()
	defer revert.Fail()

	// Create the new backup path if doesn't exist.
	if !shared.PathExists(newParentBackupsPath) {
		err := os.MkdirAll(newParentBackupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Rename the backup directory.
	err := os.Rename(oldBackupPath, newBackupPath)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.Rename(newBackupPath, oldBackupPath) })

	// Check if we can remove the old parent directory.
	empty, _ := shared.PathIsEmpty(oldParentBackupsPath)
	if empty {
		err := os.Remove(oldParentBackupsPath)
		if err != nil {
			return err
		}
	}

	// Rename the database record.
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameVolumeBackup(ctx, b.name, newName)
	})
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Delete removes a volume backup.
func (b *VolumeBackup) Delete() error {
	backupsPathBase := b.state.BackupsStoragePath("")
	backupPath := filepath.Join(backupsPathBase, "custom", b.poolName, project.StorageVolume(b.projectName, b.name))
	// Delete the on-disk data.
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the volume directory.
	backupsPath := filepath.Join(backupsPathBase, "custom", b.poolName, project.StorageVolume(b.projectName, b.volumeName))
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record.
	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteStoragePoolVolumeBackup(ctx, b.name)
	})
	if err != nil {
		return err
	}

	return nil
}

// Render returns a VolumeBackup struct of the backup.
func (b *VolumeBackup) Render() *api.StoragePoolVolumeBackup {
	return &api.StoragePoolVolumeBackup{
		Name:             strings.SplitN(b.name, "/", 2)[1],
		CreatedAt:        b.creationDate,
		ExpiresAt:        b.expiryDate,
		VolumeOnly:       b.volumeOnly,
		OptimizedStorage: b.optimizedStorage,
	}
}
