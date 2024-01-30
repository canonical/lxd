package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zitadel/oidc/v2/pkg/client"
	"github.com/zitadel/oidc/v2/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v2/pkg/http"
	"github.com/zitadel/oidc/v2/pkg/oidc"
	"github.com/zitadel/oidc/v2/pkg/op"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
)

const (
	// cookieNameIDToken is the identifier used to set and retrieve the identity token.
	cookieNameIDToken = "oidc_identity"

	// cookieNameRefreshToken is the identifier used to set and retrieve the refresh token.
	cookieNameRefreshToken = "oidc_refresh"

	// cookieNameSessionID is used to identify the session. It does not need to be encrypted.
	cookieNameSessionID = "session_id"
)

const (
	defaultConfigExpiryInterval = 5 * time.Minute
)

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	accessTokenVerifier op.AccessTokenVerifier
	relyingParty        rp.RelyingParty

	clientID  string
	issuer    string
	audience  string
	cookieKey []byte

	// host is used for setting a valid callback URL when setting the relyingParty.
	// When creating the relyingParty, the OIDC library performs discovery (e.g. it calls the /well-known/oidc-configuration endpoint).
	// We don't want to perform this on every request, so we only do it when the request host changes.
	host string
}

// AuthError represents an authentication error.
type AuthError struct {
	Err error
}

// Error implements the error interface for AuthError.
func (e AuthError) Error() string {
	return fmt.Sprintf("Failed to authenticate: %s", e.Err.Error())
}

// Unwrap implements the xerrors.Wrapper interface for AuthError.
func (e AuthError) Unwrap() error {
	return e.Err
}

// Auth extracts OIDC tokens from the request, verifies them, and returns the subject.
func (o *Verifier) Auth(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, error) {
	err := o.ensureConfig(r.Host)
	if err != nil {
		return "", fmt.Errorf("Authorization failed: %w", err)
	}

	authorizationHeader := r.Header.Get("Authorization")

	idToken, refreshToken, err := o.getCookies(r)
	if err != nil {
		// Cookies are present but we failed to decrypt them. They may have been tampered with, so delete them to force
		// the user to log in again.
		_ = o.setCookies(w, "", "", true)
		return "", fmt.Errorf("Failed to retrieve login information: %w", err)
	}

	if authorizationHeader != "" {
		// When a command line client wants to authenticate, it needs to set the Authorization HTTP header like this:
		//    Authorization Bearer <access_token>
		parts := strings.Split(authorizationHeader, "Bearer ")
		if len(parts) != 2 {
			return "", &AuthError{fmt.Errorf("Bad authorization token, expected a Bearer token")}
		}

		// Bearer tokens should always be access tokens.
		return o.authenticateAccessToken(ctx, parts[1])
	} else if idToken != "" || refreshToken != "" {
		// When authenticating via the UI, we expect that there will be ID and refresh tokens present in the request cookies.
		return o.authenticateIDToken(ctx, w, idToken, refreshToken)
	}

	return "", AuthError{Err: errors.New("No OIDC tokens provided")}
}

// authenticateAccessToken verifies the access token and checks that the configured audience is present the in access
// token claims. We do not attempt to refresh access tokens as this is performed client side. The access token subject
// is returned if no error occurs.
func (o *Verifier) authenticateAccessToken(ctx context.Context, accessToken string) (string, error) {
	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, accessToken, o.accessTokenVerifier)
	if err != nil {
		return "", AuthError{Err: fmt.Errorf("Failed to verify access token: %w", err)}
	}

	// Check that the token includes the configured audience.
	audience := claims.GetAudience()
	if o.audience != "" && !shared.ValueInSlice(o.audience, audience) {
		return "", AuthError{Err: fmt.Errorf("Provided OIDC token doesn't allow the configured audience")}
	}

	return claims.Subject, nil
}

// authenticateIDToken verifies the identity token and returns the ID token subject. If no identity token is given (or
// verification fails) it will attempt to refresh the ID token.
func (o *Verifier) authenticateIDToken(ctx context.Context, w http.ResponseWriter, idToken string, refreshToken string) (string, error) {
	var claims *oidc.IDTokenClaims
	var err error
	if idToken != "" {
		// Try to verify the ID token.
		claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](ctx, idToken, o.relyingParty.IDTokenVerifier())
		if err == nil {
			return claims.Subject, nil
		}
	}

	// If ID token verification failed (or it wasn't provided, try refreshing the token).
	tokens, err := rp.RefreshAccessToken(o.relyingParty, refreshToken, "", "")
	if err != nil {
		return "", AuthError{Err: fmt.Errorf("Failed to refresh ID tokens: %w", err)}
	}

	idTokenAny := tokens.Extra("id_token")
	if idTokenAny == nil {
		return "", AuthError{Err: errors.New("ID tokens missing from OIDC refresh response")}
	}

	idToken, ok := idTokenAny.(string)
	if !ok {
		return "", AuthError{Err: errors.New("Malformed ID tokens in OIDC refresh response")}
	}

	// Verify the refreshed ID token.
	claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](ctx, idToken, o.relyingParty.IDTokenVerifier())
	if err != nil {
		return "", AuthError{Err: fmt.Errorf("Failed to verify refreshed ID token: %w", err)}
	}

	// Update the cookies.
	err = o.setCookies(w, idToken, tokens.RefreshToken, false)
	if err != nil {
		return "", fmt.Errorf("Failed to update login cookies: %w", err)
	}

	return claims.Subject, nil
}

// Login is a http.Handler than initiates the login flow for the UI.
func (o *Verifier) Login(w http.ResponseWriter, r *http.Request) {
	err := o.ensureConfig(r.Host)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Login failed: %w", err).Error()).Render(w)
		return
	}

	handler := rp.AuthURLHandler(func() string { return uuid.New().String() }, o.relyingParty, rp.WithURLParam("audience", o.audience))
	handler(w, r)
}

// Logout deletes the ID and refresh token cookies and redirects the user to the login page.
func (o *Verifier) Logout(w http.ResponseWriter, r *http.Request) {
	err := o.setCookies(w, "", "", true)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to delete login information: %w", err).Error()).Render(w)
		return
	}

	http.Redirect(w, r, "/ui/login/", http.StatusFound)
}

// Callback is a http.HandlerFunc which implements the code exchange required on the /oidc/callback endpoint.
func (o *Verifier) Callback(w http.ResponseWriter, r *http.Request) {
	err := o.ensureConfig(r.Host)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("OIDC callback failed: %w", err).Error()).Render(w)
		return
	}

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		err := o.setCookies(w, tokens.IDToken, tokens.RefreshToken, false)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to set login information: %w", err).Error()).Render(w)
			return
		}

		// Send to the UI.
		// NOTE: Once the UI does the redirection on its own, we may be able to use the referer here instead.
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	}, o.relyingParty)

	handler(w, r)
}

// WriteHeaders writes the OIDC configuration as HTTP headers so the client can initatiate the device code flow.
func (o *Verifier) WriteHeaders(w http.ResponseWriter) error {
	w.Header().Set("X-LXD-OIDC-issuer", o.issuer)
	w.Header().Set("X-LXD-OIDC-clientid", o.clientID)
	w.Header().Set("X-LXD-OIDC-audience", o.audience)

	return nil
}

// IsRequest checks if the request is using OIDC authentication. We check for the presence of the Authorization header
// or one of the ID or refresh tokens.
func (o *Verifier) IsRequest(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}

	idTokenCookie, err := r.Cookie(cookieNameIDToken)
	if err == nil && idTokenCookie != nil {
		return true
	}

	refreshTokenCookie, err := r.Cookie(cookieNameRefreshToken)
	if err == nil && refreshTokenCookie != nil {
		return true
	}

	return false
}

// ensureConfig ensures that the relyingParty and accessTokenVerifier fields of the Verifier are non-nil. Additionally,
// if the given host is different from the Verifier host we reset the relyingParty to ensure the callback URL is set
// correctly.
func (o *Verifier) ensureConfig(host string) error {
	if o.relyingParty == nil || host != o.host {
		err := o.setRelyingParty(host)
		if err != nil {
			return err
		}

		o.host = host
	}

	if o.accessTokenVerifier == nil {
		err := o.setAccessTokenVerifier()
		if err != nil {
			return err
		}
	}

	return nil
}

// setRelyingParty sets the relyingParty on the Verifier. The host argument is used to set a valid callback URL.
func (o *Verifier) setRelyingParty(host string) error {
	cookieHandler := httphelper.NewCookieHandler(o.cookieKey, o.cookieKey)
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
	}

	oidcScopes := []string{oidc.ScopeOpenID, oidc.ScopeOfflineAccess, oidc.ScopeEmail}

	relyingParty, err := rp.NewRelyingPartyOIDC(o.issuer, o.clientID, "", fmt.Sprintf("https://%s/oidc/callback", host), oidcScopes, options...)
	if err != nil {
		return fmt.Errorf("Failed to get OIDC relying party: %w", err)
	}

	o.relyingParty = relyingParty
	return nil
}

// setAccessTokenVerifier sets the accessTokenVerifier on the Verifier. It uses the oidc.KeySet from the relyingParty if
// it is set, otherwise it calls the discovery endpoint (/.well-known/openid-configuration).
func (o *Verifier) setAccessTokenVerifier() error {
	var keySet oidc.KeySet
	if o.relyingParty != nil {
		keySet = o.relyingParty.IDTokenVerifier().KeySet()
	} else {
		discoveryConfig, err := client.Discover(o.issuer, http.DefaultClient)
		if err != nil {
			return fmt.Errorf("Failed calling OIDC discovery endpoint: %w", err)
		}

		keySet = rp.NewRemoteKeySet(http.DefaultClient, discoveryConfig.JwksURI)
	}

	o.accessTokenVerifier = op.NewAccessTokenVerifier(o.issuer, keySet)
	return nil
}

// getCookies gets the ID and refresh tokens from the request cookies.
func (*Verifier) getCookies(r *http.Request) (idToken string, refreshToken string, err error) {
	idTokenCookie, err := r.Cookie(cookieNameIDToken)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return "", "", fmt.Errorf("Failed to get ID token cookie from request: %w", err)
	}

	if idTokenCookie != nil {
		idToken = idTokenCookie.Value
	}

	refreshTokenCookie, err := r.Cookie(cookieNameRefreshToken)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return "", "", fmt.Errorf("Failed to get refresh token cookie from request: %w", err)
	}

	if refreshTokenCookie != nil {
		refreshToken = refreshTokenCookie.Value
	}

	return idToken, refreshToken, nil
}

// setCookies sets the ID and refresh tokens in the HTTP response. Cookies are only set if they are
// non-empty. If delete is true, the values are set to empty strings and the cookie expiry is set to unix zero time.
func (*Verifier) setCookies(w http.ResponseWriter, idToken string, refreshToken string, delete bool) error {
	if idToken != "" || delete {
		idTokenCookie := http.Cookie{
			Name:     cookieNameIDToken,
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		}

		if delete {
			idTokenCookie.Value = ""
			idTokenCookie.Expires = time.Unix(0, 0)
		} else {
			idTokenCookie.Value = idToken
		}

		http.SetCookie(w, &idTokenCookie)
	}

	if refreshToken != "" || delete {
		refreshTokenCookie := http.Cookie{
			Name:     cookieNameRefreshToken,
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		}

		if delete {
			refreshTokenCookie.Value = ""
			refreshTokenCookie.Expires = time.Unix(0, 0)
		} else {
			refreshTokenCookie.Value = refreshToken
		}

		http.SetCookie(w, &refreshTokenCookie)
	}

	return nil
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientid string, audience string) (*Verifier, error) {
	cookieKey, err := uuid.New().MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("Failed to create UUID: %w", err)
	}

	verifier := &Verifier{issuer: issuer, clientID: clientid, audience: audience, cookieKey: cookieKey}

	return verifier, nil
}
