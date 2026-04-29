package drivers

import (
	"context"
	"errors"
	"fmt"

	"github.com/openfga/openfga/pkg/storage"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

var authorizers = map[string]func() authorizer{}

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = errors.New("Unknown driver")

type authorizer interface {
	auth.Authorizer

	init(driverName string, logger logger.Logger, sendSecurity func(*api.EventSecurity)) error
	load(ctx context.Context, opts Opts) error
}

// Opts is used as part of the LoadAuthorizer function so that only the relevant configuration fields are passed into a
// particular driver.
type Opts struct {
	openfgaDatastore storage.OpenFGADatastore
	sendSecurity     func(*api.EventSecurity)
}

// WithOpenFGADatastore should be passed into LoadAuthorizer when using the embedded openfga driver.
func WithOpenFGADatastore(store storage.OpenFGADatastore) func(*Opts) {
	return func(o *Opts) {
		o.openfgaDatastore = store
	}
}

// WithSendSecurity wires a callback into the authorizer so denial paths can
// emit authz_fail security events without depending on the events package.
func WithSendSecurity(send func(*api.EventSecurity)) func(*Opts) {
	return func(o *Opts) {
		o.sendSecurity = send
	}
}

// LoadAuthorizer instantiates, configures, and initialises an Authorizer.
func LoadAuthorizer(ctx context.Context, driver string, logger logger.Logger, options ...func(opts *Opts)) (auth.Authorizer, error) {
	opts := &Opts{}
	for _, o := range options {
		o(opts)
	}

	driverFunc, ok := authorizers[driver]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := driverFunc()
	err := d.init(driver, logger, opts.sendSecurity)
	if err != nil {
		return nil, fmt.Errorf("Failed initializing authorizer: %w", err)
	}

	err = d.load(ctx, *opts)
	if err != nil {
		return nil, fmt.Errorf("Failed loading authorizer: %w", err)
	}

	return d, nil
}
