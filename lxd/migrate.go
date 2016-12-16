// Package migration provides the primitives for migration in LXD.
//
// See https://github.com/lxc/lxd/blob/master/specs/migration.md for a complete
// description.

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

type migrationFields struct {
	live bool

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
			shared.LogDebugf("Got error reading migration control socket %s", err)
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

func NewMigrationSource(c container) (*migrationSourceWs, error) {
	ret := migrationSourceWs{migrationFields{container: c}, make(chan bool, 1)}

	var err error
	ret.controlSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	if c.IsRunning() {
		if err := findCriu("source"); err != nil {
			return nil, err
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

func (s *migrationSourceWs) Connect(op *operation.Operation, r *http.Request, w http.ResponseWriter) error {
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
		/* If we didn't find the right secret, the user provided a bad one,
		 * which 403, not 404, since this operation.Operation actually exists */
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

func writeActionScript(directory string, operation string, secret string) error {
	script := fmt.Sprintf(`#!/bin/sh -e
if [ "$CRTOOLS_SCRIPT_ACTION" = "post-dump" ]; then
	%s migratedumpsuccess %s %s
fi
`, state.ExecPath, operation, secret)

	f, err := os.Create(filepath.Join(directory, "action.sh"))
	if err != nil {
		return err
	}
	defer f.Close()

	if err := f.Chmod(0500); err != nil {
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

func (s *migrationSourceWs) Do(migrateOp *operation.Operation) error {
	<-s.allConnected

	criuType := CRIUType_CRIU_RSYNC.Enum()
	if !s.live {
		criuType = nil

		err := s.container.StorageStart()
		if err != nil {
			return err
		}

		defer s.container.StorageStop()
	}

	idmaps := make([]*IDMapType, 0)

	idmapset := s.container.IdmapSet()
	if idmapset != nil {
		for _, ctnIdmap := range idmapset.Idmap {
			idmap := IDMapType{
				Isuid:    proto.Bool(ctnIdmap.Isuid),
				Isgid:    proto.Bool(ctnIdmap.Isgid),
				Hostid:   proto.Int(ctnIdmap.Hostid),
				Nsid:     proto.Int(ctnIdmap.Nsid),
				Maprange: proto.Int(ctnIdmap.Maprange),
			}

			idmaps = append(idmaps, &idmap)
		}
	}

	driver, fsErr := s.container.Storage().MigrationSource(s.container)
	/* the protocol says we have to send a header no matter what, so let's
	 * do that, but then immediately send an error.
	 */
	snapshots := []*Snapshot{}
	snapshotNames := []string{}
	if fsErr == nil {
		fullSnaps := driver.Snapshots()
		for _, snap := range fullSnaps {
			snapshots = append(snapshots, snapshotToProtobuf(snap))
			snapshotNames = append(snapshotNames, shared.ExtractSnapshotName(snap.Name()))
		}
	}

	myType := s.container.Storage().MigrationType()
	header := MigrationHeader{
		Fs:            &myType,
		Criu:          criuType,
		Idmap:         idmaps,
		SnapshotNames: snapshotNames,
		Snapshots:     snapshots,
	}

	if err := s.send(&header); err != nil {
		s.sendControl(err)
		return err
	}

	if fsErr != nil {
		s.sendControl(fsErr)
		return fsErr
	}

	if err := s.recv(&header); err != nil {
		s.sendControl(err)
		return err
	}

	if *header.Fs != myType {
		myType = MigrationFSType_RSYNC
		header.Fs = &myType

		driver, _ = rsyncMigrationSource(s.container)
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

	if err := driver.SendWhileRunning(s.fsConn, migrateOp); err != nil {
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

		if lxc.VersionAtLeast(2, 0, 4) {
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
			 * This script then hangs until the migration operation.Operation either
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

			actionScriptOp, err := operation.Create(
				operation.ClassWebsocket,
				nil,
				nil,
				func(op *operation.Operation) error {
					result := <-restoreSuccess
					if !result {
						return fmt.Errorf("restore failed, failing CRIU")
					}
					return nil
				},
				nil,
				func(op *operation.Operation, r *http.Request, w http.ResponseWriter) error {
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

			if err := writeActionScript(checkpointDir, actionScriptOp.Url(), actionScriptOpSecret); err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			_, err = actionScriptOp.Run()
			if err != nil {
				os.RemoveAll(checkpointDir)
				return abort(err)
			}

			go func() {
				dumpSuccess <- s.container.Migrate(lxc.MIGRATE_DUMP, checkpointDir, "migration", true, true)
				os.RemoveAll(checkpointDir)
			}()

			select {
			/* the checkpoint failed, let's just abort */
			case err = <-dumpSuccess:
				return abort(err)
			/* the dump finished, let's continue on to the restore */
			case <-dumpDone:
				shared.LogDebugf("Dump finished, continuing with restore...")
			}
		} else {
			defer os.RemoveAll(checkpointDir)
			if err := s.container.Migrate(lxc.MIGRATE_DUMP, checkpointDir, "migration", true, false); err != nil {
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
		if err := RsyncSend(shared.AddSlash(checkpointDir), s.criuConn, nil); err != nil {
			return abort(err)
		}

		if err := driver.SendAfterCheckpoint(s.fsConn); err != nil {
			return abort(err)
		}
	}

	driver.Cleanup()

	msg := MigrationControl{}
	if err := s.recv(&msg); err != nil {
		s.disconnect()
		return err
	}

	if s.live {
		restoreSuccess <- *msg.Success
		err := <-dumpSuccess
		if err != nil {
			shared.LogErrorf("dump failed after successful restore?: %q", err)
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
	Url       string
	Dialer    websocket.Dialer
	Container container
	Secrets   map[string]string
	Push      bool
	Live      bool
}

func NewMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:    migrationFields{container: args.Container},
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

	err = findCriu("destination")
	if sink.push && sink.dest.live && err != nil {
		return nil, err
	} else if sink.src.live && err != nil {
		return nil, err
	}

	return &sink, nil
}

func (c *migrationSink) connectWithSecret(secret string) (*websocket.Conn, error) {
	query := url.Values{"secret": []string{secret}}

	// The URL is a https URL to the operation.Operation, mangle to be a wss URL to the secret
	wsUrl := fmt.Sprintf("wss://%s/websocket?%s", strings.TrimPrefix(c.url, "https://"), query.Encode())

	return lxd.WebsocketDial(c.dialer, wsUrl)
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

func (s *migrationSink) Connect(op *operation.Operation, r *http.Request, w http.ResponseWriter) error {
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
		 * which 403, not 404, since this operation.Operation actually exists */
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

func (c *migrationSink) Do(migrateOp *operation.Operation) error {
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
	if !live {
		criuType = nil
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

	if err := sender(&resp); err != nil {
		controller(err)
		return err
	}

	restore := make(chan error)
	go func(c *migrationSink) {
		imagesDir := ""
		srcIdmap := new(shared.IdmapSet)

		for _, idmap := range header.Idmap {
			e := shared.IdmapEntry{
				Isuid:    *idmap.Isuid,
				Isgid:    *idmap.Isgid,
				Nsid:     int(*idmap.Nsid),
				Hostid:   int(*idmap.Hostid),
				Maprange: int(*idmap.Maprange)}
			srcIdmap.Idmap = shared.Extend(srcIdmap.Idmap, e)
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
			if err := mySink(live, c.src.container, header.Snapshots, fsConn, srcIdmap, migrateOp); err != nil {
				fsTransfer <- err
				return
			}

			if err := ShiftIfNecessary(c.src.container, srcIdmap); err != nil {
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
			if err := RsyncRecv(shared.AddSlash(imagesDir), criuConn, nil); err != nil {
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
			err = c.src.container.Migrate(lxc.MIGRATE_RESTORE, imagesDir, "migration", false, false)
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
				shared.LogDebugf("Unknown message %v from source", msg)
			}
		}
	}
}
