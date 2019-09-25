package instance

import (
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Backup represents a container backup.
type Backup struct {
	state    *state.State
	Instance Instance

	// Properties.
	id               int
	Name             string
	creationDate     time.Time
	expiryDate       time.Time
	InstanceOnly     bool
	OptimizedStorage bool
}

// Rename renames a container backup
func (b *Backup) Rename(newName string) error {
	oldBackupPath := shared.VarPath("backups", b.Name)
	newBackupPath := shared.VarPath("backups", newName)

	// Create the new backup path
	backupsPath := shared.VarPath("backups", b.Instance.Name())
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Rename the backup directory
	err := os.Rename(oldBackupPath, newBackupPath)
	if err != nil {
		return err
	}

	// Check if we can remove the container directory
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Rename the database record
	err = b.state.Cluster.ContainerBackupRename(b.Name, newName)
	if err != nil {
		return err
	}

	return nil
}

// Delete removes an instance backup
func (b *Backup) Delete() error {
	return DoBackupDelete(b.state, b.Name, b.Instance.Name())
}

func (b *Backup) Render() *api.InstanceBackup {
	return &api.InstanceBackup{
		Name:             strings.SplitN(b.Name, "/", 2)[1],
		CreatedAt:        b.creationDate,
		ExpiresAt:        b.expiryDate,
		InstanceOnly:     b.InstanceOnly,
		ContainerOnly:    b.InstanceOnly,
		OptimizedStorage: b.OptimizedStorage,
	}
}

type BackupInfo struct {
	Project         string   `json:"project" yaml:"project"`
	Name            string   `json:"name" yaml:"name"`
	Backend         string   `json:"backend" yaml:"backend"`
	Privileged      bool     `json:"privileged" yaml:"privileged"`
	Pool            string   `json:"pool" yaml:"pool"`
	Snapshots       []string `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	HasBinaryFormat bool     `json:"-" yaml:"-"`
}

// Load a backup from the database
func BackupLoadByName(s *state.State, project, name string) (*Backup, error) {
	// Get the backup database record
	args, err := s.Cluster.ContainerGetBackup(project, name)
	if err != nil {
		return nil, errors.Wrap(err, "Load backup from database")
	}

	// Load the instance it belongs to
	instance, err := InstanceLoadById(s, args.ContainerID)
	if err != nil {
		return nil, errors.Wrap(err, "Load container from database")
	}

	// Return the backup struct
	return &Backup{
		state:            s,
		Instance:         instance,
		id:               args.ID,
		Name:             name,
		creationDate:     args.CreationDate,
		expiryDate:       args.ExpiryDate,
		InstanceOnly:     args.InstanceOnly,
		OptimizedStorage: args.OptimizedStorage,
	}, nil
}

func DoBackupDelete(s *state.State, backupName, containerName string) error {
	backupPath := shared.VarPath("backups", backupName)

	// Delete the on-disk data
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the container directory
	backupsPath := shared.VarPath("backups", containerName)
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record
	err := s.Cluster.ContainerBackupRemove(backupName)
	if err != nil {
		return err
	}

	return nil
}
