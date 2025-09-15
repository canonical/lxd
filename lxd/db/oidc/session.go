package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"
	zitadelOIDC "github.com/zitadel/oidc/v3/pkg/oidc"
	"golang.org/x/oauth2"

	"github.com/canonical/lxd/lxd/auth/oidc"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// NewSessionHandler returns a new [oidc.SessionHandler]. The getSessionExpiry function must return the current value
// of oidc.session.expiry for the server configuration.
func NewSessionHandler(db *db.Cluster, events *events.Server, getSessionExpiry func() string) oidc.SessionHandler {
	return &sessionHandler{db: db, events: events, expiryFunc: getSessionExpiry}
}

type sessionHandler struct {
	db         *db.Cluster
	events     *events.Server
	expiryFunc func() string
}

// StartSession starts a new session for the identity with the email given in the [oidc.AuthenticationResult].
// For first time logins, a new identity is created. If the identity already exists, the identity metadata will be updated
// to match the new values from the IdP. A session entry will be created in the database using details extracted from the
// given [http.Request] and tokens.
//
// The [zitadelOIDC.Tokens] may be nil, this is when the client is authenticating with the CLI and their access tokens are stored locally.
//
// A [time.Time] can be provided to override the expiry of the session. This is used when a session is being refreshed to use
// a newer cluster secret.
func (s *sessionHandler) StartSession(r *http.Request, res oidc.AuthenticationResult, tokens *zitadelOIDC.Tokens[*zitadelOIDC.IDTokenClaims], expiryOverride *time.Time) (*uuid.UUID, *time.Time, error) {
	// Get a new session UUID. This is a v7 UUID from which we can extract the session creation date.
	sessionID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create new session UUID: %w", err)
	}

	// Get the session expiry. Use the override, else get from config.
	var expiry time.Time
	if expiryOverride != nil {
		expiry = *expiryOverride
	} else {
		expiryConfig := s.expiryFunc()
		expiry, err = shared.GetExpiry(time.Now().UTC(), expiryConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to get session expiry: %w", err)
		}
	}

	// Extract the IP address to store in the session.
	remoteAddr, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to parse remote address: %w", err)
	}

	// New metadata to be saved if necessary. This includes the identity provider groups, which are per-identity and not
	// per-session.
	newMetadata := cluster.OIDCMetadata{
		Subject:                res.Subject,
		IdentityProviderGroups: res.IdentityProviderGroups,
	}

	var action lifecycle.IdentityAction
	err = s.db.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the identity from their email address. If none is found, it's a first time login.
		var firstTimeLogin bool
		identity, err := cluster.GetIdentity(ctx, tx.Tx(), api.AuthenticationMethodOIDC, res.Email)
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusNotFound) {
				return fmt.Errorf("Failed to check if the identity exists: %w", err)
			}

			firstTimeLogin = true
		}

		// Check if we need to update the identity metadata
		var doUpdateIdentity bool
		if !firstTimeLogin {
			existingMetadata, err := identity.OIDCMetadata()
			if err != nil {
				return fmt.Errorf("Failed to get OIDC metadata: %w", err)
			}

			if newMetadata.Subject != existingMetadata.Subject {
				// We have historically allowed the IdP subject for a user with a given email address to change.
				// This was with the view that the end user may authenticate to the IdP with a different mechanism (such
				// as social login) and should still have the same permissions.
				//
				// This is dangerous because it means that permissions are tied to an email address and not who the IdP
				// considers to be a unique user. Consider that in a large organisation, it's possible for an email address
				// to be re-used (someone joins the org with the same name as someone that has already left the org).
				//
				// So we will instead restrict changes to the IdP subject. The consequence is that email address/subject
				// conflicts will need to be resolved by an administrator. If there does turn out to be a problem with
				// social login, we may need a mechanism to "join accounts" - but we can approach it from a secure position.
				logger.Warn("Encountered new OIDC login unexpected subject. The existing identity with this email must be deleted to allow the new login", logger.Ctx{"email": res.Email, "subject": res.Subject})

				// Return a generic error so as not to reveal that a user with this email already exists.
				return api.NewGenericStatusError(http.StatusInternalServerError)
			}

			// Allow updates to the identity name, or to the identity provider groups.
			doUpdateIdentity = res.Name != identity.Name || !existingMetadata.Equals(newMetadata)
		}

		// If we're creating or updating the identity, create the db representation.
		var newOrUpdatedIdentity cluster.Identity
		if firstTimeLogin || doUpdateIdentity {
			metadataJSON, err := json.Marshal(newMetadata)
			if err != nil {
				return fmt.Errorf("Failed to encode OIDC metadata: %w", err)
			}

			newOrUpdatedIdentity = cluster.Identity{
				AuthMethod: api.AuthenticationMethodOIDC,
				Type:       api.IdentityTypeOIDCClient,
				Identifier: res.Email,
				Name:       res.Name,
				Metadata:   string(metadataJSON),
			}
		}

		// Create or update the identity and get the identity ID for creating the session.
		var identityID int
		if firstTimeLogin {
			action = lifecycle.IdentityCreated
			identityID64, err := cluster.CreateIdentity(ctx, tx.Tx(), newOrUpdatedIdentity)
			if err != nil {
				return fmt.Errorf("Failed to create new identity with session information: %w", err)
			}

			identityID = int(identityID64)
		} else {
			identityID = identity.ID

			if doUpdateIdentity {
				action = lifecycle.IdentityUpdated
				err = cluster.UpdateIdentity(ctx, tx.Tx(), api.AuthenticationMethodOIDC, res.Email, newOrUpdatedIdentity)
				if err != nil {
					return fmt.Errorf("Failed to update user session information: %w", err)
				}
			}
		}

		// Create the session.

		// Tokens can be nil if creating a session for a CLI user (who stores their access token locally).
		var idToken, accessToken, refreshToken string
		if tokens != nil {
			idToken = tokens.IDToken
			accessToken = tokens.AccessToken
			refreshToken = tokens.RefreshToken
		}

		return cluster.CreateOIDCSession(ctx, tx.Tx(), cluster.OIDCSession{
			UUID:         sessionID,
			IdentityID:   identityID,
			IDToken:      idToken,
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			IP:           remoteAddr.Addr().String(),
			UserAgent:    r.UserAgent(),
			ExpiryDate:   expiry,
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to start session: %w", err)
	}

	// Send lifecycle event.
	if action != "" {
		lc := action.Event(api.AuthenticationMethodOIDC, res.Email, request.CreateRequestor(r.Context()), nil)
		s.events.SendLifecycle("", lc)
	}

	return &sessionID, &expiry, nil
}

// GetIdentityBySessionID gets an [oidc.AuthenticationResult], a set of [zitadelOIDC.Tokens], and an expiry [time.Time]
// for session based from the given [uuid.UUID] by joining the identities table and extracting the current [cluster.OIDCMetadata] for
// the [cluster.Identity] that holds the session.
func (s *sessionHandler) GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (res *oidc.AuthenticationResult, tokens *zitadelOIDC.Tokens[*zitadelOIDC.IDTokenClaims], sessionExpiry *time.Time, err error) {
	var identity *cluster.Identity
	var session *cluster.OIDCSession
	err = s.db.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		identity, session, err = cluster.GetIdentityAndSessionDetailsFromSessionID(ctx, tx.Tx(), sessionID)
		return err
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed to get session details: %w", err)
	}

	metadata, err := identity.OIDCMetadata()
	if err != nil {
		return nil, nil, nil, err
	}

	return &oidc.AuthenticationResult{
			IdentityType:           api.IdentityTypeOIDCClient,
			Subject:                metadata.Subject,
			Email:                  identity.Identifier,
			Name:                   identity.Name,
			IdentityProviderGroups: metadata.IdentityProviderGroups,
		}, &zitadelOIDC.Tokens[*zitadelOIDC.IDTokenClaims]{
			Token: &oauth2.Token{
				RefreshToken: session.RefreshToken,
				AccessToken:  session.AccessToken,
			},
			IDToken: session.IDToken,
		}, &session.ExpiryDate, nil
}

// DeleteSession deletes a single OIDC session by its ID.
func (s *sessionHandler) DeleteSession(ctx context.Context, sessionID uuid.UUID) error {
	return s.db.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteOIDCSessionByUUID(ctx, tx.Tx(), sessionID)
	})
}
