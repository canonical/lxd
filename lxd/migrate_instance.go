package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/logger"
)

func newMigrationSource(inst instance.Instance, stateful bool, instanceOnly bool, allowInconsistent bool, clusterMoveSourceName string) (*migrationSourceWs, error) {
	ret := migrationSourceWs{
		migrationFields: migrationFields{
			instance:          inst,
			allowInconsistent: allowInconsistent,
		},
		allConnected:          cancel.New(context.Background()),
		clusterMoveSourceName: clusterMoveSourceName,
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
	l := logger.AddContext(logger.Log, logger.Ctx{"project": s.instance.Project().Name, "instance": s.instance.Name(), "live": s.live, "clusterMoveSourceName": s.clusterMoveSourceName})

	l.Info("Waiting for migration channel connections on source")

	select {
	case <-time.After(time.Second * 10):
		s.allConnected.Cancel()
		return fmt.Errorf("Timed out waiting for migration connections")
	case <-s.allConnected.Done():
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
			ClusterMoveSourceName: s.clusterMoveSourceName,
		},
		AllowInconsistent: s.allowInconsistent,
	})
	if err != nil {
		l.Error("Failed migration on source", logger.Ctx{"err": err})
		return fmt.Errorf("Failed migration on source: %w", err)
	}

	return nil
}

func newMigrationSink(args *migrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		migrationFields: migrationFields{
			instance:     args.Instance,
			instanceOnly: args.InstanceOnly,
		},
		url:                   args.URL,
		clusterMoveSourceName: args.ClusterMoveSourceName,
		dialer:                args.Dialer,
		push:                  args.Push,
		refresh:               args.Refresh,
	}

	if sink.push {
		sink.allConnected = cancel.New(context.Background())
	}

	var ok bool
	var err error
	if sink.push {
		sink.controlSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, fmt.Errorf("Failed creating migration sink secret for control websocket: %w", err)
		}

		sink.fsSecret, err = shared.RandomCryptoString()
		if err != nil {
			return nil, fmt.Errorf("Failed creating migration sink secret for filesystem websocket: %w", err)
		}

		sink.live = args.Live
		if sink.live {
			sink.stateSecret, err = shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration sink secret for state websocket: %w", err)
			}
		}
	} else {
		sink.controlSecret, ok = args.Secrets[api.SecretNameControl]
		if !ok {
			return nil, fmt.Errorf("Missing migration sink secret for control websocket")
		}

		sink.fsSecret, ok = args.Secrets[api.SecretNameFilesystem]
		if !ok {
			return nil, fmt.Errorf("Missing migration sink secret for filesystem websocket")
		}

		sink.stateSecret, ok = args.Secrets[api.SecretNameState]
		sink.live = ok || args.Live
	}

	if sink.instance.Type() == instancetype.Container {
		_, err = exec.LookPath("criu")
		if sink.live && err != nil {
			return nil, migration.ErrNoLiveMigrationTarget
		}
	}

	return &sink, nil
}

func (c *migrationSink) Do(state *state.State, instOp *operationlock.InstanceOperation) error {
	l := logger.AddContext(logger.Log, logger.Ctx{"push": c.push, "project": c.instance.Project().Name, "instance": c.instance.Name(), "live": c.live, "clusterMoveSourceName": c.clusterMoveSourceName})

	var err error

	l.Info("Waiting for migration channel connections on target")

	if c.push {
		select {
		case <-time.After(time.Second * 10):
			c.allConnected.Cancel()
			return fmt.Errorf("Timed out waiting for migration connections")
		case <-c.allConnected.Done():
		}
	}

	if c.push {
		defer c.disconnect()
	} else {
		c.controlConn, err = c.connectWithSecret(c.controlSecret)
		if err != nil {
			err = fmt.Errorf("Failed connecting migration control sink socket: %w", err)
			return err
		}

		defer c.disconnect()

		c.fsConn, err = c.connectWithSecret(c.fsSecret)
		if err != nil {
			err = fmt.Errorf("Failed connecting migration filesystem sink socket: %w", err)
			c.sendControl(err)
			return err
		}

		if c.live && c.instance.Type() == instancetype.Container {
			c.stateConn, err = c.connectWithSecret(c.stateSecret)
			if err != nil {
				err = fmt.Errorf("Failed connecting migration state sink socket: %w", err)
				c.sendControl(err)
				return err
			}
		}
	}

	l.Info("Migration channels connected on target")
	defer l.Info("Migration channels disconnected on target")

	err = c.instance.MigrateReceive(instance.MigrateReceiveArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    c.send,
			ControlReceive: c.recv,
			StateConn:      &shared.WebsocketIO{Conn: c.stateConn},
			FilesystemConn: &shared.WebsocketIO{Conn: c.fsConn},
			Snapshots:      !c.instanceOnly,
			Live:           c.live,
			Disconnect: func() {
				if c.fsConn != nil {
					_ = c.fsConn.Close()
				}

				if c.stateConn != nil {
					_ = c.stateConn.Close()
				}
			},
			ClusterMoveSourceName: c.clusterMoveSourceName,
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
