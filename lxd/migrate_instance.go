package main

import (
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
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
		return nil, fmt.Errorf("Failed creating migration source secret for control websocket: %w", err)
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		return nil, fmt.Errorf("Failed creating migration source secret for filesystem websocket: %w", err)
	}

	if stateful && inst.IsRunning() {
		ret.live = true

		if inst.Type() == instancetype.Container {
			_, err := exec.LookPath("criu")
			if err != nil {
				return nil, migration.ErrNoLiveMigrationSource
			}

			ret.stateSecret, err = shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration source secret for state websocket: %w", err)
			}
		}
	}

	return &ret, nil
}

func (s *migrationSourceWs) Do(state *state.State, migrateOp *operations.Operation) error {
	l := logger.AddContext(logger.Log, logger.Ctx{"project": s.instance.Project().Name, "instance": s.instance.Name(), "live": s.live})

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
			StateConn:      &shared.WebsocketIO{Conn: s.stateConn},
			FilesystemConn: &shared.WebsocketIO{Conn: s.fsConn},
			Snapshots:      !s.instanceOnly,
			Live:           s.live,
			Disconnect: func() {
				if s.fsConn != nil {
					_ = s.fsConn.Close()
				}

				if s.stateConn != nil {
					_ = s.stateConn.Close()
				}
			},
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
				l.Error("Failed sending control error to target", logger.Ctx{"err": sendErr})
			} else {
				// Wait for confirmation of receipt from other side.
				// This provides the ability for both sides to synchronise and ensures we don't close our
				// connections too early, which can cause the other side to process disconnect errors
				// before our control message, causing the true failure cause to be masked.
				_ = s.controlConn.SetReadDeadline(time.Now().Add(time.Second * 5))
				_, _, _ = s.controlConn.ReadMessage()
			}
		}

		return fmt.Errorf("Failed migration on source: %w", err)
	}

	return nil
}

func newMigrationSink(args *migrationSinkArgs) (*migrationSink, error) {
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
			return nil, fmt.Errorf("Failed creating migration sink secret for control websocket: %w", err)
		}

		sink.dest.fsSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, fmt.Errorf("Failed creating migration sink secret for filesystem websocket: %w", err)
		}

		sink.dest.live = args.Live
		if sink.dest.live {
			sink.dest.stateSecret, err = shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration sink secret for state websocket: %w", err)
			}
		}
	} else {
		sink.src.controlSecret, ok = args.Secrets[api.SecretNameControl]
		if !ok {
			return nil, fmt.Errorf("Missing migration sink secret for control websocket")
		}

		sink.src.fsSecret, ok = args.Secrets[api.SecretNameFilesystem]
		if !ok {
			return nil, fmt.Errorf("Missing migration sink secret for filesystem websocket")
		}

		sink.src.stateSecret, ok = args.Secrets[api.SecretNameState]
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

func (c *migrationSink) Do(state *state.State, instOp *operationlock.InstanceOperation) error {
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

	if c.push {
		defer c.dest.disconnect()
	} else {
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
			c.src.stateConn, err = c.connectWithSecret(c.src.stateSecret)
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
	sender := c.src.send
	stateConn := c.src.stateConn
	fsConn := c.src.fsConn

	if c.push {
		receiver = c.dest.recv
		sender = c.dest.send
		stateConn = c.dest.stateConn
		fsConn = c.dest.fsConn
	}

	err = c.src.instance.MigrateReceive(instance.MigrateReceiveArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    sender,
			ControlReceive: receiver,
			StateConn:      &shared.WebsocketIO{Conn: stateConn},
			FilesystemConn: &shared.WebsocketIO{Conn: fsConn},
			Snapshots:      !c.dest.instanceOnly,
			Live:           live,
			Disconnect: func() {
				if fsConn != nil {
					_ = fsConn.Close()
				}

				if stateConn != nil {
					_ = stateConn.Close()
				}
			},
		},
		InstanceOperation: instOp,
		Refresh:           c.refresh,
	})
	if err != nil {
		l.Error("Failed migration on target", logger.Ctx{"err": err})
		return fmt.Errorf("Failed migration on target: %w", err)
	}

	return nil
}

func (s *migrationSourceWs) ConnectContainerTarget(target api.InstancePostTarget) error {
	return s.ConnectTarget(target.Certificate, target.Operation, target.Websockets)
}
