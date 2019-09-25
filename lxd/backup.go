package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"context"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Create a new backup
func backupCreate(s *state.State, args db.ContainerBackupArgs, sourceContainer instance.Instance) error {
	// Create the database entry
	err := s.Cluster.ContainerBackupCreate(args)
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("backup '%s' already exists", args.Name)
		}

		return errors.Wrap(err, "Insert backup info into database")
	}

	// Get the backup struct
	b, err := instance.BackupLoadByName(s, sourceContainer.Project(), args.Name)
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

func backupGetInfo(r io.ReadSeeker) (*instance.BackupInfo, error) {
	var tr *tar.Reader
	result := instance.BackupInfo{}
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

// fixBackupStoragePool changes the pool information in the backup.yaml. This
// is done only if the provided pool doesn't exist. In this case, the pool of
// the default profile will be used.
func backupFixStoragePool(c *db.Cluster, b instance.BackupInfo, useDefaultPool bool) error {
	var poolName string

	if useDefaultPool {
		// Get the default profile
		_, profile, err := c.ProfileGet("default", "default")
		if err != nil {
			return err
		}

		_, v, err := shared.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return err
		}

		poolName = v["pool"]
	} else {
		poolName = b.Pool
	}

	// Get the default's profile pool
	_, pool, err := c.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	f := func(path string) error {
		// Read in the backup.yaml file.
		backup, err := slurpBackupFile(path)
		if err != nil {
			return err
		}

		rootDiskDeviceFound := false

		// Change the pool in the backup.yaml
		backup.Pool = pool
		if backup.Container.Devices != nil {
			devName, _, err := shared.GetRootDiskDevice(backup.Container.Devices)
			if err == nil {
				backup.Container.Devices[devName]["pool"] = poolName
				rootDiskDeviceFound = true
			}
		}

		if backup.Container.ExpandedDevices != nil {
			devName, _, err := shared.GetRootDiskDevice(backup.Container.ExpandedDevices)
			if err == nil {
				backup.Container.ExpandedDevices[devName]["pool"] = poolName
				rootDiskDeviceFound = true
			}
		}

		if !rootDiskDeviceFound {
			return fmt.Errorf("No root device could be found")
		}

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

func backupCreateTarball(s *state.State, path string, backup instance.Backup) error {
	// Create the index
	pool, err := backup.Instance.StoragePool()
	if err != nil {
		return err
	}

	indexFile := instance.BackupInfo{
		Name:       backup.Instance.Name(),
		Backend:    backup.Instance.Storage().GetStorageTypeName(),
		Privileged: backup.Instance.IsPrivileged(),
		Pool:       pool,
		Snapshots:  []string{},
	}

	if !backup.InstanceOnly {
		snaps, err := backup.Instance.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
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
	backupsPath := shared.VarPath("backups", backup.Instance.Name())
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Create the tarball
	backupPath := shared.VarPath("backups", backup.Name)
	success := false
	defer func() {
		if success {
			return
		}

		os.RemoveAll(backupPath)
	}()

	args := []string{"-cf", backupPath, "--numeric-owner", "--xattrs", "-C", path, "--transform", "s,^./,backup/,", "."}
	_, err = shared.RunCommand("tar", args...)
	if err != nil {
		return err
	}

	err = os.RemoveAll(path)
	if err != nil {
		return err
	}

	// Compress it
	compress, err := cluster.ConfigGetString(s.Cluster, "backups.compression_algorithm")
	if err != nil {
		return err
	}

	if compress != "none" {
		infile, err := os.Open(backupPath)
		if err != nil {
			return err
		}
		defer infile.Close()

		compressed, err := os.Create(backupPath + ".compressed")
		if err != nil {
			return err
		}
		compressedName := compressed.Name()

		defer compressed.Close()
		defer os.Remove(compressedName)

		err = compressFile(compress, infile, compressed)
		if err != nil {
			return err
		}

		err = os.Remove(backupPath)
		if err != nil {
			return err
		}

		err = os.Rename(compressedName, backupPath)
		if err != nil {
			return err
		}
	}

	// Set permissions
	err = os.Chmod(backupPath, 0600)
	if err != nil {
		return err
	}

	success = true
	return nil
}

func pruneExpiredContainerBackupsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operation.Operation) error {
			return pruneExpiredContainerBackups(ctx, d)
		}

		op, err := operation.OperationCreate(d.cluster, "", operation.OperationClassTask, db.OperationBackupsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired backups operation", log.Ctx{"err": err})
			return
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
		containerName, _, _ := shared.ContainerGetParentAndSnapshotName(backup)
		err := instance.DoBackupDelete(d.State(), backup, containerName)
		if err != nil {
			return errors.Wrapf(err, "Error deleting container backup %s", backup)
		}
	}

	return nil
}
