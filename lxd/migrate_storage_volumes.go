package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/logger"
)

func newStorageMigrationSource(volumeOnly bool) (*migrationSourceWs, error) {
	ret := migrationSourceWs{
		migrationFields: migrationFields{},
		allConnected:    cancel.New(context.Background()),
	}

	ret.volumeOnly = volumeOnly

	var err error
	ret.controlSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, fmt.Errorf("Failed creating migration source secret for control websocket: %w", err)
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, fmt.Errorf("Failed creating migration source secret for filesystem websocket: %w", err)
	}

	return &ret, nil
}

func (s *migrationSourceWs) DoStorage(state *state.State, projectName string, poolName string, volName string, migrateOp *operations.Operation) error {
	logger.Info("Waiting for migration channel connections")
	select {
	case <-time.After(time.Second * 10):
		s.allConnected.Cancel()
		return fmt.Errorf("Timed out waiting for connections")
	case <-s.allConnected.Done():
	}

	logger.Info("Migration channels connected")

	defer s.disconnect()

	var poolMigrationTypes []migration.Type

	pool, err := storagePools.LoadByName(state, poolName)
	if err != nil {
		return err
	}

	srcConfig, err := pool.GenerateCustomVolumeBackupConfig(projectName, volName, !s.volumeOnly, migrateOp)
	if err != nil {
		return fmt.Errorf("Failed generating volume migration config: %w", err)
	}

	// The refresh argument passed to MigrationTypes() is always set
	// to false here. The migration source/sender doesn't need to care whether
	// or not it's doing a refresh as the migration sink/receiver will know
	// this, and adjust the migration types accordingly.
	poolMigrationTypes = pool.MigrationTypes(storageDrivers.ContentType(srcConfig.Volume.ContentType), false, !s.volumeOnly)
	if len(poolMigrationTypes) == 0 {
		return fmt.Errorf("No source migration types available")
	}

	// Convert the pool's migration type options to an offer header to target.
	offerHeader := migration.TypesToHeader(poolMigrationTypes...)

	// Offer to send index header.
	indexHeaderVersion := migration.IndexHeaderVersion
	offerHeader.IndexHeaderVersion = &indexHeaderVersion

	// Only send snapshots when requested.
	if !s.volumeOnly {
		offerHeader.Snapshots = make([]*migration.Snapshot, 0, len(srcConfig.VolumeSnapshots))
		offerHeader.SnapshotNames = make([]string, 0, len(srcConfig.VolumeSnapshots))

		for i := range srcConfig.VolumeSnapshots {
			offerHeader.SnapshotNames = append(offerHeader.SnapshotNames, srcConfig.VolumeSnapshots[i].Name)
			offerHeader.Snapshots = append(offerHeader.Snapshots, volumeSnapshotToProtobuf(srcConfig.VolumeSnapshots[i]))
		}
	}

	// Send offer to target.
	err = s.send(offerHeader)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		s.sendControl(err)
		return err
	}

	// Receive response from target.
	respHeader := &migration.MigrationHeader{}
	err = s.recv(respHeader)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		s.sendControl(err)
		return err
	}

	migrationTypes, err := migration.MatchTypes(respHeader, storagePools.FallbackMigrationType(storageDrivers.ContentType(srcConfig.Volume.ContentType)), poolMigrationTypes)
	if err != nil {
		logger.Errorf("Failed to negotiate migration type: %v", err)
		s.sendControl(err)
		return err
	}

	volSourceArgs := &migration.VolumeSourceArgs{
		IndexHeaderVersion: respHeader.GetIndexHeaderVersion(), // Enable index header frame if supported.
		Name:               volName,
		MigrationType:      migrationTypes[0],
		Snapshots:          offerHeader.SnapshotNames,
		TrackProgress:      true,
		ContentType:        srcConfig.Volume.ContentType,
		Info:               &migration.Info{Config: srcConfig},
		VolumeOnly:         s.volumeOnly,
	}

	// Only send the snapshots that the target requests when refreshing.
	if respHeader.GetRefresh() {
		volSourceArgs.Refresh = true
		volSourceArgs.Snapshots = respHeader.GetSnapshotNames()
		allSnapshots := volSourceArgs.Info.Config.VolumeSnapshots

		// Ensure that only the requested snapshots are included in the migration index header.
		volSourceArgs.Info.Config.VolumeSnapshots = make([]*api.StorageVolumeSnapshot, 0, len(volSourceArgs.Snapshots))
		for i := range allSnapshots {
			if shared.StringInSlice(allSnapshots[i].Name, volSourceArgs.Snapshots) {
				volSourceArgs.Info.Config.VolumeSnapshots = append(volSourceArgs.Info.Config.VolumeSnapshots, allSnapshots[i])
			}
		}
	}

	err = pool.MigrateCustomVolume(projectName, &shared.WebsocketIO{Conn: s.fsConn}, volSourceArgs, migrateOp)
	if err != nil {
		s.sendControl(err)
		return err
	}

	msg := migration.MigrationControl{}
	err = s.recv(&msg)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration control message")
		return err
	}

	if !*msg.Success {
		logger.Errorf("Failed to send storage volume")
		return fmt.Errorf(*msg.Message)
	}

	logger.Debugf("Migration source finished transferring storage volume")
	return nil
}

func newStorageMigrationSink(args *migrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:     migrationFields{volumeOnly: args.VolumeOnly},
		dest:    migrationFields{volumeOnly: args.VolumeOnly},
		url:     args.URL,
		dialer:  args.Dialer,
		push:    args.Push,
		refresh: args.Refresh,
	}

	if sink.push {
		sink.allConnected = cancel.New(context.Background())
	}

	var ok bool
	var err error
	if sink.push {
		sink.dest.controlSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, fmt.Errorf("Failed creating migration sink secret for control websocket: %w", err)
		}

		sink.dest.fsSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, fmt.Errorf("Failed creating migration sink secret for filesystem websocket: %w", err)
		}
	} else {
		sink.src.controlSecret, ok = args.Secrets[api.SecretNameControl]
		if !ok {
			return nil, fmt.Errorf("Missing migration sink secret for control websocket")
		}

		sink.src.fsSecret, ok = args.Secrets[api.SecretNameFilesystem]
		if !ok {
			return nil, fmt.Errorf("Missing migration sink secret for filesystem websocket")
		}
	}

	return &sink, nil
}

func (c *migrationSink) DoStorage(state *state.State, projectName string, poolName string, req *api.StorageVolumesPost, op *operations.Operation) error {
	var err error

	if c.push {
		logger.Info("Waiting for migration channel connections")
		select {
		case <-time.After(time.Second * 10):
			c.allConnected.Cancel()
			return fmt.Errorf("Timed out waiting for connections")
		case <-c.allConnected.Done():
		}

		logger.Info("Migration channels connected")
	}

	disconnector := c.src.disconnect
	if c.push {
		disconnector = c.dest.disconnect
	}

	if c.push {
		defer disconnector()
	} else {
		c.src.controlConn, err = c.connectWithSecret(c.src.controlSecret)
		if err != nil {
			err = fmt.Errorf("Failed connecting migration control sink socket: %w", err)
			return err
		}

		defer c.src.disconnect()

		c.src.fsConn, err = c.connectWithSecret(c.src.fsSecret)
		if err != nil {
			err = fmt.Errorf("Failed connecting migration filesystem sink socket: %w", err)
			c.src.sendControl(err)
			return err
		}
	}

	receiver := c.src.recv
	if c.push {
		receiver = c.dest.recv
	}

	sender := c.src.send
	if c.push {
		sender = c.dest.send
	}

	controller := c.src.sendControl
	if c.push {
		controller = c.dest.sendControl
	}

	offerHeader := &migration.MigrationHeader{}
	err = receiver(offerHeader)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		controller(err)
		return err
	}

	// The function that will be executed to receive the sender's migration data.
	var myTarget func(conn *websocket.Conn, op *operations.Operation, args migrationSinkArgs) error

	pool, err := storagePools.LoadByName(state, poolName)
	if err != nil {
		return err
	}

	dbContentType, err := storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return err
	}

	contentType, err := storagePools.VolumeDBContentTypeToContentType(dbContentType)
	if err != nil {
		return err
	}

	// The source/sender will never set Refresh. However, to determine the correct migration type
	// Refresh needs to be set.
	offerHeader.Refresh = &c.refresh

	// Extract the source's migration type and then match it against our pool's
	// supported types and features. If a match is found the combined features list
	// will be sent back to requester.
	respTypes, err := migration.MatchTypes(offerHeader, storagePools.FallbackMigrationType(contentType), pool.MigrationTypes(contentType, c.refresh, !c.src.volumeOnly))
	if err != nil {
		return err
	}

	// The migration header to be sent back to source with our target options.
	// Convert response type to response header and copy snapshot info into it.
	respHeader := migration.TypesToHeader(respTypes...)

	// Respond with our maximum supported header version if the requested version is higher than ours.
	// Otherwise just return the requested header version to the source.
	indexHeaderVersion := offerHeader.GetIndexHeaderVersion()
	if indexHeaderVersion > migration.IndexHeaderVersion {
		indexHeaderVersion = migration.IndexHeaderVersion
	}

	respHeader.IndexHeaderVersion = &indexHeaderVersion
	respHeader.SnapshotNames = offerHeader.SnapshotNames
	respHeader.Snapshots = offerHeader.Snapshots
	respHeader.Refresh = &c.refresh

	// Translate the legacy MigrationSinkArgs to a VolumeTargetArgs suitable for use
	// with the new storage layer.
	myTarget = func(conn *websocket.Conn, op *operations.Operation, args migrationSinkArgs) error {
		volTargetArgs := migration.VolumeTargetArgs{
			IndexHeaderVersion: respHeader.GetIndexHeaderVersion(),
			Name:               req.Name,
			Config:             req.Config,
			Description:        req.Description,
			MigrationType:      respTypes[0],
			TrackProgress:      true,
			ContentType:        req.ContentType,
			Refresh:            args.Refresh,
			VolumeOnly:         args.VolumeOnly,
		}

		// A zero length Snapshots slice indicates volume only migration in
		// VolumeTargetArgs. So if VoluneOnly was requested, do not populate them.
		if !args.VolumeOnly {
			volTargetArgs.Snapshots = make([]string, 0, len(args.Snapshots))
			for _, snap := range args.Snapshots {
				volTargetArgs.Snapshots = append(volTargetArgs.Snapshots, *snap.Name)
			}
		}

		return pool.CreateCustomVolumeFromMigration(projectName, &shared.WebsocketIO{Conn: conn}, volTargetArgs, op)
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
			controller(err)
			return err
		}

		targetSnapshotsComparable := make([]storagePools.ComparableSnapshot, 0, len(targetSnapshots))
		for _, targetSnap := range targetSnapshots {
			_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name)

			targetSnapshotsComparable = append(targetSnapshotsComparable, storagePools.ComparableSnapshot{
				Name:         targetSnapName,
				CreationDate: targetSnap.CreationDate,
			})
		}

		// Compare the two sets.
		syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := storagePools.CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

		// Delete the extra local snapshots first.
		for _, deleteTargetSnapshotIndex := range deleteTargetSnapshotIndexes {
			err := pool.DeleteCustomVolumeSnapshot(projectName, targetSnapshots[deleteTargetSnapshotIndex].Name, op)
			if err != nil {
				controller(err)
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

	err = sender(respHeader)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		controller(err)
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
			var fsConn *websocket.Conn
			if c.push {
				fsConn = c.dest.fsConn
			} else {
				fsConn = c.src.fsConn
			}

			// Get rsync options from sender, these are passed into mySink function
			// as part of MigrationSinkArgs below.
			rsyncFeatures := respHeader.GetRsyncFeaturesSlice()
			args := migrationSinkArgs{
				RsyncFeatures: rsyncFeatures,
				Snapshots:     respHeader.Snapshots,
				VolumeOnly:    c.src.volumeOnly,
				Refresh:       c.refresh,
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

	var source <-chan *migration.ControlResponse
	if c.push {
		source = c.dest.controlChannel()
	} else {
		source = c.src.controlChannel()
	}

	for {
		select {
		case err = <-restore:
			if err != nil {
				disconnector()
				return err
			}

			controller(nil)
			logger.Debug("Migration sink finished receiving storage volume")

			return nil
		case msg := <-source:
			if msg.Err != nil {
				disconnector()

				return fmt.Errorf("Got error reading migration source: %w", msg.Err)
			}

			if !*msg.Success {
				disconnector()

				return fmt.Errorf(*msg.Message)
			}

			// The source can only tell us it failed (e.g. if
			// checkpointing failed). We have to tell the source
			// whether or not the restore was successful.
			logger.Warn("Unknown message from migration source", logger.Ctx{"message": *msg.Message})
		}
	}
}

func (s *migrationSourceWs) ConnectStorageTarget(target api.StorageVolumePostTarget) error {
	logger.Debugf("Storage migration source is connecting")
	return s.ConnectTarget(target.Certificate, target.Operation, target.Websockets)
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
		CreationDate: proto.Int64(0),
		LastUsedDate: proto.Int64(0),
		ExpiryDate:   proto.Int64(0),
	}
}
