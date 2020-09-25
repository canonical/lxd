package backup

import (
	"os"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Instance represents the backup relevant subset of a LXD instance.
// This is used rather than instance.Instance to avoid import loops.
type Instance interface {
	Name() string
	Project() string
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

// Rename renames an instance backup.
func (b *InstanceBackup) Rename(newName string) error {
	oldBackupPath := shared.VarPath("backups", "instances", project.Instance(b.instance.Project(), b.name))
	newBackupPath := shared.VarPath("backups", "instances", project.Instance(b.instance.Project(), newName))

	// Create the new backup path.
	backupsPath := shared.VarPath("backups", "instances", project.Instance(b.instance.Project(), b.instance.Name()))
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Rename the backup directory.
	err := os.Rename(oldBackupPath, newBackupPath)
	if err != nil {
		return err
	}

	// Check if we can remove the instance directory.
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Rename the database record.
	err = b.state.Cluster.RenameInstanceBackup(b.name, newName)
	if err != nil {
		return err
	}

	return nil
}

// Delete removes an instance backup.
func (b *InstanceBackup) Delete() error {
	backupPath := shared.VarPath("backups", "instances", project.Instance(b.instance.Project(), b.name))

	// Delete the on-disk data.
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the instance directory.
	backupsPath := shared.VarPath("backups", "instances", project.Instance(b.instance.Project(), b.instance.Name()))
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record.
	err := b.state.Cluster.DeleteInstanceBackup(b.name)
	if err != nil {
		return err
	}

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
