package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

func newStorageMigrationSource(volumeOnly bool, pushTarget *api.StorageVolumePostTarget) (*migrationSourceWs, error) {
	ret := migrationSourceWs{
		migrationFields: migrationFields{},
	}

	if pushTarget != nil {
		ret.pushCertificate = pushTarget.Certificate
		ret.pushOperationURL = pushTarget.Operation
		ret.pushSecrets = pushTarget.Websockets
	}

	ret.volumeOnly = volumeOnly

	secretNames := []string{api.SecretNameControl, api.SecretNameFilesystem}
	ret.conns = make(map[string]*migrationConn, len(secretNames))
	for _, connName := range secretNames {
		if ret.pushOperationURL != "" {
			if ret.pushSecrets[connName] == "" {
				return nil, fmt.Errorf("Expected %q connection secret missing from migration source target request", connName)
			}

			dialer, err := setupWebsocketDialer(ret.pushCertificate)
			if err != nil {
				return nil, fmt.Errorf("Failed setting up websocket dialer for migration source %q connection: %w", connName, err)
			}

			u, err := url.Parse(fmt.Sprintf("wss://%s/websocket", strings.TrimPrefix(ret.pushOperationURL, "https://")))
			if err != nil {
				return nil, fmt.Errorf("Failed parsing websocket URL for migration source %q connection: %w", connName, err)
			}

			ret.conns[connName] = newMigrationConn(ret.pushSecrets[connName], dialer, u)
		} else {
			secret, err := shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration source secret for %q connection: %w", connName, err)
			}

			ret.conns[connName] = newMigrationConn(secret, nil, nil)
		}
	}

	return &ret, nil
}

// DoStorage handles the migration of a storage volume from the source to the target.
// It waits for migration connections, negotiates migration types, and initiates
// the volume transfer.
func (s *migrationSourceWs) DoStorage(state *state.State, projectName string, poolName string, volName string, migrateOp *operations.Operation) error {
	l := logger.AddContext(logger.Ctx{"project": projectName, "pool": poolName, "volume": volName, "push": s.pushOperationURL != ""})

	ctx, cancel := context.WithTimeout(state.ShutdownCtx, time.Second*10)
	defer cancel()

	l.Info("Waiting for migration connections on source")

	for _, connName := range []string{api.SecretNameControl, api.SecretNameFilesystem} {
		_, err := s.conns[connName].WebSocket(ctx)
		if err != nil {
			return fmt.Errorf("Failed waiting for migration %q connection on source: %w", connName, err)
		}
	}

	l.Info("Migration channels connected on source")

	defer l.Info("Migration channels disconnected on source")
	defer s.disconnect()

	var poolMigrationTypes []migration.Type

	pool, err := storagePools.LoadByName(state, poolName)
	if err != nil {
		return err
	}

	srcConfig, err := pool.GenerateCustomVolumeBackupConfig(projectName, volName, !s.volumeOnly, migrateOp)
	if err != nil {
		return fmt.Errorf("Failed generating migration config of volume %q in pool %q and project %q: %w", volName, poolName, projectName, err)
	}

	customVol, err := srcConfig.CustomVolume()
	if err != nil {
		return fmt.Errorf("Failed getting the custom volume: %w", err)
	}

	// The refresh argument passed to MigrationTypes() is always set
	// to false here. The migration source/sender doesn't need to care whether
	// or not it's doing a refresh as the migration sink/receiver will know
	// this, and adjust the migration types accordingly.
	poolMigrationTypes = pool.MigrationTypes(storageDrivers.ContentType(customVol.ContentType), false, !s.volumeOnly)
	if len(poolMigrationTypes) == 0 {
		return errors.New("No source migration types available")
	}

	// Convert the pool's migration type options to an offer header to target.
	offerHeader := migration.TypesToHeader(poolMigrationTypes...)

	// Offer to send index header.
	indexHeaderVersion := migration.IndexHeaderVersion
	offerHeader.IndexHeaderVersion = &indexHeaderVersion

	// Only send snapshots when requested.
	if !s.volumeOnly {
		offerHeader.Snapshots = make([]*migration.Snapshot, 0, len(customVol.Snapshots))
		offerHeader.SnapshotNames = make([]string, 0, len(customVol.Snapshots))

		for i := range customVol.Snapshots {
			offerHeader.SnapshotNames = append(offerHeader.SnapshotNames, customVol.Snapshots[i].Name)
			offerHeader.Snapshots = append(offerHeader.Snapshots, volumeSnapshotToProtobuf(customVol.Snapshots[i]))
		}
	}

	// Send offer to target.
	err = s.send(offerHeader)
	if err != nil {
		logger.Error("Failed to send storage volume migration header")
		s.sendControl(err)
		return err
	}

	// Receive response from target.
	respHeader := &migration.MigrationHeader{}
	err = s.recv(respHeader)
	if err != nil {
		logger.Error("Failed to receive storage volume migration header")
		s.sendControl(err)
		return err
	}

	migrationTypes, err := migration.MatchTypes(respHeader, storagePools.FallbackMigrationType(storageDrivers.ContentType(customVol.ContentType)), poolMigrationTypes)
	if err != nil {
		logger.Errorf("Failed to negotiate migration type: %v", err)
		s.sendControl(err)
		return err
	}

	volSourceArgs := &migration.VolumeSourceArgs{
		IndexHeaderVersion: respHeader.GetIndexHeaderVersion(), // Enable index header frame if supported.
		Name:               customVol.Name,
		MigrationType:      migrationTypes[0],
		Snapshots:          offerHeader.SnapshotNames,
		TrackProgress:      true,
		ContentType:        customVol.ContentType,
		Info:               &migration.Info{Config: srcConfig},
		VolumeOnly:         s.volumeOnly,
	}

	// Only send the snapshots that the target requests when refreshing.
	if respHeader.GetRefresh() {
		volSourceArgs.Refresh = true
		volSourceArgs.Snapshots = respHeader.GetSnapshotNames()
		allSnapshots := customVol.Snapshots

		// Ensure that only the requested snapshots are included in the migration index header.
		customVol.Snapshots = make([]*api.StorageVolumeSnapshot, 0, len(volSourceArgs.Snapshots))
		for i := range allSnapshots {
			if slices.Contains(volSourceArgs.Snapshots, allSnapshots[i].Name) {
				customVol.Snapshots = append(customVol.Snapshots, allSnapshots[i])
			}
		}
	}

	fsConn, err := s.conns[api.SecretNameFilesystem].WebsocketIO(state.ShutdownCtx)
	if err != nil {
		return err
	}

	err = pool.MigrateCustomVolume(projectName, fsConn, volSourceArgs, migrateOp)
	if err != nil {
		s.sendControl(err)
		return err
	}

	msg := migration.MigrationControl{}
	err = s.recv(&msg)
	if err != nil {
		logger.Error("Failed to receive storage volume migration control message")
		return err
	}

	if !msg.GetSuccess() {
		logger.Error("Failed to send storage volume")
		return errors.New(msg.GetMessage())
	}

	logger.Debug("Migration source finished transferring storage volume")
	return nil
}

func newStorageMigrationSink(args *migrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		migrationFields: migrationFields{
			volumeOnly: args.volumeOnly,
		},
		url:     args.url,
		push:    args.push,
		refresh: args.refresh,
	}

	secretNames := []string{api.SecretNameControl, api.SecretNameFilesystem}
	sink.conns = make(map[string]*migrationConn, len(secretNames))
	for _, connName := range secretNames {
		if !sink.push {
			if args.secrets[connName] == "" {
				return nil, fmt.Errorf("Expected %q connection secret missing from migration sink target request", connName)
			}

			u, err := url.Parse(fmt.Sprintf("wss://%s/websocket", strings.TrimPrefix(args.url, "https://")))
			if err != nil {
				return nil, fmt.Errorf("Failed parsing websocket URL for migration sink %q connection: %w", connName, err)
			}

			sink.conns[connName] = newMigrationConn(args.secrets[connName], args.dialer, u)
		} else {
			secret, err := shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration sink secret for %q connection: %w", connName, err)
			}

			sink.conns[connName] = newMigrationConn(secret, nil, nil)
		}
	}

	return &sink, nil
}

// DoStorage handles the storage volume migration on the target side. It waits for
// migration connections, negotiates migration types, and initiates the volume reception.
func (c *migrationSink) DoStorage(state *state.State, projectName string, poolName string, req *api.StorageVolumesPost, op *operations.Operation) error {
	l := logger.AddContext(logger.Ctx{"project": projectName, "pool": poolName, "volume": req.Name, "push": c.push})

	ctx, cancel := context.WithTimeout(state.ShutdownCtx, time.Second*10)
	defer cancel()

	l.Info("Waiting for migration connections on target")

	for _, connName := range []string{api.SecretNameControl, api.SecretNameFilesystem} {
		_, err := c.conns[connName].WebSocket(ctx)
		if err != nil {
			return fmt.Errorf("Failed waiting for migration %q connection on target: %w", connName, err)
		}
	}

	l.Info("Migration channels connected on target")

	defer l.Info("Migration channels disconnected on target")

	if c.push {
		defer c.disconnect()
	}

	offerHeader := &migration.MigrationHeader{}
	err := c.recv(offerHeader)
	if err != nil {
		logger.Error("Failed to receive storage volume migration header")
		c.sendControl(err)
		return err
	}

	// The function that will be executed to receive the sender's migration data.
	var myTarget func(conn io.ReadWriteCloser, op *operations.Operation, args migrationSinkArgs) error

	pool, err := storagePools.LoadByName(state, poolName)
	if err != nil {
		return err
	}

	dbContentType, err := cluster.StoragePoolVolumeContentTypeFromName(req.ContentType)
	if err != nil {
		return err
	}

	contentType := storagePools.VolumeDBContentTypeToContentType(dbContentType)

	// The source/sender will never set Refresh. However, to determine the correct migration type
	// Refresh needs to be set.
	offerHeader.Refresh = &c.refresh

	// Extract the source's migration type and then match it against our pool's
	// supported types and features. If a match is found the combined features list
	// will be sent back to requester.
	respTypes, err := migration.MatchTypes(offerHeader, storagePools.FallbackMigrationType(contentType), pool.MigrationTypes(contentType, c.refresh, !c.volumeOnly))
	if err != nil {
		return err
	}

	// The migration header to be sent back to source with our target options.
	// Convert response type to response header and copy snapshot info into it.
	respHeader := migration.TypesToHeader(respTypes...)

	// Respond with our maximum supported header version if the requested version is higher than ours.
	// Otherwise just return the requested header version to the source.
	indexHeaderVersion := min(offerHeader.GetIndexHeaderVersion(), migration.IndexHeaderVersion)

	respHeader.IndexHeaderVersion = &indexHeaderVersion
	respHeader.SnapshotNames = offerHeader.SnapshotNames
	respHeader.Snapshots = offerHeader.Snapshots
	respHeader.Refresh = &c.refresh

	// Translate the legacy MigrationSinkArgs to a VolumeTargetArgs suitable for use
	// with the new storage layer.
	myTarget = func(conn io.ReadWriteCloser, op *operations.Operation, args migrationSinkArgs) error {
		volTargetArgs := migration.VolumeTargetArgs{
			IndexHeaderVersion: respHeader.GetIndexHeaderVersion(),
			Name:               req.Name,
			Config:             req.Config,
			Description:        req.Description,
			MigrationType:      respTypes[0],
			TrackProgress:      true,
			ContentType:        req.ContentType,
			Refresh:            args.refresh,
			VolumeOnly:         args.volumeOnly,
		}

		// A zero length Snapshots slice indicates volume only migration in
		// VolumeTargetArgs. So if VoluneOnly was requested, do not populate them.
		if !args.volumeOnly {
			volTargetArgs.Snapshots = make([]string, 0, len(args.snapshots))
			for _, snap := range args.snapshots {
				volTargetArgs.Snapshots = append(volTargetArgs.Snapshots, *snap.Name)
			}
		}

		return pool.CreateCustomVolumeFromMigration(projectName, conn, volTargetArgs, op)
	}

	if c.refresh {
		// Get the remote snapshots on the source.
		sourceSnapshots := offerHeader.GetSnapshots()
		sourceSnapshotComparable := make([]storagePools.ComparableSnapshot, 0, len(sourceSnapshots))
		for _, sourceSnap := range sourceSnapshots {
			sourceSnapshotComparable = append(sourceSnapshotComparable, storagePools.ComparableSnapshot{
				Name:         sourceSnap.GetName(),
				CreationDate: time.Unix(sourceSnap.GetCreationDate(), 0),
			})
		}

		// Get existing snapshots on the local target.
		targetSnapshots, err := storagePools.VolumeDBSnapshotsGet(pool, projectName, req.Name, storageDrivers.VolumeTypeCustom)
		if err != nil {
			c.sendControl(err)
			return err
		}

		targetSnapshotsComparable := make([]storagePools.ComparableSnapshot, 0, len(targetSnapshots))
		for _, targetSnap := range targetSnapshots {
			_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name)

			targetSnapshotsComparable = append(targetSnapshotsComparable, storagePools.ComparableSnapshot{
				Name: targetSnapName,

				// The list of source snapshots from the offer header
				// contains the creation timestamps in seconds granularity.
				// Also use second based granularity for the target snapshots to be able to compare them.
				// They are stored with nanoseconds in the database.
				// Retrieve the timestamp using second based granularity the same way as it's done on the source.
				CreationDate: time.Unix(targetSnap.CreationDate.Unix(), 0),
			})
		}

		// Compare the two sets.
		syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := storagePools.CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

		// Delete the extra local snapshots first.
		for _, deleteTargetSnapshotIndex := range deleteTargetSnapshotIndexes {
			err := pool.DeleteCustomVolumeSnapshot(projectName, targetSnapshots[deleteTargetSnapshotIndex].Name, op)
			if err != nil {
				c.sendControl(err)
				return err
			}
		}

		// Only request to send the snapshots that need updating.
		syncSnapshotNames := make([]string, 0, len(syncSourceSnapshotIndexes))
		syncSnapshots := make([]*migration.Snapshot, 0, len(syncSourceSnapshotIndexes))
		for _, syncSourceSnapshotIndex := range syncSourceSnapshotIndexes {
			syncSnapshotNames = append(syncSnapshotNames, sourceSnapshots[syncSourceSnapshotIndex].GetName())
			syncSnapshots = append(syncSnapshots, sourceSnapshots[syncSourceSnapshotIndex])
		}

		respHeader.Snapshots = syncSnapshots
		respHeader.SnapshotNames = syncSnapshotNames
		offerHeader.Snapshots = syncSnapshots
		offerHeader.SnapshotNames = syncSnapshotNames
	}

	err = c.send(respHeader)
	if err != nil {
		logger.Error("Failed to send storage volume migration header")
		c.sendControl(err)
		return err
	}

	restore := make(chan error)

	go func(c *migrationSink) {
		// We do the fs receive in parallel so we don't have to reason about when to receive
		// what. The sending side is smart enough to send the filesystem bits that it can
		// before it seizes the container to start checkpointing, so the total transfer time
		// will be minimized even if we're dumb here.
		fsTransfer := make(chan error)

		go func() {
			// Get rsync options from sender, these are passed into mySink function
			// as part of MigrationSinkArgs below.
			rsyncFeatures := respHeader.GetRsyncFeaturesSlice()
			args := migrationSinkArgs{
				rsyncFeatures: rsyncFeatures,
				snapshots:     respHeader.Snapshots,
				volumeOnly:    c.volumeOnly,
				refresh:       c.refresh,
			}

			fsConn, err := c.conns[api.SecretNameFilesystem].WebsocketIO(state.ShutdownCtx)
			if err != nil {
				fsTransfer <- err
				return
			}

			err = myTarget(fsConn, op, args)
			if err != nil {
				fsTransfer <- err
				return
			}

			fsTransfer <- nil
		}()

		err := <-fsTransfer
		if err != nil {
			restore <- err
			return
		}

		restore <- nil
	}(c)

	for {
		select {
		case err = <-restore:
			if err != nil {
				c.disconnect()
				return err
			}

			c.sendControl(nil)
			logger.Debug("Migration sink finished receiving storage volume")

			return nil
		case msg := <-c.controlChannel():
			if msg.Err != nil {
				c.disconnect()

				return fmt.Errorf("Got error reading migration source: %w", msg.Err)
			}

			if !msg.GetSuccess() {
				c.disconnect()

				return errors.New(msg.GetMessage())
			}

			// The source can only tell us it failed (e.g. if
			// checkpointing failed). We have to tell the source
			// whether or not the restore was successful.
			logger.Warn("Unknown message from migration source", logger.Ctx{"message": msg.GetMessage()})
		}
	}
}

func volumeSnapshotToProtobuf(vol *api.StorageVolumeSnapshot) *migration.Snapshot {
	config := []*migration.Config{}
	for k, v := range vol.Config {
		kCopy := string(k)
		vCopy := string(v)
		config = append(config, &migration.Config{Key: &kCopy, Value: &vCopy})
	}

	return &migration.Snapshot{
		Name:         &vol.Name,
		LocalConfig:  config,
		Profiles:     []string{},
		Ephemeral:    proto.Bool(false),
		LocalDevices: []*migration.Device{},
		Architecture: proto.Int32(0),
		Stateful:     proto.Bool(false),
		CreationDate: proto.Int64(vol.CreatedAt.Unix()),
		LastUsedDate: proto.Int64(0),
		ExpiryDate:   proto.Int64(0),
	}
}
