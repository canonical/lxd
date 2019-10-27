package backup

import (
	"archive/tar"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// InstanceLoadByID returns instance config by ID.
var InstanceLoadByID func(s *state.State, id int) (Instance, error)

// Instance represents the backup relevant subset of a LXD instance.
type Instance interface {
	Name() string
	Project() string
}

// Info represents exported backup information.
type Info struct {
	Project         string   `json:"project" yaml:"project"`
	Name            string   `json:"name" yaml:"name"`
	Backend         string   `json:"backend" yaml:"backend"`
	Privileged      bool     `json:"privileged" yaml:"privileged"`
	Pool            string   `json:"pool" yaml:"pool"`
	Snapshots       []string `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	HasBinaryFormat bool     `json:"-" yaml:"-"`
}

// GetInfo extracts backup information from a given ReadSeeker.
func GetInfo(r io.ReadSeeker) (*Info, error) {
	var tr *tar.Reader
	result := Info{}
	hasBinaryFormat := false
	hasIndexFile := false

	// Extract
	r.Seek(0, 0)
	_, algo, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, err
	}
	r.Seek(0, 0)

	if unpacker == nil {
		return nil, fmt.Errorf("Unsupported backup compression")
	}

	if len(unpacker) > 0 {
		if algo == ".squashfs" {
			// 'sqfs2tar' tool does not support reading from stdin. So
			// create a temporary file to write the compressed data and
			// pass it to the tool as program argument
			tempfile, err := ioutil.TempFile("", "lxd_decompress_")
			if err != nil {
				return nil, err
			}
			defer os.Remove(tempfile.Name())

			// Write compressed data
			_, err = io.Copy(tempfile, r)
			if err != nil {
				return nil, err
			}

			tempfile.Close()
			// Prepare to pass the temporary file as program argument
			unpacker = append(unpacker, tempfile.Name())
		}
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
			hasBinaryFormat = true
		}
	}

	if !hasIndexFile {
		return nil, fmt.Errorf("Backup is missing index.yaml")
	}

	result.HasBinaryFormat = hasBinaryFormat
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
	oldBackupPath := shared.VarPath("backups", project.Prefix(b.instance.Project(), b.name))
	newBackupPath := shared.VarPath("backups", project.Prefix(b.instance.Project(), newName))

	// Create the new backup path
	backupsPath := shared.VarPath("backups", project.Prefix(b.instance.Project(), b.instance.Name()))
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

// LoadByName load a backup from the database.
func LoadByName(s *state.State, project, name string) (*Backup, error) {
	// Get the backup database record
	args, err := s.Cluster.ContainerGetBackup(project, name)
	if err != nil {
		return nil, errors.Wrap(err, "Load backup from database")
	}

	// Load the instance it belongs to
	instance, err := InstanceLoadByID(s, args.InstanceID)
	if err != nil {
		return nil, errors.Wrap(err, "Load container from database")
	}

	return &Backup{
		state:            s,
		instance:         instance,
		id:               args.ID,
		name:             name,
		creationDate:     args.CreationDate,
		expiryDate:       args.ExpiryDate,
		instanceOnly:     args.InstanceOnly,
		optimizedStorage: args.OptimizedStorage,
	}, nil
}

// DoBackupDelete deletes a backup.
func DoBackupDelete(s *state.State, projectName, backupName, containerName string) error {
	backupPath := shared.VarPath("backups", project.Prefix(projectName, backupName))

	// Delete the on-disk data
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the container directory
	backupsPath := shared.VarPath("backups", project.Prefix(projectName, containerName))
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
