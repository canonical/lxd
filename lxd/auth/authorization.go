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

const (
	// DriverTLS is used at start up to allow communication between cluster members and initialise the cluster database.
	DriverTLS string = "tls"

	// DriverEmbeddedOpenFGA is the default authorization driver. It currently falls back to DriverTLS for all TLS
	// clients. It cannot be initialised until after the cluster database is operational.
	DriverEmbeddedOpenFGA string = "embedded-openfga"
)

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

var authorizers = map[string]func() authorizer{
	DriverTLS: func() authorizer { return &tls{} },
	DriverEmbeddedOpenFGA: func() authorizer {
		return &embeddedOpenFGA{}
	},
}

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
	StopService(ctx context.Context) error

	CheckPermission(ctx context.Context, r *http.Request, entityURL *api.URL, entitlement Entitlement) error
	GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, entityType entity.Type) (PermissionChecker, error)

	AddProject(ctx context.Context, projectID int64, projectName string) error
	DeleteProject(ctx context.Context, projectID int64, projectName string) error
	RenameProject(ctx context.Context, projectID int64, oldName string, newName string) error

	AddCertificate(ctx context.Context, fingerprint string) error
	DeleteCertificate(ctx context.Context, fingerprint string) error

	AddStoragePool(ctx context.Context, storagePoolName string) error
	DeleteStoragePool(ctx context.Context, storagePoolName string) error

	AddImage(ctx context.Context, projectName string, fingerprint string) error
	DeleteImage(ctx context.Context, projectName string, fingerprint string) error

	AddImageAlias(ctx context.Context, projectName string, imageAliasName string) error
	DeleteImageAlias(ctx context.Context, projectName string, imageAliasName string) error
	RenameImageAlias(ctx context.Context, projectName string, oldAliasName string, newAliasName string) error

	AddInstance(ctx context.Context, projectName string, instanceName string) error
	DeleteInstance(ctx context.Context, projectName string, instanceName string) error
	RenameInstance(ctx context.Context, projectName string, oldInstanceName string, newInstanceName string) error

	AddNetwork(ctx context.Context, projectName string, networkName string) error
	DeleteNetwork(ctx context.Context, projectName string, networkName string) error
	RenameNetwork(ctx context.Context, projectName string, oldNetworkName string, newNetworkName string) error

	AddNetworkZone(ctx context.Context, projectName string, networkZoneName string) error
	DeleteNetworkZone(ctx context.Context, projectName string, networkZoneName string) error

	AddNetworkACL(ctx context.Context, projectName string, networkACLName string) error
	DeleteNetworkACL(ctx context.Context, projectName string, networkACLName string) error
	RenameNetworkACL(ctx context.Context, projectName string, oldNetworkACLName string, newNetworkACLName string) error

	AddProfile(ctx context.Context, projectName string, profileName string) error
	DeleteProfile(ctx context.Context, projectName string, profileName string) error
	RenameProfile(ctx context.Context, projectName string, oldProfileName string, newProfileName string) error

	AddStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string) error
	DeleteStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string) error
	RenameStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, oldStorageVolumeName string, newStorageVolumeName string) error

	AddStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string) error
	DeleteStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string) error
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
