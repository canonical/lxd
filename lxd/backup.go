package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"context"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/instancewriter"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

// Create a new backup.
func backupCreate(s *state.State, args db.InstanceBackup, sourceInst instance.Instance) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"project": sourceInst.Project(), "instance": sourceInst.Name(), "name": args.Name})
	logger.Debug("Instance backup started")
	defer logger.Debug("Instance backup finished")

	revert := revert.New()
	defer revert.Fail()

	// Get storage pool.
	pool, err := storagePools.GetPoolByInstance(s, sourceInst)
	if err != nil {
		return errors.Wrap(err, "Load instance storage pool")
	}

	// Ignore requests for optimized backups when pool driver doesn't support it.
	if args.OptimizedStorage && !pool.Driver().Info().OptimizedBackups {
		args.OptimizedStorage = false
	}

	// Create the database entry.
	err = s.Cluster.CreateInstanceBackup(args)
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("Backup %q already exists", args.Name)
		}

		return errors.Wrap(err, "Insert backup info into database")
	}

	revert.Add(func() { s.Cluster.DeleteInstanceBackup(args.Name) })

	// Get the backup struct.
	b, err := instance.BackupLoadByName(s, sourceInst.Project(), args.Name)
	if err != nil {
		return errors.Wrap(err, "Load backup object")
	}

	// Detect compression method.
	var compress string
	b.SetCompressionAlgorithm(args.CompressionAlgorithm)
	if b.CompressionAlgorithm() != "" {
		compress = b.CompressionAlgorithm()
	} else {
		compress, err = cluster.ConfigGetString(s.Cluster, "backups.compression_algorithm")
		if err != nil {
			return err
		}
	}

	// Create the target path if needed.
	backupsPath := shared.VarPath("backups", project.Instance(sourceInst.Project(), sourceInst.Name()))
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}

		revert.Add(func() { os.Remove(backupsPath) })
	}

	target := shared.VarPath("backups", project.Instance(sourceInst.Project(), b.Name()))

	// Setup the tarball writer.
	logger.Debug("Opening backup tarball for writing", log.Ctx{"path": target})
	tarFileWriter, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return errors.Wrapf(err, "Error opening backup tarball for writing %q", target)
	}
	defer tarFileWriter.Close()
	revert.Add(func() { os.Remove(target) })

	// Get IDMap to unshift container as the tarball is created.
	var idmap *idmap.IdmapSet
	if sourceInst.Type() == instancetype.Container {
		c := sourceInst.(instance.Container)
		idmap, err = c.DiskIdmap()
		if err != nil {
			return errors.Wrap(err, "Error getting container IDMAP")
		}
	}

	// Create the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	defer tarPipeWriter.Close() // Ensure that go routine below always ends.
	tarWriter := instancewriter.NewInstanceTarWriter(tarPipeWriter, idmap)

	// Setup tar writer go routine, with optional compression.
	tarWriterRes := make(chan error, 0)
	var compressErr error

	go func(resCh chan<- error) {
		logger.Debug("Started backup tarball writer")
		defer logger.Debug("Finished backup tarball writer")
		if compress != "none" {
			compressErr = compressFile(compress, tarPipeReader, tarFileWriter)

			// If a compression error occurred, close the tarPipeWriter to end the export.
			if compressErr != nil {
				tarPipeWriter.Close()
			}
		} else {
			_, err = io.Copy(tarFileWriter, tarPipeReader)
		}
		resCh <- err
	}(tarWriterRes)

	// Write index file.
	logger.Debug("Adding backup index file")
	err = backupWriteIndex(sourceInst, pool, b.OptimizedStorage(), !b.InstanceOnly(), tarWriter)

	// Check compression errors.
	if compressErr != nil {
		return compressErr
	}

	// Check backupWriteIndex for errors.
	if err != nil {
		return errors.Wrapf(err, "Error writing backup index file")
	}

	err = pool.BackupInstance(sourceInst, tarWriter, b.OptimizedStorage(), !b.InstanceOnly(), nil)
	if err != nil {
		return errors.Wrap(err, "Backup create")
	}

	// Close off the tarball file.
	err = tarWriter.Close()
	if err != nil {
		return errors.Wrap(err, "Error closing tarball writer")
	}

	// Close off the tarball pipe writer (this will end the go routine above).
	err = tarPipeWriter.Close()
	if err != nil {
		return errors.Wrap(err, "Error closing tarball pipe writer")
	}

	err = <-tarWriterRes
	if err != nil {
		return errors.Wrap(err, "Error writing tarball")
	}

	revert.Success()
	backup.Lifecycle(s, sourceInst, args.Name, "created", nil)
	return nil
}

// backupWriteIndex generates an index.yaml file and then writes it to the root of the backup tarball.
func backupWriteIndex(sourceInst instance.Instance, pool storagePools.Pool, optimized bool, snapshots bool, tarWriter *instancewriter.InstanceTarWriter) error {
	// Indicate whether the driver will include a driver-specific optimized header.
	poolDriverOptimizedHeader := false
	if optimized {
		poolDriverOptimizedHeader = pool.Driver().Info().OptimizedBackupHeader
	}

	backupType := backup.InstanceTypeToBackupType(api.InstanceType(sourceInst.Type().String()))
	if backupType == backup.TypeUnknown {
		return fmt.Errorf("Unrecognised instance type for backup type conversion")
	}

	indexInfo := backup.Info{
		Name:             sourceInst.Name(),
		Pool:             pool.Name(),
		Snapshots:        []string{},
		Backend:          pool.Driver().Info().Name,
		Type:             backupType,
		OptimizedStorage: &optimized,
		OptimizedHeader:  &poolDriverOptimizedHeader,
	}

	if snapshots {
		snaps, err := sourceInst.Snapshots()
		if err != nil {
			return err
		}

		for _, snap := range snaps {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
			indexInfo.Snapshots = append(indexInfo.Snapshots, snapName)
		}
	}

	// Convert to YAML.
	indexData, err := yaml.Marshal(&indexInfo)
	if err != nil {
		return err
	}
	r := bytes.NewReader(indexData)

	indexFileInfo := instancewriter.FileInfo{
		FileName:    "backup/index.yaml",
		FileSize:    int64(len(indexData)),
		FileMode:    0644,
		FileModTime: time.Now(),
	}

	// Write to tarball.
	err = tarWriter.WriteFileFromReader(r, &indexFileInfo)
	if err != nil {
		return err
	}

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
	backups, err := d.cluster.GetExpiredInstanceBackups()
	if err != nil {
		return errors.Wrap(err, "Unable to retrieve the list of expired instance backups")
	}

	for _, b := range backups {
		inst, err := instance.LoadByID(d.State(), b.InstanceID)
		if err != nil {
			return errors.Wrapf(err, "Error loading instance for deleting backup %q", b.Name)
		}

		instBackup := backup.NewInstanceBackup(d.State(), inst, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.InstanceOnly, b.OptimizedStorage)
		err = instBackup.Delete()
		if err != nil {
			return errors.Wrapf(err, "Error deleting instance backup %q", b.Name)
		}
	}

	return nil
}
