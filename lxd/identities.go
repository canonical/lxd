package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/auth/encryption"
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

const (
	// defaultBearerTokenExpiry is used when issuing bearer tokens if no expiry is provided.
	// The default value is 10 years (essentially no expiry).
	defaultBearerTokenExpiry = "10y"
)

var identitiesCmd = APIEndpoint{
	Name:        "identities",
	Path:        "auth/identities",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		// Empty authentication method will return all identities.
		Handler:       identitiesGet(""),
		AccessHandler: allowAuthenticated,
	},
}

var currentIdentityCmd = APIEndpoint{
	Name:        "identities",
	Path:        "auth/identities/current",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identityGetCurrent,
		AccessHandler: allowAuthenticated,
	},
}

var tlsIdentitiesCmd = APIEndpoint{
	Name:        "identities",
	Path:        "auth/identities/tls",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identitiesGet(api.AuthenticationMethodTLS),
		AccessHandler: allowAuthenticated,
	},
	Post: APIEndpointAction{
		Handler:        identitiesTLSPost,
		AllowUntrusted: true,
	},
}

var oidcIdentitiesCmd = APIEndpoint{
	Name:        "identities",
	Path:        "auth/identities/oidc",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identitiesGet(api.AuthenticationMethodOIDC),
		AccessHandler: allowAuthenticated,
	},
}

var bearerIdentitiesCmd = APIEndpoint{
	Name:        "identities",
	Path:        "auth/identities/bearer",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identitiesGet(api.AuthenticationMethodBearer),
		AccessHandler: allowAuthenticated,
	},
	Post: APIEndpointAction{
		Handler:       identitiesBearerPost,
		AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanCreateIdentities),
	},
}

var tlsIdentityCmd = APIEndpoint{
	Name:        "identity",
	Path:        "auth/identities/tls/{nameOrIdentifier}",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identityGet,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodTLS, auth.EntitlementCanView),
	},
	Put: APIEndpointAction{
		Handler:       identityPut(api.AuthenticationMethodTLS),
		AccessHandler: allowAuthenticated,
	},
	Patch: APIEndpointAction{
		Handler:       identityPatch(api.AuthenticationMethodTLS),
		AccessHandler: allowAuthenticated,
	},
	Delete: APIEndpointAction{
		Handler:       identityDelete,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodTLS, auth.EntitlementCanDelete),
	},
}

var oidcIdentityCmd = APIEndpoint{
	Name:        "identity",
	Path:        "auth/identities/oidc/{nameOrIdentifier}",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identityGet,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanView),
	},
	Put: APIEndpointAction{
		Handler:       identityPut(api.AuthenticationMethodOIDC),
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanEdit),
	},
	Patch: APIEndpointAction{
		Handler:       identityPatch(api.AuthenticationMethodOIDC),
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanEdit),
	},
	Delete: APIEndpointAction{
		Handler:       identityDelete,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodOIDC, auth.EntitlementCanDelete),
	},
}

var bearerIdentityCmd = APIEndpoint{
	Name:        "identity",
	Path:        "auth/identities/bearer/{nameOrIdentifier}",
	MetricsType: entity.TypeIdentity,

	Get: APIEndpointAction{
		Handler:       identityGet,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodBearer, auth.EntitlementCanView),
	},
	Put: APIEndpointAction{
		Handler:       identityPut(api.AuthenticationMethodBearer),
		AccessHandler: identityAccessHandler(api.AuthenticationMethodBearer, auth.EntitlementCanEdit),
	},
	Patch: APIEndpointAction{
		Handler:       identityPatch(api.AuthenticationMethodBearer),
		AccessHandler: identityAccessHandler(api.AuthenticationMethodBearer, auth.EntitlementCanEdit),
	},
	Delete: APIEndpointAction{
		Handler:       identityDelete,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodBearer, auth.EntitlementCanDelete),
	},
}

var bearerIdentityTokenCmd = APIEndpoint{
	Path:        "auth/identities/bearer/{nameOrIdentifier}/token",
	MetricsType: entity.TypeIdentity,

	Post: APIEndpointAction{
		Handler:       identityBearerTokenPost,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodBearer, auth.EntitlementCanEdit),
	},
	Delete: APIEndpointAction{
		Handler:       identityBearerTokenDelete,
		AccessHandler: identityAccessHandler(api.AuthenticationMethodBearer, auth.EntitlementCanEdit),
	},
}

// identityNotificationFunc is used when an identity is created, updated, or deleted.
// The signature is defined here as a convenience so that the function signature doesn't need to be written in full when used as an argument.
type identityNotificationFunc func(action lifecycle.IdentityAction, authenticationMethod string, identifier string, updateCache bool) (*api.EventLifecycle, error)

const (
	// ctxClusterDBIdentity is used in the identityAccessHandler to set a cluster.Identity into the request context.
	// The database call is required for authorization and this avoids performing the same query twice.
	ctxClusterDBIdentity request.CtxKey = "cluster-db-identity"
)

// addIdentityDetailsToContext queries the database for the identity with the given authentication method and the
// `nameOrIdentifier` path argument. This expands the `nameOrIdentifier` so that we can get the fully qualified URL
// of the identity matching what is expected by the authorizer. It returns the Identity for convenience, and also adds
// it to the request context with the ctxClusterDBIdentity context key for later use.
func addIdentityDetailsToContext(s *state.State, r *http.Request, authenticationMethod string) (*dbCluster.Identity, error) {
	muxVars := mux.Vars(r)
	nameOrID, err := url.PathUnescape(muxVars["nameOrIdentifier"])
	if err != nil {
		return nil, fmt.Errorf("Failed to unescape path argument: %w", err)
	}

	var id *dbCluster.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err = dbCluster.GetIdentityByNameOrIdentifier(ctx, tx.Tx(), authenticationMethod, nameOrID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Mask not found error to prevent discovery
			return nil, api.NewGenericStatusError(http.StatusNotFound)
		}

		return nil, err
	}

	request.SetContextValue(r, ctxClusterDBIdentity, id)
	return id, nil
}

// identityAccessHandler performs some initial validation of the request and gets the identity by its name or
// identifier. If one is found, the identifier is used in the URL that is passed to (auth.Authorizer).CheckPermission.
// The cluster.Identity is set in the request context.
func identityAccessHandler(authenticationMethod string, entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		id, err := addIdentityDetailsToContext(s, r, authenticationMethod)
		if err != nil {
			return response.SmartError(err)
		}

		identityType, err := identity.New(string(id.Type))
		if err != nil {
			return response.SmartError(err)
		}

		if identityType.IsFineGrained() {
			err = s.Authorizer.CheckPermission(r.Context(), entity.IdentityURL(authenticationMethod, id.Identifier), entitlement)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			err = s.Authorizer.CheckPermission(r.Context(), entity.CertificateURL(id.Identifier), entitlement)
			if err != nil {
				return response.SmartError(err)
			}
		}

		return response.EmptySyncResponse
	}
}

// swagger:operation POST /1.0/auth/identities/tls?public identities identities_post_tls_untrusted
//
//  Add a TLS identity
//
//  Adds a TLS identity as a trusted client.
//  In this mode, the `trust_token` property must be set to the correct value.
//  The certificate that the client sent during the TLS handshake will be added.
//  The `certificate` field must be omitted.
//
//  The `?public` part of the URL isn't required, it's simply used to
//  separate the two behaviors of this endpoint.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: body
//      name: TLS identity
//      description: TLS Identity
//      required: true
//      schema:
//        $ref: "#/definitions/IdentitiesPostTLS"
//  responses:
//    "201":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation POST /1.0/auth/identities/tls identities identities_post_tls
//
//	Add a TLS identity.
//
//	Adds a TLS identity as a trusted client, or creates a pending TLS identity and returns a token
//	for use by an untrusted client. One of `token` or `certificate` must be set.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: TLS identity
//	    description: TLS Identity
//	    required: true
//	    schema:
//	      $ref: "#/definitions/IdentitiesTLSPost"
//	responses:
//	  "201":
//	    oneOf:
//	      - $ref: "#/responses/CertificateAddToken"
//	      - $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identitiesTLSPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	req := api.IdentitiesTLSPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	if !requestor.IsTrusted() {
		return createIdentityTLSUntrusted(r.Context(), s, r.TLS.PeerCertificates, networkCert, req, notify)
	}

	var peerCertificates []*x509.Certificate
	idType, err := requestor.CallerIdentityType()
	if err == nil {
		if idType.Name() == api.IdentityTypeBearerTokenInitialUI && r.TLS != nil {
			// When authenticated as the initial UI identity, allow creating a TLS identity from the presented peer certificate.
			// This allows LXD UI to establish mTLS by injecting a client certificate during initial UI bearer-token access.
			peerCertificates = r.TLS.PeerCertificates
		}
	}

	return createIdentityTLSTrusted(r.Context(), s, peerCertificates, networkCert, req, notify)
}

// swagger:operation POST /1.0/auth/identities/bearer identities identities_post_bearer
//
//	Add a bearer identity.
//
//	Creates a new bearer identity.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: Bearer identity
//	    description: Bearer Identity
//	    required: true
//	    schema:
//	      $ref: "#/definitions/IdentitiesBearerPost"
//	responses:
//	  "201":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identitiesBearerPost(d *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	req := api.IdentitiesBearerPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		return response.BadRequest(errors.New("Identity name must be provided"))
	}

	idType, err := identity.New(req.Type)
	if err != nil {
		return response.SmartError(err)
	}

	if idType.AuthenticationMethod() != api.AuthenticationMethodBearer {
		return response.BadRequest(fmt.Errorf("Identities of type %q cannot be created via the bearer API", req.Type))
	}

	if req.Type == api.IdentityTypeBearerTokenInitialUI && requestor.CallerProtocol() != request.ProtocolUnix {
		return response.Forbidden(errors.New("Initial UI identities may only be created via unix socket"))
	}

	newIdentityID := uuid.New()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the identity.
		id, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
			AuthMethod: api.AuthenticationMethodBearer,
			Type:       dbCluster.IdentityType(req.Type),
			Identifier: newIdentityID.String(),
			Name:       req.Name,
		})
		if err != nil {
			return err
		}

		if len(req.Groups) > 0 {
			return dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), int(id), req.Groups)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)

	// Send lifecycle event for identity creation.
	// No need to update cache because no token has been issued for the identity yet, so they can't authenticate.
	lc, err := notify(lifecycle.IdentityCreated, api.AuthenticationMethodBearer, newIdentityID.String(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation POST /1.0/auth/identities/bearer/{nameOrID}/token identities identity_post_bearer_token
//
//	Issue a token for a bearer identity.
//
//	Issues a new token for the bearer identity and revokes any existing token.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: Token request
//	    description: Parameters of token creation
//	    required: true
//	    schema:
//	      $ref: "#/definitions/IdentityBearerTokenPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/IdentityBearerToken"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identityBearerTokenPost(d *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	var req api.IdentityBearerTokenPost
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	id, err := request.GetContextValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	if id.Type == api.IdentityTypeBearerTokenInitialUI {
		if requestor.CallerProtocol() != request.ProtocolUnix {
			return response.Forbidden(errors.New("Initial UI identity tokens may only be issued via unix socket"))
		}

		if req.Expiry != "" {
			return response.BadRequest(errors.New("The initial UI token expiry cannot be set"))
		}

		req.Expiry = "1d"
	}

	expiry := req.Expiry
	if expiry == "" {
		expiry = defaultBearerTokenExpiry
	}

	expiresAt, err := shared.GetExpiry(time.Now().UTC(), expiry)
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	var secret []byte
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		secret, err = dbCluster.RotateBearerIdentitySigningKey(ctx, tx.Tx(), id.ID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var token string
	switch id.Type {
	case api.IdentityTypeBearerTokenClient, api.IdentityTypeBearerTokenInitialUI:
		var serverCertFingerprint string

		// When creating LXD bearer tokens, include the server certificate fingerprint.
		serverCertFingerprint, err = shared.CertFingerprintStr(string(s.Endpoints.NetworkPublicKey()))
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to parse server certificate fingerprint: %w", err))
		}

		token, err = encryption.GetClientBearerToken(secret, id.Identifier, s.GlobalConfig.ClusterUUID(), expiresAt, serverCertFingerprint)
	case api.IdentityTypeBearerTokenDevLXD:
		token, err = encryption.GetDevLXDBearerToken(secret, id.Identifier, s.GlobalConfig.ClusterUUID(), expiresAt)
	default:
		err = api.StatusErrorf(http.StatusBadRequest, "Token cannot be issued for identity of type %q", id.Type)
	}

	if err != nil {
		return response.SmartError(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)
	_, err = notify(lifecycle.IdentityUpdated, api.AuthenticationMethodBearer, id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, api.IdentityBearerToken{Token: token})
}

// swagger:operation POST /1.0/auth/identities/bearer/{nameOrID}/token identities identity_delete_bearer_token
//
//	Revoke a bearer identity token.
//
//	Revokes any existing token for the identity.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	responses:
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identityBearerTokenDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := request.GetContextValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := dbCluster.DeleteBearerIdentitySigningKey(ctx, tx.Tx(), id.ID)
		if err != nil {
			return fmt.Errorf("Failed to revoke token: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	notify := newIdentityNotificationFunc(s, r, networkCert, serverCert)
	_, err = notify(lifecycle.IdentityUpdated, api.AuthenticationMethodBearer, id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// createIdentityTLSUntrusted handles requests to create an identity when the caller is not trusted.
func createIdentityTLSUntrusted(ctx context.Context, s *state.State, peerCertificates []*x509.Certificate, networkCert *shared.CertInfo, req api.IdentitiesTLSPost, notify identityNotificationFunc) response.Response {
	// If not trusted a token must be provided.
	if req.TrustToken == "" {
		return response.Forbidden(errors.New("Trust token required"))
	}

	// If not trusted other fields must not be populated.
	if req.Token || req.Certificate != "" || req.Name != "" || len(req.Groups) > 0 {
		return response.Forbidden(errors.New("Only trust token must be provided"))
	}

	// If not trusted get the certificate from the request TLS config.
	if len(peerCertificates) < 1 {
		return response.BadRequest(errors.New("No client certificate provided"))
	}

	cert := peerCertificates[len(peerCertificates)-1]

	// Validate certificate.
	err := certificateValidate(networkCert, cert)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check if certificate add token is valid.
	joinToken, err := shared.CertificateTokenDecode(req.TrustToken)
	if err != nil {
		return response.Forbidden(nil)
	}

	// If so then check there is a matching pending TLS identity.
	identifier, err := tlsIdentityTokenValidate(ctx, s, *joinToken)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed during search for pending TLS identity: %w", err))
	}

	// Activate the pending identity with the certificate.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.ActivateTLSIdentity(ctx, tx.Tx(), identifier, cert)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other members, update the cache, and send a lifecycle event.
	lc, err := notify(lifecycle.IdentityUpdated, api.AuthenticationMethodTLS, identifier.String(), true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// createIdentityTLSTrusted handles requests to create an identity when the caller is trusted.
func createIdentityTLSTrusted(ctx context.Context, s *state.State, peerCertificates []*x509.Certificate, networkCert *shared.CertInfo, req api.IdentitiesTLSPost, notify identityNotificationFunc) response.Response {
	// Check if the caller has permission to create identities.
	err := s.Authorizer.CheckPermission(ctx, entity.ServerURL(), auth.EntitlementCanCreateIdentities)
	if err != nil {
		return response.SmartError(err)
	}

	// A name is required whether getting a token or directly creating the identity with a certificate.
	if req.Name == "" {
		return response.BadRequest(errors.New("Identity name must be provided"))
	}

	// If the caller is trusted, they should not be providing a trust token
	if req.TrustToken != "" {
		return response.Conflict(errors.New("Client already trusted"))
	}

	// Can't request a token if a certificate is provided.
	if req.Token && req.Certificate != "" {
		return response.BadRequest(errors.New("Can't use certificate if token is requested"))
	}

	// If a token is requested, create a pending TLS identity and return an api.CertificateAddToken.
	if req.Token {
		return createIdentityTLSPending(ctx, s, req, notify)
	}

	cert := req.Certificate
	if cert == "" && len(peerCertificates) > 0 {
		// Use peer certificate if no certificate was provided in the request body.
		peerCert := peerCertificates[len(peerCertificates)-1]
		peerCertPEM := &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: peerCert.Raw,
		}

		cert = string(pem.EncodeToMemory(peerCertPEM))
	}

	// Validate the certificate.
	fingerprint, metadata, err := validateIdentityCert(networkCert, cert)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the identity.
		id, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
			AuthMethod: api.AuthenticationMethodTLS,
			Type:       api.IdentityTypeCertificateClient,
			Identifier: fingerprint,
			Name:       req.Name,
			Metadata:   metadata,
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				// Check if we already have the certificate.
				_, err := dbCluster.GetIdentityID(ctx, tx.Tx(), api.AuthenticationMethodTLS, fingerprint)
				if err == nil {
					return api.NewStatusError(http.StatusConflict, "Identity already exists")
				}

				// If there are no identities with the same fingerprint, then there is a name conflict
				return api.StatusErrorf(http.StatusConflict, "An identity with name %q already exists", req.Name)
			}
		}

		if len(req.Groups) > 0 {
			return dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), int(id), req.Groups)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other members, update the cache, and send a lifecycle event.
	lc, err := notify(lifecycle.IdentityCreated, api.AuthenticationMethodTLS, fingerprint, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, lc.Source)
}

func createCertificateAddToken(s *state.State, clientName string, identityType string) (*api.CertificateAddToken, error) {
	localHTTPSAddress := s.LocalConfig.HTTPSAddress()

	// Tokens are useless if the server isn't listening (how will the untrusted client contact the server?)
	if localHTTPSAddress == "" {
		return nil, api.NewStatusError(http.StatusBadRequest, "Can't issue token when server isn't listening on network")
	}

	// Get all addresses the server is listening on. This is encoded in the certificate token,
	// so that the client will not have to specify a server address. The client will iterate
	// through all these addresses until it can connect to one of them.
	addresses, err := util.ListenAddresses(localHTTPSAddress)
	if err != nil {
		return nil, err
	}

	// Generate join secret for new client. This will be stored inside the join token operation and will be
	// supplied by the joining client (encoded inside the join token) which will allow us to lookup the correct
	// operation in order to validate the requested joining client name is correct and authorised.
	joinSecret, err := shared.RandomCryptoString()
	if err != nil {
		return nil, err
	}

	// Generate fingerprint of network certificate so joining member can automatically trust the correct
	// certificate when it is presented during the join process.
	fingerprint, err := shared.CertFingerprintStr(string(s.Endpoints.NetworkPublicKey()))
	if err != nil {
		return nil, err
	}

	// Calculate an expiry for the pending TLS identity.
	expiry := s.GlobalConfig.RemoteTokenExpiry()
	var expiresAt time.Time
	if expiry != "" {
		expiresAt, err = shared.GetExpiry(time.Now(), expiry)
		if err != nil {
			return nil, err
		}
	}

	// Return the CertificateAddToken.
	token := api.CertificateAddToken{
		ClientName:  clientName,
		Fingerprint: fingerprint,
		Addresses:   addresses,
		Secret:      joinSecret,
		ExpiresAt:   expiresAt,
		// Set the Type field so that the client can differentiate
		// between tokens meant for the certificates API and the auth API.
		Type: identityType,
	}

	return &token, nil
}

func createIdentityTLSPending(ctx context.Context, s *state.State, req api.IdentitiesTLSPost, notify identityNotificationFunc) response.Response {
	// Create CertificateAddToken token.
	token, err := createCertificateAddToken(s, req.Name, api.IdentityTypeCertificateClient)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create certificate add token: %w", err))
	}

	// Generate an identifier for the identity and calculate its metadata.
	identifier := uuid.New()
	metadata := dbCluster.PendingTLSMetadata{
		Secret: token.Secret,
		Expiry: token.ExpiresAt,
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to encode pending TLS identity metadata: %w", err))
	}

	// Create the identity.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
			AuthMethod: api.AuthenticationMethodTLS,
			Type:       api.IdentityTypeCertificateClientPending,
			Identifier: identifier.String(),
			Name:       req.Name,
			Metadata:   string(metadataJSON),
		})
		if err != nil {
			return err
		}

		if len(req.Groups) > 0 {
			return dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), int(id), req.Groups)
		}

		return nil
	})
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			// The only conflict here should be on identity name, since the identifier is a UUID.
			return response.Conflict(fmt.Errorf("An identity with name %q already exists", req.Name))
		}

		return response.SmartError(fmt.Errorf("Failed to create pending TLS identity: %w", err))
	}

	// Notify other members, update the cache, and send a lifecycle event.
	lc, err := notify(lifecycle.IdentityCreated, api.AuthenticationMethodTLS, identifier.String(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, token, lc.Source)
}

func tlsIdentityTokenValidate(ctx context.Context, s *state.State, token api.CertificateAddToken) (uuid.UUID, error) {
	var id *dbCluster.Identity
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = dbCluster.GetPendingTLSIdentityByTokenSecret(ctx, tx.Tx(), token.Secret)
		return err
	})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("Failed to find a matching pending identity: %w", err)
	}

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() {
		err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.DeleteIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier)
		})
		if err != nil {
			logger.Warn("Failed to delete invalid or expired pending TLS identity", logger.Ctx{"err": err, "identity_id": id.Identifier})
		}
	})

	metadata, err := id.PendingTLSMetadata()
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("Failed extracting pending TLS identity metadata: %w", err)
	}

	if !metadata.Expiry.IsZero() && metadata.Expiry.Before(time.Now()) {
		return uuid.UUID{}, api.StatusErrorf(http.StatusForbidden, "Token has expired")
	}

	uid, err := uuid.Parse(id.Identifier)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("Unexpected identifier format for pending TLS identity: %w", err)
	}

	reverter.Success()
	return uid, nil
}

// swagger:operation GET /1.0/auth/identities identities identities_get
//
//	Get the identities
//
//	Returns a list of identities (URLs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/auth/identities/tls/e1e06266e36f67431c996d5678e66d732dfd12fe5073c161e62e6360619fc226",
//	              "/1.0/auth/identities/oidc/auth0|4daf5e37ce230e455b64b65b"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities?recursion=1 identities identities_get_recursion1
//
//	Get the identities
//
//	Returns a list of identities.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of identities
//	          items:
//	            $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/bearer identities identities_get_bearer
//
//	Get the bearer identities
//
//	Returns a list of bearer identities (URLs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/auth/identities/bearer/my-identity",
//	              "/1.0/auth/identities/bearer/2040864b-df39-4267-a8e2-e55cde33601d"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/tls identities identities_get_tls
//
//	Get the TLS identities
//
//	Returns a list of TLS identities (URLs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/auth/identities/tls/e1e06266e36f67431c996d5678e66d732dfd12fe5073c161e62e6360619fc226",
//	              "/1.0/auth/identities/tls/6d5678e66d732dfd12fe5073c161eec9962e6360619fc2261e06266e36f67431"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/oidc identities identities_get_oidc
//
//	Get the OIDC identities
//
//	Returns a list of OIDC identities (URLs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/auth/identities/oidc/jane.doe@example.com",
//	              "/1.0/auth/identities/oidc/joe.bloggs@example.com"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/bearer?recursion=1 identities identities_get_bearer_recursion1
//
//	Get the bearer identities
//
//	Returns a list of bearer identities.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of identities
//	          items:
//	            $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/tls?recursion=1 identities identities_get_tls_recursion1
//
//	Get the TLS identities
//
//	Returns a list of TLS identities.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of identities
//	          items:
//	            $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/oidc?recursion=1 identities identities_get_oidc_recursion1
//
//	Get the OIDC identities
//
//	Returns a list of OIDC identities.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of identities
//	          items:
//	            $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identitiesGet(authenticationMethod string) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		recursion, _ := util.IsRecursionRequest(r)
		s := d.State()
		canViewIdentity, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeIdentity)
		if err != nil {
			return response.SmartError(err)
		}

		withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeIdentity, true)
		if err != nil {
			return response.SmartError(err)
		}

		canViewCertificate, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeCertificate)
		if err != nil {
			return response.SmartError(err)
		}

		canView := func(id dbCluster.Identity) bool {
			identityType, err := identity.New(string(id.Type))
			if err != nil {
				return false
			}

			if identityType.IsFineGrained() {
				return canViewIdentity(entity.IdentityURL(string(id.AuthMethod), id.Identifier))
			}

			return canViewCertificate(entity.CertificateURL(id.Identifier))
		}

		canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
		if err != nil {
			return response.SmartError(err)
		}

		var identities []dbCluster.Identity
		var groupsByIdentityID map[int][]dbCluster.AuthGroup
		var apiIdentity *api.Identity
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get all identities, filter by authentication method if present.
			var filters []dbCluster.IdentityFilter
			if authenticationMethod != "" {
				clusterAuthMethod := dbCluster.AuthMethod(authenticationMethod)
				filters = append(filters, dbCluster.IdentityFilter{AuthMethod: &clusterAuthMethod})
			}

			allIdentities, err := dbCluster.GetIdentitys(ctx, tx.Tx(), filters...)
			if err != nil {
				return err
			}

			// Filter results by what the user is allowed to view.
			for _, id := range allIdentities {
				if canView(id) {
					identities = append(identities, id)
				}
			}

			if len(identities) == 0 {
				return nil
			}

			if recursion > 0 && len(identities) == 1 {
				// If there is only one identity to return (either the caller can only view themselves, or there is only one identity in database)
				// we can optimise here by only getting the groups for that user. This sets the value of `apiIdentity`
				// which is to be returned if non-nil.
				apiIdentity, err = identities[0].ToAPI(ctx, tx.Tx(), canViewGroup)
				if err != nil {
					return err
				}
			} else if recursion > 0 {
				// Otherwise, get all groups and populate the identities outside of the transaction.
				// This optimisation prevents us from iterating through each identity and querying the database for the
				// groups of each identity in turn.
				groupsByIdentityID, err = dbCluster.GetAllAuthGroupsByIdentityIDs(ctx, tx.Tx())
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Optimisation for when only one identity is present on the system.
		if apiIdentity != nil {
			if len(withEntitlements) > 0 {
				err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeIdentity, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.IdentityURL(apiIdentity.AuthenticationMethod, apiIdentity.Identifier): apiIdentity})
				if err != nil {
					return response.SmartError(err)
				}
			}

			return response.SyncResponse(true, []api.Identity{*apiIdentity})
		}

		if recursion > 0 {
			// Convert the []cluster.Group in the groupsByIdentityID map to string slices of the group names.
			groupNamesByIdentityID := make(map[int][]string, len(groupsByIdentityID))
			for identityID, groups := range groupsByIdentityID {
				for _, group := range groups {
					if canViewGroup(entity.AuthGroupURL(group.Name)) {
						groupNamesByIdentityID[identityID] = append(groupNamesByIdentityID[identityID], group.Name)
					}
				}
			}

			apiIdentities := make([]*api.Identity, 0, len(identities))
			urlToIdentity := make(map[*api.URL]auth.EntitlementReporter, len(identities))
			for _, id := range identities {
				var certificate string
				identityType, err := identity.New(string(id.Type))
				if err != nil {
					return response.SmartError(err)
				}

				if id.AuthMethod == api.AuthenticationMethodTLS && !identityType.IsPending() {
					metadata, err := id.CertificateMetadata()
					if err != nil {
						return response.SmartError(err)
					}

					certificate = metadata.Certificate
				}

				identity := &api.Identity{
					AuthenticationMethod: string(id.AuthMethod),
					Type:                 string(id.Type),
					Identifier:           id.Identifier,
					Name:                 id.Name,
					Groups:               groupNamesByIdentityID[id.ID],
					TLSCertificate:       certificate,
				}

				apiIdentities = append(apiIdentities, identity)
				urlToIdentity[entity.IdentityURL(string(id.AuthMethod), id.Identifier)] = identity
			}

			if len(withEntitlements) > 0 {
				err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeIdentity, withEntitlements, urlToIdentity)
				if err != nil {
					return response.SmartError(err)
				}
			}

			return response.SyncResponse(true, apiIdentities)
		}

		urls := make([]string, 0, len(identities))
		for _, id := range identities {
			urls = append(urls, entity.IdentityURL(string(id.AuthMethod), id.Identifier).String())
		}

		return response.SyncResponse(true, urls)
	}
}

// swagger:operation GET /1.0/auth/identities/bearer/{nameOrIdentifier} identities identity_get_bearer
//
//	Get the bearer identity
//
//	Gets a specific bearer identity.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_get_tls
//
//	Get the TLS identity
//
//	Gets a specific TLS identity.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_get_oidc
//
//	Get the OIDC identity
//
//	Gets a specific OIDC identity.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Identity"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identityGet(d *Daemon, r *http.Request) response.Response {
	id, err := request.GetContextValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeIdentity, false)
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIdentity *api.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeIdentity, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.IdentityURL(string(id.AuthMethod), id.Identifier): apiIdentity})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, apiIdentity, apiIdentity)
}

// swagger:operation GET /1.0/auth/identities/current identities identity_get_current
//
//	Get the current identity
//
//	Gets the identity of the requestor, including contextual authorization information.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/IdentityInfo"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func identityGetCurrent(d *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	username := requestor.CallerUsername()
	if username == "" {
		return response.SmartError(errors.New("Failed to get identity identifier from request info"))
	}

	protocol := requestor.CallerProtocol()
	if protocol == "" {
		return response.SmartError(errors.New("Failed to get authentication method from request info"))
	}

	// Must be a remote API request.
	err = identity.ValidateAuthenticationMethod(protocol)
	if err != nil {
		return response.BadRequest(errors.New("Current identity information must be requested via the HTTPS API"))
	}

	s := d.State()
	var apiIdentity *api.Identity
	var effectiveGroups []string
	var effectivePermissions []api.Permission
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := dbCluster.GetIdentity(ctx, tx.Tx(), dbCluster.AuthMethod(requestor.CallerProtocol()), requestor.CallerUsername())
		if err != nil {
			return fmt.Errorf("Failed to get current identity from database: %w", err)
		}

		// Using a permission checker here is redundant, we know who the user is, and we know that they are allowed
		// to view the groups that they are a member of.
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), func(entityURL *api.URL) bool { return true })
		if err != nil {
			return fmt.Errorf("Failed to populate LXD groups: %w", err)
		}

		effectiveGroups = requestor.CallerEffectiveAuthorizationGroupNames()
		permissions, err := dbCluster.GetDistinctPermissionsByGroupNames(ctx, tx.Tx(), effectiveGroups)
		if err != nil {
			return fmt.Errorf("Failed to get effective permissions: %w", err)
		}

		permissions, entityURLs, err := dbCluster.GetPermissionEntityURLs(ctx, tx.Tx(), permissions)
		if err != nil {
			return fmt.Errorf("Failed to get entity URLs for effective permissions: %w", err)
		}

		effectivePermissions = make([]api.Permission, 0, len(permissions))
		for _, permission := range permissions {
			effectivePermissions = append(effectivePermissions, api.Permission{
				EntityType:      string(permission.EntityType),
				EntityReference: entityURLs[entity.Type(permission.EntityType)][permission.EntityID].String(),
				Entitlement:     string(permission.Entitlement),
			})
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	identityType, err := identity.New(apiIdentity.Type)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, api.IdentityInfo{
		Identity:             *apiIdentity,
		EffectiveGroups:      effectiveGroups,
		EffectivePermissions: effectivePermissions,
		FineGrained:          identityType.IsFineGrained(),
		ExpiresAt:            requestor.ExpiresAt(),
	})
}

// swagger:operation PUT /1.0/auth/identities/bearer/{nameOrIdentifier} identities identity_put_bearer
//
//	Update the bearer identity
//
//	Replaces the editable fields of a bearer identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation PUT /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_put_tls
//
//	Update the TLS identity
//
//	Replaces the editable fields of a TLS identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation PUT /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_put_oidc
//
//	Update the OIDC identity
//
//	Replaces the editable fields of an OIDC identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"
func identityPut(authenticationMethod string) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		id, err := addIdentityDetailsToContext(s, r, authenticationMethod)
		if err != nil {
			return response.SmartError(err)
		}

		identityType, err := identity.New(string(id.Type))
		if err != nil {
			return response.SmartError(err)
		}

		if !identityType.IsFineGrained() {
			return response.NotImplemented(fmt.Errorf("Identities of type %q cannot be modified via this API", id.Type))
		}

		var identityPut api.IdentityPut
		err = json.NewDecoder(r.Body).Decode(&identityPut)
		if err != nil {
			return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
		}

		if identityPut.TLSCertificate != "" && (identityType.AuthenticationMethod() != api.AuthenticationMethodTLS || identityType.IsPending()) {
			return response.BadRequest(fmt.Errorf("Cannot update certificate for identities of type %q", id.Type))
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.IdentityURL(authenticationMethod, id.Identifier), auth.EntitlementCanEdit)
		if err == nil {
			return updateIdentityPrivileged(s, r, *id, identityPut)
		} else if !auth.IsDeniedError(err) {
			return response.SmartError(err)
		}

		requestor, err := request.GetRequestor(r.Context())
		if err != nil {
			return response.SmartError(err)
		}

		// Identities may only update their own certificate
		if requestor.CallerUsername() != id.Identifier {
			return response.Forbidden(nil)
		}

		return updateSelfIdentityUnprivileged(s, r, *id, identityPut)
	}
}

// updateSelfIdentityUnprivileged is only invoked when an identity of type api.IdentityTypeClientCertificate updates their
// own identity and does not have permission to change their own groups.
func updateSelfIdentityUnprivileged(s *state.State, r *http.Request, id dbCluster.Identity, identityPut api.IdentityPut) response.Response {
	// Validate the given certificate
	fingerprint, metadata, err := validateIdentityCert(s.Endpoints.NetworkCert(), identityPut.TLSCertificate)
	if err != nil {
		return response.SmartError(err)
	}

	// We need to perform an ETag check. To do so we need to convert the DB Identity to an API identity and this
	// requires a permission checker on groups. We know that the caller is updating themselves and they are able to view
	// all groups that they are a member of, so we can return true for any url here.
	canViewGroup := func(entityURL *api.URL) bool { return true }

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		apiIdentity, err := id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIdentity)
		if err != nil {
			return err
		}

		// Return an error if the caller tries to update their own groups.
		if !slices.Equal(identityPut.Groups, apiIdentity.Groups) {
			return api.NewStatusError(http.StatusForbidden, "Only the certificate may be changed")
		}

		// We needed to start this transaction to check the ETag and the list of groups. However, the only property
		// that the unprivileged caller is allowed to update is the certificate. If the given certificate is identical
		// to the existing certificate there is no reason to perform the update and we can return without an error
		// (making the request idempotent).
		if fingerprint == id.Identifier {
			return nil
		}

		return dbCluster.UpdateIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier, dbCluster.Identity{
			AuthMethod: id.AuthMethod,
			Type:       id.Type,
			Identifier: fingerprint,
			Name:       id.Name,
			Metadata:   metadata,
		})
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notify := newIdentityNotificationFunc(s, r, s.Endpoints.NetworkCert(), s.ServerCert())
	_, err = notify(lifecycle.IdentityUpdated, string(id.AuthMethod), id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// updateIdentityPrivileged is called when the caller has `can_edit` on the identity. It must account for both OIDC and TLS identities.
func updateIdentityPrivileged(s *state.State, r *http.Request, id dbCluster.Identity, identityPut api.IdentityPut) response.Response {
	// Validate certificate if given (not present for OIDC or pending TLS identities).
	var fingerprint string
	var metadata string
	if identityPut.TLSCertificate != "" {
		var err error
		fingerprint, metadata, err = validateIdentityCert(s.Endpoints.NetworkCert(), identityPut.TLSCertificate)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// We need to perform an ETag check. To do so we need to convert the DB Identity to an API identity and this
	// requires a permission checker on groups.
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		apiIdentity, err := id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIdentity)
		if err != nil {
			return err
		}

		// Set the groups
		err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), id.ID, identityPut.Groups)
		if err != nil {
			return err
		}

		if identityPut.TLSCertificate == "" || fingerprint == id.Identifier {
			return nil
		}

		// Only update certificate if present and different to the existing one.
		return dbCluster.UpdateIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier, dbCluster.Identity{
			AuthMethod: id.AuthMethod,
			Type:       id.Type,
			Identifier: fingerprint,
			Name:       id.Name,
			Metadata:   metadata,
		})
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notify := newIdentityNotificationFunc(s, r, s.Endpoints.NetworkCert(), s.ServerCert())
	_, err = notify(lifecycle.IdentityUpdated, string(id.AuthMethod), id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/auth/identities/bearer/{nameOrIdentifier} identities identity_patch_bearer
//
//	Partially update the bearer identity
//
//	Updates the editable fields of a bearer identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation PATCH /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_patch_tls
//
//	Partially update the TLS identity
//
//	Updates the editable fields of a TLS identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation PATCH /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_patch_oidc
//
//	Partially update the OIDC identity
//
//	Updates the editable fields of an OIDC identity
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: identity
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/IdentityPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"
func identityPatch(authenticationMethod string) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		id, err := addIdentityDetailsToContext(s, r, authenticationMethod)
		if err != nil {
			return response.SmartError(err)
		}

		identityType, err := identity.New(string(id.Type))
		if err != nil {
			return response.SmartError(err)
		}

		if !identityType.IsFineGrained() {
			return response.NotImplemented(fmt.Errorf("Identities of type %q cannot be modified via this API", id.Type))
		}

		var identityPut api.IdentityPut
		err = json.NewDecoder(r.Body).Decode(&identityPut)
		if err != nil {
			return response.BadRequest(fmt.Errorf("Failed to unmarshal request body: %w", err))
		}

		if identityPut.TLSCertificate != "" && (identityType.AuthenticationMethod() != api.AuthenticationMethodTLS || identityType.IsPending()) {
			return response.BadRequest(fmt.Errorf("Cannot update certificate for identities of type %q", id.Type))
		}

		if len(identityPut.Groups) == 0 && identityPut.TLSCertificate == "" {
			// Nothing to do
			return response.EmptySyncResponse
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.IdentityURL(authenticationMethod, id.Identifier), auth.EntitlementCanEdit)
		if err == nil {
			return patchIdentityPrivileged(s, r, *id, identityPut)
		} else if !auth.IsDeniedError(err) {
			return response.SmartError(err)
		}

		requestor, err := request.GetRequestor(r.Context())
		if err != nil {
			return response.SmartError(err)
		}

		// Identities may only update their own certificate
		if requestor.CallerUsername() != id.Identifier {
			return response.Forbidden(nil)
		}

		return patchSelfIdentityUnprivileged(s, r, *id, identityPut)
	}
}

// patchIdentityPrivileged is invoked when the caller has `can_edit` on the identity. It must handle both OIDC and TLS identities.
func patchIdentityPrivileged(s *state.State, r *http.Request, id dbCluster.Identity, identityPut api.IdentityPut) response.Response {
	// Parse the certificate if given.
	var fingerprint string
	var metadata string
	if identityPut.TLSCertificate != "" {
		var err error
		fingerprint, metadata, err = validateIdentityCert(s.Endpoints.NetworkCert(), identityPut.TLSCertificate)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// We need to perform an ETag check. To do so we need to convert the DB Identity to an API identity and this
	// requires a permission checker on groups.
	canViewGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeAuthGroup)
	if err != nil {
		return response.SmartError(err)
	}

	var apiIdentity *api.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIdentity)
		if err != nil {
			return err
		}

		for _, groupName := range identityPut.Groups {
			if !slices.Contains(apiIdentity.Groups, groupName) {
				apiIdentity.Groups = append(apiIdentity.Groups, groupName)
			}
		}

		err = dbCluster.SetIdentityAuthGroups(ctx, tx.Tx(), id.ID, identityPut.Groups)
		if err != nil {
			return err
		}

		// Only update the certificate if it is given. Additionally, we don't need to update it if it's the same as the
		// existing one.
		if identityPut.TLSCertificate != "" && fingerprint != id.Identifier {
			return dbCluster.UpdateIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier, dbCluster.Identity{
				AuthMethod: id.AuthMethod,
				Type:       id.Type,
				Identifier: fingerprint,
				Name:       id.Name,
				Metadata:   metadata,
			})
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notify := newIdentityNotificationFunc(s, r, s.Endpoints.NetworkCert(), s.ServerCert())
	_, err = notify(lifecycle.IdentityUpdated, string(id.AuthMethod), id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// patchSelfIdentityUnprivileged is only invoked when an identity of type api.IdentityTypeClientCertificate updates their
// own identity and does not have permission to change their own groups.
func patchSelfIdentityUnprivileged(s *state.State, r *http.Request, id dbCluster.Identity, identityPut api.IdentityPut) response.Response {
	if len(identityPut.Groups) > 0 {
		return response.Forbidden(errors.New("Only the certificate may be changed"))
	}

	if identityPut.TLSCertificate == "" {
		// Can only edit the TLS certificate, if one wasn't provided there's nothing to do.
		return response.EmptySyncResponse
	}

	fingerprint, metadata, err := validateIdentityCert(s.Endpoints.NetworkCert(), identityPut.TLSCertificate)
	if err != nil {
		return response.SmartError(err)
	}

	if fingerprint == id.Identifier {
		// The only property that the unprivileged caller is allowed to update is the certificate. If the given
		// certificate is identical to the existing certificate there is no reason to perform the update and we can
		// return without an error (making the request idempotent).
		return response.EmptySyncResponse
	}

	// We need to perform an ETag check. To do so we need to convert the DB Identity to an API identity and this
	// requires a permission checker on groups. We know that the caller is updating themselves and they are able to view
	// all groups that they are a member of, so we can return true for any url here.
	canViewGroup := func(entityURL *api.URL) bool { return true }

	var apiIdentity *api.Identity
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		apiIdentity, err = id.ToAPI(ctx, tx.Tx(), canViewGroup)
		if err != nil {
			return err
		}

		err = util.EtagCheck(r, apiIdentity)
		if err != nil {
			return err
		}

		return dbCluster.UpdateIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier, dbCluster.Identity{
			AuthMethod: id.AuthMethod,
			Type:       id.Type,
			Identifier: fingerprint,
			Name:       id.Name,
			Metadata:   metadata,
		})
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notify := newIdentityNotificationFunc(s, r, s.Endpoints.NetworkCert(), s.ServerCert())
	_, err = notify(lifecycle.IdentityUpdated, string(id.AuthMethod), id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/auth/identities/bearer/{nameOrIdentifier} identities identity_delete_bearer
//
//	Delete the bearer identity
//
//	Removes the bearer identity.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation DELETE /1.0/auth/identities/tls/{nameOrIdentifier} identities identity_delete_tls
//
//	Delete the TLS identity
//
//	Removes the TLS identity and revokes trust.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"

// swagger:operation DELETE /1.0/auth/identities/oidc/{nameOrIdentifier} identities identity_delete_oidc
//
//	Delete the OIDC identity
//
//	Removes the OIDC identity.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
//	  "501":
//	    $ref: "#/responses/NotImplemented"
func identityDelete(d *Daemon, r *http.Request) response.Response {
	id, err := request.GetContextValue[*dbCluster.Identity](r.Context(), ctxClusterDBIdentity)
	if err != nil {
		return response.SmartError(err)
	}

	identityType, err := identity.New(string(id.Type))
	if err != nil {
		return response.SmartError(err)
	}

	if !identityType.IsFineGrained() && identityType.Name() != api.IdentityTypeBearerTokenInitialUI {
		return response.NotImplemented(fmt.Errorf("Identities of type %q cannot be modified via this API", id.Type))
	}

	s := d.State()
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteIdentity(ctx, tx.Tx(), id.AuthMethod, id.Identifier)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Notify other cluster members to update their identity cache.
	notify := newIdentityNotificationFunc(s, r, s.Endpoints.NetworkCert(), s.ServerCert())
	_, err = notify(lifecycle.IdentityDeleted, string(id.AuthMethod), id.Identifier, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// newIdentityNotificationFunc returns a function that creates and sends a lifecycle event for the identity.
// If updateCache is true, the local identity cache is updated and a notification is sent to other members to do the same.
func newIdentityNotificationFunc(s *state.State, r *http.Request, networkCert *shared.CertInfo, serverCert *shared.CertInfo) identityNotificationFunc {
	return func(action lifecycle.IdentityAction, authenticationMethod string, identifier string, updateCache bool) (*api.EventLifecycle, error) {
		if updateCache {
			// Send a notification to other cluster members to refresh their identity cache.
			notifier, err := cluster.NewNotifier(s, networkCert, serverCert, cluster.NotifyAlive)
			if err != nil {
				return nil, err
			}

			err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
				_, _, err := client.RawQuery(http.MethodPost, "/internal/identity-cache-refresh", nil, "")
				return err
			})
			if err != nil {
				return nil, err
			}

			// Reload the identity cache to add the new certificate.
			s.UpdateIdentityCache()
		}

		lc := action.Event(authenticationMethod, identifier, request.CreateRequestor(r.Context()), nil)
		s.Events.SendLifecycle("", lc)

		return &lc, nil
	}
}

// validateIdentityCert validates the certificate and returns the fingerprint and dbCluster.CertificateMetadata for the
// identity encoded as JSON.
func validateIdentityCert(networkCert *shared.CertInfo, cert string) (fingerprint string, metadataJSON string, err error) {
	if cert == "" {
		return "", "", api.NewStatusError(http.StatusBadRequest, "Must provide a certificate")
	}

	x509Cert, err := shared.ParseCert([]byte(cert))
	if err != nil {
		return "", "", api.StatusErrorf(http.StatusBadRequest, "Failed to parse certificate: %w", err)
	}

	err = certificateValidate(networkCert, x509Cert)
	if err != nil {
		return "", "", fmt.Errorf("Invalid certificate: %w", err)
	}

	b, err := json.Marshal(dbCluster.CertificateMetadata{Certificate: cert})
	if err != nil {
		return "", "", fmt.Errorf("Failed to encode certificate metadata: %w", err)
	}

	return shared.CertFingerprint(x509Cert), string(b), nil
}

// updateIdentityCache reads all identities from the database and sets them in the identity.Cache.
// The certificates in the local database are replaced with identities in the cluster database that
// are of type api.IdentityTypeCertificateServer. This ensures that this cluster member is able to
// trust other cluster members on restart.
func updateIdentityCache(d *Daemon) {
	s := d.State()

	logger.Debug("Refreshing identity cache")

	var identities []dbCluster.Identity
	bearerIdentitySecrets := make(map[int]dbCluster.AuthSecretValue)
	var err error
	err = s.DB.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		identities, err = dbCluster.GetIdentitys(ctx, tx.Tx())
		if err != nil {
			return err
		}

		bearerIdentitySecrets, err = dbCluster.GetAllBearerIdentitySigningKeys(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed reading identities from global database", logger.Ctx{"err": err})
		return
	}

	serverCerts := make(map[string]*x509.Certificate)
	clientCerts := make(map[string]*x509.Certificate)
	metricsCerts := make(map[string]*x509.Certificate)
	secrets := make(map[string][]byte)
	var initialUITokenSecret []byte
	var localServerCerts []dbCluster.Certificate
	for _, id := range identities {
		identityType, err := identity.New(string(id.Type))
		if err != nil {
			logger.Warn("Failed to create identity type", logger.Ctx{"type": string(id.Type), "err": err})
			continue
		}

		if identityType.IsPending() {
			continue
		}

		if identityType.AuthenticationMethod() == api.AuthenticationMethodTLS {
			cert, err := id.X509()
			if err != nil {
				logger.Warn("Failed to extract x509 certificate from TLS identity metadata", logger.Ctx{"err": err})
				continue
			}

			legacyCertType, _ := identityType.LegacyCertificateType()
			switch legacyCertType {
			case certificate.TypeMetrics:
				metricsCerts[id.Identifier] = cert
			case certificate.TypeServer:
				dbCert, err := id.ToCertificate()
				if err != nil {
					logger.Warn("Failed to convert TLS identity to server certificate", logger.Ctx{"err": err})
					continue
				}

				// Add server cert to local backup to allow cluster startup.
				localServerCerts = append(localServerCerts, *dbCert)
				serverCerts[id.Identifier] = cert
			default:
				clientCerts[id.Identifier] = cert
			}
		} else if identityType.AuthenticationMethod() == api.AuthenticationMethodBearer {
			secret, ok := bearerIdentitySecrets[id.ID]
			if !ok {
				// No need to add bearer identities with no secret to the cache, they cannot authenticate.
				continue
			}

			if identityType.Name() == api.IdentityTypeBearerTokenInitialUI {
				initialUITokenSecret = secret
				continue
			}

			secrets[id.Identifier] = secret
		}
	}

	// Write out the server certs to the local database to allow the cluster to restart.
	err = s.DB.Node.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceCertificates(localServerCerts)
	})
	if err != nil {
		logger.Warn("Failed writing certificates to local database", logger.Ctx{"err": err})
		// Don't return here, as we still should update the in-memory cache to allow the cluster to
		// continue functioning, and hopefully the write will succeed on next update.
	}

	d.identityCache.ReplaceAll(serverCerts, clientCerts, metricsCerts, secrets, initialUITokenSecret)
}

// updateIdentityCacheFromLocal loads trusted server certificates from local database into the identity cache.
func updateIdentityCacheFromLocal(d *Daemon) error {
	logger.Debug("Refreshing identity cache with local trusted certificates")

	var localServerCerts []dbCluster.Certificate
	var err error

	err = d.State().DB.Node.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.NodeTx) error {
		localServerCerts, err = tx.GetCertificates(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed reading certificates from local database: %w", err)
	}

	// identityCacheEntries needs to be pre-allocated.
	serverCerts := make(map[string]*x509.Certificate)
	for _, dbCert := range localServerCerts {
		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", logger.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		serverCerts[dbCert.Fingerprint] = cert
	}

	d.identityCache.ReplaceAll(serverCerts, nil, nil, nil, nil)
	return nil
}
