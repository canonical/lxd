//go:build linux && cgo && !agent

package auth

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/openfga/openfga/pkg/server"
	openFGAErrors "github.com/openfga/openfga/pkg/server/errors"
	"go.uber.org/zap"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

const (
	// DriverEmbeddedOpenFGA is the default authorization driver. It currently falls back to DriverTLS for all TLS
	// clients. It cannot be initialised until after the cluster database is operational.
	DriverEmbeddedOpenFGA string = "embedded-openfga"
)

func init() {
	authorizers[DriverEmbeddedOpenFGA] = func() authorizer { return &embeddedOpenFGA{} }
}

//go:embed driver_openfga_model.openfga
var model string

// embeddedOpenFGA implements Authorizer using an embedded OpenFGA server.
type embeddedOpenFGA struct {
	commonAuthorizer
	tlsAuthorizer *tls
	server        openfgav1.OpenFGAServiceServer
	identityCache *identity.Cache
}

// The OpenFGA server requires a ULID to specify the store that we are querying against.
// Our storage.OpenFGADatastore implementation only has one store, so use a dummy value.
var dummyDatastoreULID = ulid.Make().String()

// load sets up the authorizer.
func (e *embeddedOpenFGA) load(ctx context.Context, identityCache *identity.Cache, opts Opts) error {
	if identityCache == nil {
		return fmt.Errorf("Must provide certificate cache")
	}

	e.identityCache = identityCache

	// Use the TLS driver for TLS authenticated users for now.
	tlsDriver := &tls{}
	err := tlsDriver.load(ctx, identityCache, opts)
	if err != nil {
		return err
	}

	e.tlsAuthorizer = tlsDriver

	if opts.openfgaDatastore == nil {
		return fmt.Errorf("The OpenFGA datastore option must be set")
	}

	openfgaServerOptions := []server.OpenFGAServiceV1Option{
		// Use our embedded datastore.
		server.WithDatastore(opts.openfgaDatastore),
		// Use our logger.
		server.WithLogger(openfgaLogger{l: e.logger}),
		// Set the max concurrency to 1 for both read and check requests.
		// Our driver cannot perform concurrent reads.
		server.WithMaxConcurrentReadsForListObjects(1),
		server.WithMaxConcurrentReadsForCheck(1),
	}

	e.server, err = server.NewServerWithOpts(openfgaServerOptions...)
	if err != nil {
		return err
	}

	// Transform the model from the DSL into the protobuf type.
	protoModel, err := transformer.TransformDSLToProto(model)
	if err != nil {
		return err
	}

	// Write the model to the server.
	_, err = e.server.WriteAuthorizationModel(ctx, &openfgav1.WriteAuthorizationModelRequest{
		StoreId:         dummyDatastoreULID,
		TypeDefinitions: protoModel.TypeDefinitions,
		SchemaVersion:   protoModel.SchemaVersion,
	})
	if err != nil {
		return err
	}

	return nil
}

// CheckPermission checks whether the user who sent the request has the given entitlement on the given entity using the
// embedded OpenFGA server.
func (e *embeddedOpenFGA) CheckPermission(ctx context.Context, r *http.Request, entityURL *api.URL, entitlement Entitlement) error {
	logCtx := logger.Ctx{"entity_url": entityURL.String(), "entitlement": entitlement, "request_url": r.URL.String(), "method": r.Method}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Inspect request.
	details, err := e.requestDetails(r)
	if err != nil {
		return fmt.Errorf("Failed to extract request details: %w", err)
	}

	// Untrusted requests are denied.
	if !details.trusted {
		return api.StatusErrorf(http.StatusForbidden, http.StatusText(http.StatusForbidden))
	}

	// Cluster or unix socket requests have admin permission.
	if details.isInternalOrUnix() {
		return nil
	}

	username := details.username()
	protocol := details.authenticationProtocol()
	logCtx["username"] = username
	logCtx["protocol"] = protocol
	l := e.logger.AddContext(logCtx)

	// If the authentication method was TLS, use the TLS driver instead.
	if protocol == api.AuthenticationMethodTLS {
		return e.tlsAuthorizer.CheckPermission(ctx, r, entityURL, entitlement)
	}

	// Get the identity.
	identityCacheEntry, err := e.identityCache.Get(protocol, username)
	if err != nil {
		return fmt.Errorf("Failed loading identity for %q: %w", username, err)
	}

	// If the identity type is not restricted, allow all (TLS authorization compatibility).
	isRestricted, err := identity.IsRestrictedIdentityType(identityCacheEntry.IdentityType)
	if err != nil {
		return fmt.Errorf("Failed to check restricted status for %q: %w", username, err)
	}

	if !isRestricted {
		return nil
	}

	// Combine the users LXD groups with any mappings that have come from the IDP.
	groups := identityCacheEntry.Groups
	idpGroups := details.identityProviderGroups()
	for _, idpGroup := range idpGroups {
		lxdGroups, err := e.identityCache.GetIdentityProviderGroupMapping(idpGroup)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("Failed to get identity provider group mapping for group %q: %w", idpGroup, err)
		} else if err != nil {
			continue
		}

		for _, lxdGroup := range lxdGroups {
			if !shared.ValueInSlice(lxdGroup, groups) {
				groups = append(groups, lxdGroup)
			}
		}
	}

	// Construct OpenFGA objects for the user (identity) and the entity.
	entityType, _, _, _, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Authorization driver failed to parse entity URL %q: %w", entityURL.String(), err)
	}

	userObject := fmt.Sprintf("%s:%s", entity.TypeIdentity, entity.IdentityURL(protocol, username).String())
	entityObject := fmt.Sprintf("%s:%s", entityType, entityURL.String())

	// Construct an OpenFGA check request.
	req := &openfgav1.CheckRequest{
		StoreId: dummyDatastoreULID,
		TupleKey: &openfgav1.CheckRequestTupleKey{
			User:     userObject,
			Relation: string(entitlement),
			Object:   entityObject,
		},
		ContextualTuples: &openfgav1.ContextualTupleKeys{
			// Users can always view (but not edit) themselves.
			TupleKeys: []*openfgav1.TupleKey{
				{
					User:     userObject,
					Relation: string(EntitlementCanView),
					Object:   userObject,
				},
			},
		},
	}

	// For each group, append a contextual tuple to make the identity a member.
	for _, groupName := range groups {
		req.ContextualTuples.TupleKeys = append(req.ContextualTuples.TupleKeys, &openfgav1.TupleKey{
			User:     userObject,
			Relation: "member",
			Object:   fmt.Sprintf("%s:%s", entity.TypeAuthGroup, entity.AuthGroupURL(groupName).String()),
		})
	}

	// For each project, append a contextual tuple to set make the identity an operator of that project (TLS authorization compatibility).
	for _, projectName := range identityCacheEntry.Projects {
		req.ContextualTuples.TupleKeys = append(req.ContextualTuples.TupleKeys, &openfgav1.TupleKey{
			User:     userObject,
			Relation: string(EntitlementOperator),
			Object:   fmt.Sprintf("%s:%s", entity.TypeProject, entity.ProjectURL(projectName).String()),
		})
	}

	// Perform the check.
	l.Debug("Checking OpenFGA relation")
	resp, err := e.server.Check(ctx, req)
	if err != nil {
		// Attempt to extract the internal error. This allows bubbling errors up from the OpenFGA datastore implementation.
		// (Otherwise we just get "rpc error (4000): Internal Server Error" or similar which isn't useful).
		var openFGAInternalError openFGAErrors.InternalError
		if errors.As(err, &openFGAInternalError) {
			err = openFGAInternalError.Internal()
		}

		return fmt.Errorf("Failed to check OpenFGA relation: %w", err)
	}

	// If not allowed, decide if the user can view the resource.
	if !resp.GetAllowed() {
		responseCode := http.StatusForbidden
		if r.Method == http.MethodGet {
			responseCode = http.StatusNotFound
		} else {
			// Otherwise, check if we can view the resource.
			req.TupleKey.Relation = string(EntitlementCanView)

			l.Debug("Checking OpenFGA relation")
			resp, err := e.server.Check(ctx, req)
			if err != nil {
				// Attempt to extract the internal error. This allows bubbling errors up from the OpenFGA datastore implementation.
				// (Otherwise we just get "rpc error (4000): Internal Server Error" or similar which isn't useful).
				var openFGAInternalError openFGAErrors.InternalError
				if errors.As(err, &openFGAInternalError) {
					err = openFGAInternalError.Internal()
				}

				return fmt.Errorf("Failed to check OpenFGA relation: %w", err)
			}

			// If we can't view the resource, return a generic not found error.
			if !resp.GetAllowed() {
				responseCode = http.StatusNotFound
			}
		}

		// For some entities, a GET request will check if the caller has permission edit permission and conditionally
		// populate configuration that may be sensitive. To reduce log verbosity, only log these cases at debug level.
		if entitlement == EntitlementCanEdit && r.Method == http.MethodGet {
			l.Debug("Access denied", logger.Ctx{"http_code": responseCode})
		} else {
			l.Info("Access denied", logger.Ctx{"http_code": responseCode})
		}

		return api.StatusErrorf(responseCode, http.StatusText(responseCode))
	}

	return nil
}

// GetPermissionChecker returns a PermissionChecker using the embedded OpenFGA server.
func (e *embeddedOpenFGA) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, entityType entity.Type) (PermissionChecker, error) {
	logCtx := logger.Ctx{"entity_type": entityType, "entitlement": entitlement, "url": r.URL.String(), "method": r.Method}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// allowFunc is used to allow/disallow all.
	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	// Inspect request.
	details, err := e.requestDetails(r)
	if err != nil {
		return nil, fmt.Errorf("Failed to extract request details: %w", err)
	}

	// Untrusted requests are denied.
	if !details.trusted {
		return allowFunc(false), nil
	}

	// Cluster or unix socket requests have admin permission.
	if details.isInternalOrUnix() {
		return allowFunc(true), nil
	}

	username := details.username()
	protocol := details.authenticationProtocol()
	logCtx["username"] = username
	logCtx["protocol"] = protocol
	l := e.logger.AddContext(logCtx)

	// If the authentication method was TLS, use the TLS driver instead.
	if protocol == api.AuthenticationMethodTLS {
		return e.tlsAuthorizer.GetPermissionChecker(ctx, r, entitlement, entityType)
	}

	// Get the identity.
	identityCacheEntry, err := e.identityCache.Get(protocol, username)
	if err != nil {
		if err != nil {
			return nil, fmt.Errorf("Failed loading identity for %q: %w", username, err)
		}
	}

	// If the identity type is not restricted, allow all (TLS authorization compatibility).
	isRestricted, err := identity.IsRestrictedIdentityType(identityCacheEntry.IdentityType)
	if err != nil {
		return nil, fmt.Errorf("Failed to check restricted status for %q: %w", username, err)
	}

	if !isRestricted {
		return allowFunc(true), nil
	}

	// Combine the users LXD groups with any mappings that have come from the IDP.
	groups := identityCacheEntry.Groups
	idpGroups := details.identityProviderGroups()
	for _, idpGroup := range idpGroups {
		lxdGroups, err := e.identityCache.GetIdentityProviderGroupMapping(idpGroup)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, fmt.Errorf("Failed to get identity provider group mapping for group %q: %w", idpGroup, err)
		} else if err != nil {
			continue
		}

		for _, lxdGroup := range lxdGroups {
			if !shared.ValueInSlice(lxdGroup, groups) {
				groups = append(groups, lxdGroup)
			}
		}
	}

	// Construct an OpenFGA list objects request.
	userObject := fmt.Sprintf("%s:%s", entity.TypeIdentity, entity.IdentityURL(protocol, username).String())
	req := &openfgav1.ListObjectsRequest{
		StoreId:  dummyDatastoreULID,
		Type:     entityType.String(),
		Relation: string(entitlement),
		User:     userObject,
		ContextualTuples: &openfgav1.ContextualTupleKeys{
			// Users can always view (but not edit) themselves.
			TupleKeys: []*openfgav1.TupleKey{
				{
					User:     userObject,
					Relation: string(EntitlementCanView),
					Object:   userObject,
				},
			},
		},
	}

	// For each group, append a contextual tuple to make the identity a member.
	for _, groupName := range groups {
		req.ContextualTuples.TupleKeys = append(req.ContextualTuples.TupleKeys, &openfgav1.TupleKey{
			User:     userObject,
			Relation: "member",
			Object:   fmt.Sprintf("%s:%s", entity.TypeAuthGroup, entity.AuthGroupURL(groupName).String()),
		})
	}

	// For each project, append a contextual tuple to set make the identity an operator of that project (TLS authorization compatibility).
	for _, projectName := range identityCacheEntry.Projects {
		req.ContextualTuples.TupleKeys = append(req.ContextualTuples.TupleKeys, &openfgav1.TupleKey{
			User:     userObject,
			Relation: string(EntitlementOperator),
			Object:   fmt.Sprintf("%s:%s", entity.TypeProject, entity.ProjectURL(projectName).String()),
		})
	}

	// Perform the request.
	l.Debug("Listing related objects for user")
	resp, err := e.server.ListObjects(ctx, req)
	if err != nil {
		// Attempt to extract the internal error. This allows bubbling errors up from the OpenFGA datastore implementation.
		// (Otherwise we just get "rpc error (4000): Internal Server Error" or similar which isn't useful).
		var openFGAInternalError openFGAErrors.InternalError
		if errors.As(err, &openFGAInternalError) {
			err = openFGAInternalError.Internal()
		}

		return nil, fmt.Errorf("Failed to list OpenFGA objects of type %q with entitlement %q for user %q: %w", entityType.String(), entitlement, username, err)
	}

	objects := resp.GetObjects()

	// Return a permission checker that constructs an OpenFGA object from the given URL and returns true if the object is
	// found in the list of objects in the response.
	return func(entityURL *api.URL) bool {
		object := fmt.Sprintf("%s:%s", entityType, entityURL.String())
		return shared.ValueInSlice(object, objects)
	}, nil
}

// openfgaLogger implements OpenFGAs logger.Logger interface but delegates to our logger.
type openfgaLogger struct {
	l logger.Logger
}

func logCtxFromFields(fields []zap.Field) logger.Ctx {
	ctx := make(logger.Ctx, len(fields))
	for _, f := range fields {
		if f.Integer != 0 {
			ctx[f.Key] = f.Integer
		} else if f.String != "" {
			ctx[f.Key] = f.String
		} else {
			ctx[f.Key] = f.Interface
		}
	}

	return ctx
}

// Debug delegates to the authorizers logger.
func (o openfgaLogger) Debug(s string, field ...zap.Field) {
	o.l.Debug(s, logCtxFromFields(field))
}

// Info delegates to the authorizers logger.
func (o openfgaLogger) Info(s string, field ...zap.Field) {
	o.l.Info(s, logCtxFromFields(field))
}

// Warn delegates to the authorizers logger.
func (o openfgaLogger) Warn(s string, field ...zap.Field) {
	o.l.Warn(s, logCtxFromFields(field))
}

// Error delegates to the authorizers logger.
func (o openfgaLogger) Error(s string, field ...zap.Field) {
	o.l.Error(s, logCtxFromFields(field))
}

// Panic delegates to the authorizers logger.
func (o openfgaLogger) Panic(s string, field ...zap.Field) {
	o.l.Panic(s, logCtxFromFields(field))
}

// Fatal delegates to the authorizers logger.
func (o openfgaLogger) Fatal(s string, field ...zap.Field) {
	o.l.Fatal(s, logCtxFromFields(field))
}

// DebugWithContext delegates to the authorizers logger.
func (o openfgaLogger) DebugWithContext(ctx context.Context, s string, field ...zap.Field) {
	o.l.Debug(s, logCtxFromFields(field))
}

// InfoWithContext delegates to the authorizers logger.
func (o openfgaLogger) InfoWithContext(ctx context.Context, s string, field ...zap.Field) {
	o.l.Info(s, logCtxFromFields(field))
}

// WarnWithContext delegates to the authorizers logger.
func (o openfgaLogger) WarnWithContext(ctx context.Context, s string, field ...zap.Field) {
	o.l.Warn(s, logCtxFromFields(field))
}

// ErrorWithContext delegates to the authorizers logger.
func (o openfgaLogger) ErrorWithContext(ctx context.Context, s string, field ...zap.Field) {
	o.l.Error(s, logCtxFromFields(field))
}

// PanicWithContext delegates to the authorizers logger.
func (o openfgaLogger) PanicWithContext(ctx context.Context, s string, field ...zap.Field) {
	o.l.Panic(s, logCtxFromFields(field))
}

// FatalWithContext delegates to the authorizers logger.
func (o openfgaLogger) FatalWithContext(ctx context.Context, s string, field ...zap.Field) {
	o.l.Fatal(s, logCtxFromFields(field))
}
