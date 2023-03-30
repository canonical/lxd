package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"time"

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

func newMigrationSource(inst instance.Instance, stateful bool, instanceOnly bool, allowInconsistent bool, clusterMoveSourceName string, pushTarget *api.InstancePostTarget) (*migrationSourceWs, error) {
	ret := migrationSourceWs{
		migrationFields: migrationFields{
			instance:          inst,
			allowInconsistent: allowInconsistent,
		},
		clusterMoveSourceName: clusterMoveSourceName,
	}

	if pushTarget != nil {
		ret.pushCertificate = pushTarget.Certificate
		ret.pushOperationURL = pushTarget.Operation
		ret.pushSecrets = pushTarget.Websockets
	}

	ret.instanceOnly = instanceOnly

	secretNames := []string{api.SecretNameControl, api.SecretNameFilesystem}
	if stateful && inst.IsRunning() {
		if inst.Type() == instancetype.Container {
			_, err := exec.LookPath("criu")
			if err != nil {
				return nil, migration.ErrNoLiveMigrationSource
			}
		}

		ret.live = true
		secretNames = append(secretNames, api.SecretNameState)
	}

	ret.conns = make(map[string]*migrationConn, len(secretNames))
	for _, connName := range secretNames {
		if ret.pushOperationURL != "" {
			if ret.pushSecrets[connName] == "" {
				return nil, fmt.Errorf("Expected %q connection secret missing from migration source target request", connName)
			}

			dialer, err := setupWebsocketDialer(ret.pushCertificate)
			if err != nil {
				return nil, fmt.Errorf("Failed setting up websocket dialer for migration source %q connection: %w", connName, err)
			}

			u, err := url.Parse(fmt.Sprintf("wss://%s/websocket", strings.TrimPrefix(ret.pushOperationURL, "https://")))
			if err != nil {
				return nil, fmt.Errorf("Failed parsing websocket URL for migration source %q connection: %w", connName, err)
			}

			ret.conns[connName] = newMigrationConn(ret.pushSecrets[connName], dialer, u)
		} else {
			secret, err := shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration source secret for %q connection: %w", connName, err)
			}

			ret.conns[connName] = newMigrationConn(secret, nil, nil)
		}
	}

	return &ret, nil
}

func (s *migrationSourceWs) Do(state *state.State, migrateOp *operations.Operation) error {
	l := logger.AddContext(logger.Log, logger.Ctx{"project": s.instance.Project().Name, "instance": s.instance.Name(), "live": s.live, "clusterMoveSourceName": s.clusterMoveSourceName})

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*10)
	defer cancel()

	l.Info("Waiting for migration connections on source")

	for _, connName := range []string{api.SecretNameControl, api.SecretNameFilesystem} {
		_, err := s.conns[connName].WebSocket(ctx)
		if err != nil {
			return fmt.Errorf("Failed waiting for migration %q connection on source: %w", connName, err)
		}
	}

	l.Info("Migration channels connected on source")

	defer l.Info("Migration channels disconnected on source")
	defer s.disconnect()

	stateConnFunc := func(ctx context.Context) io.ReadWriteCloser {
		conn := s.conns[api.SecretNameState]
		if conn != nil {
			wsConn, err := conn.WebsocketIO(ctx)
			if err != nil {
				l.Error("Failed getting migration source websocket", logger.Ctx{"connName": api.SecretNameState, "err": err})

				return nil
			}

			return wsConn
		}

		return nil
	}

	filesystemConnFunc := func(ctx context.Context) io.ReadWriteCloser {
		conn := s.conns[api.SecretNameFilesystem]
		if conn != nil {
			wsConn, err := conn.WebsocketIO(ctx)
			if err != nil {
				l.Error("Failed getting migration source websocket", logger.Ctx{"connName": api.SecretNameFilesystem, "err": err})

				return nil
			}

			return wsConn
		}

		return nil
	}

	s.instance.SetOperation(migrateOp)
	err := s.instance.MigrateSend(instance.MigrateSendArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    s.send,
			ControlReceive: s.recv,
			StateConn:      stateConnFunc,
			FilesystemConn: filesystemConnFunc,
			Snapshots:      !s.instanceOnly,
			Live:           s.live,
			Disconnect: func() {
				for connName, conn := range s.conns {
					if connName != api.SecretNameControl {
						conn.Close()
					}
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
			live:         args.Live,
		},
		url:                   args.URL,
		clusterMoveSourceName: args.ClusterMoveSourceName,
		push:                  args.Push,
		refresh:               args.Refresh,
	}

	secretNames := []string{api.SecretNameControl, api.SecretNameFilesystem}
	if sink.live {
		if sink.instance.Type() == instancetype.Container {
			_, err := exec.LookPath("criu")
			if err != nil {
				return nil, migration.ErrNoLiveMigrationTarget
			}
		}

		secretNames = append(secretNames, api.SecretNameState)
	}

	sink.conns = make(map[string]*migrationConn, len(secretNames))
	for _, connName := range secretNames {
		if !sink.push {
			if args.Secrets[connName] == "" {
				return nil, fmt.Errorf("Expected %q connection secret missing from migration sink target request", connName)
			}

			u, err := url.Parse(fmt.Sprintf("wss://%s/websocket", strings.TrimPrefix(args.URL, "https://")))
			if err != nil {
				return nil, fmt.Errorf("Failed parsing websocket URL for migration sink %q connection: %w", connName, err)
			}

			sink.conns[connName] = newMigrationConn(args.Secrets[connName], args.Dialer, u)
		} else {
			secret, err := shared.RandomCryptoString()
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration sink secret for %q connection: %w", connName, err)
			}

			sink.conns[connName] = newMigrationConn(secret, nil, nil)
		}
	}

	return &sink, nil
}

func (c *migrationSink) Do(state *state.State, instOp *operationlock.InstanceOperation) error {
	l := logger.AddContext(logger.Log, logger.Ctx{"project": c.instance.Project().Name, "instance": c.instance.Name(), "live": c.live, "clusterMoveSourceName": c.clusterMoveSourceName, "push": c.push})

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*10)
	defer cancel()

	l.Info("Waiting for migration connections on target")

	for _, connName := range []string{api.SecretNameControl, api.SecretNameFilesystem} {
		_, err := c.conns[connName].WebSocket(ctx)
		if err != nil {
			return fmt.Errorf("Failed waiting for migration %q connection on target: %w", connName, err)
		}
	}

	l.Info("Migration channels connected on target")

	defer l.Info("Migration channels disconnected on target")

	if c.push {
		defer c.disconnect()
	}

	stateConnFunc := func(ctx context.Context) io.ReadWriteCloser {
		conn := c.conns[api.SecretNameState]
		if conn != nil {
			wsConn, err := conn.WebsocketIO(ctx)
			if err != nil {
				l.Error("Failed getting migration sink websocket", logger.Ctx{"connName": api.SecretNameState, "err": err})

				return nil
			}

			return wsConn
		}

		return nil
	}

	filesystemConnFunc := func(ctx context.Context) io.ReadWriteCloser {
		conn := c.conns[api.SecretNameFilesystem]
		if conn != nil {
			wsConn, err := conn.WebsocketIO(ctx)
			if err != nil {
				l.Error("Failed getting migration sink websocket", logger.Ctx{"connName": api.SecretNameFilesystem, "err": err})

				return nil
			}

			return wsConn
		}

		return nil
	}

	err := c.instance.MigrateReceive(instance.MigrateReceiveArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    c.send,
			ControlReceive: c.recv,
			StateConn:      stateConnFunc,
			FilesystemConn: filesystemConnFunc,
			Snapshots:      !c.instanceOnly,
			Live:           c.live,
			Disconnect: func() {
				for connName, conn := range c.conns {
					if connName != api.SecretNameControl {
						conn.Close()
					}
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
