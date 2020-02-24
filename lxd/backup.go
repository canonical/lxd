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
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Create a new backup.
func backupCreate(s *state.State, args db.InstanceBackupArgs, sourceInst instance.Instance) error {
	// Create the database entry.
	err := s.Cluster.ContainerBackupCreate(args)
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("backup '%s' already exists", args.Name)
		}

		return errors.Wrap(err, "Insert backup info into database")
	}

	revert := true
	defer func() {
		if !revert {
			return
		}
		s.Cluster.ContainerBackupRemove(args.Name)
	}()

	// Get the backup struct.
	b, err := instance.BackupLoadByName(s, sourceInst.Project(), args.Name)
	if err != nil {
		return errors.Wrap(err, "Load backup object")
	}

	b.SetCompressionAlgorithm(args.CompressionAlgorithm)

	// Create a temporary path for the backup.
	tmpPath, err := ioutil.TempDir(shared.VarPath("backups"), "lxd_backup_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpPath)

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByInstance(s, sourceInst)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented {
		if err != nil {
			return errors.Wrap(err, "Load instance storage pool")
		}

		err = pool.BackupInstance(sourceInst, tmpPath, b.OptimizedStorage(), !b.InstanceOnly(), nil)
		if err != nil {
			return errors.Wrap(err, "Backup create")
		}
	} else if sourceInst.Type() == instancetype.Container {
		ourStart, err := sourceInst.StorageStart()
		if err != nil {
			return err
		}
		if ourStart {
			defer sourceInst.StorageStop()
		}

		ct := sourceInst.(*containerLXC)
		err = ct.Storage().ContainerBackupCreate(tmpPath, *b, sourceInst)
		if err != nil {
			return errors.Wrap(err, "Backup create")
		}
	} else {
		return fmt.Errorf("Instance type not supported")
	}

	// Pack the backup.
	err = backupCreateTarball(s, tmpPath, *b, sourceInst)
	if err != nil {
		return err
	}

	revert = false
	return nil
}

func backupCreateTarball(s *state.State, path string, b backup.Backup, c instance.Instance) error {
	// Create the index
	poolName, err := c.StoragePool()
	if err != nil {
		return err
	}

	if c.Type() != instancetype.Container {
		return fmt.Errorf("Instance type must be container")
	}

	indexFile := backup.Info{
		Name:       c.Name(),
		Privileged: c.IsPrivileged(),
		Pool:       poolName,
		Snapshots:  []string{},
	}

	pool, err := storagePools.GetPoolByInstance(s, c)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented && err != db.ErrNoSuchObject {
		if err != nil {
			return err
		}

		info := pool.Driver().Info()
		indexFile.Backend = info.Name
	} else {
		ct := c.(*containerLXC)
		indexFile.Backend = ct.Storage().GetStorageTypeName()
	}

	if !b.InstanceOnly() {
		snaps, err := c.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
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

	args := []string{"-cf", backupPath, "--numeric-owner", "--xattrs", "-C", path, "--transform", "s,^./,backup/,S", "."}
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
		inst, err := instance.LoadByID(d.State(), b.InstanceID)
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
