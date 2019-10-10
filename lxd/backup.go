package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"context"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Create a new backup
func backupCreate(s *state.State, args db.InstanceBackupArgs, sourceContainer Instance) error {
	// Create the database entry
	err := s.Cluster.ContainerBackupCreate(args)
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("backup '%s' already exists", args.Name)
		}

		return errors.Wrap(err, "Insert backup info into database")
	}

	// Get the backup struct
	b, err := backup.LoadByName(s, sourceContainer.Project(), args.Name)
	if err != nil {
		return errors.Wrap(err, "Load backup object")
	}

	b.SetCompressionAlgorithm(args.CompressionAlgorithm)

	ourStart, err := sourceContainer.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer sourceContainer.StorageStop()
	}

	// Create a temporary path for the backup
	tmpPath, err := ioutil.TempDir(shared.VarPath("backups"), "lxd_backup_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpPath)

	// Now create the empty snapshot
	err = sourceContainer.Storage().ContainerBackupCreate(tmpPath, *b, sourceContainer)
	if err != nil {
		s.Cluster.ContainerBackupRemove(args.Name)
		return errors.Wrap(err, "Backup storage")
	}

	// Pack the backup
	return backupCreateTarball(s, tmpPath, *b, sourceContainer)
}

// fixBackupStoragePool changes the pool information in the backup.yaml. This
// is done only if the provided pool doesn't exist. In this case, the pool of
// the default profile will be used.
func backupFixStoragePool(c *db.Cluster, b backup.Info, useDefaultPool bool) error {
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

	err = f(shared.VarPath("storage-pools", pool.Name, "containers", project.Prefix(b.Project, b.Name), "backup.yaml"))
	if err != nil {
		return err
	}

	for _, snap := range b.Snapshots {
		err = f(shared.VarPath("storage-pools", pool.Name, "containers-snapshots", project.Prefix(b.Project, b.Name), snap,
			"backup.yaml"))
		if err != nil {
			return err
		}
	}
	return nil
}

func backupCreateTarball(s *state.State, path string, b backup.Backup, c Instance) error {
	// Create the index
	pool, err := c.StoragePool()
	if err != nil {
		return err
	}

	indexFile := backup.Info{
		Name:       c.Name(),
		Backend:    c.Storage().GetStorageTypeName(),
		Privileged: c.IsPrivileged(),
		Pool:       pool,
		Snapshots:  []string{},
	}

	if !b.InstanceOnly() {
		snaps, err := c.Snapshots()
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
	backupsPath := shared.VarPath("backups", project.Prefix(c.Project(), c.Name()))
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Create the tarball
	backupPath := shared.VarPath("backups", project.Prefix(c.Project(), b.Name()))
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

	var compress string

	if b.CompressionAlgorithm() != "" {
		compress = b.CompressionAlgorithm()
	} else {
		compress, err = cluster.ConfigGetString(s.Cluster, "backups.compression_algorithm")
		if err != nil {
			return err
		}
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
		opRun := func(op *operations.Operation) error {
			return pruneExpiredContainerBackups(ctx, d)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationBackupsExpire, nil, nil, opRun, nil, nil)
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

	for _, b := range backups {
		inst, err := instanceLoadById(d.State(), b.InstanceID)
		if err != nil {
			return errors.Wrapf(err, "Error deleting container backup %s", b.Name)
		}

		err = backup.DoBackupDelete(d.State(), inst.Project(), b.Name, inst.Name())
		if err != nil {
			return errors.Wrapf(err, "Error deleting container backup %s", b.Name)
		}
	}

	return nil
}
