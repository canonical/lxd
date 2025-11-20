package drivers

import (
	"context"
	"errors"
	"fmt"

	"github.com/openfga/openfga/pkg/storage"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/logger"
)

var authorizers = map[string]func() authorizer{}

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = errors.New("Unknown driver")

type authorizer interface {
	auth.Authorizer

	init(driverName string, logger logger.Logger) error
	load(ctx context.Context, certificateCache *identity.Cache, opts Opts) error
}

// Opts is used as part of the LoadAuthorizer function so that only the relevant configuration fields are passed into a
// particular driver.
type Opts struct {
	config           map[string]any
	openfgaDatastore storage.OpenFGADatastore
}

// WithOpenFGADatastore should be passed into LoadAuthorizer when using the embedded openfga driver.
func WithOpenFGADatastore(store storage.OpenFGADatastore) func(*Opts) {
	return func(o *Opts) {
		o.openfgaDatastore = store
	}
}

// LoadAuthorizer instantiates, configures, and initialises an Authorizer.
func LoadAuthorizer(ctx context.Context, driver string, logger logger.Logger, certificateCache *identity.Cache, options ...func(opts *Opts)) (auth.Authorizer, error) {
	opts := &Opts{}
	for _, o := range options {
		o(opts)
	}

	driverFunc, ok := authorizers[driver]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := driverFunc()
	err := d.init(driver, logger)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize authorizer: %w", err)
	}

	err = d.load(ctx, certificateCache, *opts)
	if err != nil {
		return nil, fmt.Errorf("Failed to load authorizer: %w", err)
	}

	return d, nil
}
