package oidc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/auth/oidc"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// NewSessionHandler returns a new oidc.SessionHandler.
func NewSessionHandler(db *db.Cluster, events *events.Server, getSessionExpiry func() (time.Time, error)) oidc.SessionHandler {
	return &sessionHandler{db: db, events: events, expiryFunc: getSessionExpiry}
}

type sessionHandler struct {
	db         *db.Cluster
	events     *events.Server
	expiryFunc func() (time.Time, error)
}

// StartSession sets an oidc.AuthenticationResult in the database. This contains information about the identity and session
// information. If the identity exists, its metadata is updated.
func (s *sessionHandler) StartSession(r *http.Request, res oidc.AuthenticationResult, idToken string, accessToken string, refreshToken string) (*uuid.UUID, *time.Time, error) {
	sessionID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create new session UUID: %w", err)
	}

	expiresAt, err := s.expiryFunc()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to get session expiry information: %w", err)
	}

	addrport, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to parse remote address: %w", err)
	}

	newMetadata := cluster.OIDCMetadata{
		Subject:                res.Subject,
		IdentityProviderGroups: res.IdentityProviderGroups,
	}

	var action lifecycle.IdentityAction
	err = s.db.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var firstTimeLogin bool
		identity, err := cluster.GetIdentity(ctx, tx.Tx(), api.AuthenticationMethodOIDC, res.Email)
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusNotFound) {
				return err
			}

			firstTimeLogin = true
		}

		// Check if we need to update the identity metadata
		var doUpdateMetadata bool
		if !firstTimeLogin {
			existingMetadata, err := identity.OIDCMetadata()
			if err != nil {
				return err
			}

			doUpdateMetadata = res.Name != identity.Name || !existingMetadata.Equals(newMetadata)
		}

		// If we're creating updating the identity, create the db representation
		var newOrUpdatedIdentity cluster.Identity
		if firstTimeLogin || doUpdateMetadata {
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

			if doUpdateMetadata {
				action = lifecycle.IdentityUpdated

				err = cluster.UpdateIdentity(ctx, tx.Tx(), api.AuthenticationMethodOIDC, res.Email, newOrUpdatedIdentity)
				if err != nil {
					return fmt.Errorf("Failed to update user session information: %w", err)
				}
			}
		}

		return cluster.CreateOIDCSession(ctx, tx.Tx(), cluster.OIDCSession{
			UUID:         sessionID.String(),
			IdentityID:   identityID,
			IDToken:      idToken,
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			IP:           addrport.Addr().String(),
			UserAgent:    r.UserAgent(),
			ExpiryDate:   expiresAt,
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to start session: %w", err)
	}

	if action != "" {
		lc := action.Event(api.AuthenticationMethodOIDC, res.Email, request.CreateRequestor(r.Context()), nil)
		s.events.SendLifecycle("", lc)
	}

	return &sessionID, &expiresAt, nil
}

// GetIdentityBySessionID gets an api.IdentityInfo corresponding to the session ID. This should be set in the request context
// so that the auth.Authorizer can use the api.IdentityInfo.EffectiveGroups to determine access.
func (s *sessionHandler) GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (res *oidc.AuthenticationResult, idToken string, accessToken string, refreshToken string, err error) {
	var identity *cluster.Identity
	var session *cluster.OIDCSession
	err = s.db.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		identity, session, err = cluster.GetIdentityAndSessionDetailsFromSessionID(ctx, tx.Tx(), sessionID)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", "", "", api.NewStatusError(http.StatusNotFound, "Session not found")
		}

		return nil, "", "", "", fmt.Errorf("Failed to get session details: %w", err)
	}

	metadata, err := identity.OIDCMetadata()
	if err != nil {
		return nil, "", "", "", err
	}

	return &oidc.AuthenticationResult{
		IdentityType:           api.IdentityTypeOIDCClient,
		Subject:                metadata.Subject,
		Email:                  identity.Identifier,
		Name:                   identity.Name,
		IdentityProviderGroups: metadata.IdentityProviderGroups,
	}, session.IDToken, session.AccessToken, session.RefreshToken, nil
}
