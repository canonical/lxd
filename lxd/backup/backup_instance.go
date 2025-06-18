package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Instance represents the backup relevant subset of a LXD instance.
// This is used rather than instance.Instance to avoid import loops.
type Instance interface {
	Name() string
	Project() api.Project
	Operation() *operations.Operation
}

// InstanceBackup represents an instance backup.
type InstanceBackup struct {
	CommonBackup

	instance     Instance
	instanceOnly bool
}

// NewInstanceBackup instantiates a new InstanceBackup struct.
func NewInstanceBackup(state *state.State, inst Instance, ID int, name string, creationDate time.Time, expiryDate time.Time, instanceOnly bool, optimizedStorage bool) *InstanceBackup {
	return &InstanceBackup{
		CommonBackup: CommonBackup{
			state:            state,
			id:               ID,
			name:             name,
			creationDate:     creationDate,
			expiryDate:       expiryDate,
			optimizedStorage: optimizedStorage,
		},
		instance:     inst,
		instanceOnly: instanceOnly,
	}
}

// InstanceOnly returns whether only the instance itself is to be backed up.
func (b *InstanceBackup) InstanceOnly() bool {
	return b.instanceOnly
}

// Instance returns the instance to be backed up.
func (b *InstanceBackup) Instance() Instance {
	return b.instance
}

// Rename renames an instance backup.
func (b *InstanceBackup) Rename(newName string) error {
	backupsPath := b.state.BackupsStoragePath("")
	oldBackupPath := filepath.Join(backupsPath, "instances", project.Instance(b.instance.Project().Name, b.name))
	newBackupPath := filepath.Join(backupsPath, "instances", project.Instance(b.instance.Project().Name, newName))

	// Extract the old and new parent backup paths from the old and new backup names rather than use
	// instance.Name() as this may be in flux if the instance itself is being renamed, whereas the relevant
	// instance name is encoded into the backup names.
	oldParentName, _, _ := api.GetParentAndSnapshotName(b.name)
	oldParentBackupsPath := filepath.Join(backupsPath, "instances", project.Instance(b.instance.Project().Name, oldParentName))
	newParentName, _, _ := api.GetParentAndSnapshotName(newName)
	newParentBackupsPath := filepath.Join(backupsPath, "instances", project.Instance(b.instance.Project().Name, newParentName))

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
		return tx.RenameInstanceBackup(ctx, b.name, newName)
	})
	if err != nil {
		return err
	}

	oldName := b.name
	b.name = newName
	b.state.Events.SendLifecycle(b.instance.Project().Name, lifecycle.InstanceBackupRenamed.Event(b.name, b.instance, map[string]any{"old_name": oldName}))
	return nil
}

// Delete removes an instance backup.
func (b *InstanceBackup) Delete() error {
	backupsPathBase := b.state.BackupsStoragePath("")
	backupPath := filepath.Join(backupsPathBase, "instances", project.Instance(b.instance.Project().Name, b.name))

	// Delete the on-disk data.
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the instance directory.
	backupsPath := filepath.Join(backupsPathBase, "instances", project.Instance(b.instance.Project().Name, b.instance.Name()))
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record.
	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteInstanceBackup(ctx, b.name)
	})
	if err != nil {
		return err
	}

	b.state.Events.SendLifecycle(b.instance.Project().Name, lifecycle.InstanceBackupDeleted.Event(b.name, b.instance, nil))

	return nil
}

// Render returns an InstanceBackup struct of the backup.
func (b *InstanceBackup) Render() *api.InstanceBackup {
	return &api.InstanceBackup{
		Name:             strings.SplitN(b.name, "/", 2)[1],
		CreatedAt:        b.creationDate,
		ExpiresAt:        b.expiryDate,
		InstanceOnly:     b.instanceOnly,
		ContainerOnly:    b.instanceOnly,
		OptimizedStorage: b.optimizedStorage,
	}
}
