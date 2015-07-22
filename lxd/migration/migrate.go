// Package migration provides the primitives for migration in LXD.
//
// See https://github.com/lxc/lxd/blob/master/specs/migration.md for a complete
// description.

package migration

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type migrationFields struct {
	live bool

	controlSecret string
	controlConn   *websocket.Conn

	criuSecret string
	criuConn   *websocket.Conn

	fsSecret string
	fsConn   *websocket.Conn

	container *lxc.Container
}

func (c *migrationFields) send(m proto.Message) error {
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
		return fmt.Errorf("only binary messages allowed")
	}

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	return proto.Unmarshal(buf, m)
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
			shared.Debugf("got error reading migration control socket %s", err)
			close(ch)
			return
		}
		ch <- msg
	}()

	return ch
}

func collectMigrationLogFile(c *lxc.Container, imagesDir string, method string) error {
	t := time.Now().Format(time.RFC3339)
	newPath := shared.LogPath(c.Name(), fmt.Sprintf("migration_%s_%s.log", method, t))
	return shared.FileCopy(filepath.Join(imagesDir, fmt.Sprintf("%s.log", method)), newPath)
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

	if c.Running() {
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

	if s.controlConn != nil && (!s.live || s.criuConn != nil) && s.fsConn != nil {
		s.allConnected <- true
	}

	return nil
}

func (s *migrationSourceWs) Do() shared.OperationResult {
	<-s.allConnected

	criuType := CRIUType_CRIU_RSYNC.Enum()
	if !s.live {
		criuType = nil
	}

	header := MigrationHeader{
		Fs:   MigrationFSType_RSYNC.Enum(),
		Criu: criuType,
	}

	if err := s.send(&header); err != nil {
		s.sendControl(err)
		return shared.OperationError(err)
	}

	if err := s.recv(&header); err != nil {
		s.sendControl(err)
		return shared.OperationError(err)
	}

	if *header.Fs != MigrationFSType_RSYNC {
		err := fmt.Errorf("formats other than rsync not understood")
		s.sendControl(err)
		return shared.OperationError(err)
	}

	if s.live {
		if header.Criu == nil {
			err := fmt.Errorf("got no CRIU socket type for live migration")
			s.sendControl(err)
			return shared.OperationError(err)
		} else if *header.Criu != CRIUType_CRIU_RSYNC {
			err := fmt.Errorf("formats other than criu rsync not understood")
			s.sendControl(err)
			return shared.OperationError(err)
		}

		checkpointDir, err := ioutil.TempDir("", "lxd_migration_")
		if err != nil {
			s.sendControl(err)
			return shared.OperationError(err)
		}
		defer os.RemoveAll(checkpointDir)

		opts := lxc.CheckpointOptions{Stop: true, Directory: checkpointDir, Verbose: true}
		err = s.container.Checkpoint(opts)

		if err2 := collectMigrationLogFile(s.container, checkpointDir, "dump"); err2 != nil {
			shared.Debugf("error collecting checkpoint log file %s", err)
		}

		if err != nil {
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
		if err := RsyncSend(shared.AddSlash(checkpointDir), s.criuConn); err != nil {
			s.sendControl(err)
			return shared.OperationError(err)
		}
	}

	fsDir := s.container.ConfigItem("lxc.rootfs")[0]
	if err := RsyncSend(shared.AddSlash(fsDir), s.fsConn); err != nil {
		s.sendControl(err)
		return shared.OperationError(err)
	}

	msg := MigrationControl{}
	if err := s.recv(&msg); err != nil {
		s.disconnect()
		return shared.OperationError(err)
	}

	// TODO: should we add some config here about automatically restarting
	// the container migrate failure? What about the failures above?
	if !*msg.Success {
		return shared.OperationError(fmt.Errorf(*msg.Message))
	}

	return shared.OperationSuccess
}

type migrationSink struct {
	migrationFields

	url      string
	dialer   websocket.Dialer
	IdmapSet *shared.IdmapSet
}

type MigrationSinkArgs struct {
	Url       string
	Dialer    websocket.Dialer
	Container *lxc.Container
	Secrets   map[string]string
	IdMapSet  *shared.IdmapSet
}

func NewMigrationSink(args *MigrationSinkArgs) (func() error, error) {
	sink := migrationSink{
		migrationFields{container: args.Container},
		args.Url,
		args.Dialer,
		args.IdMapSet,
	}

	var ok bool
	sink.controlSecret, ok = args.Secrets["control"]
	if !ok {
		return nil, fmt.Errorf("missing control secret")
	}

	sink.fsSecret, ok = args.Secrets["fs"]
	if !ok {
		return nil, fmt.Errorf("missing fs secret")
	}

	sink.criuSecret, ok = args.Secrets["criu"]
	sink.live = ok

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

	if c.live {
		c.criuConn, err = c.connectWithSecret(c.criuSecret)
		if err != nil {
			c.sendControl(err)
			return err
		}
	}

	// For now, we just ignore whatever the server sends us. We only
	// support RSYNC, so that's what we respond with.
	header := MigrationHeader{}
	if err := c.recv(&header); err != nil {
		c.sendControl(err)
		return err
	}

	criuType := CRIUType_CRIU_RSYNC.Enum()
	if !c.live {
		criuType = nil
	}

	resp := MigrationHeader{Fs: MigrationFSType_RSYNC.Enum(), Criu: criuType}
	if err := c.send(&resp); err != nil {
		c.sendControl(err)
		return err
	}

	restore := make(chan error)
	go func(c *migrationSink) {
		imagesDir := ""
		if c.live {
			var err error
			imagesDir, err = ioutil.TempDir("", "lxd_migration_")
			if err != nil {
				os.RemoveAll(imagesDir)
				c.sendControl(err)
				return
			}

			defer func() {
				err := collectMigrationLogFile(c.container, imagesDir, "restore")
				/*
				 * If the checkpoint fails, we won't have any log to collect,
				 * so don't warn about that.
				 */
				if err != nil && !os.IsNotExist(err) {
					shared.Debugf("error collectiong migration log file %s", err)
				}

				os.RemoveAll(imagesDir)
			}()

			if err := RsyncRecv(shared.AddSlash(imagesDir), c.criuConn); err != nil {
				restore <- err
				os.RemoveAll(imagesDir)
				c.sendControl(err)
				return
			}
		}

		fsDir := c.container.ConfigItem("lxc.rootfs")[0]
		if err := RsyncRecv(shared.AddSlash(fsDir), c.fsConn); err != nil {
			restore <- err
			c.sendControl(err)
			return
		}

		if c.IdmapSet != nil {
			if err := c.IdmapSet.ShiftRootfs(shared.VarPath("lxc", c.container.Name())); err != nil {
				restore <- err
				c.sendControl(err)
				return
			}
		}

		if c.live {
			f, err := ioutil.TempFile("", "lxd_lxc_migrateconfig_")
			if err != nil {
				restore <- err
				return
			}

			if err = f.Chmod(0600); err != nil {
				f.Close()
				os.Remove(f.Name())
				return
			}
			f.Close()

			if err := c.container.SaveConfigFile(f.Name()); err != nil {
				restore <- err
				return
			}

			cmd := exec.Command(
				os.Args[0],
				"forkmigrate",
				c.container.Name(),
				c.container.ConfigPath(),
				f.Name(),
				imagesDir,
			)

			restore <- cmd.Run()
		} else {
			restore <- nil
		}
	}(c)

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
			if !*msg.Success {
				c.disconnect()
				return fmt.Errorf(*msg.Message)
			} else {
				// The source can only tell us it failed (e.g. if
				// checkpointing failed). We have to tell the source
				// whether or not the restore was successful.
				shared.Debugf("unknown message %v from source", msg)
			}
		}
	}
}

/*
 * Similar to forkstart, this is called when lxd is invoked as:
 *
 *    lxd forkmigrate <container> <lxcpath> <path_to_config> <path_to_criu_images>
 *
 * liblxc's restore() sets up the processes in such a way that the monitor ends
 * up being a child of the process that calls it, in our case lxd. However, we
 * really want the monitor to be daemonized, so we fork again. Additionally, we
 * want to fork for the same reasons we do forkstart (i.e. reduced memory
 * footprint when we fork tasks that will never free golang's memory, etc.)
 */
func MigrateContainer(args []string) error {
	if len(args) != 5 {
		return fmt.Errorf("Bad arguments %q", args)
	}

	name := args[1]
	lxcpath := args[2]
	configPath := args[3]
	imagesDir := args[4]

	defer os.Remove(configPath)

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return err
	}

	if err := c.LoadConfigFile(configPath); err != nil {
		return err
	}

	return c.Restore(lxc.RestoreOptions{
		Directory: imagesDir,
		Verbose:   true,
	})
}
