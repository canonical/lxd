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

	pool, err := storagePools.GetPoolByInstance(s, sourceInst)
	if err != nil {
		return errors.Wrap(err, "Load instance storage pool")
	}

	err = pool.BackupInstance(sourceInst, tmpPath, b.OptimizedStorage(), !b.InstanceOnly(), nil)
	if err != nil {
		return errors.Wrap(err, "Backup create")
	}

	// Pack the backup.
	err = backupCreateTarball(s, tmpPath, *b, sourceInst)
	if err != nil {
		return err
	}

	revert = false
	return nil
}

func pruneExpiredContainerBackupsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operations.Operation) error {
			return pruneExpiredContainerBackups(ctx, d)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationBackupsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired instance backups operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired instance backups")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to expire instance backups", log.Ctx{"err": err})
		}
		logger.Info("Done pruning expired instance backups")
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
		return errors.Wrap(err, "Unable to retrieve the list of expired instance backups")
	}

	for _, b := range backups {
		inst, err := instance.LoadByID(d.State(), b.InstanceID)
		if err != nil {
			return errors.Wrapf(err, "Error deleting instance backup %s", b.Name)
		}

		err = backup.DoBackupDelete(d.State(), inst.Project(), b.Name, inst.Name())
		if err != nil {
			return errors.Wrapf(err, "Error deleting instance backup %s", b.Name)
		}
	}

	return nil
}
