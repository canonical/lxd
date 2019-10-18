package main

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

func NewStorageMigrationSource(volumeOnly bool) (*migrationSourceWs, error) {
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

func (s *migrationSourceWs) DoStorage(state *state.State, poolName string, volName string, migrateOp *operations.Operation) error {
	<-s.allConnected
	defer s.disconnect()

	var offerHeader migration.MigrationHeader
	var poolMigrationTypes []migration.Type

	// Check if sending storage pool supports new storage layer.
	pool, err := storagePools.GetPoolByName(state, poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return err
		}

		poolMigrationTypes = pool.MigrationTypes(storageDrivers.ContentTypeFS)
		if len(poolMigrationTypes) < 0 {
			return fmt.Errorf("No source migration types available")
		}

		// Convert the pool's migration type options to an offer header to target.
		offerHeader = migration.TypesToHeader(poolMigrationTypes...)
	} else {
		storage, err := storagePoolVolumeInit(state, "default", poolName, volName, storagePoolVolumeTypeCustom)
		if err != nil {
			return err
		}
		s.storage = storage
		myType := s.storage.MigrationType()
		hasFeature := true
		offerHeader = migration.MigrationHeader{
			Fs: &myType,
			RsyncFeatures: &migration.RsyncFeatures{
				Xattrs:        &hasFeature,
				Delete:        &hasFeature,
				Compress:      &hasFeature,
				Bidirectional: &hasFeature,
			},
		}

		if len(zfsVersion) >= 3 && zfsVersion[0:3] != "0.6" {
			offerHeader.ZfsFeatures = &migration.ZfsFeatures{
				Compress: &hasFeature,
			}
		}

		// Storage needs to start unconditionally now, since we need to initialize a new
		// storage interface.
		ourMount, err := s.storage.StoragePoolVolumeMount()
		if err != nil {
			logger.Errorf("Failed to mount storage volume")
			return err
		}
		if ourMount {
			defer s.storage.StoragePoolVolumeUmount()
		}
	}

	snapshots := []*migration.Snapshot{}
	snapshotNames := []string{}

	// Only send snapshots when requested.
	if !s.volumeOnly {
		var err error
		snaps, err := storagePools.VolumeSnapshotsGet(state, poolName, volName, storagePoolVolumeTypeCustom)
		if err == nil {
			poolID, err := state.Cluster.StoragePoolGetID(poolName)
			if err == nil {
				for _, snap := range snaps {
					_, snapVolume, err := state.Cluster.StoragePoolNodeVolumeGetType(snap.Name, storagePoolVolumeTypeCustom, poolID)
					if err != nil {
						continue
					}

					snapshots = append(snapshots, volumeSnapshotToProtobuf(snapVolume))
					_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name)
					snapshotNames = append(snapshotNames, snapName)
				}
			}

		}
	}

	// Add snapshot info to source header.
	offerHeader.SnapshotNames = snapshotNames
	offerHeader.Snapshots = snapshots

	// The protocol says we have to send a header no matter what, so let's
	// do that, but then immediately send an error.
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

	// Use new storage layer for migration if supported.
	if pool != nil {
		migrationType, err := migration.MatchTypes(respHeader, poolMigrationTypes)
		if err != nil {
			logger.Errorf("Failed to neogotiate migration type: %v", err)
			s.sendControl(err)
			return err
		}

		volSourceArgs := migration.VolumeSourceArgs{
			Name:          volName,
			MigrationType: migrationType,
			Snapshots:     snapshotNames,
		}

		err = pool.MigrateCustomVolume(&shared.WebsocketIO{Conn: s.fsConn}, volSourceArgs, migrateOp)
		if err != nil {
			go s.sendControl(err)
			return err
		}
	} else {
		// Use legacy storage layer for migration.

		// Get target's rsync options.
		rsyncFeatures := respHeader.GetRsyncFeaturesSlice()
		if !shared.StringInSlice("bidirectional", rsyncFeatures) {
			// If no bi-directional support, assume LXD 3.7 level
			// NOTE: Do NOT extend this list of arguments
			rsyncFeatures = []string{"xattrs", "delete", "compress"}
		}

		// Get target's zfs options.
		zfsFeatures := respHeader.GetZfsFeaturesSlice()

		// Set source args
		sourceArgs := MigrationSourceArgs{
			RsyncFeatures: rsyncFeatures,
			ZfsFeatures:   zfsFeatures,
			VolumeOnly:    s.volumeOnly,
		}

		driver, fsErr := s.storage.StorageMigrationSource(sourceArgs)
		if fsErr != nil {
			logger.Errorf("Failed to initialize new storage volume migration driver")
			s.sendControl(fsErr)
			return fsErr
		}

		bwlimit := ""

		if *offerHeader.Fs != *respHeader.Fs {
			driver, _ = rsyncStorageMigrationSource(sourceArgs)

			// Check if this storage pool has a rate limit set for rsync.
			poolwritable := s.storage.GetStoragePoolWritable()
			if poolwritable.Config != nil {
				bwlimit = poolwritable.Config["rsync.bwlimit"]
			}
		}

		abort := func(err error) error {
			driver.Cleanup()
			go s.sendControl(err)
			return err
		}

		err = driver.SendStorageVolume(s.fsConn, migrateOp, bwlimit, s.storage, s.volumeOnly)
		if err != nil {
			logger.Errorf("Failed to send storage volume")
			return abort(err)
		}
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

func NewStorageMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:    migrationFields{storage: args.Storage, volumeOnly: args.VolumeOnly},
		dest:   migrationFields{storage: args.Storage, volumeOnly: args.VolumeOnly},
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

func (c *migrationSink) DoStorage(state *state.State, poolName string, req *api.StorageVolumesPost, op *operations.Operation) error {
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

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(state, poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return err
		}

		// Extract the source's migration type and then match it against our pool's
		// supported types and features. If a match is found the combined features list
		// will be sent back to requester.
		respType, err := migration.MatchTypes(offerHeader, pool.MigrationTypes(storageDrivers.ContentTypeFS))
		if err != nil {
			return err
		}

		// Convert response type to response header and copy snapshot info into it.
		respHeader = migration.TypesToHeader(respType)
		respHeader.SnapshotNames = offerHeader.SnapshotNames
		respHeader.Snapshots = offerHeader.Snapshots

		// Translate the legacy MigrationSinkArgs to a VolumeTargetArgs suitable for use
		// with the new storage layer.
		myTarget = func(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
			volTargetArgs := migration.VolumeTargetArgs{
				Name:          req.Name,
				Config:        req.Config,
				Description:   req.Description,
				MigrationType: respType,
			}

			// A zero length Snapshots slice indicates volume only migration in
			// VolumeTargetArgs. So if VoluneOnly was requested, do not populate them.
			if !args.VolumeOnly {
				volTargetArgs.Snapshots = make([]string, 0, len(args.Snapshots))
				for _, snap := range args.Snapshots {
					volTargetArgs.Snapshots = append(volTargetArgs.Snapshots, *snap.Name)
				}
			}

			return pool.CreateCustomVolumeFromMigration(&shared.WebsocketIO{Conn: conn}, volTargetArgs, op)
		}
	} else {
		// Setup legacy storage migration sink if destination pool isn't supported yet by
		// new storage layer.
		storage, err := storagePoolVolumeDBCreateInternal(state, poolName, req)
		if err != nil {
			return err
		}

		// Link the storage variable into the migrationSink (like NewStorageMigrationSink
		// would have done originally).
		c.src.storage = storage
		c.dest.storage = storage
		myTarget = c.src.storage.StorageMigrationSink
		myType := c.src.storage.MigrationType()

		hasFeature := true
		respHeader = migration.MigrationHeader{
			Fs:            &myType,
			Snapshots:     offerHeader.Snapshots,
			SnapshotNames: offerHeader.SnapshotNames,
			RsyncFeatures: &migration.RsyncFeatures{
				Xattrs:        &hasFeature,
				Delete:        &hasFeature,
				Compress:      &hasFeature,
				Bidirectional: &hasFeature,
			},
		}

		if len(zfsVersion) >= 3 && zfsVersion[0:3] != "0.6" {
			respHeader.ZfsFeatures = &migration.ZfsFeatures{
				Compress: &hasFeature,
			}
		}

		// If the storage type the source has doesn't match what we have, then we have to
		// use rsync.
		if *offerHeader.Fs != *respHeader.Fs {
			myTarget = rsyncStorageMigrationSink
			myType = migration.MigrationFSType_RSYNC
		}
	}

	err = sender(&respHeader)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		controller(err)
		return err
	}

	restore := make(chan error)

	go func(c *migrationSink) {
		/* We do the fs receive in parallel so we don't have to reason
		 * about when to receive what. The sending side is smart enough
		 * to send the filesystem bits that it can before it seizes the
		 * container to start checkpointing, so the total transfer time
		 * will be minimized even if we're dumb here.
		 */
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
				Storage:       c.dest.storage,
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
			} else {
				// The source can only tell us it failed (e.g. if
				// checkpointing failed). We have to tell the source
				// whether or not the restore was successful.
				logger.Debugf("Unknown message %v from source", msg)
			}
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

	_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(vol.Name)

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
