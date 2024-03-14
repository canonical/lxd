package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openfga/openfga/pkg/storage"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

var authorizers = map[string]func() authorizer{}

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

type authorizer interface {
	Authorizer

	init(driverName string, logger logger.Logger) error
	load(ctx context.Context, certificateCache *identity.Cache, opts Opts) error
}

// PermissionChecker is a type alias for a function that returns whether a user has required permissions on an object.
// It is returned by Authorizer.GetPermissionChecker.
type PermissionChecker func(entityURL *api.URL) bool

// Authorizer is the primary external API for this package.
type Authorizer interface {
	Driver() string

	CheckPermission(ctx context.Context, r *http.Request, entityURL *api.URL, entitlement Entitlement) error
	GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, entityType entity.Type) (PermissionChecker, error)
}

// Opts is used as part of the LoadAuthorizer function so that only the relevant configuration fields are passed into a
// particular driver.
type Opts struct {
	config           map[string]any
	openfgaDatastore storage.OpenFGADatastore
}

// WithConfig can be passed into LoadAuthorizer to pass in driver specific configuration.
func WithConfig(c map[string]any) func(*Opts) {
	return func(o *Opts) {
		o.config = c
	}
}

// WithOpenFGADatastore should be passed into LoadAuthorizer when using the embedded openfga driver.
func WithOpenFGADatastore(store storage.OpenFGADatastore) func(*Opts) {
	return func(o *Opts) {
		o.openfgaDatastore = store
	}
}

// LoadAuthorizer instantiates, configures, and initialises an Authorizer.
func LoadAuthorizer(ctx context.Context, driver string, logger logger.Logger, certificateCache *identity.Cache, options ...func(opts *Opts)) (Authorizer, error) {
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
