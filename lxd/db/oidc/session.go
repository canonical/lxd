package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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
func NewSessionHandler(db *db.Cluster, events *events.Server) oidc.SessionHandler {
	return &sessionHandler{db: db, events: events}
}

type sessionHandler struct {
	db     *db.Cluster
	events *events.Server
}

// StartSession sets an oidc.AuthenticationResult in the database. This contains information about the identity and session
// information. If the identity exists, its metadata is updated.
func (s *sessionHandler) StartSession(ctx context.Context, r *http.Request, res oidc.AuthenticationResult) error {
	return s.db.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		sqltx := tx.Tx()
		exists, err := cluster.IdentityExists(ctx, sqltx, api.AuthenticationMethodOIDC, res.Email)
		if err != nil {
			return err
		}

		var action lifecycle.IdentityAction

		metadata := cluster.OIDCMetadata{
			Subject:           res.Subject,
			SessionID:         res.SessionID.String(),
			RefreshToken:      res.RefreshToken,
			IDPGroups:         res.IdentityProviderGroups,
			SessionTerminated: false,
		}

		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("Failed to encode OIDC metadata: %w", err)
		}

		if exists {
			action = lifecycle.IdentityUpdated
			err = cluster.UpdateIdentity(ctx, sqltx, api.AuthenticationMethodOIDC, res.Email, cluster.Identity{
				AuthMethod: api.AuthenticationMethodOIDC,
				Type:       api.IdentityTypeOIDCClient,
				Identifier: res.Email,
				Name:       res.Name,
				Metadata:   string(metadataJSON),
			})
			if err != nil {
				return fmt.Errorf("Failed to update user session information: %w", err)
			}
		} else {
			action = lifecycle.IdentityCreated
			_, err = cluster.CreateIdentity(ctx, sqltx, cluster.Identity{
				AuthMethod: api.AuthenticationMethodOIDC,
				Type:       api.IdentityTypeOIDCClient,
				Identifier: res.Email,
				Name:       res.Name,
				Metadata:   string(metadataJSON),
			})
			if err != nil {
				return fmt.Errorf("Failed to create new identity with session information: %w", err)
			}
		}

		lc := action.Event(api.AuthenticationMethodOIDC, res.Email, request.CreateRequestor(r), nil)
		s.events.SendLifecycle(api.ProjectDefaultName, lc)
		return nil
	})
}

// GetIdentityBySessionID gets an api.IdentityInfo corresponding to the session ID. This should be set in the request context
// so that the auth.Authorizer can use the api.IdentityInfo.EffectiveGroups to determine access.
func (s *sessionHandler) GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (*api.IdentityInfo, bool, string, error) {
	var info *api.IdentityInfo
	var sessionTerminated bool
	var refreshToken string
	err := s.db.Transaction(ctx, func(ctx context.Context, clusterTx *db.ClusterTx) error {
		tx := clusterTx.Tx()
		id, err := cluster.GetOIDCIdentityBySessionID(ctx, tx, sessionID)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				sessionTerminated = true
			}

			return err
		}

		info, sessionTerminated, refreshToken, err = id.ToAPIInfo(ctx, tx, nil)
		return err
	})
	if err != nil {
		return nil, false, "", err
	}

	return info, sessionTerminated, refreshToken, nil
}
