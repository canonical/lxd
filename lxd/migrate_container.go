package main

import (
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/rsync"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

func NewMigrationSource(inst instance.Instance, stateful bool, instanceOnly bool) (*migrationSourceWs, error) {
	ret := migrationSourceWs{migrationFields{instance: inst}, make(chan bool, 1)}
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
		_, err := exec.LookPath("criu")
		if err != nil {
			return nil, fmt.Errorf("Unable to perform container live migration. CRIU isn't installed on the source server")
		}

		ret.live = true
		ret.criuSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, err
		}
	}

	return &ret, nil
}

func writeActionScript(directory string, operation string, secret string, execPath string) error {
	script := fmt.Sprintf(`#!/bin/sh -e
if [ "$CRTOOLS_SCRIPT_ACTION" = "post-dump" ]; then
	%s migratedumpsuccess %s %s
fi
`, execPath, operation, secret)

	f, err := os.Create(filepath.Join(directory, "action.sh"))
	if err != nil {
		return err
	}
	defer f.Close()

	err = f.Chmod(0500)
	if err != nil {
		return err
	}

	_, err = f.WriteString(script)
	return err
}

func snapshotToProtobuf(c instance.Instance) *migration.Snapshot {
	config := []*migration.Config{}
	for k, v := range c.LocalConfig() {
		kCopy := string(k)
		vCopy := string(v)
		config = append(config, &migration.Config{Key: &kCopy, Value: &vCopy})
	}

	devices := []*migration.Device{}
	for name, d := range c.LocalDevices() {
		props := []*migration.Config{}
		for k, v := range d {
			kCopy := string(k)
			vCopy := string(v)
			props = append(props, &migration.Config{Key: &kCopy, Value: &vCopy})
		}

		devices = append(devices, &migration.Device{Name: &name, Config: props})
	}

	parts := strings.SplitN(c.Name(), shared.SnapshotDelimiter, 2)
	isEphemeral := c.IsEphemeral()
	arch := int32(c.Architecture())
	stateful := c.IsStateful()

	creationDate := c.CreationDate().UTC().Unix()
	lastUsedDate := c.LastUsedDate().UTC().Unix()

	return &migration.Snapshot{
		Name:         &parts[len(parts)-1],
		LocalConfig:  config,
		Profiles:     c.Profiles(),
		Ephemeral:    &isEphemeral,
		LocalDevices: devices,
		Architecture: &arch,
		Stateful:     &stateful,
		CreationDate: &creationDate,
		LastUsedDate: &lastUsedDate,
	}
}

// Check if CRIU supports pre-dumping and number of
// pre-dump iterations
func (s *migrationSourceWs) checkForPreDumpSupport() (bool, int) {
	// Ask CRIU if this architecture/kernel/criu combination
	// supports pre-copy (dirty memory tracking)
	criuMigrationArgs := CriuMigrationArgs{
		cmd:          lxc.MIGRATE_FEATURE_CHECK,
		stateDir:     "",
		function:     "feature-check",
		stop:         false,
		actionScript: false,
		dumpDir:      "",
		preDumpDir:   "",
		features:     lxc.FEATURE_MEM_TRACK,
	}

	if s.instance.Type() != instancetype.Container {
		return false, 0
	}

	c := s.instance.(*containerLXC)
	err := c.Migrate(&criuMigrationArgs)

	if err != nil {
		// CRIU says it does not know about dirty memory tracking.
		// This means the rest of this function is irrelevant.
		return false, 0
	}

	// CRIU says it can actually do pre-dump. Let's set it to true
	// unless the user wants something else.
	use_pre_dumps := true

	// What does the configuration say about pre-copy
	tmp := s.instance.ExpandedConfig()["migration.incremental.memory"]

	if tmp != "" {
		use_pre_dumps = shared.IsTrue(tmp)
	}

	var max_iterations int

	// migration.incremental.memory.iterations is the value after which the
	// container will be definitely migrated, even if the remaining number
	// of memory pages is below the defined threshold.
	tmp = s.instance.ExpandedConfig()["migration.incremental.memory.iterations"]
	if tmp != "" {
		max_iterations, _ = strconv.Atoi(tmp)
	} else {
		// default to 10
		max_iterations = 10
	}
	if max_iterations > 999 {
		// the pre-dump directory is hardcoded to a string
		// with maximal 3 digits. 999 pre-dumps makes no
		// sense at all, but let's make sure the number
		// is not higher than this.
		max_iterations = 999
	}
	logger.Debugf("Using maximal %d iterations for pre-dumping", max_iterations)

	return use_pre_dumps, max_iterations
}

// The function readCriuStatsDump() reads the CRIU 'stats-dump' file
// in path and returns the pages_written, pages_skipped_parent, error.
func readCriuStatsDump(path string) (uint64, uint64, error) {
	statsDump := shared.AddSlash(path) + "stats-dump"
	in, err := ioutil.ReadFile(statsDump)
	if err != nil {
		logger.Errorf("Error reading CRIU's 'stats-dump' file: %s", err.Error())
		return 0, 0, err
	}

	// According to the CRIU file image format it starts with two magic values.
	// First magic IMG_SERVICE: 1427134784
	if binary.LittleEndian.Uint32(in[0:4]) != 1427134784 {
		msg := "IMG_SERVICE(1427134784) criu magic not found"
		logger.Errorf(msg)
		return 0, 0, fmt.Errorf(msg)
	}
	// Second magic STATS: 1460220678
	if binary.LittleEndian.Uint32(in[4:8]) != 1460220678 {
		msg := "STATS(1460220678) criu magic not found"
		logger.Errorf(msg)
		return 0, 0, fmt.Errorf(msg)
	}

	// Next, read the size of the image payload
	size := binary.LittleEndian.Uint32(in[8:12])

	statsEntry := &migration.StatsEntry{}
	if err = proto.Unmarshal(in[12:12+size], statsEntry); err != nil {
		logger.Errorf("Failed to parse CRIU's 'stats-dump' file: %s", err.Error())
		return 0, 0, err
	}

	written := statsEntry.GetDump().GetPagesWritten()
	skipped := statsEntry.GetDump().GetPagesSkippedParent()
	return written, skipped, nil
}

type preDumpLoopArgs struct {
	checkpointDir string
	bwlimit       string
	preDumpDir    string
	dumpDir       string
	final         bool
	rsyncFeatures []string
}

// The function preDumpLoop is the main logic behind the pre-copy migration.
// This function contains the actual pre-dump, the corresponding rsync
// transfer and it tells the outer loop to abort if the threshold
// of memory pages transferred by pre-dumping has been reached.
func (s *migrationSourceWs) preDumpLoop(args *preDumpLoopArgs) (bool, error) {
	// Do a CRIU pre-dump
	criuMigrationArgs := CriuMigrationArgs{
		cmd:          lxc.MIGRATE_PRE_DUMP,
		stop:         false,
		actionScript: false,
		preDumpDir:   args.preDumpDir,
		dumpDir:      args.dumpDir,
		stateDir:     args.checkpointDir,
		function:     "migration",
	}

	logger.Debugf("Doing another pre-dump in %s", args.preDumpDir)

	final := args.final

	if s.instance.Type() != instancetype.Container {
		return false, fmt.Errorf("Instance is not container type")
	}

	c := s.instance.(*containerLXC)
	err := c.Migrate(&criuMigrationArgs)
	if err != nil {
		return final, err
	}

	// Send the pre-dump.
	ctName, _, _ := shared.InstanceGetParentAndSnapshotName(s.instance.Name())
	state := s.instance.DaemonState()
	err = rsync.Send(ctName, shared.AddSlash(args.checkpointDir), &shared.WebsocketIO{Conn: s.criuConn}, nil, args.rsyncFeatures, args.bwlimit, state.OS.ExecPath)
	if err != nil {
		return final, err
	}

	// Read the CRIU's 'stats-dump' file
	dumpPath := shared.AddSlash(args.checkpointDir)
	dumpPath += shared.AddSlash(args.dumpDir)
	written, skipped_parent, err := readCriuStatsDump(dumpPath)
	if err != nil {
		return final, err
	}

	logger.Debugf("CRIU pages written %d", written)
	logger.Debugf("CRIU pages skipped %d", skipped_parent)

	total_pages := written + skipped_parent

	percentage_skipped := int(100 - ((100 * written) / total_pages))

	logger.Debugf("CRIU pages skipped percentage %d%%", percentage_skipped)

	// threshold is the percentage of memory pages that needs
	// to be pre-copied for the pre-copy migration to stop.
	var threshold int
	tmp := s.instance.ExpandedConfig()["migration.incremental.memory.goal"]
	if tmp != "" {
		threshold, _ = strconv.Atoi(tmp)
	} else {
		// defaults to 70%
		threshold = 70
	}

	if percentage_skipped > threshold {
		logger.Debugf("Memory pages skipped (%d%%) due to pre-copy is larger than threshold (%d%%)", percentage_skipped, threshold)
		logger.Debugf("This was the last pre-dump; next dump is the final dump")
		final = true
	}

	// If in pre-dump mode, the receiving side
	// expects a message to know if this was the
	// last pre-dump
	logger.Debugf("Sending another header")
	sync := migration.MigrationSync{
		FinalPreDump: proto.Bool(final),
	}

	data, err := proto.Marshal(&sync)

	if err != nil {
		return final, err
	}

	err = s.criuConn.WriteMessage(websocket.BinaryMessage, data)
	if err != nil {
		s.sendControl(err)
		return final, err
	}
	logger.Debugf("Sending another header done")

	return final, nil
}

func (s *migrationSourceWs) Do(state *state.State, migrateOp *operations.Operation) error {
	<-s.allConnected
	if s.instance.Type() != instancetype.Container {
		return fmt.Errorf("Instance is not container type")
	}

	ct := s.instance.(*containerLXC)

	var offerHeader migration.MigrationHeader
	var poolMigrationTypes []migration.Type

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByInstance(state, s.instance)
	if err != storageDrivers.ErrUnknownDriver && err != db.ErrNoSuchObject {
		if err != nil {
			return err
		}

		poolMigrationTypes = pool.MigrationTypes(storagePools.InstanceContentType(s.instance))
		if len(poolMigrationTypes) < 0 {
			return fmt.Errorf("No source migration types available")
		}

		// Convert the pool's migration type options to an offer header to target.
		// Populate the Fs, ZfsFeatures and RsyncFeatures fields.
		offerHeader = migration.TypesToHeader(poolMigrationTypes...)
	} else if s.instance.Type() == instancetype.Container {
		// Fallback to legacy storage layer and populate the Fs, ZfsFeatures and
		// RsyncFeatures fields.

		// Storage needs to start unconditionally now, since we need to initialize a new
		// storage interface.
		ourStart, err := s.instance.StorageStart()
		if err != nil {
			return err
		}
		if ourStart {
			defer s.instance.StorageStop()
		}

		myType := ct.Storage().MigrationType()
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
	} else {
		return fmt.Errorf("Instance type not supported")
	}

	// Add CRIO info to source header.
	criuType := migration.CRIUType_CRIU_RSYNC.Enum()
	if !s.live {
		criuType = nil
		if s.instance.IsRunning() {
			criuType = migration.CRIUType_NONE.Enum()
		}
	}
	offerHeader.Criu = criuType

	// Add idmap info to source header.
	idmaps := make([]*migration.IDMapType, 0)
	idmapset, err := ct.DiskIdmap()
	if err != nil {
		return err
	} else if idmapset != nil {
		for _, ctnIdmap := range idmapset.Idmap {
			idmap := migration.IDMapType{
				Isuid:    proto.Bool(ctnIdmap.Isuid),
				Isgid:    proto.Bool(ctnIdmap.Isgid),
				Hostid:   proto.Int32(int32(ctnIdmap.Hostid)),
				Nsid:     proto.Int32(int32(ctnIdmap.Nsid)),
				Maprange: proto.Int32(int32(ctnIdmap.Maprange)),
			}

			idmaps = append(idmaps, &idmap)
		}
	}

	offerHeader.Idmap = idmaps

	// Add snapshot info to source header if needed.
	snapshots := []*migration.Snapshot{}
	snapshotNames := []string{}
	if !s.instanceOnly {
		fullSnaps, err := s.instance.Snapshots()
		if err == nil {
			for _, snap := range fullSnaps {
				snapshots = append(snapshots, snapshotToProtobuf(snap))
				_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
				snapshotNames = append(snapshotNames, snapName)
			}
		}
	}

	offerHeader.SnapshotNames = snapshotNames
	offerHeader.Snapshots = snapshots

	// Add predump info to source header.
	offerUsePreDumps := false
	maxDumpIterations := 0
	if s.live {
		offerUsePreDumps, maxDumpIterations = s.checkForPreDumpSupport()
	}

	offerHeader.Predump = proto.Bool(offerUsePreDumps)

	// Send offer to target.
	err = s.send(&offerHeader)
	if err != nil {
		s.sendControl(err)
		return err
	}

	// Receive response from target.
	var respHeader migration.MigrationHeader
	err = s.recv(&respHeader)
	if err != nil {
		s.sendControl(err)
		return err
	}

	var legacyDriver MigrationStorageSourceDriver
	var legacyCleanup func()         // Called after migration, to remove any temporary snapshots, etc.
	var migrationType migration.Type // Negotiated migration type.
	var rsyncBwlimit string          // Used for CRIU state and legacy storage rsync transfers.

	// Handle rsync options.
	rsyncFeatures := respHeader.GetRsyncFeaturesSlice()
	if !shared.StringInSlice("bidirectional", rsyncFeatures) {
		// If no bi-directional support, assume LXD 3.7 level.
		// NOTE: Do NOT extend this list of arguments.
		rsyncFeatures = []string{"xattrs", "delete", "compress"}
	}

	// All failure paths need to do a few things to correctly handle errors before returning.
	// Unfortunately, handling errors is not well-suited to defer as the code depends on the
	// status of driver and the error value. The error value is especially tricky due to the
	// common case of creating a new err variable (intentional or not) due to scoping and use
	// of ":=".  Capturing err in a closure for use in defer would be fragile, which defeats
	// the purpose of using defer. An abort function reduces the odds of mishandling errors
	// without introducing the fragility of closing on err.
	abort := func(err error) error {
		if legacyCleanup != nil {
			legacyCleanup()
		}

		go s.sendControl(err)
		return err
	}

	if pool != nil {
		rsyncBwlimit = pool.Driver().Config()["rsync.bwlimit"]
		migrationType, err = migration.MatchTypes(respHeader, migration.MigrationFSType_RSYNC, poolMigrationTypes)
		if err != nil {
			logger.Errorf("Failed to negotiate migration type: %v", err)
			return abort(err)
		}

		sendSnapshotNames := snapshotNames

		// If we are in refresh mode, only send the snapshots the target has asked for.
		if respHeader.GetRefresh() {
			sendSnapshotNames = respHeader.GetSnapshotNames()
		}

		volSourceArgs := migration.VolumeSourceArgs{
			Name:          s.instance.Name(),
			MigrationType: migrationType,
			Snapshots:     sendSnapshotNames,
			TrackProgress: true,
			FinalSync:     false,
		}

		err = pool.MigrateInstance(s.instance, &shared.WebsocketIO{Conn: s.fsConn}, volSourceArgs, migrateOp)
		if err != nil {
			return abort(err)
		}
	} else {
		// Handle zfs options.
		zfsFeatures := respHeader.GetZfsFeaturesSlice()

		// Set source args.
		sourceArgs := MigrationSourceArgs{
			Instance:      s.instance,
			InstanceOnly:  s.instanceOnly,
			RsyncFeatures: rsyncFeatures,
			ZfsFeatures:   zfsFeatures,
		}

		// Initialize storage driver.
		legacyDriver, err = ct.Storage().MigrationSource(sourceArgs)
		if err != nil {
			return abort(err)
		}
		legacyCleanup = legacyDriver.Cleanup

		if respHeader.GetRefresh() || *offerHeader.Fs != *respHeader.Fs {
			myType := migration.MigrationFSType_RSYNC
			respHeader.Fs = &myType

			if respHeader.GetRefresh() {
				legacyDriver, _ = rsyncRefreshSource(respHeader.GetSnapshotNames(), sourceArgs)
			} else {
				legacyDriver, _ = rsyncMigrationSource(sourceArgs)
			}

			// Check if this storage pool has a rate limit set for rsync.
			poolwritable := ct.Storage().GetStoragePoolWritable()
			if poolwritable.Config != nil {
				rsyncBwlimit = poolwritable.Config["rsync.bwlimit"]
			}
		}

		logger.Debugf("SendWhileRunning starting")
		err = legacyDriver.SendWhileRunning(s.fsConn, migrateOp, rsyncBwlimit, s.instanceOnly)
		if err != nil {
			return abort(err)
		}
		logger.Debugf("SendWhileRunning finished")
	}

	restoreSuccess := make(chan bool, 1)
	dumpSuccess := make(chan error, 1)

	if s.live {
		if respHeader.Criu == nil {
			return abort(fmt.Errorf("Got no CRIU socket type for live migration"))
		} else if *respHeader.Criu != migration.CRIUType_CRIU_RSYNC {
			return abort(fmt.Errorf("Formats other than criu rsync not understood"))
		}

		checkpointDir, err := ioutil.TempDir("", "lxd_checkpoint_")
		if err != nil {
			return abort(err)
		}

		if util.RuntimeLiblxcVersionAtLeast(2, 0, 4) {
			// What happens below is slightly convoluted. Due to various complications
			// with networking, there's no easy way for criu to exit and leave the
			// container in a frozen state for us to somehow resume later.
			// Instead, we use what criu calls an "action-script", which is basically a
			// callback that lets us know when the dump is done. (Unfortunately, we
			// can't pass arguments, just an executable path, so we write a custom
			// action script with the real command we want to run.)
			// This script then hangs until the migration operation either finishes
			// successfully or fails, and exits 1 or 0, which causes criu to either
			// leave the container running or kill it as we asked.
			dumpDone := make(chan bool, 1)
			actionScriptOpSecret, err := shared.RandomCryptoString()
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			actionScriptOp, err := operations.OperationCreate(
				state,
				s.instance.Project(),
				operations.OperationClassWebsocket,
				db.OperationContainerLiveMigrate,
				nil,
				nil,
				func(op *operations.Operation) error {
					result := <-restoreSuccess
					if !result {
						return fmt.Errorf("restore failed, failing CRIU")
					}
					return nil
				},
				nil,
				func(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
					secret := r.FormValue("secret")
					if secret == "" {
						return fmt.Errorf("missing secret")
					}

					if secret != actionScriptOpSecret {
						return os.ErrPermission
					}

					c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
					if err != nil {
						return err
					}

					dumpDone <- true

					closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
					return c.WriteMessage(websocket.CloseMessage, closeMsg)
				},
			)
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			err = writeActionScript(checkpointDir, actionScriptOp.URL(), actionScriptOpSecret, state.OS.ExecPath)
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			preDumpCounter := 0
			preDumpDir := ""

			// Check if the other side knows about pre-dumping and the associated
			// rsync protocol.
			if respHeader.GetPredump() {
				logger.Debugf("The other side does support pre-copy")
				final := false
				for !final {
					preDumpCounter++
					if preDumpCounter < maxDumpIterations {
						final = false
					} else {
						final = true
					}
					dumpDir := fmt.Sprintf("%03d", preDumpCounter)
					loopArgs := preDumpLoopArgs{
						checkpointDir: checkpointDir,
						bwlimit:       rsyncBwlimit,
						preDumpDir:    preDumpDir,
						dumpDir:       dumpDir,
						final:         final,
						rsyncFeatures: rsyncFeatures,
					}
					final, err = s.preDumpLoop(&loopArgs)
					if err != nil {
						os.RemoveAll(checkpointDir)
						return abort(err)
					}
					preDumpDir = fmt.Sprintf("%03d", preDumpCounter)
					preDumpCounter++
				}
			} else {
				logger.Debugf("The other side does not support pre-copy")
			}

			_, err = actionScriptOp.Run()
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			go func() {
				criuMigrationArgs := CriuMigrationArgs{
					cmd:          lxc.MIGRATE_DUMP,
					stop:         true,
					actionScript: true,
					preDumpDir:   preDumpDir,
					dumpDir:      "final",
					stateDir:     checkpointDir,
					function:     "migration",
				}

				// Do the final CRIU dump. This is needs no special handling if
				// pre-dumps are used or not.
				dumpSuccess <- ct.Migrate(&criuMigrationArgs)
				os.RemoveAll(checkpointDir)
			}()

			select {
			// The checkpoint failed, let's just abort.
			case err = <-dumpSuccess:
				return abort(err)
			// The dump finished, let's continue on to the restore.
			case <-dumpDone:
				logger.Debugf("Dump finished, continuing with restore...")
			}
		} else {
			logger.Debugf("The version of liblxc is older than 2.0.4 and the live migration will probably fail")
			defer os.RemoveAll(checkpointDir)
			criuMigrationArgs := CriuMigrationArgs{
				cmd:          lxc.MIGRATE_DUMP,
				stateDir:     checkpointDir,
				function:     "migration",
				stop:         true,
				actionScript: false,
				dumpDir:      "final",
				preDumpDir:   "",
			}

			err = ct.Migrate(&criuMigrationArgs)
			if err != nil {
				return abort(err)
			}
		}

		// We do the transfer serially right now, but there's really no reason for us to;
		// since we have separate websockets, we can do it in parallel if we wanted to.
		// However assuming we're network bound, there's really no reason to do these in.
		// parallel. In the future when we're using p.haul's protocol, it will make sense
		// to do these in parallel.
		ctName, _, _ := shared.InstanceGetParentAndSnapshotName(s.instance.Name())
		err = rsync.Send(ctName, shared.AddSlash(checkpointDir), &shared.WebsocketIO{Conn: s.criuConn}, nil, rsyncFeatures, rsyncBwlimit, state.OS.ExecPath)
		if err != nil {
			return abort(err)
		}
	}

	// If s.live is true or Criu is set to CRIUTYPE_NONE rather than nil, it indicates
	// that the source container is running and that we should do a two stage transfer
	// to minimize downtime.
	if s.live || (respHeader.Criu != nil && *respHeader.Criu == migration.CRIUType_NONE) {
		if pool != nil {
			volSourceArgs := migration.VolumeSourceArgs{
				Name:          s.instance.Name(),
				MigrationType: migrationType,
				TrackProgress: true,
				FinalSync:     true,
			}

			err = pool.MigrateInstance(s.instance, &shared.WebsocketIO{Conn: s.fsConn}, volSourceArgs, migrateOp)
			if err != nil {
				return abort(err)
			}
		} else {
			logger.Debugf("SendAfterCheckpoint starting")
			err = legacyDriver.SendAfterCheckpoint(s.fsConn, rsyncBwlimit)
			if err != nil {
				return abort(err)
			}
			logger.Debugf("SendAfterCheckpoint finished")
		}
	}

	// Perform any storage level cleanup, such as removing any temporary snapshots.
	if legacyCleanup != nil {
		legacyCleanup()
	}

	msg := migration.MigrationControl{}
	err = s.recv(&msg)
	if err != nil {
		s.disconnect()
		return err
	}

	if s.live {
		restoreSuccess <- *msg.Success
		err := <-dumpSuccess
		if err != nil {
			logger.Errorf("Dump failed after successful restore?: %q", err)
		}
	}

	if !*msg.Success {
		return fmt.Errorf(*msg.Message)
	}

	return nil
}

func NewMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:     migrationFields{instance: args.Instance, instanceOnly: args.InstanceOnly},
		dest:    migrationFields{instanceOnly: args.InstanceOnly},
		url:     args.Url,
		dialer:  args.Dialer,
		push:    args.Push,
		refresh: args.Refresh,
	}

	if sink.push {
		sink.allConnected = make(chan bool, 1)
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
		sink.src.live = ok
	}

	_, err = exec.LookPath("criu")
	if sink.push && sink.dest.live && err != nil {
		return nil, fmt.Errorf("Unable to perform container live migration. CRIU isn't installed on the destination server")
	} else if sink.src.live && err != nil {
		return nil, fmt.Errorf("Unable to perform container live migration. CRIU isn't installed on the destination server")
	}

	return &sink, nil
}

func (c *migrationSink) Do(state *state.State, migrateOp *operations.Operation) error {
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
			return err
		}
		defer c.src.disconnect()

		c.src.fsConn, err = c.connectWithSecret(c.src.fsSecret)
		if err != nil {
			c.src.sendControl(err)
			return err
		}

		if c.src.live {
			c.src.criuConn, err = c.connectWithSecret(c.src.criuSecret)
			if err != nil {
				c.src.sendControl(err)
				return err
			}
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
		controller(err)
		return err
	}

	live := c.src.live
	if c.push {
		live = c.dest.live
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

	// The migration header to be sent back to source with our target options.
	var respHeader migration.MigrationHeader

	pool, err := storagePools.GetPoolByInstance(state, c.src.instance)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented {
		if err != nil {
			return err
		}

		// Extract the source's migration type and then match it against our pool's
		// supported types and features. If a match is found the combined features list
		// will be sent back to requester.
		respType, err := migration.MatchTypes(offerHeader, migration.MigrationFSType_RSYNC, pool.MigrationTypes(storagePools.InstanceContentType(c.src.instance)))
		if err != nil {
			return err
		}

		// Convert response type to response header and copy snapshot info into it.
		respHeader = migration.TypesToHeader(respType)
		respHeader.SnapshotNames = offerHeader.SnapshotNames
		respHeader.Snapshots = offerHeader.Snapshots
		respHeader.Refresh = &c.refresh

		// Translate the legacy MigrationSinkArgs to a VolumeTargetArgs suitable for use
		// with the new storage layer.
		myTarget = func(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
			volTargetArgs := migration.VolumeTargetArgs{
				Name:          args.Instance.Name(),
				MigrationType: respType,
				Refresh:       args.Refresh, // Indicate to receiver volume should exist.
				TrackProgress: false,        // Do not use a progress tracker on receiver.
				Live:          args.Live,    // Indicates we will get a final rootfs sync.
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
					snapArgs := snapshotProtobufToInstanceArgs(args.Instance.Project(), args.Instance.Name(), snap)

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

					// Check if snapshot exists already and if not then create
					// a new snapshot DB record so that the storage layer can
					// populate the volume on the storage device.
					_, err := instance.LoadByProjectAndName(args.Instance.DaemonState(), args.Instance.Project(), snapArgs.Name)
					if err != nil {
						// Create the snapshot as it doesn't seem to exist.
						_, err := instanceCreateInternal(state, snapArgs)
						if err != nil {
							return err
						}
					}
				}
			}

			return pool.CreateInstanceFromMigration(args.Instance, &shared.WebsocketIO{Conn: conn}, volTargetArgs, op)
		}
	} else if c.src.instance.Type() == instancetype.Container {
		ct := c.src.instance.(*containerLXC)
		myTarget = ct.Storage().MigrationSink
		myType := ct.Storage().MigrationType()

		respHeader = migration.MigrationHeader{
			Fs:            &myType,
			Snapshots:     offerHeader.Snapshots,
			SnapshotNames: offerHeader.SnapshotNames,
			Refresh:       &c.refresh,
		}

		// Return those rsync features we know about (with the value sent by the remote).
		if offerHeader.RsyncFeatures != nil {
			respHeader.RsyncFeatures = &migration.RsyncFeatures{
				Xattrs:        offerHeader.RsyncFeatures.Xattrs,
				Delete:        offerHeader.RsyncFeatures.Delete,
				Compress:      offerHeader.RsyncFeatures.Compress,
				Bidirectional: offerHeader.RsyncFeatures.Bidirectional,
			}
		}

		// Return those ZFS features we know about (with the value sent by the remote).
		if len(zfsVersion) >= 3 && zfsVersion[0:3] != "0.6" {
			if offerHeader.ZfsFeatures != nil && offerHeader.ZfsFeatures.Compress != nil {
				respHeader.ZfsFeatures = &migration.ZfsFeatures{
					Compress: offerHeader.ZfsFeatures.Compress,
				}
			}
		}

		// If refresh mode or the storage type the source has doesn't match what we have,
		// then we have to use rsync.
		if c.refresh || *offerHeader.Fs != *respHeader.Fs {
			myTarget = rsyncMigrationSink
			myType = migration.MigrationFSType_RSYNC
		}
	} else {
		return fmt.Errorf("Instance type not supported")
	}

	// Add CRIU info to response.
	respHeader.Criu = criuType

	if c.refresh {
		// Get our existing snapshots.
		targetSnapshots, err := c.src.instance.Snapshots()
		if err != nil {
			controller(err)
			return err
		}

		// Get the remote snapshots.
		sourceSnapshots := offerHeader.GetSnapshots()

		// Compare the two sets.
		syncSnapshots, deleteSnapshots := migrationCompareSnapshots(sourceSnapshots, targetSnapshots)

		// Delete the extra local ones.
		for _, snap := range deleteSnapshots {
			err := snap.Delete()
			if err != nil {
				controller(err)
				return err
			}
		}

		snapshotNames := []string{}
		for _, snap := range syncSnapshots {
			snapshotNames = append(snapshotNames, snap.GetName())
		}

		respHeader.Snapshots = syncSnapshots
		respHeader.SnapshotNames = snapshotNames
		offerHeader.Snapshots = syncSnapshots
		offerHeader.SnapshotNames = snapshotNames
	}

	if offerHeader.GetPredump() == true {
		// If the other side wants pre-dump and if this side supports it, let's use it.
		respHeader.Predump = proto.Bool(true)
	} else {
		respHeader.Predump = proto.Bool(false)
	}

	// Get rsync options from sender, these are passed into mySink function as part of
	// MigrationSinkArgs below.
	rsyncFeatures := respHeader.GetRsyncFeaturesSlice()

	err = sender(&respHeader)
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
				Maprange: int64(*idmapSet.Maprange)}
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
				for _, name := range offerHeader.SnapshotNames {
					base := snapshotToProtobuf(c.src.instance)
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
			if live {
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
				ct := c.src.instance.(*containerLXC)
				err = resetContainerDiskIdmap(ct, srcIdmap)
				if err != nil {
					fsTransfer <- err
					return
				}
			}

			fsTransfer <- nil
		}()

		if live {
			var err error
			imagesDir, err = ioutil.TempDir("", "lxd_restore_")
			if err != nil {
				restore <- err
				return
			}

			defer os.RemoveAll(imagesDir)

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
					logger.Debugf("About to receive rsync")
					// Transfer a CRIU pre-dump.
					err = rsync.Recv(shared.AddSlash(imagesDir), &shared.WebsocketIO{Conn: criuConn}, nil, rsyncFeatures)
					if err != nil {
						restore <- err
						return
					}
					logger.Debugf("Done receiving from rsync")

					logger.Debugf("About to receive header")
					// Check if this was the last pre-dump.
					// Only the FinalPreDump element if of interest.
					mtype, data, err := criuConn.ReadMessage()
					if err != nil {
						restore <- err
						return
					}
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
			err = rsync.Recv(shared.AddSlash(imagesDir), &shared.WebsocketIO{Conn: criuConn}, nil, rsyncFeatures)
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
			criuMigrationArgs := CriuMigrationArgs{
				cmd:          lxc.MIGRATE_RESTORE,
				stateDir:     imagesDir,
				function:     "migration",
				stop:         false,
				actionScript: false,
				dumpDir:      "final",
				preDumpDir:   "",
			}

			// Currently we only do a single CRIU pre-dump so we can hardcode "final"
			// here since we know that "final" is the folder for CRIU's final dump.
			if c.src.instance.Type() == instancetype.Container {
				ct := c.src.instance.(*containerLXC)
				err = ct.Migrate(&criuMigrationArgs)
				if err != nil {
					restore <- err
					return
				}
			}
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
			controller(err)
			return err
		case msg, ok := <-source:
			if !ok {
				disconnector()
				return fmt.Errorf("Got error reading source")
			}
			if !*msg.Success {
				disconnector()
				return fmt.Errorf(*msg.Message)
			}

			// The source can only tell us it failed (e.g. if checkpointing failed).
			// We have to tell the source whether or not the restore was successful.
			logger.Debugf("Unknown message %v from source", msg)
		}
	}
}

func (s *migrationSourceWs) ConnectContainerTarget(target api.InstancePostTarget) error {
	return s.ConnectTarget(target.Certificate, target.Operation, target.Websockets)
}

func migrationCompareSnapshots(sourceSnapshots []*migration.Snapshot, targetSnapshots []instance.Instance) ([]*migration.Snapshot, []instance.Instance) {
	// Compare source and target
	sourceSnapshotsTime := map[string]int64{}
	targetSnapshotsTime := map[string]int64{}

	toDelete := []instance.Instance{}
	toSync := []*migration.Snapshot{}

	for _, snap := range sourceSnapshots {
		snapName := snap.GetName()

		sourceSnapshotsTime[snapName] = snap.GetCreationDate()
	}

	for _, snap := range targetSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		targetSnapshotsTime[snapName] = snap.CreationDate().Unix()
		existDate, exists := sourceSnapshotsTime[snapName]
		if !exists {
			toDelete = append(toDelete, snap)
		} else if existDate != snap.CreationDate().Unix() {
			toDelete = append(toDelete, snap)
		}
	}

	for _, snap := range sourceSnapshots {
		snapName := snap.GetName()

		existDate, exists := targetSnapshotsTime[snapName]
		if !exists || existDate != snap.GetCreationDate() {
			toSync = append(toSync, snap)
		}
	}

	return toSync, toDelete
}
