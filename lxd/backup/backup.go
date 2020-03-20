package backup

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

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

// Info represents exported backup information.
type Info struct {
	Project          string   `json:"project" yaml:"project"`
	Name             string   `json:"name" yaml:"name"`
	Backend          string   `json:"backend" yaml:"backend"`
	Pool             string   `json:"pool" yaml:"pool"`
	Snapshots        []string `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	OptimizedStorage bool     `json:"-" yaml:"-"`
}

// GetInfo extracts backup information from a given ReadSeeker.
func GetInfo(r io.ReadSeeker) (*Info, error) {
	var tr *tar.Reader
	result := Info{}
	optimizedStorage := false
	hasIndexFile := false

	// Extract
	r.Seek(0, 0)
	_, _, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, err
	}
	r.Seek(0, 0)

	if unpacker == nil {
		return nil, fmt.Errorf("Unsupported backup compression")
	}

	if len(unpacker) > 0 {
		cmd := exec.Command(unpacker[0], unpacker[1:]...)
		cmd.Stdin = r

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		defer stdout.Close()

		err = cmd.Start()
		if err != nil {
			return nil, err
		}
		defer cmd.Wait()

		tr = tar.NewReader(stdout)
	} else {
		tr = tar.NewReader(r)
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return nil, err
		}

		if hdr.Name == "backup/index.yaml" {
			err = yaml.NewDecoder(tr).Decode(&result)
			if err != nil {
				return nil, err
			}

			hasIndexFile = true
		}

		if hdr.Name == "backup/container.bin" {
			optimizedStorage = true
		}
	}

	if !hasIndexFile {
		return nil, fmt.Errorf("Backup is missing index.yaml")
	}

	result.OptimizedStorage = optimizedStorage
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

// Rename renames a container backup
func (b *Backup) Rename(newName string) error {
	oldBackupPath := shared.VarPath("backups", project.Instance(b.instance.Project(), b.name))
	newBackupPath := shared.VarPath("backups", project.Instance(b.instance.Project(), newName))

	// Create the new backup path
	backupsPath := shared.VarPath("backups", project.Instance(b.instance.Project(), b.instance.Name()))
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
	err = b.state.Cluster.ContainerBackupRename(b.name, newName)
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
func DoBackupDelete(s *state.State, projectName, backupName, containerName string) error {
	backupPath := shared.VarPath("backups", project.Instance(projectName, backupName))

	// Delete the on-disk data
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the container directory
	backupsPath := shared.VarPath("backups", project.Instance(projectName, containerName))
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record
	err := s.Cluster.InstanceBackupRemove(backupName)
	if err != nil {
		return err
	}

	return nil
}
