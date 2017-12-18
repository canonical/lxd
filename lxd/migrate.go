// Package migration provides the primitives for migration in LXD.
//
// See https://github.com/lxc/lxd/blob/master/specs/migration.md for a complete
// description.

package main

import (
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

type migrationFields struct {
	live bool

	containerOnly bool

	controlSecret string
	controlConn   *websocket.Conn
	controlLock   sync.Mutex

	criuSecret string
	criuConn   *websocket.Conn

	fsSecret string
	fsConn   *websocket.Conn

	container container
}

func (c *migrationFields) send(m proto.Message) error {
	/* gorilla websocket doesn't allow concurrent writes, and
	 * panic()s if it sees them (which is reasonable). If e.g. we
	 * happen to fail, get scheduled, start our write, then get
	 * unscheduled before the write is bit to a new thread which is
	 * receiving an error from the other side (due to our previous
	 * close), we can engage in these concurrent writes, which
	 * casuses the whole daemon to panic.
	 *
	 * Instead, let's lock sends to the controlConn so that we only ever
	 * write one message at the time.
	 */
	c.controlLock.Lock()
	defer c.controlLock.Unlock()
	w, err := c.controlConn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer w.Close()

	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}

	return shared.WriteAll(w, data)
}

func (c *migrationFields) recv(m proto.Message) error {
	mt, r, err := c.controlConn.NextReader()
	if err != nil {
		return err
	}

	if mt != websocket.BinaryMessage {
		return fmt.Errorf("Only binary messages allowed")
	}

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	return proto.Unmarshal(buf, m)
}

func (c *migrationFields) disconnect() {
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")

	c.controlLock.Lock()
	if c.controlConn != nil {
		c.controlConn.WriteMessage(websocket.CloseMessage, closeMsg)
		c.controlConn = nil /* don't close twice */
	}
	c.controlLock.Unlock()

	/* Below we just Close(), which doesn't actually write to the
	 * websocket, it just closes the underlying connection. If e.g. there
	 * is still a filesystem transfer going on, but the other side has run
	 * out of disk space, writing an actual CloseMessage here will cause
	 * gorilla websocket to panic. Instead, we just force close this
	 * connection, since we report the error over the control channel
	 * anyway.
	 */
	if c.fsConn != nil {
		c.fsConn.Close()
	}

	if c.criuConn != nil {
		c.criuConn.Close()
	}
}

func (c *migrationFields) sendControl(err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}

	msg := MigrationControl{
		Success: proto.Bool(err == nil),
		Message: proto.String(message),
	}
	c.send(&msg)

	if err != nil {
		c.disconnect()
	}
}

func (c *migrationFields) controlChannel() <-chan MigrationControl {
	ch := make(chan MigrationControl)
	go func() {
		msg := MigrationControl{}
		err := c.recv(&msg)
		if err != nil {
			logger.Debugf("Got error reading migration control socket %s", err)
			close(ch)
			return
		}
		ch <- msg
	}()

	return ch
}

type migrationSourceWs struct {
	migrationFields

	allConnected chan bool
}

func NewMigrationSource(c container, stateful bool, containerOnly bool) (*migrationSourceWs, error) {
	ret := migrationSourceWs{migrationFields{container: c}, make(chan bool, 1)}
	ret.containerOnly = containerOnly

	var err error
	ret.controlSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	if stateful && c.IsRunning() {
		_, err := exec.LookPath("criu")
		if err != nil {
			return nil, fmt.Errorf("Unable to perform container live migration. CRIU isn't installed on the source server.")
		}

		ret.live = true
		ret.criuSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, err
		}
	}

	return &ret, nil
}

func (s *migrationSourceWs) Metadata() interface{} {
	secrets := shared.Jmap{
		"control": s.controlSecret,
		"fs":      s.fsSecret,
	}

	if s.criuSecret != "" {
		secrets["criu"] = s.criuSecret
	}

	return secrets
}

func (s *migrationSourceWs) Connect(op *operation, r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return fmt.Errorf("missing secret")
	}

	var conn **websocket.Conn

	switch secret {
	case s.controlSecret:
		conn = &s.controlConn
	case s.criuSecret:
		conn = &s.criuConn
	case s.fsSecret:
		conn = &s.fsConn
	default:
		// If we didn't find the right secret, the user provided a bad
		// one, which 403, not 404, since this operation actually
		// exists.
		return os.ErrPermission
	}

	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	*conn = c

	if s.controlConn != nil && (!s.live || s.criuConn != nil) && s.fsConn != nil {
		s.allConnected <- true
	}

	return nil
}

func (s *migrationSourceWs) ConnectTarget(target api.ContainerPostTarget) error {
	var err error
	var cert *x509.Certificate

	if target.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(target.Certificate))
		if certBlock == nil {
			return fmt.Errorf("Invalid certificate")
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return err
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		TLSClientConfig: config,
		NetDial:         shared.RFC3493Dialer,
	}

	for name, secret := range target.Websockets {
		var conn **websocket.Conn

		switch name {
		case "control":
			conn = &s.controlConn
		case "fs":
			conn = &s.fsConn
		case "criu":
			conn = &s.criuConn
		default:
			return fmt.Errorf("Unknown secret provided: %s", name)
		}

		query := url.Values{"secret": []string{secret}}

		// The URL is a https URL to the operation, mangle to be a wss URL to the secret
		wsUrl := fmt.Sprintf("wss://%s/websocket?%s", strings.TrimPrefix(target.Operation, "https://"), query.Encode())

		wsConn, _, err := dialer.Dial(wsUrl, http.Header{})
		if err != nil {
			return err
		}

		*conn = wsConn
	}

	s.allConnected <- true

	return nil
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

func snapshotToProtobuf(c container) *Snapshot {
	config := []*Config{}
	for k, v := range c.LocalConfig() {
		kCopy := string(k)
		vCopy := string(v)
		config = append(config, &Config{Key: &kCopy, Value: &vCopy})
	}

	devices := []*Device{}
	for name, d := range c.LocalDevices() {
		props := []*Config{}
		for k, v := range d {
			kCopy := string(k)
			vCopy := string(v)
			props = append(props, &Config{Key: &kCopy, Value: &vCopy})
		}

		devices = append(devices, &Device{Name: &name, Config: props})
	}

	parts := strings.SplitN(c.Name(), shared.SnapshotDelimiter, 2)
	isEphemeral := c.IsEphemeral()
	arch := int32(c.Architecture())
	stateful := c.IsStateful()

	return &Snapshot{
		Name:         &parts[len(parts)-1],
		LocalConfig:  config,
		Profiles:     c.Profiles(),
		Ephemeral:    &isEphemeral,
		LocalDevices: devices,
		Architecture: &arch,
		Stateful:     &stateful,
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
	err := s.container.Migrate(&criuMigrationArgs)

	if err != nil {
		// CRIU says it does not know about dirty memory tracking.
		// This means the rest of this function is irrelevant.
		return false, 0
	}

	// CRIU says it can actually do pre-dump. Let's set it to true
	// unless the user wants something else.
	use_pre_dumps := true

	// What does the configuration say about pre-copy
	tmp := s.container.ExpandedConfig()["migration.incremental.memory"]

	if tmp != "" {
		use_pre_dumps = shared.IsTrue(tmp)
	}
	logger.Debugf("migration.incremental.memory %d", use_pre_dumps)

	var max_iterations int

	// migration.incremental.memory.iterations is the value after which the
	// container will be definitely migrated, even if the remaining number
	// of memory pages is below the defined threshold.
	tmp = s.container.ExpandedConfig()["migration.incremental.memory.iterations"]
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
	logger.Debugf("using maximal %d iterations for pre-dumping", max_iterations)

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
	logger.Debugf("stats-dump payload size %d", size)

	statsEntry := &StatsEntry{}
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

	err := s.container.Migrate(&criuMigrationArgs)
	if err != nil {
		return final, err
	}

	// Send the pre-dump.
	ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())
	state := s.container.DaemonState()
	err = RsyncSend(ctName, shared.AddSlash(args.checkpointDir), s.criuConn, nil, args.bwlimit, state.OS.ExecPath)
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
	tmp := s.container.ExpandedConfig()["migration.incremental.memory.goal"]
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
	sync := MigrationSync{
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

func (s *migrationSourceWs) Do(migrateOp *operation) error {
	<-s.allConnected

	criuType := CRIUType_CRIU_RSYNC.Enum()
	if !s.live {
		criuType = nil
		if s.container.IsRunning() {
			criuType = CRIUType_NONE.Enum()
		}
	}

	// Storage needs to start unconditionally now, since we need to
	// initialize a new storage interface.
	ourStart, err := s.container.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer s.container.StorageStop()
	}

	idmaps := make([]*IDMapType, 0)

	idmapset, err := s.container.IdmapSet()
	if err != nil {
		return err
	}

	if idmapset != nil {
		for _, ctnIdmap := range idmapset.Idmap {
			idmap := IDMapType{
				Isuid:    proto.Bool(ctnIdmap.Isuid),
				Isgid:    proto.Bool(ctnIdmap.Isgid),
				Hostid:   proto.Int32(int32(ctnIdmap.Hostid)),
				Nsid:     proto.Int32(int32(ctnIdmap.Nsid)),
				Maprange: proto.Int32(int32(ctnIdmap.Maprange)),
			}

			idmaps = append(idmaps, &idmap)
		}
	}

	driver, fsErr := s.container.Storage().MigrationSource(s.container, s.containerOnly)

	snapshots := []*Snapshot{}
	snapshotNames := []string{}
	// Only send snapshots when requested.
	if !s.containerOnly {
		if fsErr == nil {
			fullSnaps := driver.Snapshots()
			for _, snap := range fullSnaps {
				snapshots = append(snapshots, snapshotToProtobuf(snap))
				snapshotNames = append(snapshotNames, shared.ExtractSnapshotName(snap.Name()))
			}
		}
	}

	use_pre_dumps, max_iterations := s.checkForPreDumpSupport()

	// The protocol says we have to send a header no matter what, so let's
	// do that, but then immediately send an error.
	myType := s.container.Storage().MigrationType()
	header := MigrationHeader{
		Fs:            &myType,
		Criu:          criuType,
		Idmap:         idmaps,
		SnapshotNames: snapshotNames,
		Snapshots:     snapshots,
		Predump:       proto.Bool(use_pre_dumps),
	}

	err = s.send(&header)
	if err != nil {
		s.sendControl(err)
		return err
	}

	if fsErr != nil {
		s.sendControl(fsErr)
		return fsErr
	}

	err = s.recv(&header)
	if err != nil {
		s.sendControl(err)
		return err
	}

	bwlimit := ""
	if *header.Fs != myType {
		myType = MigrationFSType_RSYNC
		header.Fs = &myType

		driver, _ = rsyncMigrationSource(s.container, s.containerOnly)

		// Check if this storage pool has a rate limit set for rsync.
		poolwritable := s.container.Storage().GetStoragePoolWritable()
		if poolwritable.Config != nil {
			bwlimit = poolwritable.Config["rsync.bwlimit"]
		}
	}

	// Check if the other side knows about pre-dumping and
	// the associated rsync protocol
	use_pre_dumps = header.GetPredump()
	if use_pre_dumps {
		logger.Debugf("The other side does support pre-copy")
	} else {
		logger.Debugf("The other side does not support pre-copy")
	}

	// All failure paths need to do a few things to correctly handle errors before returning.
	// Unfortunately, handling errors is not well-suited to defer as the code depends on the
	// status of driver and the error value.  The error value is especially tricky due to the
	// common case of creating a new err variable (intentional or not) due to scoping and use
	// of ":=".  Capturing err in a closure for use in defer would be fragile, which defeats
	// the purpose of using defer.  An abort function reduces the odds of mishandling errors
	// without introducing the fragility of closing on err.
	abort := func(err error) error {
		driver.Cleanup()
		s.sendControl(err)
		return err
	}

	err = driver.SendWhileRunning(s.fsConn, migrateOp, bwlimit, s.containerOnly)
	if err != nil {
		return abort(err)
	}

	restoreSuccess := make(chan bool, 1)
	dumpSuccess := make(chan error, 1)

	if s.live {
		if header.Criu == nil {
			return abort(fmt.Errorf("Got no CRIU socket type for live migration"))
		} else if *header.Criu != CRIUType_CRIU_RSYNC {
			return abort(fmt.Errorf("Formats other than criu rsync not understood"))
		}

		checkpointDir, err := ioutil.TempDir("", "lxd_checkpoint_")
		if err != nil {
			return abort(err)
		}

		if util.RuntimeLiblxcVersionAtLeast(2, 0, 4) {
			/* What happens below is slightly convoluted. Due to various
			 * complications with networking, there's no easy way for criu
			 * to exit and leave the container in a frozen state for us to
			 * somehow resume later.
			 *
			 * Instead, we use what criu calls an "action-script", which is
			 * basically a callback that lets us know when the dump is
			 * done. (Unfortunately, we can't pass arguments, just an
			 * executable path, so we write a custom action script with the
			 * real command we want to run.)
			 *
			 * This script then hangs until the migration operation either
			 * finishes successfully or fails, and exits 1 or 0, which
			 * causes criu to either leave the container running or kill it
			 * as we asked.
			 */
			dumpDone := make(chan bool, 1)
			actionScriptOpSecret, err := shared.RandomCryptoString()
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			actionScriptOp, err := operationCreate(
				operationClassWebsocket,
				nil,
				nil,
				func(op *operation) error {
					result := <-restoreSuccess
					if !result {
						return fmt.Errorf("restore failed, failing CRIU")
					}
					return nil
				},
				nil,
				func(op *operation, r *http.Request, w http.ResponseWriter) error {
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

			state := s.container.DaemonState()
			err = writeActionScript(checkpointDir, actionScriptOp.url, actionScriptOpSecret, state.OS.ExecPath)
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			preDumpCounter := 0
			preDumpDir := ""
			if use_pre_dumps {
				final := false
				for !final {
					preDumpCounter++
					if preDumpCounter < max_iterations {
						final = false
					} else {
						final = true
					}
					dumpDir := fmt.Sprintf("%03d", preDumpCounter)
					loop_args := preDumpLoopArgs{
						checkpointDir: checkpointDir,
						bwlimit:       bwlimit,
						preDumpDir:    preDumpDir,
						dumpDir:       dumpDir,
						final:         final,
					}
					final, err = s.preDumpLoop(&loop_args)
					if err != nil {
						os.RemoveAll(checkpointDir)
						return abort(err)
					}
					preDumpDir = fmt.Sprintf("%03d", preDumpCounter)
					preDumpCounter++
				}
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

				// Do the final CRIU dump. This is needs no special
				// handling if pre-dumps are used or not
				dumpSuccess <- s.container.Migrate(&criuMigrationArgs)
				os.RemoveAll(checkpointDir)
			}()

			select {
			/* the checkpoint failed, let's just abort */
			case err = <-dumpSuccess:
				return abort(err)
			/* the dump finished, let's continue on to the restore */
			case <-dumpDone:
				logger.Debugf("Dump finished, continuing with restore...")
			}
		} else {
			logger.Debugf("liblxc version is older than 2.0.4 and the live migration will probably fail")
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

			err = s.container.Migrate(&criuMigrationArgs)
			if err != nil {
				return abort(err)
			}
		}

		/*
		 * We do the serially right now, but there's really no reason for us
		 * to; since we have separate websockets, we can do it in parallel if
		 * we wanted to. However, assuming we're network bound, there's really
		 * no reason to do these in parallel. In the future when we're using
		 * p.haul's protocol, it will make sense to do these in parallel.
		 */
		ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())
		state := s.container.DaemonState()
		err = RsyncSend(ctName, shared.AddSlash(checkpointDir), s.criuConn, nil, bwlimit, state.OS.ExecPath)
		if err != nil {
			return abort(err)
		}
	}

	if s.live || (header.Criu != nil && *header.Criu == CRIUType_NONE) {
		err = driver.SendAfterCheckpoint(s.fsConn, bwlimit)
		if err != nil {
			return abort(err)
		}
	}

	driver.Cleanup()

	msg := MigrationControl{}
	err = s.recv(&msg)
	if err != nil {
		s.disconnect()
		return err
	}

	if s.live {
		restoreSuccess <- *msg.Success
		err := <-dumpSuccess
		if err != nil {
			logger.Errorf("dump failed after successful restore?: %q", err)
		}
	}

	if !*msg.Success {
		return fmt.Errorf(*msg.Message)
	}

	return nil
}

type migrationSink struct {
	// We are pulling the container from src in pull mode.
	src migrationFields
	// The container is pushed from src to dest in push mode. Note that
	// websocket connections are not set in push mode. Only the secret
	// fields are used since the client will connect to the sockets.
	dest migrationFields

	url          string
	dialer       websocket.Dialer
	allConnected chan bool
	push         bool
}

type MigrationSinkArgs struct {
	Url           string
	Dialer        websocket.Dialer
	Container     container
	Secrets       map[string]string
	Push          bool
	Live          bool
	ContainerOnly bool
}

func NewMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:    migrationFields{container: args.Container, containerOnly: args.ContainerOnly},
		dest:   migrationFields{containerOnly: args.ContainerOnly},
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
		return nil, fmt.Errorf("Unable to perform container live migration. CRIU isn't installed on the destination server.")
	} else if sink.src.live && err != nil {
		return nil, fmt.Errorf("Unable to perform container live migration. CRIU isn't installed on the destination server.")
	}

	return &sink, nil
}

func (c *migrationSink) connectWithSecret(secret string) (*websocket.Conn, error) {
	query := url.Values{"secret": []string{secret}}

	// The URL is a https URL to the operation, mangle to be a wss URL to the secret
	wsUrl := fmt.Sprintf("wss://%s/websocket?%s", strings.TrimPrefix(c.url, "https://"), query.Encode())

	conn, _, err := c.dialer.Dial(wsUrl, http.Header{})
	if err != nil {
		return nil, err
	}

	return conn, err
}

func (s *migrationSink) Metadata() interface{} {
	secrets := shared.Jmap{
		"control": s.dest.controlSecret,
		"fs":      s.dest.fsSecret,
	}

	if s.dest.criuSecret != "" {
		secrets["criu"] = s.dest.criuSecret
	}

	return secrets
}

func (s *migrationSink) Connect(op *operation, r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return fmt.Errorf("missing secret")
	}

	var conn **websocket.Conn

	switch secret {
	case s.dest.controlSecret:
		conn = &s.dest.controlConn
	case s.dest.criuSecret:
		conn = &s.dest.criuConn
	case s.dest.fsSecret:
		conn = &s.dest.fsConn
	default:
		/* If we didn't find the right secret, the user provided a bad one,
		 * which 403, not 404, since this operation actually exists */
		return os.ErrPermission
	}

	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	*conn = c

	if s.dest.controlConn != nil && (!s.dest.live || s.dest.criuConn != nil) && s.dest.fsConn != nil {
		s.allConnected <- true
	}

	return nil
}

func (c *migrationSink) Do(migrateOp *operation) error {
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

	header := MigrationHeader{}
	if err := receiver(&header); err != nil {
		controller(err)
		return err
	}

	live := c.src.live
	if c.push {
		live = c.dest.live
	}

	criuType := CRIUType_CRIU_RSYNC.Enum()
	if header.Criu != nil && *header.Criu == CRIUType_NONE {
		criuType = CRIUType_NONE.Enum()
	} else {
		if !live {
			criuType = nil
		}
	}

	mySink := c.src.container.Storage().MigrationSink
	myType := c.src.container.Storage().MigrationType()
	resp := MigrationHeader{
		Fs:   &myType,
		Criu: criuType,
	}

	// If the storage type the source has doesn't match what we have, then
	// we have to use rsync.
	if *header.Fs != *resp.Fs {
		mySink = rsyncMigrationSink
		myType = MigrationFSType_RSYNC
		resp.Fs = &myType
	}

	if header.GetPredump() == true {
		// If the other side wants pre-dump and if
		// this side supports it, let's use it.
		resp.Predump = proto.Bool(true)
	} else {
		resp.Predump = proto.Bool(false)
	}

	err = sender(&resp)
	if err != nil {
		controller(err)
		return err
	}

	restore := make(chan error)
	go func(c *migrationSink) {
		imagesDir := ""
		srcIdmap := new(idmap.IdmapSet)

		for _, idmapSet := range header.Idmap {
			e := idmap.IdmapEntry{
				Isuid:    *idmapSet.Isuid,
				Isgid:    *idmapSet.Isgid,
				Nsid:     int64(*idmapSet.Nsid),
				Hostid:   int64(*idmapSet.Hostid),
				Maprange: int64(*idmapSet.Maprange)}
			srcIdmap.Idmap = idmap.Extend(srcIdmap.Idmap, e)
		}

		/* We do the fs receive in parallel so we don't have to reason
		 * about when to receive what. The sending side is smart enough
		 * to send the filesystem bits that it can before it seizes the
		 * container to start checkpointing, so the total transfer time
		 * will be minimized even if we're dumb here.
		 */
		fsTransfer := make(chan error)
		go func() {
			snapshots := []*Snapshot{}

			/* Legacy: we only sent the snapshot names, so we just
			 * copy the container's config over, same as we used to
			 * do.
			 */
			if len(header.SnapshotNames) != len(header.Snapshots) {
				for _, name := range header.SnapshotNames {
					base := snapshotToProtobuf(c.src.container)
					base.Name = &name
					snapshots = append(snapshots, base)
				}
			} else {
				snapshots = header.Snapshots
			}

			var fsConn *websocket.Conn
			if c.push {
				fsConn = c.dest.fsConn
			} else {
				fsConn = c.src.fsConn
			}

			sendFinalFsDelta := false
			if live {
				sendFinalFsDelta = true
			}

			if criuType != nil && *criuType == CRIUType_NONE {
				sendFinalFsDelta = true
			}

			err = mySink(sendFinalFsDelta, c.src.container,
				snapshots, fsConn, srcIdmap, migrateOp,
				c.src.containerOnly)
			if err != nil {
				fsTransfer <- err
				return
			}

			err = ShiftIfNecessary(c.src.container, srcIdmap)
			if err != nil {
				fsTransfer <- err
				return
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

			sync := &MigrationSync{
				FinalPreDump: proto.Bool(false),
			}

			if resp.GetPredump() {
				logger.Debugf("Before the receive loop %s", sync.GetFinalPreDump())
				for !sync.GetFinalPreDump() {
					logger.Debugf("About to receive rsync")
					// Transfer a CRIU pre-dump
					err = RsyncRecv(shared.AddSlash(imagesDir), criuConn, nil)
					if err != nil {
						restore <- err
						return
					}
					logger.Debugf("rsync receive done")

					logger.Debugf("About to receive header")
					// Check if this was the last pre-dump
					// Only the FinalPreDump element if of interest
					mtype, data, err := criuConn.ReadMessage()
					if err != nil {
						logger.Debugf("err %s", err)
						restore <- err
						return
					}
					if mtype != websocket.BinaryMessage {
						restore <- err
						return
					}
					err = proto.Unmarshal(data, sync)
					if err != nil {
						logger.Debugf("err %s", err)
						restore <- err
						return
					}
					logger.Debugf("At the end of the receive loop %s", sync.GetFinalPreDump())
				}
			}

			// Final CRIU dump
			err = RsyncRecv(shared.AddSlash(imagesDir), criuConn, nil)
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

			// Currently we only do a single CRIU pre-dump so we
			// can hardcode "final" here since we know that "final" is the
			// folder for CRIU's final dump.
			err = c.src.container.Migrate(&criuMigrationArgs)
			if err != nil {
				restore <- err
				return
			}

		}

		restore <- nil
	}(c)

	var source <-chan MigrationControl
	if c.push {
		source = c.dest.controlChannel()
	} else {
		source = c.src.controlChannel()
	}

	for {
		select {
		case err = <-restore:
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
			} else {
				// The source can only tell us it failed (e.g. if
				// checkpointing failed). We have to tell the source
				// whether or not the restore was successful.
				logger.Debugf("Unknown message %v from source", msg)
			}
		}
	}
}
