// Package migration provides the primitives for migration in LXD.
//
// Migration has two pieces, a "source", that is, the host that already has the
// container, and a "sink", the host that's getting the container. Currently,
// in the 'pull' mode, the source sets up an operation, and the sink connects
// to the source and pulls the container.
//
// There are three websockets (channels) used in migration: 1. the control
// stream, 2. the criu images stream, and 3. the filesystem stream. When a
// migration is initiated, information about the container, its configuration,
// etc. are sent over the control channel, the criu images and container
// filesystem are synced over their respective channels, and the result of the
// restore operation is sent from the sink to the source over the control
// channel.
//
// In particular, the protocol that is spoken over the criu channel and
// filesystem channel can vary, depending on what is negotiated over the
// control socket. For example, both the source and the sink's LXD directory is
// on btrfs, the filesystem socket can speak btrfs-send/receive. Additionally,
// although we do a "stop the world" type migration right now, support for
// criu's p.haul protocol will happen over the criu socket.
package migration

import (
	"encoding/gob"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"

	"github.com/gorilla/websocket"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"
)

type FilesystemType int

const (
	Rsync FilesystemType = 0
)

type MigrationHeader struct {
	FSType FilesystemType
}

type ControlMessageType int

const (
	Failure ControlMessageType = 0
	Success ControlMessageType = 1
)

type MigrationControl struct {
	Type    ControlMessageType
	Message string
}

type migrationFields struct {
	controlSecret string
	controlConn   *websocket.Conn

	criuSecret string
	criuConn   *websocket.Conn

	fsSecret string
	fsConn   *websocket.Conn

	container *lxc.Container
}

func (c *migrationFields) send(m interface{}) error {
	w, err := c.controlConn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}

	defer w.Close()

	// TODO: We should change the encoding here to be a protobuf, so that
	// non-go users can easily talk to the migration socket if we want.
	// I'll (tych0) investigate that in a bit, but for now gob is easier to
	// use.
	return gob.NewEncoder(w).Encode(m)
}

func (c *migrationFields) recv(m interface{}) error {
	_, r, err := c.controlConn.NextReader()
	if err != nil {
		return err
	}
	return gob.NewDecoder(r).Decode(m)
}

func (c *migrationFields) disconnect() {
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	if c.controlConn != nil {
		c.controlConn.WriteMessage(websocket.CloseMessage, closeMsg)
	}

	if c.fsConn != nil {
		c.fsConn.WriteMessage(websocket.CloseMessage, closeMsg)
	}

	if c.criuConn != nil {
		c.criuConn.WriteMessage(websocket.CloseMessage, closeMsg)
	}
}

func (c *migrationFields) sendControl(err error) {
	msg := MigrationControl{}
	if err != nil {
		msg.Type = Failure
		msg.Message = err.Error()
	} else {
		msg.Type = Success
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
			shared.Debugf("got error reading migration control socket %s", err)
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

func NewMigrationSource(c *lxc.Container) (shared.OperationWebsocket, error) {
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

	ret.criuSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	return &ret, nil
}

func (s *migrationSourceWs) Metadata() interface{} {
	return shared.Jmap{
		"control": s.controlSecret,
		"fs":      s.fsSecret,
		"criu":    s.criuSecret,
	}
}

func (s *migrationSourceWs) Connect(secret string, r *http.Request, w http.ResponseWriter) error {
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
		 * which 403, not 404, since this operation actually exists */
		return os.ErrPermission
	}

	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	*conn = c

	if s.controlConn != nil && s.criuConn != nil && s.fsConn != nil {
		s.allConnected <- true
	}

	return nil
}

func (s *migrationSourceWs) Do() shared.OperationResult {
	<-s.allConnected

	err := s.send(&MigrationHeader{Rsync})
	if err != nil {
		s.disconnect()
		return shared.OperationError(err)
	}

	checkpointDir, err := ioutil.TempDir("", "lxd_migration")
	if err != nil {
		s.sendControl(err)
		return shared.OperationError(err)
	}
	defer os.RemoveAll(checkpointDir)

	opts := lxc.CheckpointOptions{Stop: true, Directory: checkpointDir}
	if err := s.container.Checkpoint(opts); err != nil {
		// TODO: we should probably clean up checkpointDir here, but
		// where should we put the checkpoint log so people can debug
		// why things didn't checkpoint?
		s.sendControl(err)
		return shared.OperationError(err)
	}

	/*
	 * We do the serially right now, but there's really no reason for us
	 * to; since we have separate websockets, we can do it in parallel if
	 * we wanted to. However, assuming we're network bound, there's really
	 * no reason to do these in parallel. In the future when we're using
	 * p.haul's protocol, it will make sense to do these in parallel.
	 */
	if err := RsyncSend(checkpointDir, s.criuConn); err != nil {
		s.sendControl(err)
		return shared.OperationError(err)
	}

	fsDir := s.container.ConfigItem("lxc.rootfs")[0]
	if err := RsyncSend(fsDir, s.fsConn); err != nil {
		s.sendControl(err)
		return shared.OperationError(err)
	}

	msg := MigrationControl{}
	if err = s.recv(&msg); err != nil {
		s.disconnect()
		return shared.OperationError(err)
	}

	// TODO: should we add some config here about automatically restarting
	// the container migrate failure? What about the failures above?
	if msg.Type == Failure {
		return shared.OperationError(fmt.Errorf(msg.Message))
	}

	return shared.OperationSuccess
}

type migrationSink struct {
	migrationFields

	url    string
	dialer websocket.Dialer
}

func NewMigrationSink(url string, dialer websocket.Dialer, c *lxc.Container, secrets map[string]string) (func() error, error) {
	sink := migrationSink{migrationFields{container: c}, url, dialer}

	var ok bool
	sink.controlSecret, ok = secrets["control"]
	if !ok {
		return nil, fmt.Errorf("missing control secret")
	}

	sink.fsSecret, ok = secrets["fs"]
	if !ok {
		return nil, fmt.Errorf("missing fs secret")
	}

	sink.criuSecret, ok = secrets["criu"]
	if !ok {
		return nil, fmt.Errorf("missing criu secret")
	}

	return sink.do, nil
}

func (c *migrationSink) connectWithSecret(secret string) (*websocket.Conn, error) {

	query := url.Values{"secret": []string{secret}}

	// TODO: we shouldn't assume this is a HTTP URL
	url := c.url + "?" + query.Encode()

	return lxd.WebsocketDial(c.dialer, url)
}

func (c *migrationSink) do() error {
	var err error
	c.controlConn, err = c.connectWithSecret(c.controlSecret)
	if err != nil {
		return err
	}
	defer c.disconnect()

	c.fsConn, err = c.connectWithSecret(c.fsSecret)
	if err != nil {
		c.sendControl(err)
		return err
	}

	c.criuConn, err = c.connectWithSecret(c.criuSecret)
	if err != nil {
		c.sendControl(err)
		return err
	}

	header := MigrationHeader{FSType: Rsync}
	if err := c.recv(&header); err != nil {
		c.sendControl(err)
		return err
	}

	if header.FSType != Rsync {
		err = fmt.Errorf("formats other than rsync not understood")
		c.sendControl(err)
		return err
	}

	imagesDir, err := ioutil.TempDir("", "lxd_migration")
	if err != nil {
		os.RemoveAll(imagesDir)
		c.sendControl(err)
		return err
	}

	restore := make(chan error)
	go func() {
		if err := RsyncRecv(AddSlash(imagesDir), c.criuConn); err != nil {
			restore <- err
			os.RemoveAll(imagesDir)
			c.sendControl(err)
			return
		}

		fsDir := c.container.ConfigItem("lxc.rootfs")[0]
		if err := RsyncRecv(AddSlash(fsDir), c.fsConn); err != nil {
			restore <- err
			os.RemoveAll(fsDir)
			c.sendControl(err)
			return
		}

		opts := lxc.RestoreOptions{Directory: imagesDir, Verbose: true}
		err := c.container.Restore(opts)
		// TODO: We should remove this directory, but for now we leave
		// it for debugging restores. Perhaps we should copy the log
		// somewhere and delete it?
		// os.RemoveAll(imagesDir)
		restore <- err
	}()

	source := c.controlChannel()

	for {
		select {
		case err = <-restore:
			c.sendControl(err)
			return err
		case msg, ok := <-source:
			if !ok {
				c.disconnect()
				return fmt.Errorf("got error reading source")
			}
			if msg.Type == Failure {
				c.disconnect()
				return fmt.Errorf(msg.Message)
			} else {
				// The source can only tell us it failed (e.g. if
				// checkpointing failed). We have to tell the source
				// whether or not the restore was successful.
				shared.Debugf("unknown message %v from source", msg)
			}
		}
	}
}
