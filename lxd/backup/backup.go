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

// WorkingDirPrefix is used when temporary working directories are needed.
const WorkingDirPrefix = "lxd_backup"

// Backup represents a container backup
type Backup struct {
	state    *state.State
	instance Instance

	// Properties
	id                   int
	name                 string
	creationDate         time.Time
	expiryDate           time.Time
	instanceOnly         bool
	optimizedStorage     bool
	compressionAlgorithm string
}

// New instantiates a new Backup struct.
func New(state *state.State, inst Instance, ID int, name string, creationDate, expiryDate time.Time, instanceOnly, optimizedStorage bool) *Backup {
	return &Backup{
		state:            state,
		instance:         inst,
		id:               ID,
		name:             name,
		creationDate:     creationDate,
		expiryDate:       expiryDate,
		instanceOnly:     instanceOnly,
		optimizedStorage: optimizedStorage,
	}
}

// CompressionAlgorithm returns the compression used for the tarball.
func (b *Backup) CompressionAlgorithm() string {
	return b.compressionAlgorithm
}

// SetCompressionAlgorithm sets the tarball compression.
func (b *Backup) SetCompressionAlgorithm(compression string) {
	b.compressionAlgorithm = compression
}

// InstanceOnly returns whether only the instance itself is to be backed up.
func (b *Backup) InstanceOnly() bool {
	return b.instanceOnly
}

// Name returns the name of the backup.
func (b *Backup) Name() string {
	return b.name
}

// OptimizedStorage returns whether the backup is to be performed using
// optimization supported by the storage driver.
func (b *Backup) OptimizedStorage() bool {
	return b.optimizedStorage
}

// Rename renames an instance backup.
func (b *Backup) Rename(newName string) error {
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

// Delete removes an instance backup
func (b *Backup) Delete() error {
	return DoBackupDelete(b.state, b.instance.Project(), b.name, b.instance.Name())
}

// Render returns an InstanceBackup struct of the backup.
func (b *Backup) Render() *api.InstanceBackup {
	return &api.InstanceBackup{
		Name:             strings.SplitN(b.name, "/", 2)[1],
		CreatedAt:        b.creationDate,
		ExpiresAt:        b.expiryDate,
		InstanceOnly:     b.instanceOnly,
		ContainerOnly:    b.instanceOnly,
		OptimizedStorage: b.optimizedStorage,
	}
}

// DoBackupDelete deletes a backup.
func DoBackupDelete(s *state.State, projectName, backupName, instanceName string) error {
	backupPath := shared.VarPath("backups", "instances", project.Instance(projectName, backupName))

	// Delete the on-disk data.
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the instance directory.
	backupsPath := shared.VarPath("backups", "instances", project.Instance(projectName, instanceName))
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record.
	err := s.Cluster.DeleteInstanceBackup(backupName)
	if err != nil {
		return err
	}

	return nil
}
