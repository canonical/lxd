package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/operationlock"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

type conversionSink struct {
	fsConn   *migrationConn
	url      string
	instance instance.Instance

	sourceDiskSize    int64
	conversionOptions []string
}

// conversionSinkArgs arguments to configure conversion sink.
type conversionSinkArgs struct {
	// General conversion fields.
	secrets  map[string]string
	url      string
	instance instance.Instance

	// Conversion specific fields.
	conversionOptions []string

	// Storage specific fields.
	sourceDiskSize int64
}

func newConversionSink(args *conversionSinkArgs) (*conversionSink, error) {
	sink := conversionSink{
		instance:          args.instance,
		url:               args.url,
		sourceDiskSize:    args.sourceDiskSize,
		conversionOptions: args.conversionOptions,
	}

	secret, err := shared.RandomCryptoString()
	if err != nil {
		return nil, fmt.Errorf("Failed creating conversion sink secret for %q connection: %w", api.SecretNameFilesystem, err)
	}

	sink.fsConn = newMigrationConn(secret, nil, nil)

	return &sink, nil
}

// Metadata returns metadata for the conversion sink.
func (s *conversionSink) Metadata() any {
	return shared.Jmap{
		api.SecretNameFilesystem: s.fsConn.Secret(),
	}
}

// Do performs the conversion operation on the target side (sink) for the given
// state and instance operation. It sets up the necessary websocket connection
// for filesystem, and then receives the conversion data.
func (s *conversionSink) Do(state *state.State, instOp *operationlock.InstanceOperation) error {
	l := logger.AddContext(logger.Ctx{"project": s.instance.Project().Name, "instance": s.instance.Name()})

	defer l.Info("Conversion channels disconnected on target")
	defer s.fsConn.Close()

	filesystemConnFunc := func(ctx context.Context) (io.ReadWriteCloser, error) {
		if s.fsConn == nil {
			return nil, fmt.Errorf("Conversion target filesystem connection not initialized")
		}

		wsConn, err := s.fsConn.WebsocketIO(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed getting conversion target filesystem connection: %w", err)
		}

		return wsConn, nil
	}

	args := instance.ConversionReceiveArgs{
		ConversionArgs: instance.ConversionArgs{
			FilesystemConn: filesystemConnFunc,
			Disconnect:     func() { s.fsConn.Close() },
		},
		SourceDiskSize:    s.sourceDiskSize,
		ConversionOptions: s.conversionOptions,
	}

	err := s.instance.ConversionReceive(args)
	if err != nil {
		l.Error("Failed conversion on target", logger.Ctx{"err": err})
		return fmt.Errorf("Failed conversion on target: %w", err)
	}

	return nil
}

// Connect connects to the conversion source.
func (s *conversionSink) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	incomingSecret := r.FormValue("secret")
	if incomingSecret == "" {
		return api.StatusErrorf(http.StatusBadRequest, "Missing conversion sink secret")
	}

	if incomingSecret == s.fsConn.Secret() {
		err := s.fsConn.AcceptIncoming(r, w)
		if err != nil {
			return fmt.Errorf("Failed accepting incoming conversion sink %q connection: %w", api.SecretNameFilesystem, err)
		}

		return nil
	}

	// If we didn't find the right secret, the user provided a bad one, so return 403, not 404, since this
	// operation actually exists.
	return api.StatusErrorf(http.StatusForbidden, "Invalid conversion sink secret")
}
