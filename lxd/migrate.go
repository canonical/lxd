// Package migration provides the primitives for migration in LXD.
//
// See https://github.com/lxc/lxd/blob/master/specs/migration.md for a complete
// description.

package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

type migrationFields struct {
	controlSecret string
	controlConn   *websocket.Conn
	controlLock   sync.Mutex

	criuSecret string
	criuConn   *websocket.Conn

	fsSecret string
	fsConn   *websocket.Conn

	// container specific fields
	live          bool
	containerOnly bool
	container     container

	// storage specific fields
	storage storage
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

	err := migration.ProtoSend(c.controlConn, m)
	if err != nil {
		return err
	}

	return nil
}

func (c *migrationFields) recv(m proto.Message) error {
	return migration.ProtoRecv(c.controlConn, m)
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
	c.controlLock.Lock()
	defer c.controlLock.Unlock()

	migration.ProtoSendControl(c.controlConn, err)

	if err != nil {
		c.disconnect()
	}
}

func (c *migrationFields) controlChannel() <-chan migration.MigrationControl {
	ch := make(chan migration.MigrationControl)
	go func() {
		msg := migration.MigrationControl{}
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

func (s *migrationSourceWs) ConnectTarget(certificate string, operation string, websockets map[string]string) error {
	var err error
	var cert *x509.Certificate

	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
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

	for name, secret := range websockets {
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
		wsUrl := fmt.Sprintf("wss://%s/websocket?%s", strings.TrimPrefix(operation, "https://"), query.Encode())

		wsConn, _, err := dialer.Dial(wsUrl, http.Header{})
		if err != nil {
			return err
		}

		*conn = wsConn
	}

	s.allConnected <- true

	return nil
}

type migrationSink struct {
	// We are pulling the entity from src in pull mode.
	src migrationFields
	// The entity is pushed from src to dest in push mode. Note that
	// websocket connections are not set in push mode. Only the secret
	// fields are used since the client will connect to the sockets.
	dest migrationFields

	url          string
	dialer       websocket.Dialer
	allConnected chan bool
	push         bool
	refresh      bool
}

type MigrationSinkArgs struct {
	// General migration fields
	Dialer  websocket.Dialer
	Push    bool
	Secrets map[string]string
	Url     string

	// Container specific fields
	Container     container
	ContainerOnly bool
	Idmap         *idmap.IdmapSet
	Live          bool
	Refresh       bool
	Snapshots     []*migration.Snapshot

	// Storage specific fields
	Storage storage

	// Transport specific fields
	RsyncFeatures []string
}

type MigrationSourceArgs struct {
	// Container specific fields
	Container     container
	ContainerOnly bool

	// Transport specific fields
	RsyncFeatures []string
	ZfsFeatures   []string
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
