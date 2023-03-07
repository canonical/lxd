package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
	liblxc "github.com/lxc/go-lxc"
	"google.golang.org/protobuf/proto"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

func newMigrationSource(inst instance.Instance, stateful bool, instanceOnly bool, allowInconsistent bool) (*migrationSourceWs, error) {
	ret := migrationSourceWs{
		migrationFields: migrationFields{
			instance:          inst,
			allowInconsistent: allowInconsistent,
		},
		allConnected: make(chan struct{}),
	}

	ret.instanceOnly = instanceOnly

	var err error
	ret.controlSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	if stateful && inst.IsRunning() {
		ret.live = true

		if inst.Type() == instancetype.Container {
			_, err := exec.LookPath("criu")
			if err != nil {
				return nil, migration.ErrNoLiveMigrationSource
			}

			ret.criuSecret, err = shared.RandomCryptoString()
			if err != nil {
				return nil, err
			}
		}
	}

	return &ret, nil
}

func (s *migrationSourceWs) Do(state *state.State, migrateOp *operations.Operation) error {
	l := logger.AddContext(logger.Log, logger.Ctx{"project": s.instance.Project().Name, "instance": s.instance.Name()})

	l.Info("Waiting for migration channel connections on source")

	select {
	case <-time.After(time.Second * 10):
		return fmt.Errorf("Timed out waiting for migration connections")
	case <-s.allConnected:
	}

	l.Info("Migration channels connected on source")

	defer l.Info("Migration channels disconnected on source")
	defer s.disconnect()

	s.instance.SetOperation(migrateOp)
	err := s.instance.MigrateSend(instance.MigrateSendArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    s.send,
			ControlReceive: s.recv,
			LiveConn:       &shared.WebsocketIO{Conn: s.criuConn},
			DataConn:       &shared.WebsocketIO{Conn: s.fsConn},
			Snapshots:      !s.instanceOnly,
			Live:           s.live,
		},
		AllowInconsistent: s.allowInconsistent,
	})
	if err != nil {
		l.Error("Failed migration on source", logger.Ctx{"err": err})

		var wsCloseErr *websocket.CloseError
		if !errors.As(err, &wsCloseErr) {
			// Send error to other side if not closed.
			msg := migration.MigrationControl{
				Success: proto.Bool(err == nil),
				Message: proto.String(err.Error()),
			}

			sendErr := s.send(&msg)
			if sendErr != nil {
				return fmt.Errorf("Failed sending control error to target: %v (%w)", sendErr, err)
			}
		}

		return fmt.Errorf("Failed migration on source: %w", err)
	}

	return nil
}

func newMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:     migrationFields{instance: args.Instance, instanceOnly: args.InstanceOnly},
		dest:    migrationFields{instanceOnly: args.InstanceOnly},
		url:     args.URL,
		dialer:  args.Dialer,
		push:    args.Push,
		refresh: args.Refresh,
	}

	if sink.push {
		sink.allConnected = make(chan struct{})
	}

	var ok bool
	var err error
	if sink.push {
		sink.dest.controlSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, err
		}

		sink.dest.fsSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, err
		}

		sink.dest.live = args.Live
		if sink.dest.live {
			sink.dest.criuSecret, err = shared.RandomCryptoString()
			if err != nil {
				return nil, err
			}
		}
	} else {
		sink.src.controlSecret, ok = args.Secrets["control"]
		if !ok {
			return nil, fmt.Errorf("Missing control secret")
		}

		sink.src.fsSecret, ok = args.Secrets["fs"]
		if !ok {
			return nil, fmt.Errorf("Missing fs secret")
		}

		sink.src.criuSecret, ok = args.Secrets["criu"]
		sink.src.live = ok || args.Live
	}

	if sink.src.instance.Type() == instancetype.Container {
		_, err = exec.LookPath("criu")
		if sink.push && sink.dest.live && err != nil {
			return nil, migration.ErrNoLiveMigrationTarget
		} else if sink.src.live && err != nil {
			return nil, migration.ErrNoLiveMigrationTarget
		}
	}

	return &sink, nil
}

func (c *migrationSink) Do(state *state.State, revert *revert.Reverter, migrateOp *operations.Operation) error {
	live := c.src.live
	if c.push {
		live = c.dest.live
	}

	l := logger.AddContext(logger.Log, logger.Ctx{"push": c.push, "project": c.src.instance.Project().Name, "instance": c.src.instance.Name(), "live": live})

	var err error

	l.Info("Waiting for migration channel connections on target")

	if c.push {
		select {
		case <-time.After(time.Second * 10):
			return fmt.Errorf("Timed out waiting for migration connections")
		case <-c.allConnected:
		}
	}

	var disconnector func()

	if c.push {
		disconnector = c.dest.disconnect
		defer disconnector()
	} else {
		disconnector = c.src.disconnect
		c.src.controlConn, err = c.connectWithSecret(c.src.controlSecret)
		if err != nil {
			err = fmt.Errorf("Failed connecting control sink socket: %w", err)
			return err
		}

		defer c.src.disconnect()

		c.src.fsConn, err = c.connectWithSecret(c.src.fsSecret)
		if err != nil {
			err = fmt.Errorf("Failed connecting filesystem sink socket: %w", err)
			c.src.sendControl(err)
			return err
		}

		if c.src.live && c.src.instance.Type() == instancetype.Container {
			c.src.criuConn, err = c.connectWithSecret(c.src.criuSecret)
			if err != nil {
				err = fmt.Errorf("Failed connecting CRIU sink socket: %w", err)
				c.src.sendControl(err)
				return err
			}
		}
	}

	l.Info("Migration channels connected on target")
	defer l.Info("Migration channels disconnected on target")

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
		err = fmt.Errorf("Failed receiving migration offer header: %w", err)
		controller(err)
		return err
	}

	criuType := migration.CRIUType_CRIU_RSYNC.Enum()
	if offerHeader.Criu != nil && *offerHeader.Criu == migration.CRIUType_NONE {
		criuType = migration.CRIUType_NONE.Enum()
	} else {
		if !live {
			criuType = nil
		}
	}

	// The function that will be executed to receive the sender's migration data.
	var myTarget func(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error

	pool, err := storagePools.LoadByInstance(state, c.src.instance)
	if err != nil {
		return err
	}

	// The source/sender will never set Refresh. However, to determine the correct migration type
	// Refresh needs to be set.
	offerHeader.Refresh = &c.refresh

	// Extract the source's migration type and then match it against our pool's
	// supported types and features. If a match is found the combined features list
	// will be sent back to requester.
	contentType := storagePools.InstanceContentType(c.src.instance)
	respTypes, err := migration.MatchTypes(offerHeader, storagePools.FallbackMigrationType(contentType), pool.MigrationTypes(contentType, c.refresh))
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
	myTarget = func(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
		volTargetArgs := migration.VolumeTargetArgs{
			IndexHeaderVersion: respHeader.GetIndexHeaderVersion(),
			Name:               args.Instance.Name(),
			MigrationType:      respTypes[0],
			Refresh:            args.Refresh,    // Indicate to receiver volume should exist.
			TrackProgress:      true,            // Use a progress tracker on receiver to get in-cluster progress information.
			Live:               args.Live,       // Indicates we will get a final rootfs sync.
			VolumeSize:         args.VolumeSize, // Block size setting override.
			VolumeOnly:         args.VolumeOnly,
		}

		// At this point we have already figured out the parent container's root
		// disk device so we can simply retrieve it from the expanded devices.
		parentStoragePool := ""
		parentExpandedDevices := args.Instance.ExpandedDevices()
		parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices.CloneNative())
		if parentLocalRootDiskDeviceKey != "" {
			parentStoragePool = parentLocalRootDiskDevice["pool"]
		}

		if parentStoragePool == "" {
			return fmt.Errorf("Instance's root device is missing the pool property")
		}

		// A zero length Snapshots slice indicates volume only migration in
		// VolumeTargetArgs. So if VolumeOnly was requested, do not populate them.
		if !args.VolumeOnly {
			volTargetArgs.Snapshots = make([]string, 0, len(args.Snapshots))
			for _, snap := range args.Snapshots {
				volTargetArgs.Snapshots = append(volTargetArgs.Snapshots, *snap.Name)
				snapArgs, err := snapshotProtobufToInstanceArgs(state, args.Instance, snap)
				if err != nil {
					return err
				}

				// Ensure that snapshot and parent container have the same
				// storage pool in their local root disk device. If the root
				// disk device for the snapshot comes from a profile on the
				// new instance as well we don't need to do anything.
				if snapArgs.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapArgs.Devices.CloneNative())
					if snapLocalRootDiskDeviceKey != "" {
						snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				// Create the snapshot instance.
				_, snapInstOp, cleanup, err := instance.CreateInternal(state, *snapArgs, true)
				if err != nil {
					return fmt.Errorf("Failed creating instance snapshot record %q: %w", snapArgs.Name, err)
				}

				revert.Add(cleanup)
				defer snapInstOp.Done(err)
			}
		}

		err = pool.CreateInstanceFromMigration(args.Instance, &shared.WebsocketIO{Conn: conn}, volTargetArgs, op)
		if err != nil {
			return fmt.Errorf("Failed creating instance on target: %w", err)
		}

		// Only delete entire instance on error if the pool volume creation has succeeded to avoid
		// deleting an existing conflicting volume.
		if !volTargetArgs.Refresh {
			revert.Add(func() { _ = args.Instance.Delete(true) })
		}

		return nil
	}

	// Add CRIU info to response.
	respHeader.Criu = criuType

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
		targetSnapshots, err := c.src.instance.Snapshots()
		if err != nil {
			controller(err)
			return err
		}

		targetSnapshotsComparable := make([]storagePools.ComparableSnapshot, 0, len(targetSnapshots))
		for _, targetSnap := range targetSnapshots {
			_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name())

			targetSnapshotsComparable = append(targetSnapshotsComparable, storagePools.ComparableSnapshot{
				Name:         targetSnapName,
				CreationDate: targetSnap.CreationDate(),
			})
		}

		// Compare the two sets.
		syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := storagePools.CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

		// Delete the extra local snapshots first.
		for _, deleteTargetSnapshotIndex := range deleteTargetSnapshotIndexes {
			err := targetSnapshots[deleteTargetSnapshotIndex].Delete(true)
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

	if offerHeader.GetPredump() {
		// If the other side wants pre-dump and if this side supports it, let's use it.
		respHeader.Predump = proto.Bool(true)
	} else {
		respHeader.Predump = proto.Bool(false)
	}

	// Get rsync options from sender, these are passed into mySink function as part of
	// MigrationSinkArgs below.
	rsyncFeatures := respHeader.GetRsyncFeaturesSlice()

	err = sender(respHeader)
	if err != nil {
		controller(err)
		return err
	}

	restore := make(chan error)
	go func(c *migrationSink) {
		imagesDir := ""
		srcIdmap := new(idmap.IdmapSet)

		for _, idmapSet := range offerHeader.Idmap {
			e := idmap.IdmapEntry{
				Isuid:    *idmapSet.Isuid,
				Isgid:    *idmapSet.Isgid,
				Nsid:     int64(*idmapSet.Nsid),
				Hostid:   int64(*idmapSet.Hostid),
				Maprange: int64(*idmapSet.Maprange),
			}

			srcIdmap.Idmap = idmap.Extend(srcIdmap.Idmap, e)
		}

		// We do the fs receive in parallel so we don't have to reason about when to receive
		// what. The sending side is smart enough to send the filesystem bits that it can
		// before it seizes the container to start checkpointing, so the total transfer time
		// will be minimized even if we're dumb here.
		fsTransfer := make(chan error)
		go func() {
			snapshots := []*migration.Snapshot{}

			// Legacy: we only sent the snapshot names, so we just copy the container's
			// config over, same as we used to do.
			if len(offerHeader.SnapshotNames) != len(offerHeader.Snapshots) {
				// Convert the instance to an api.InstanceSnapshot.

				profileNames := make([]string, 0, len(c.src.instance.Profiles()))
				for _, p := range c.src.instance.Profiles() {
					profileNames = append(profileNames, p.Name)
				}

				architectureName, _ := osarch.ArchitectureName(c.src.instance.Architecture())
				apiInstSnap := &api.InstanceSnapshot{
					InstanceSnapshotPut: api.InstanceSnapshotPut{
						ExpiresAt: time.Time{},
					},
					Architecture: architectureName,
					CreatedAt:    c.src.instance.CreationDate(),
					LastUsedAt:   c.src.instance.LastUsedDate(),
					Config:       c.src.instance.LocalConfig(),
					Devices:      c.src.instance.LocalDevices().CloneNative(),
					Ephemeral:    c.src.instance.IsEphemeral(),
					Stateful:     c.src.instance.IsStateful(),
					Profiles:     profileNames,
				}

				for _, name := range offerHeader.SnapshotNames {
					base := instance.SnapshotToProtobuf(apiInstSnap)
					base.Name = &name
					snapshots = append(snapshots, base)
				}
			} else {
				snapshots = offerHeader.Snapshots
			}

			var fsConn *websocket.Conn
			if c.push {
				fsConn = c.dest.fsConn
			} else {
				fsConn = c.src.fsConn
			}

			// Default to not expecting to receive the final rootfs sync.
			sendFinalFsDelta := false

			// If we are doing a stateful live transfer or the CRIU type indicates we
			// are doing a stateless transfer with a running instance then we should
			// expect the source to send us a final rootfs sync.
			if live && c.src.instance.Type() == instancetype.Container {
				sendFinalFsDelta = true
			}

			if criuType != nil && *criuType == migration.CRIUType_NONE {
				sendFinalFsDelta = true
			}

			args := MigrationSinkArgs{
				Instance:      c.src.instance,
				InstanceOnly:  c.src.instanceOnly,
				Idmap:         srcIdmap,
				Live:          sendFinalFsDelta,
				Refresh:       c.refresh,
				RsyncFeatures: rsyncFeatures,
				Snapshots:     snapshots,
				VolumeSize:    offerHeader.GetVolumeSize(), // Block size setting override.
			}

			err = myTarget(fsConn, migrateOp, args)
			if err != nil {
				fsTransfer <- err
				return
			}

			// For containers, the fs map of the source is sent as part of the migration
			// stream, then at the end we need to record that map as last_state so that
			// LXD can shift on startup if needed.
			if c.src.instance.Type() == instancetype.Container {
				ct := c.src.instance.(instance.Container)
				err = resetContainerDiskIdmap(ct, srcIdmap)
				if err != nil {
					fsTransfer <- err
					return
				}
			}

			fsTransfer <- nil
		}()

		if live && c.src.instance.Type() == instancetype.Container {
			var err error
			imagesDir, err = os.MkdirTemp("", "lxd_restore_")
			if err != nil {
				restore <- err
				return
			}

			defer func() { _ = os.RemoveAll(imagesDir) }()

			var criuConn *websocket.Conn
			if c.push {
				criuConn = c.dest.criuConn
			} else {
				criuConn = c.src.criuConn
			}

			sync := &migration.MigrationSync{
				FinalPreDump: proto.Bool(false),
			}

			if respHeader.GetPredump() {
				for !sync.GetFinalPreDump() {
					l.Debug("About to receive pre-dump rsync")
					// Transfer a CRIU pre-dump.
					err = rsync.Recv(shared.AddSlash(imagesDir), &shared.WebsocketIO{Conn: criuConn}, nil, rsyncFeatures)
					if err != nil {
						restore <- err
						return
					}

					l.Debug("Done receiving pre-dump rsync")

					l.Debug("About to receive pre-dump header")
					// Check if this was the last pre-dump.
					// Only the FinalPreDump element if of interest.
					mtype, data, err := criuConn.ReadMessage()
					if err != nil {
						restore <- err
						return
					}

					l.Debug("Done receiving pre-dump header")

					if mtype != websocket.BinaryMessage {
						restore <- err
						return
					}

					err = proto.Unmarshal(data, sync)
					if err != nil {
						restore <- err
						return
					}
				}
			}

			// Final CRIU dump.
			l.Debug("About to receive final dump rsync")
			err = rsync.Recv(shared.AddSlash(imagesDir), &shared.WebsocketIO{Conn: criuConn}, nil, rsyncFeatures)
			l.Debug("Done receiving final dump rsync")
			if err != nil {
				restore <- err
				return
			}
		}

		err := <-fsTransfer
		if err != nil {
			restore <- err
			return
		}

		if live {
			if c.src.instance.Type() == instancetype.Container {
				criuMigrationArgs := instance.CriuMigrationArgs{
					Cmd:          liblxc.MIGRATE_RESTORE,
					StateDir:     imagesDir,
					Function:     "migration",
					Stop:         false,
					ActionScript: false,
					DumpDir:      "final",
					PreDumpDir:   "",
				}

				// Currently we only do a single CRIU pre-dump so we can hardcode "final"
				// here since we know that "final" is the folder for CRIU's final dump.
				err = c.src.instance.MigrateReceive(&criuMigrationArgs)
				if err != nil {
					restore <- err
					return
				}
			}

			if c.src.instance.Type() == instancetype.VM {
				err = c.src.instance.MigrateReceive(nil)
				if err != nil {
					restore <- err
					return
				}
			}
		}

		restore <- nil
	}(c)

	var source <-chan *migrationControlResponse
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

			controller(err)
			return err
		case msg := <-source:
			if msg.err != nil {
				disconnector()

				return fmt.Errorf("Got error reading migration source: %w", msg.err)
			}

			if !*msg.Success {
				disconnector()

				return fmt.Errorf(*msg.Message)
			}

			// The source can only tell us it failed (e.g. if checkpointing failed).
			// We have to tell the source whether or not the restore was successful.
			logger.Warn("Unknown message from migration source", logger.Ctx{"message": *msg.Message})
		}
	}
}

func (s *migrationSourceWs) ConnectContainerTarget(target api.InstancePostTarget) error {
	return s.ConnectTarget(target.Certificate, target.Operation, target.Websockets)
}
