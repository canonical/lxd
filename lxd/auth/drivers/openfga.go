//go:build linux && cgo && !agent

package drivers

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

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
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

//go:embed openfga_model.openfga
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
	tlsDriver := &tls{
		commonAuthorizer: commonAuthorizer{
			logger: e.logger,
		},
	}

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
// embedded OpenFGA server. A http.StatusNotFound error is returned when the entity does not exist, or when the entity
// exists but the caller does not have permission to view it. A http.StatusForbidden error is returned if the caller has
// permission to view the entity, but does not have the given entitlement.
//
// Note: Internally we call (openfgav1.OpenFGAServiceServer).Check to implement this. Since our implementation of
// storage.OpenFGADatastore pulls data directly from the database, we need to be careful about the handling of entities
// contained within projects that do not have features enabled. For example, if the given entity URL is for a network in
// project "foo", but project "foo" does not have `features.networks=true`, then we must not use project "foo" in our
// authorization check because this network does not exist in the database. We will always expect the given entity URL
// to contain the request project name, but we expect that request.CtxEffectiveProjectName will be set in the request
// context. The driver will rewrite the project name with the effective project name for the purpose of the authorization
// check, but will not automatically allow "punching through" to the effective (default) project. An administrator can
// allow specific permissions against those entities.
func (e *embeddedOpenFGA) CheckPermission(ctx context.Context, entityURL *api.URL, entitlement auth.Entitlement) error {
	logCtx := logger.Ctx{"entity_url": entityURL.String(), "entitlement": entitlement}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Untrusted requests are denied.
	if !auth.IsTrusted(ctx) {
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	isRoot, err := auth.IsServerAdmin(ctx, e.identityCache)
	if err != nil {
		return fmt.Errorf("Failed to check caller privilege: %w", err)
	}

	// Cluster or unix socket requests have admin permission.
	if isRoot {
		return nil
	}

	id, err := auth.GetIdentityFromCtx(ctx, e.identityCache)
	if err != nil {
		return fmt.Errorf("Failed to get caller identity: %w", err)
	}

	logCtx["username"] = id.Identifier
	logCtx["protocol"] = id.AuthenticationMethod
	l := e.logger.AddContext(logCtx)

	// If the authentication method was TLS, use the TLS driver instead.
	if id.AuthenticationMethod == api.AuthenticationMethodTLS {
		return e.tlsAuthorizer.CheckPermission(ctx, entityURL, entitlement)
	}

	// Combine the users LXD groups with any mappings that have come from the IDP.
	groups := id.Groups
	idpGroups, err := auth.GetIdentityProviderGroupsFromCtx(ctx)
	if err != nil {
		return fmt.Errorf("Failed to get caller identity provider groups: %w", err)
	}

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

	// Deconstruct the given URL.
	entityType, projectName, location, pathArguments, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return fmt.Errorf("Authorization driver failed to parse entity URL %q: %w", entityURL.String(), err)
	}

	// The project in the given URL may be for a project that does not have a feature enabled, in this case the auth check
	// will fail because the resource doesn't actually exist in that project. To correct this, we use the effective project from
	// the request context if present.
	effectiveProject, _ := request.GetCtxValue[string](ctx, request.CtxEffectiveProjectName)
	if effectiveProject != "" {
		projectName = effectiveProject
	}

	// Construct the URL in a standardised form (adding the project parameter if it was not present).
	entityURL, err = entityType.URL(projectName, location, pathArguments...)
	if err != nil {
		return fmt.Errorf("Failed to standardize entity URL: %w", err)
	}

	userObject := fmt.Sprintf("%s:%s", entity.TypeIdentity, entity.IdentityURL(id.AuthenticationMethod, id.Identifier).String())
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
					Relation: string(auth.EntitlementCanView),
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
		if entitlement == auth.EntitlementCanView {
			responseCode = http.StatusNotFound
		} else {
			// Otherwise, check if we can view the resource.
			req.TupleKey.Relation = string(auth.EntitlementCanView)

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
		// populate configuration that may be sensitive. To reduce log verbosity, only log `can_edit` on `server` at debug level.
		if entitlement == auth.EntitlementCanEdit && entityType == entity.TypeServer {
			l.Debug("Access denied", logger.Ctx{"http_code": responseCode})
		} else {
			l.Info("Access denied", logger.Ctx{"http_code": responseCode})
		}

		return api.NewGenericStatusError(responseCode)
	}

	return nil
}

// GetPermissionChecker returns an auth.PermissionChecker using the embedded OpenFGA server.
//
// Note: As with CheckPermission, we need to be careful about the usage of this function for entity types that may not
// be enabled within a project. For these cases request.CtxEffectiveProjectName must be set in the given context before
// this function is called. The returned auth.PermissionChecker will expect entity URLs to contain the request URL. These
// will be re-written to contain the effective project if set, so that they correspond to the list returned by OpenFGA.
func (e *embeddedOpenFGA) GetPermissionChecker(ctx context.Context, entitlement auth.Entitlement, entityType entity.Type) (auth.PermissionChecker, error) {
	logCtx := logger.Ctx{"entity_type": entityType, "entitlement": entitlement}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// allowFunc is used to allow/disallow all.
	allowFunc := func(b bool) func(*api.URL) bool {
		return func(*api.URL) bool {
			return b
		}
	}

	// There is only one server entity, so no need to do a ListObjects request if the entity type is a server. Instead perform a permission check against
	// the server URL and return an appropriate PermissionChecker.
	if entityType == entity.TypeServer {
		err := e.CheckPermission(ctx, entity.ServerURL(), entitlement)
		if err == nil {
			return allowFunc(true), nil
		} else if auth.IsDeniedError(err) {
			return allowFunc(false), nil
		}

		return nil, fmt.Errorf("Failed to get a permission checker: %w", err)
	}

	// Untrusted requests are denied.
	if !auth.IsTrusted(ctx) {
		return allowFunc(false), nil
	}

	isRoot, err := auth.IsServerAdmin(ctx, e.identityCache)
	if err != nil {
		return nil, fmt.Errorf("Failed to check caller privilege: %w", err)
	}

	// Cluster or unix socket requests have admin permission.
	if isRoot {
		return allowFunc(true), nil
	}

	id, err := auth.GetIdentityFromCtx(ctx, e.identityCache)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller identity: %w", err)
	}

	logCtx["username"] = id.Identifier
	logCtx["protocol"] = id.AuthenticationMethod
	l := e.logger.AddContext(logCtx)

	// If the authentication method was TLS, use the TLS driver instead.
	if id.AuthenticationMethod == api.AuthenticationMethodTLS {
		return e.tlsAuthorizer.GetPermissionChecker(ctx, entitlement, entityType)
	}

	// Combine the users LXD groups with any mappings that have come from the IDP.
	groups := id.Groups
	idpGroups, err := auth.GetIdentityProviderGroupsFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get caller identity provider groups: %w", err)
	}

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
	userObject := fmt.Sprintf("%s:%s", entity.TypeIdentity, entity.IdentityURL(id.AuthenticationMethod, id.Identifier).String())
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
					Relation: string(auth.EntitlementCanView),
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

		return nil, fmt.Errorf("Failed to list OpenFGA objects of type %q with entitlement %q for user %q: %w", entityType.String(), entitlement, id.Identifier, err)
	}

	objects := resp.GetObjects()

	// Return a permission checker that constructs an OpenFGA object from the given URL and returns true if the object is
	// found in the list of objects in the response.
	return func(entityURL *api.URL) bool {
		parsedEntityType, projectName, location, pathArguments, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			l.Error("Failed to parse permission checker entity URL", logger.Ctx{"url": entityURL.String(), "err": err})
			return false
		}

		if parsedEntityType != entityType {
			l.Error("Unexpected permission checker input URL", logger.Ctx{"expected_entity_type": entityType, "actual_entity_type": parsedEntityType, "url": entityURL.String()})
			return false
		}

		// The project in the given URL may be for a project that does not have a feature enabled, in this case the auth check
		// will fail because the resource doesn't actually exist in that project. To correct this, we use the effective project from
		// the request context if present.
		effectiveProject, _ := request.GetCtxValue[string](ctx, request.CtxEffectiveProjectName)
		if effectiveProject != "" {
			projectName = effectiveProject
		}

		standardisedEntityURL, err := entityType.URL(projectName, location, pathArguments...)
		if err != nil {
			l.Error("Failed to standardise permission checker entity URL", logger.Ctx{"url": entityURL.String(), "err": err})
			return false
		}

		object := fmt.Sprintf("%s:%s", entityType, standardisedEntityURL.String())
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
