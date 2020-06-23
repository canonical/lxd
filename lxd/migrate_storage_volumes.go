package main

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

func newStorageMigrationSource(volumeOnly bool) (*migrationSourceWs, error) {
	ret := migrationSourceWs{migrationFields{}, make(chan bool, 1)}
	ret.volumeOnly = volumeOnly

	var err error
	ret.controlSecret, err = shared.RandomCryptoString()
	if err != nil {
		logger.Errorf("Failed to create migration source secrect for control websocket")
		return nil, err
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		logger.Errorf("Failed to create migration source secrect for filesystem websocket")
		return nil, err
	}

	return &ret, nil
}

func (s *migrationSourceWs) DoStorage(state *state.State, projectName string, poolName string, volName string, migrateOp *operations.Operation) error {
	<-s.allConnected
	defer s.disconnect()

	var offerHeader migration.MigrationHeader
	var poolMigrationTypes []migration.Type

	pool, err := storagePools.GetPoolByName(state, poolName)
	if err != nil {
		return err
	}

	_, vol, err := state.Cluster.GetLocalStoragePoolVolume(projectName, volName, db.StoragePoolVolumeTypeCustom, pool.ID())
	if err != nil {
		return err
	}

	dbContentType, err := storagePools.VolumeContentTypeNameToContentType(vol.ContentType)
	if err != nil {
		return err
	}

	volContentType, err := storagePools.VolumeDBContentTypeToContentType(dbContentType)
	if err != nil {
		return err
	}

	// The refresh argument passed to MigrationTypes() is always set
	// to false here. The migration source/sender doesn't need to care whether
	// or not it's doing a refresh as the migration sink/receiver will know
	// this, and adjust the migration types accordingly.
	poolMigrationTypes = pool.MigrationTypes(volContentType, false)
	if len(poolMigrationTypes) < 0 {
		return fmt.Errorf("No source migration types available")
	}

	// Convert the pool's migration type options to an offer header to target.
	offerHeader = migration.TypesToHeader(poolMigrationTypes...)

	snapshots := []*migration.Snapshot{}
	snapshotNames := []string{}

	// Only send snapshots when requested.
	if !s.volumeOnly {
		var err error
		snaps, err := storagePools.VolumeSnapshotsGet(state, projectName, poolName, volName, db.StoragePoolVolumeTypeCustom)
		if err == nil {
			for _, snap := range snaps {
				_, snapVolume, err := state.Cluster.GetLocalStoragePoolVolume(projectName, snap.Name, db.StoragePoolVolumeTypeCustom, pool.ID())
				if err != nil {
					continue
				}

				snapshots = append(snapshots, volumeSnapshotToProtobuf(snapVolume))
				_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name)
				snapshotNames = append(snapshotNames, snapName)
			}
		}
	}

	// Add snapshot info to source header.
	offerHeader.SnapshotNames = snapshotNames
	offerHeader.Snapshots = snapshots

	// Send offer to target.
	err = s.send(&offerHeader)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		s.sendControl(err)
		return err
	}

	// Receive response from target.
	var respHeader migration.MigrationHeader
	err = s.recv(&respHeader)

	if err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		s.sendControl(err)
		return err
	}

	migrationTypes, err := migration.MatchTypes(respHeader, storagePools.FallbackMigrationType(volContentType), poolMigrationTypes)
	if err != nil {
		logger.Errorf("Failed to negotiate migration type: %v", err)
		s.sendControl(err)
		return err
	}

	volSourceArgs := &migration.VolumeSourceArgs{
		Name:          volName,
		MigrationType: migrationTypes[0],
		Snapshots:     snapshotNames,
		TrackProgress: true,
		ContentType:   vol.ContentType,
	}

	err = pool.MigrateCustomVolume(projectName, &shared.WebsocketIO{Conn: s.fsConn}, volSourceArgs, migrateOp)
	if err != nil {
		go s.sendControl(err)
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

func newStorageMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:    migrationFields{volumeOnly: args.VolumeOnly},
		dest:   migrationFields{volumeOnly: args.VolumeOnly},
		url:    args.Url,
		dialer: args.Dialer,
		push:   args.Push,
	}

	if sink.push {
		sink.allConnected = make(chan bool, 1)
	}

	var ok bool
	var err error
	if sink.push {
		sink.dest.controlSecret, err = shared.RandomCryptoString()
		if err != nil {
			logger.Errorf("Failed to create migration sink secrect for control websocket")
			return nil, err
		}

		sink.dest.fsSecret, err = shared.RandomCryptoString()
		if err != nil {
			logger.Errorf("Failed to create migration sink secrect for filesystem websocket")
			return nil, err
		}
	} else {
		sink.src.controlSecret, ok = args.Secrets["control"]
		if !ok {
			logger.Errorf("Missing migration sink secrect for control websocket")
			return nil, fmt.Errorf("Missing control secret")
		}

		sink.src.fsSecret, ok = args.Secrets["fs"]
		if !ok {
			logger.Errorf("Missing migration sink secrect for filesystem websocket")
			return nil, fmt.Errorf("Missing fs secret")
		}
	}

	return &sink, nil
}

func (c *migrationSink) DoStorage(state *state.State, projectName string, poolName string, req *api.StorageVolumesPost, op *operations.Operation) error {
	var err error

	if c.push {
		<-c.allConnected
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
			logger.Errorf("Failed to connect migration sink control socket")
			return err
		}
		defer c.src.disconnect()

		c.src.fsConn, err = c.connectWithSecret(c.src.fsSecret)
		if err != nil {
			logger.Errorf("Failed to connect migration sink filesystem socket")
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

	offerHeader := migration.MigrationHeader{}
	if err := receiver(&offerHeader); err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		controller(err)
		return err
	}

	// The function that will be executed to receive the sender's migration data.
	var myTarget func(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error

	// The migration header to be sent back to source with our target options.
	var respHeader migration.MigrationHeader

	pool, err := storagePools.GetPoolByName(state, poolName)
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

	// Extract the source's migration type and then match it against our pool's
	// supported types and features. If a match is found the combined features list
	// will be sent back to requester.
	respTypes, err := migration.MatchTypes(offerHeader, storagePools.FallbackMigrationType(contentType), pool.MigrationTypes(contentType, c.refresh))
	if err != nil {
		return err
	}

	// Convert response type to response header and copy snapshot info into it.
	respHeader = migration.TypesToHeader(respTypes...)
	respHeader.SnapshotNames = offerHeader.SnapshotNames
	respHeader.Snapshots = offerHeader.Snapshots

	// Translate the legacy MigrationSinkArgs to a VolumeTargetArgs suitable for use
	// with the new storage layer.
	myTarget = func(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
		volTargetArgs := migration.VolumeTargetArgs{
			Name:          req.Name,
			Config:        req.Config,
			Description:   req.Description,
			MigrationType: respTypes[0],
			TrackProgress: true,
			ContentType:   req.ContentType,
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

	err = sender(&respHeader)
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
			args := MigrationSinkArgs{
				RsyncFeatures: rsyncFeatures,
				Snapshots:     respHeader.Snapshots,
				VolumeOnly:    c.src.volumeOnly,
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

	var source <-chan migration.MigrationControl
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
			logger.Debugf("Migration sink finished receiving storage volume")
			return nil
		case msg, ok := <-source:
			if !ok {
				disconnector()
				return fmt.Errorf("Got error reading source")
			}

			if !*msg.Success {
				disconnector()
				return fmt.Errorf(*msg.Message)
			}

			// The source can only tell us it failed (e.g. if
			// checkpointing failed). We have to tell the source
			// whether or not the restore was successful.
			logger.Debugf("Unknown message %v from source", msg)
		}
	}
}

func (s *migrationSourceWs) ConnectStorageTarget(target api.StorageVolumePostTarget) error {
	logger.Debugf("Storage migration source is connecting")
	return s.ConnectTarget(target.Certificate, target.Operation, target.Websockets)
}

func volumeSnapshotToProtobuf(vol *api.StorageVolume) *migration.Snapshot {
	config := []*migration.Config{}
	for k, v := range vol.Config {
		kCopy := string(k)
		vCopy := string(v)
		config = append(config, &migration.Config{Key: &kCopy, Value: &vCopy})
	}

	_, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(vol.Name)

	return &migration.Snapshot{
		Name:         &snapOnlyName,
		LocalConfig:  config,
		Profiles:     []string{},
		Ephemeral:    proto.Bool(false),
		LocalDevices: []*migration.Device{},
		Architecture: proto.Int32(0),
		Stateful:     proto.Bool(false),
		CreationDate: proto.Int64(0),
		LastUsedDate: proto.Int64(0),
	}
}
