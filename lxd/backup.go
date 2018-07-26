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

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// backup represents a container backup.
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
	Name            string   `json:"name" yaml:"name"`
	Backend         string   `json:"backend" yaml:"backend"`
	Privileged      bool     `json:"privileged" yaml:"privileged"`
	Pool            string   `json:"pool" yaml:"pool"`
	Snapshots       []string `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	HasBinaryFormat bool     `json:"-" yaml:"-"`
}

// Rename renames a container backup.
func (b *backup) Rename(newName string) error {
	ourStart, err := b.container.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer b.container.StorageStop()
	}

	// Rename the directories and files
	err = b.container.Storage().ContainerBackupRename(*b, newName)
	if err != nil {
		return err
	}

	// Rename the database entry
	err = b.state.Cluster.ContainerBackupRename(b.Name(), newName)
	if err != nil {
		return err
	}

	return nil
}

// Delete removes a container backup.
func (b *backup) Delete() error {
	ourStart, err := b.container.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer b.container.StorageStop()
	}

	// Delete backup from storage
	err = b.container.Storage().ContainerBackupDelete(b.Name())
	if err != nil {
		return err
	}

	// Remove the database record
	err = b.state.Cluster.ContainerBackupRemove(b.Name())
	if err != nil {
		return err
	}

	return nil
}

// Dump dumps the container including its snapshots.
func (b *backup) Dump() ([]byte, error) {
	ourStart, err := b.container.StorageStart()
	if err != nil {
		return nil, err
	}
	if ourStart {
		defer b.container.StorageStop()
	}

	data, err := b.container.Storage().ContainerBackupDump(*b)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (b *backup) Render() interface{} {
	return &api.ContainerBackup{
		Name:             strings.SplitN(b.name, "/", 2)[1],
		CreationDate:     b.creationDate,
		ExpiryDate:       b.expiryDate,
		ContainerOnly:    b.containerOnly,
		OptimizedStorage: b.optimizedStorage,
	}
}

func (b *backup) Id() int {
	return b.id
}

func (b *backup) Name() string {
	return b.name
}

func (b *backup) CreationDate() time.Time {
	return b.creationDate
}

func (b *backup) ExpiryDate() time.Time {
	return b.expiryDate
}

func (b *backup) ContainerOnly() bool {
	return b.containerOnly
}

func (b *backup) OptimizedStorage() bool {
	return b.optimizedStorage
}

func getBackupInfo(r io.Reader) (*backupInfo, error) {
	var buf bytes.Buffer
	err := shared.RunCommandWithFds(r, &buf, "unxz", "-")
	if err != nil {
		return nil, err
	}

	result := backupInfo{}
	hasBinaryFormat := false
	hasIndexFile := false
	tr := tar.NewReader(&buf)
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
func fixBackupStoragePool(c *db.Cluster, b backupInfo) error {
	// Get the default profile
	_, profile, err := c.ProfileGet("default")
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
		err = f(shared.VarPath("storage-pools", pool.Name, "snapshots", b.Name, snap,
			"backup.yaml"))
		if err != nil {
			return err
		}
	}
	return nil
}

func createBackupIndexFile(container container, backup backup) error {
	pool, err := container.StoragePool()
	if err != nil {
		return err
	}

	file, err := os.Create(filepath.Join(getBackupMountPoint(pool, backup.Name()), "index.yaml"))
	if err != nil {
		return err
	}
	defer file.Close()

	indexFile := backupInfo{
		Name:       container.Name(),
		Backend:    container.Storage().GetStorageTypeName(),
		Privileged: container.IsPrivileged(),
		Pool:       pool,
		Snapshots:  []string{},
	}

	if !backup.ContainerOnly() {
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

	_, err = file.Write(data)
	if err != nil {
		return err
	}

	return nil
}
