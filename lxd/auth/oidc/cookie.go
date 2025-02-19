package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/db/cluster/secret"
)

const (
	// cookieNameLXDSession is the identifier used to set and retrieve session information.
	cookieNameLXDSession = "lxd_session"

	// defaultNotBefore is added to time.Now and used as the "nbf" claim.
	// A token is not valid if it is used before this time.
	// It is used to allow for some time skew between cluster members.
	defaultNotBefore = 5 * time.Minute
)

// newSessionCookie returns a new cookie for the session.
func newSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameLXDSession,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// getSessionCookie gets the session cookie.
func getSessionCookie(r *http.Request) (sessionCookieJWT string, err error) {
	sessionCookie, err := r.Cookie(cookieNameLXDSession)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return "", fmt.Errorf("Failed to get session ID cookie from request: %w", err)
	} else if sessionCookie == nil {
		return "", nil
	}

	return sessionCookie.Value, nil
}

// deleteSessionCookie sets a session cookie on the request with expiry set to zero.
// This tells the browser or persistent cookie jar to delete it.
func deleteSessionCookie(w http.ResponseWriter) {
	cookie := newSessionCookie()
	cookie.Expires = time.Unix(0, 0)
	http.SetCookie(w, cookie)
}

// setSessionCookie creates a JWT and signs it with a key that is unique per-session.
func setSessionCookie(ctx context.Context, w http.ResponseWriter, clusterSecretFunc func(context.Context) (secret.Secret, bool, error), clusterCertFingerprint func() string, sessionID uuid.UUID, lifetime time.Duration) error {
	sessionKey, err := sessionKey(ctx, clusterSecretFunc, sessionID)
	if err != nil {
		return err
	}

	// We are the issuer and we are the audience, we'll verify that the issuer and audience match out when we receive
	// this cookie again.
	issAndAud := "lxd:" + clusterCertFingerprint()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.RegisteredClaims{
		Issuer: issAndAud,
		// Subject is set to the session ID. This is not OIDC and doesn't have to be a stable identifier. It is only for our use.
		Subject:  sessionID.String(),
		Audience: jwt.ClaimStrings{issAndAud},
		// Set the lifetime.
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(lifetime)),
		// Not before used in case of time-skew between members.
		NotBefore: jwt.NewNumericDate(time.Now().Add(defaultNotBefore)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}).SignedString(sessionKey)
	if err != nil {
		return fmt.Errorf("Failed to sign JWT: %w", err)
	}

	cookie := newSessionCookie()
	// Max age must be set for persistent cookie jar to save it.
	cookie.MaxAge = int(lifetime.Seconds())
	cookie.Value = token
	http.SetCookie(w, cookie)
	return nil
}
