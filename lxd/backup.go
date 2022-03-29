package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"context"

	log "gopkg.in/inconshreveable/log15.v2"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/instancewriter"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/units"
)

// Create a new backup.
func backupCreate(s *state.State, args db.InstanceBackup, sourceInst instance.Instance, op *operations.Operation) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"project": sourceInst.Project(), "instance": sourceInst.Name(), "name": args.Name})
	logger.Debug("Instance backup started")
	defer logger.Debug("Instance backup finished")

	revert := revert.New()
	defer revert.Fail()

	// Get storage pool.
	pool, err := storagePools.LoadByInstance(s, sourceInst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
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

		return fmt.Errorf("Insert backup info into database: %w", err)
	}

	revert.Add(func() { s.Cluster.DeleteInstanceBackup(args.Name) })

	// Get the backup struct.
	b, err := instance.BackupLoadByName(s, sourceInst.Project(), args.Name)
	if err != nil {
		return fmt.Errorf("Load backup object: %w", err)
	}

	// Detect compression method.
	var compress string
	b.SetCompressionAlgorithm(args.CompressionAlgorithm)
	if b.CompressionAlgorithm() != "" {
		compress = b.CompressionAlgorithm()
	} else {
		p, err := s.Cluster.GetProject(sourceInst.Project())
		if err != nil {
			return err
		}

		if p.Config["backups.compression_algorithm"] != "" {
			compress = p.Config["backups.compression_algorithm"]
		} else {
			compress, err = cluster.ConfigGetString(s.Cluster, "backups.compression_algorithm")
			if err != nil {
				return err
			}
		}

	}

	// Create the target path if needed.
	backupsPath := shared.VarPath("backups", "instances", project.Instance(sourceInst.Project(), sourceInst.Name()))
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}

		revert.Add(func() { os.Remove(backupsPath) })
	}

	target := shared.VarPath("backups", "instances", project.Instance(sourceInst.Project(), b.Name()))

	// Setup the tarball writer.
	logger.Debug("Opening backup tarball for writing", log.Ctx{"path": target})
	tarFileWriter, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("Error opening backup tarball for writing %q: %w", target, err)
	}
	defer tarFileWriter.Close()
	revert.Add(func() { os.Remove(target) })

	// Get IDMap to unshift container as the tarball is created.
	var idmap *idmap.IdmapSet
	if sourceInst.Type() == instancetype.Container {
		c := sourceInst.(instance.Container)
		idmap, err = c.DiskIdmap()
		if err != nil {
			return fmt.Errorf("Error getting container IDMAP: %w", err)
		}
	}

	// Create the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	defer tarPipeWriter.Close() // Ensure that go routine below always ends.
	tarWriter := instancewriter.NewInstanceTarWriter(tarPipeWriter, idmap)

	// Setup tar writer go routine, with optional compression.
	tarWriterRes := make(chan error, 0)
	var compressErr error

	backupProgressWriter := &ioprogress.ProgressWriter{
		Tracker: &ioprogress.ProgressTracker{
			Handler: func(value, speed int64) {
				meta := op.Metadata()
				if meta == nil {
					meta = make(map[string]interface{})
				}

				progressText := fmt.Sprintf("%s (%s/s)", units.GetByteSizeString(value, 2), units.GetByteSizeString(speed, 2))
				meta["create_backup_progress"] = progressText
				op.UpdateMetadata(meta)
			},
		},
	}

	go func(resCh chan<- error) {
		logger.Debug("Started backup tarball writer")
		defer logger.Debug("Finished backup tarball writer")
		if compress != "none" {
			backupProgressWriter.WriteCloser = tarFileWriter
			compressErr = compressFile(compress, tarPipeReader, backupProgressWriter)

			// If a compression error occurred, close the tarPipeWriter to end the export.
			if compressErr != nil {
				tarPipeWriter.Close()
			}
		} else {
			backupProgressWriter.WriteCloser = tarFileWriter
			_, err = io.Copy(backupProgressWriter, tarPipeReader)
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
		return fmt.Errorf("Error writing backup index file: %w", err)
	}

	err = pool.BackupInstance(sourceInst, tarWriter, b.OptimizedStorage(), !b.InstanceOnly(), nil)
	if err != nil {
		return fmt.Errorf("Backup create: %w", err)
	}

	// Close off the tarball file.
	err = tarWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball writer: %w", err)
	}

	// Close off the tarball pipe writer (this will end the go routine above).
	err = tarPipeWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball pipe writer: %w", err)
	}

	err = <-tarWriterRes
	if err != nil {
		return fmt.Errorf("Error writing tarball: %w", err)
	}

	revert.Success()
	s.Events.SendLifecycle(sourceInst.Project(), lifecycle.InstanceBackupCreated.Event(args.Name, b.Instance(), nil))

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

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationBackupsExpire, nil, nil, opRun, nil, nil, nil)
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
		return fmt.Errorf("Unable to retrieve the list of expired instance backups: %w", err)
	}

	for _, b := range backups {
		inst, err := instance.LoadByID(d.State(), b.InstanceID)
		if err != nil {
			return fmt.Errorf("Error loading instance for deleting backup %q: %w", b.Name, err)
		}

		instBackup := backup.NewInstanceBackup(d.State(), inst, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.InstanceOnly, b.OptimizedStorage)
		err = instBackup.Delete()
		if err != nil {
			return fmt.Errorf("Error deleting instance backup %q: %w", b.Name, err)
		}
	}

	return nil
}

func volumeBackupCreate(s *state.State, args db.StoragePoolVolumeBackup, projectName string, poolName string, volumeName string) error {
	logger := logging.AddContext(logger.Log, log.Ctx{"project": projectName, "storage_volume": volumeName, "name": args.Name})
	logger.Debug("Volume backup started")
	defer logger.Debug("Volume backup finished")

	revert := revert.New()
	defer revert.Fail()

	// Get storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
	}

	_, vol, err := s.Cluster.GetLocalStoragePoolVolume(projectName, volumeName, db.StoragePoolVolumeTypeCustom, pool.ID())
	if err != nil {
		return fmt.Errorf("Failed loading custom volume %q: %w", volumeName, err)
	}

	// Ignore requests for optimized backups when pool driver doesn't support it.
	if args.OptimizedStorage && !pool.Driver().Info().OptimizedBackups {
		args.OptimizedStorage = false
	}

	// Create the database entry.
	err = s.Cluster.CreateStoragePoolVolumeBackup(args)
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("Backup %q already exists", args.Name)
		}

		return fmt.Errorf("Failed creating backup record: %w", err)
	}

	revert.Add(func() { s.Cluster.DeleteStoragePoolVolumeBackup(args.Name) })

	backupRow, err := s.Cluster.GetStoragePoolVolumeBackup(projectName, poolName, args.Name)
	if err != nil {
		return fmt.Errorf("Failed getting backup record: %w", err)
	}

	// Detect compression method.
	var compress string

	backupRow.CompressionAlgorithm = args.CompressionAlgorithm

	if backupRow.CompressionAlgorithm != "" {
		compress = backupRow.CompressionAlgorithm
	} else {
		compress, err = cluster.ConfigGetString(s.Cluster, "backups.compression_algorithm")
		if err != nil {
			return err
		}
	}

	// Create the target path if needed.
	backupsPath := shared.VarPath("backups", "custom", pool.Name(), project.StorageVolume(projectName, volumeName))
	if !shared.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}

		revert.Add(func() { os.Remove(backupsPath) })
	}

	target := shared.VarPath("backups", "custom", pool.Name(), project.StorageVolume(projectName, backupRow.Name))

	// Setup the tarball writer.
	logger.Debug("Opening backup tarball for writing", log.Ctx{"path": target})
	tarFileWriter, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("Error opening backup tarball for writing %q: %w", target, err)
	}
	defer tarFileWriter.Close()
	revert.Add(func() { os.Remove(target) })

	// Create the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	defer tarPipeWriter.Close() // Ensure that go routine below always ends.
	tarWriter := instancewriter.NewInstanceTarWriter(tarPipeWriter, nil)

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
	err = volumeBackupWriteIndex(s, projectName, vol, pool, backupRow.OptimizedStorage, !backupRow.VolumeOnly, tarWriter)

	// Check compression errors.
	if compressErr != nil {
		return compressErr
	}

	// Check backupWriteIndex for errors.
	if err != nil {
		return fmt.Errorf("Error writing backup index file: %w", err)
	}

	err = pool.BackupCustomVolume(projectName, volumeName, tarWriter, backupRow.OptimizedStorage, !backupRow.VolumeOnly, nil)
	if err != nil {
		return fmt.Errorf("Backup create: %w", err)
	}

	// Close off the tarball file.
	err = tarWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball writer: %w", err)
	}

	// Close off the tarball pipe writer (this will end the go routine above).
	err = tarPipeWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball pipe writer: %w", err)
	}

	err = <-tarWriterRes
	if err != nil {
		return fmt.Errorf("Error writing tarball: %w", err)
	}

	revert.Success()
	return nil
}

// volumeBackupWriteIndex generates an index.yaml file and then writes it to the root of the backup tarball.
func volumeBackupWriteIndex(s *state.State, projectName string, vol *api.StorageVolume, pool storagePools.Pool, optimized bool, snapshots bool, tarWriter *instancewriter.InstanceTarWriter) error {
	if vol.Type != db.StoragePoolVolumeTypeNameCustom {
		return fmt.Errorf("Unsupported volume type %q", vol.Type)
	}

	// Indicate whether the driver will include a driver-specific optimized header.
	poolDriverOptimizedHeader := false
	if optimized {
		poolDriverOptimizedHeader = pool.Driver().Info().OptimizedBackupHeader
	}

	indexInfo := backup.Info{
		Name:             vol.Name,
		Pool:             pool.Name(),
		Snapshots:        []string{},
		Backend:          pool.Driver().Info().Name,
		OptimizedStorage: &optimized,
		OptimizedHeader:  &poolDriverOptimizedHeader,
		Type:             backup.TypeCustom,
		Config: &backup.Config{
			Volume: vol,
		},
	}

	if snapshots {
		volID, err := s.Cluster.GetStoragePoolNodeVolumeID(projectName, vol.Name, db.StoragePoolVolumeTypeCustom, pool.ID())
		if err != nil {
			return err
		}

		snaps, err := s.Cluster.GetStorageVolumeSnapshotsNames(volID)
		if err != nil {
			return err
		}

		for _, snapName := range snaps {
			snapVolName := storageDrivers.GetSnapshotVolumeName(vol.Name, snapName)
			snapVolID, snapVol, err := s.Cluster.GetLocalStoragePoolVolume(projectName, snapVolName, db.StoragePoolVolumeTypeCustom, pool.ID())
			if err != nil {
				return fmt.Errorf("Failed loading custom volume snapshot %q: %w", snapVolName, err)
			}

			indexInfo.Snapshots = append(indexInfo.Snapshots, snapName)

			snapExpiry, err := s.Cluster.GetStorageVolumeSnapshotExpiry(snapVolID)
			if err != nil {
				return fmt.Errorf("Failed loading custom volume snapshot expiry for %q: %w", snapVolName, err)
			}

			snapshot := api.StorageVolumeSnapshot{}
			snapshot.Config = snapVol.Config
			snapshot.Description = snapVol.Description
			snapshot.Name = snapName // Snapshot only name, not full name.
			snapshot.ExpiresAt = &snapExpiry
			snapshot.ContentType = snapVol.ContentType

			indexInfo.Config.VolumeSnapshots = append(indexInfo.Config.VolumeSnapshots, &snapshot)
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
