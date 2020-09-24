package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// WorkingDirPrefix is used when temporary working directories are needed.
const WorkingDirPrefix = "lxd_backup"

// Info represents exported backup information.
type Info struct {
	Project          string           `json:"-" yaml:"-"` // Project is set during import based on current project.
	Name             string           `json:"name" yaml:"name"`
	Backend          string           `json:"backend" yaml:"backend"`
	Pool             string           `json:"pool" yaml:"pool"`
	Snapshots        []string         `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	OptimizedStorage *bool            `json:"optimized,omitempty" yaml:"optimized,omitempty"`               // Optional field to handle older optimized backups that don't have this field.
	OptimizedHeader  *bool            `json:"optimized_header,omitempty" yaml:"optimized_header,omitempty"` // Optional field to handle older optimized backups that don't have this field.
	Type             api.InstanceType `json:"type" yaml:"type"`
}

// GetInfo extracts backup information from a given ReadSeeker.
func GetInfo(r io.ReadSeeker) (*Info, error) {
	result := Info{}
	hasIndexFile := false

	// Define some bools used to create points for OptimizedStorage field.
	optimizedStorageFalse := false
	optimizedHeaderFalse := false

	// Extract
	r.Seek(0, 0)
	_, _, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, err
	}

	if unpacker == nil {
		return nil, fmt.Errorf("Unsupported backup compression")
	}

	tr, cancelFunc, err := shared.CompressedTarReader(context.Background(), r, unpacker)
	if err != nil {
		return nil, err
	}
	defer cancelFunc()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return nil, errors.Wrapf(err, "Error reading backup file info")
		}

		if hdr.Name == "backup/index.yaml" {
			err = yaml.NewDecoder(tr).Decode(&result)
			if err != nil {
				return nil, err
			}

			hasIndexFile = true

			// Default to container if index doesn't specify instance type.
			if result.Type == api.InstanceTypeAny {
				result.Type = api.InstanceTypeContainer
			}

			// Default to no optimized header if not specified.
			if result.OptimizedHeader == nil {
				result.OptimizedHeader = &optimizedHeaderFalse
			}

			if result.OptimizedStorage != nil {
				// No need to continue looking for optimized storage hint using the presence of the
				// container.bin file below, as the index.yaml file tells us directly.
				cancelFunc()
				break
			} else {
				// Default to non-optimized if not specified and continue reading to see if
				// optimized container.bin file present.
				result.OptimizedStorage = &optimizedStorageFalse
			}
		}

		// If the tarball contains a binary dump of the container, then this is an optimized backup.
		if hdr.Name == "backup/container.bin" {
			optimizedStorageTrue := true
			result.OptimizedStorage = &optimizedStorageTrue

			// Stop read loop if index.yaml already parsed.
			if hasIndexFile {
				cancelFunc()
				break
			}
		}
	}

	if !hasIndexFile {
		return nil, fmt.Errorf("Backup is missing index.yaml")
	}

	return &result, nil
}

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
