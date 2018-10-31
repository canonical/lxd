package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/context"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Load a backup from the database
func backupLoadByName(s *state.State, project, name string) (*backup, error) {
	// Get the backup database record
	args, err := s.Cluster.ContainerGetBackup(project, name)
	if err != nil {
		return nil, errors.Wrap(err, "Load backup from database")
	}

	// Load the container it belongs to
	c, err := containerLoadById(s, args.ContainerID)
	if err != nil {
		return nil, errors.Wrap(err, "Load container from database")
	}

	// Return the backup struct
	return &backup{
		state:            s,
		container:        c,
		id:               args.ID,
		name:             name,
		creationDate:     args.CreationDate,
		expiryDate:       args.ExpiryDate,
		containerOnly:    args.ContainerOnly,
		optimizedStorage: args.OptimizedStorage,
	}, nil
}

// Create a new backup
func backupCreate(s *state.State, args db.ContainerBackupArgs, sourceContainer container) error {
	// Create the database entry
	err := s.Cluster.ContainerBackupCreate(args)
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("backup '%s' already exists", args.Name)
		}

		return errors.Wrap(err, "Insert backup info into database")
	}

	// Get the backup struct
	b, err := backupLoadByName(s, sourceContainer.Project(), args.Name)
	if err != nil {
		return errors.Wrap(err, "Load backup object")
	}

	// Now create the empty snapshot
	err = sourceContainer.Storage().ContainerBackupCreate(*b, sourceContainer)
	if err != nil {
		s.Cluster.ContainerBackupRemove(args.Name)
		return errors.Wrap(err, "Backup storage")
	}

	return nil
}

// backup represents a container backup
type backup struct {
	state     *state.State
	container container

	// Properties
	id               int
	name             string
	creationDate     time.Time
	expiryDate       time.Time
	containerOnly    bool
	optimizedStorage bool
}

type backupInfo struct {
	Project         string   `json:"project" yaml:"project"`
	Name            string   `json:"name" yaml:"name"`
	Backend         string   `json:"backend" yaml:"backend"`
	Privileged      bool     `json:"privileged" yaml:"privileged"`
	Pool            string   `json:"pool" yaml:"pool"`
	Snapshots       []string `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	HasBinaryFormat bool     `json:"-" yaml:"-"`
}

// Rename renames a container backup
func (b *backup) Rename(newName string) error {
	oldBackupPath := shared.VarPath("backups", b.name)
	newBackupPath := shared.VarPath("backups", newName)

	// Create the new backup path
	backupsPath := shared.VarPath("backups", b.container.Name())
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

// Delete removes a container backup
func (b *backup) Delete() error {
	return doBackupDelete(b.state, b.name, b.container.Name())
}

func (b *backup) Render() *api.ContainerBackup {
	return &api.ContainerBackup{
		Name:             strings.SplitN(b.name, "/", 2)[1],
		CreationDate:     b.creationDate,
		ExpiryDate:       b.expiryDate,
		ContainerOnly:    b.containerOnly,
		OptimizedStorage: b.optimizedStorage,
	}
}

func backupGetInfo(r io.ReadSeeker) (*backupInfo, error) {
	var tr *tar.Reader
	result := backupInfo{}
	hasBinaryFormat := false
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
		var buf bytes.Buffer

		err := shared.RunCommandWithFds(r, &buf, unpacker[0], unpacker[1:]...)
		if err != nil {
			return nil, err
		}

		tr = tar.NewReader(&buf)
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

// fixBackupStoragePool changes the pool information in the backup.yaml. This
// is done only if the provided pool doesn't exist. In this case, the pool of
// the default profile will be used.
func backupFixStoragePool(c *db.Cluster, b backupInfo) error {
	// Get the default profile
	_, profile, err := c.ProfileGet("default", "default")
	if err != nil {
		return err
	}

	_, v, err := shared.GetRootDiskDevice(profile.Devices)
	if err != nil {
		return err
	}

	// Get the default's profile pool
	_, pool, err := c.StoragePoolGet(v["pool"])
	if err != nil {
		return err
	}

	f := func(path string) error {
		// Read in the backup.yaml file.
		backup, err := slurpBackupFile(path)
		if err != nil {
			return err
		}

		// Change the pool in the backup.yaml
		backup.Pool = pool
		backup.Container.Devices["root"]["pool"] = "default"

		file, err := os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()

		data, err := yaml.Marshal(&backup)
		if err != nil {
			return err
		}

		_, err = file.Write(data)
		if err != nil {
			return err
		}

		return nil
	}

	err = f(shared.VarPath("storage-pools", pool.Name, "containers", b.Name, "backup.yaml"))
	if err != nil {
		return err
	}

	for _, snap := range b.Snapshots {
		err = f(shared.VarPath("storage-pools", pool.Name, "containers-snapshots", b.Name, snap,
			"backup.yaml"))
		if err != nil {
			return err
		}
	}
	return nil
}

func backupCreateTarball(s *state.State, path string, backup backup) error {
	container := backup.container

	// Create the index
	pool, err := container.StoragePool()
	if err != nil {
		return err
	}

	indexFile := backupInfo{
		Name:       container.Name(),
		Backend:    container.Storage().GetStorageTypeName(),
		Privileged: container.IsPrivileged(),
		Pool:       pool,
		Snapshots:  []string{},
	}

	if !backup.containerOnly {
		snaps, err := container.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())
			indexFile.Snapshots = append(indexFile.Snapshots, snapName)
		}
	}

	data, err := yaml.Marshal(&indexFile)
	if err != nil {
		return err
	}

	file, err := os.Create(filepath.Join(path, "index.yaml"))
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	file.Close()
	if err != nil {
		return err
	}

	// Create the target path if needed
	backupsPath := shared.VarPath("backups", backup.container.Name())
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Create the tarball
	backupPath := shared.VarPath("backups", backup.name)
	args := []string{"-cf", backupPath, "--xattrs", "-C", path, "--transform", "s,^./,backup/,", "."}
	_, err = shared.RunCommand("tar", args...)
	if err != nil {
		return err
	}

	// Compress it
	compress, err := cluster.ConfigGetString(s.Cluster, "backups.compression_algorithm")
	if err != nil {
		return err
	}

	if compress != "none" {
		compressedPath, err := compressFile(backupPath, compress)
		if err != nil {
			return err
		}

		err = os.Remove(backupPath)
		if err != nil {
			return err
		}

		err = os.Rename(compressedPath, backupPath)
		if err != nil {
			return err
		}
	}

	// Set permissions
	err = os.Chmod(backupPath, 0600)
	if err != nil {
		return err
	}

	return nil
}

func pruneExpiredContainerBackupsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operation) error {
			return pruneExpiredContainerBackups(ctx, d)
		}

		op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationBackupsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired backups operation", log.Ctx{"err": err})
		}

		logger.Info("Pruning expired container backups")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to expire backups", log.Ctx{"err": err})
		}
		logger.Info("Done pruning expired container backups")
	}

	f(context.Background())

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Hour

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func pruneExpiredContainerBackups(ctx context.Context, d *Daemon) error {
	// Get the list of expired backups.
	backups, err := d.cluster.ContainerBackupsGetExpired()
	if err != nil {
		return errors.Wrap(err, "Unable to retrieve the list of expired container backups")
	}

	for _, backup := range backups {
		containerName, _, _ := containerGetParentAndSnapshotName(backup)
		err := doBackupDelete(d.State(), backup, containerName)
		if err != nil {
			return errors.Wrapf(err, "Error deleting container backup %s", backup)
		}
	}

	return nil
}

func doBackupDelete(s *state.State, backupName, containerName string) error {
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
