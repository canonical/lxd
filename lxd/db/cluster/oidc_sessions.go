package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// OIDCSession represents an OIDC session.
type OIDCSession struct {
	ID           int
	UUID         uuid.UUID
	IdentityID   int
	Email        string
	Username     string
	IDToken      string
	AccessToken  string
	RefreshToken string
	IP           string
	UserAgent    string
	ExpiryDate   time.Time
}

// ToAPI returns an [api.OIDCSession] from the [OIDCSession].
func (s OIDCSession) ToAPI() api.OIDCSession {
	sec, nsec := s.UUID.Time().UnixTime()
	creationDate := time.Unix(sec, nsec)

	return api.OIDCSession{
		UUID:      s.UUID.String(),
		Email:     s.Email,
		Username:  s.Username,
		IP:        s.IP,
		UserAgent: s.UserAgent,
		ExpiresAt: s.ExpiryDate,
		CreatedAt: creationDate,
	}
}

const allSessionsQuery = `SELECT 
	oidc_sessions.id, 
	oidc_sessions.uuid,
	identities.id,
	identities.identifier, 
	identities.name, 
	oidc_sessions.id_token,
	oidc_sessions.access_token,
	oidc_sessions.refresh_token, 
	oidc_sessions.ip, 
	oidc_sessions.user_agent, 
	oidc_sessions.expiry_date
FROM oidc_sessions 
	JOIN identities ON oidc_sessions.identity_id = identities.id
`

// GetAllOIDCSessions gets all OIDC sessions.
func GetAllOIDCSessions(ctx context.Context, tx *sql.Tx) ([]OIDCSession, error) {
	return getOIDCSessions(ctx, tx, allSessionsQuery)
}

// GetOIDCSessionsByEmail gets all sessions for the identity with the given email.
func GetOIDCSessionsByEmail(ctx context.Context, tx *sql.Tx, email string) ([]OIDCSession, error) {
	q := allSessionsQuery + `WHERE identities.identifier = ? AND identities.auth_method = ? AND identities.type = ?`
	return getOIDCSessions(ctx, tx, q, email, authMethodOIDC, IdentityType(api.IdentityTypeOIDCClient))
}

// GetOIDCSessionByUUID gets a session by UUID.
func GetOIDCSessionByUUID(ctx context.Context, tx *sql.Tx, uuid uuid.UUID) (*OIDCSession, error) {
	q := allSessionsQuery + `WHERE uuid = ?`
	sessions, err := getOIDCSessions(ctx, tx, q, uuid.String())
	if err != nil {
		return nil, err
	}

	if len(sessions) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Session not found")
	}

	return &sessions[0], nil
}

func getOIDCSessions(ctx context.Context, tx *sql.Tx, stmt string, args ...any) ([]OIDCSession, error) {
	var sessions []OIDCSession
	rowFunc := func(scan func(dest ...any) error) error {
		var session OIDCSession
		err := scan(&session.ID, &session.UUID, &session.IdentityID, &session.Email, &session.Username, &session.IDToken, &session.AccessToken, &session.RefreshToken, &session.IP, &session.UserAgent, &session.ExpiryDate)
		if err != nil {
			return err
		}

		sessions = append(sessions, session)
		return nil
	}

	err := query.Scan(ctx, tx, stmt, rowFunc, args...)
	if err != nil {
		return nil, err
	}

	return sessions, nil
}

// getIdentityAndSessionDetailsFromSessionID is prepared as this is used on the fast path when authenticating OIDC identities.
var getIdentityAndSessionDetailsFromSessionID = RegisterStmt(`SELECT 
	identities.id, 
	identities.auth_method, 
	identities.type, 
	identities.identifier, 
	identities.name, 
	identities.metadata,
	oidc_sessions.id,
	oidc_sessions.id_token,
	oidc_sessions.access_token,
	oidc_sessions.refresh_token, 
	oidc_sessions.ip, 
	oidc_sessions.user_agent, 
	oidc_sessions.expiry_date
FROM oidc_sessions
	JOIN identities ON oidc_sessions.identity_id = identities.id
WHERE oidc_sessions.uuid = ?
`)

// GetIdentityAndSessionDetailsFromSessionID gets both the [Identity] and the [OIDCSession] from the given session ID.
func GetIdentityAndSessionDetailsFromSessionID(ctx context.Context, tx *sql.Tx, sessionID uuid.UUID) (*Identity, *OIDCSession, error) {
	stmt, err := Stmt(tx, getIdentityAndSessionDetailsFromSessionID)
	if err != nil {
		return nil, nil, fmt.Errorf(`Failed to get "getIdentityAndSessionDetailsFromSessionID" prepared statement: %w`, err)
	}

	var identity Identity
	var session OIDCSession
	row := stmt.QueryRowContext(ctx, sessionID.String())
	err = row.Scan(&identity.ID, &identity.AuthMethod, &identity.Type, &identity.Identifier, &identity.Name, &identity.Metadata, &session.ID, &session.IDToken, &session.AccessToken, &session.RefreshToken, &session.IP, &session.UserAgent, &session.ExpiryDate)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, api.StatusErrorf(http.StatusNotFound, "Session not found")
		}

		return nil, nil, fmt.Errorf("Failed to get session details: %w", err)
	}

	session.IdentityID = identity.ID
	session.Email = identity.Identifier
	session.Username = identity.Name

	return &identity, &session, nil
}

// CreateOIDCSession creates an OIDC session.
func CreateOIDCSession(ctx context.Context, tx *sql.Tx, session OIDCSession) error {
	q := `INSERT INTO oidc_sessions (identity_id, uuid, id_token, access_token, refresh_token, ip, user_agent, expiry_date) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := tx.ExecContext(ctx, q, session.IdentityID, session.UUID, session.IDToken, session.AccessToken, session.RefreshToken, session.IP, session.UserAgent, session.ExpiryDate)
	if err != nil {
		return fmt.Errorf("Failed to write session data: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to verify session data write: %w", err)
	}

	if rowsAffected != 1 {
		return fmt.Errorf("Failed to write session data: Expected to write 1 row, but wrote %d rows", rowsAffected)
	}

	return nil
}

// DeleteOIDCSessionByUUID deletes a session by UUID.
func DeleteOIDCSessionByUUID(ctx context.Context, tx *sql.Tx, sessionID uuid.UUID) error {
	q := `DELETE FROM oidc_sessions WHERE uuid = ?`
	res, err := tx.ExecContext(ctx, q, sessionID.String())
	if err != nil {
		return err
	}

	nRows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if nRows == 0 {
		return api.StatusErrorf(http.StatusNotFound, "No session found with UUID %q", sessionID.String())
	}

	return nil
}
