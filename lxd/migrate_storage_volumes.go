package main

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

func NewStorageMigrationSource(storage instance.Storage, volumeOnly bool) (*migrationSourceWs, error) {
	ret := migrationSourceWs{migrationFields{storage: storage}, make(chan bool, 1)}
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

func (s *migrationSourceWs) DoStorage(migrateOp *operation.Operation) error {
	<-s.allConnected

	// Storage needs to start unconditionally now, since we need to
	// initialize a new storage interface.
	ourMount, err := s.storage.StoragePoolVolumeMount()
	if err != nil {
		logger.Errorf("Failed to mount storage volume")
		return err
	}
	if ourMount {
		defer s.storage.StoragePoolVolumeUmount()
	}

	snapshots := []*migration.Snapshot{}
	snapshotNames := []string{}

	// Only send snapshots when requested.
	if !s.volumeOnly {
		state := s.storage.GetState()
		pool := s.storage.GetStoragePool()
		volume := s.storage.GetStoragePoolVolume()

		var err error

		snaps, err := storagePoolVolumeSnapshotsGet(state, pool.Name, volume.Name, storagePoolVolumeTypeCustom)
		if err == nil {
			poolID, err := state.Cluster.StoragePoolGetID(pool.Name)
			if err == nil {
				for _, name := range snaps {
					_, snapVolume, err := state.Cluster.StoragePoolNodeVolumeGetType(name, storagePoolVolumeTypeCustom, poolID)
					if err != nil {
						continue
					}

					snapshots = append(snapshots, volumeSnapshotToProtobuf(snapVolume))
					snapshotNames = append(snapshotNames, shared.ExtractSnapshotName(name))
				}
			}

		}
	}

	// The protocol says we have to send a header no matter what, so let's
	// do that, but then immediately send an error.
	myType := s.storage.MigrationType()
	hasFeature := true
	header := migration.MigrationHeader{
		Fs:            &myType,
		SnapshotNames: snapshotNames,
		Snapshots:     snapshots,
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:        &hasFeature,
			Delete:        &hasFeature,
			Compress:      &hasFeature,
			Bidirectional: &hasFeature,
		},
	}

	if len(zfsVersion) >= 3 && zfsVersion[0:3] != "0.6" {
		header.ZfsFeatures = &migration.ZfsFeatures{
			Compress: &hasFeature,
		}
	}

	err = s.send(&header)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		s.sendControl(err)
		return err
	}

	err = s.recv(&header)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		s.sendControl(err)
		return err
	}

	// Handle rsync options
	rsyncFeatures := header.GetRsyncFeaturesSlice()
	if !shared.StringInSlice("bidirectional", rsyncFeatures) {
		// If no bi-directional support, assume LXD 3.7 level
		// NOTE: Do NOT extend this list of arguments
		rsyncFeatures = []string{"xattrs", "delete", "compress"}
	}

	// Handle zfs options
	zfsFeatures := header.GetZfsFeaturesSlice()

	// Set source args
	sourceArgs := instance.MigrationSourceArgs{
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
	if *header.Fs != myType {
		myType = migration.MigrationFSType_RSYNC
		header.Fs = &myType

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

	msg := migration.MigrationControl{}
	err = s.recv(&msg)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration control message")
		s.disconnect()
		return err
	}

	if !*msg.Success {
		logger.Errorf("Failed to send storage volume")
		return fmt.Errorf(*msg.Message)
	}

	logger.Debugf("Migration source finished transferring storage volume")
	return nil
}

func NewStorageMigrationSink(args *instance.MigrationSinkArgs) (*migrationSink, error) {
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

func (c *migrationSink) DoStorage(migrateOp *operation.Operation) error {
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

	header := migration.MigrationHeader{}
	if err := receiver(&header); err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		controller(err)
		return err
	}

	mySink := c.src.storage.StorageMigrationSink
	myType := c.src.storage.MigrationType()
	hasFeature := true
	resp := migration.MigrationHeader{
		Fs:            &myType,
		Snapshots:     header.Snapshots,
		SnapshotNames: header.SnapshotNames,
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:        &hasFeature,
			Delete:        &hasFeature,
			Compress:      &hasFeature,
			Bidirectional: &hasFeature,
		},
	}

	if len(zfsVersion) >= 3 && zfsVersion[0:3] != "0.6" {
		resp.ZfsFeatures = &migration.ZfsFeatures{
			Compress: &hasFeature,
		}
	}

	// If the storage type the source has doesn't match what we have, then
	// we have to use rsync.
	if *header.Fs != *resp.Fs {
		mySink = rsyncStorageMigrationSink
		myType = migration.MigrationFSType_RSYNC
		resp.Fs = &myType
	}

	// Handle rsync options
	rsyncFeatures := header.GetRsyncFeaturesSlice()

	err = sender(&resp)
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

			args := instance.MigrationSinkArgs{
				Storage:       c.dest.storage,
				RsyncFeatures: rsyncFeatures,
				Snapshots:     header.Snapshots,
				VolumeOnly:    c.src.volumeOnly,
			}

			err = mySink(fsConn, migrateOp, args)
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

	/*
			var fsConn *websocket.Conn
			if c.push {
				fsConn = c.dest.fsConn
			} else {
				fsConn = c.src.fsConn
			}

			err = mySink(fsConn, migrateOp, args)
			if err != nil {
				logger.Errorf("Failed to start storage volume migration sink")
				controller(err)
				return err
			}

			controller(nil)
		logger.Debugf("Migration sink finished receiving storage volume")
		return nil
	*/
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

	snapOnlyName := shared.ExtractSnapshotName(vol.Name)

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
