package oidc

import (
	"context"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/zitadel/oidc/v2/pkg/client"
	"github.com/zitadel/oidc/v2/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v2/pkg/http"
	"github.com/zitadel/oidc/v2/pkg/oidc"
	"github.com/zitadel/oidc/v2/pkg/op"
	"golang.org/x/crypto/hkdf"

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

	clientID    string
	issuer      string
	audience    string
	clusterCert func() *shared.CertInfo

	// host is used for setting a valid callback URL when setting the relyingParty.
	// When creating the relyingParty, the OIDC library performs discovery (e.g. it calls the /well-known/oidc-configuration endpoint).
	// We don't want to perform this on every request, so we only do it when the request host changes.
	host string

	// configExpiry is the next time at which the relying party and access token verifier will be considered out of date
	// and will be refreshed. This refreshes the cookie encryption keys that the relying party uses.
	configExpiry         time.Time
	configExpiryInterval time.Duration
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

// getCookies gets the sessionID, identity and refresh tokens from the request cookies and decrypts them.
func (o *Verifier) getCookies(r *http.Request) (sessionIDPtr *uuid.UUID, idToken string, refreshToken string, err error) {
	sessionIDCookie, err := r.Cookie(cookieNameSessionID)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return nil, "", "", fmt.Errorf("Failed to get session ID cookie from request: %w", err)
	} else if sessionIDCookie == nil {
		return nil, "", "", nil
	}

	sessionID, err := uuid.Parse(sessionIDCookie.Value)
	if err != nil {
		return nil, "", "", fmt.Errorf("Invalid session ID cookie: %w", err)
	}

	secureCookie, err := o.secureCookieFromSession(sessionID)
	if err != nil {
		return nil, "", "", fmt.Errorf("Failed to decrypt cookies: %w", err)
	}

	idTokenCookie, err := r.Cookie(cookieNameIDToken)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return nil, "", "", fmt.Errorf("Failed to get ID token cookie from request: %w", err)
	}

	if idTokenCookie != nil {
		err = secureCookie.Decode(cookieNameIDToken, idTokenCookie.Value, &idToken)
		if err != nil {
			return nil, "", "", fmt.Errorf("Failed to decrypt ID token cookie: %w", err)
		}
	}

	refreshTokenCookie, err := r.Cookie(cookieNameRefreshToken)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return nil, "", "", fmt.Errorf("Failed to get refresh token cookie from request: %w", err)
	}

	if refreshTokenCookie != nil {
		err = secureCookie.Decode(cookieNameRefreshToken, refreshTokenCookie.Value, &refreshToken)
		if err != nil {
			return nil, "", "", fmt.Errorf("Failed to decrypt refresh token cookie: %w", err)
		}
	}

	return &sessionID, idToken, refreshToken, nil
}

// setCookies encrypts the session, ID, and refresh tokens and sets them in the HTTP response. Cookies are only set if they are
// non-empty. If delete is true, the values are set to empty strings and the cookie expiry is set to unix zero time.
func (*Verifier) setCookies(w http.ResponseWriter, secureCookie *securecookie.SecureCookie, sessionID uuid.UUID, idToken string, refreshToken string, delete bool) error {
	idTokenCookie := http.Cookie{
		Name:     cookieNameIDToken,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}

	refreshTokenCookie := http.Cookie{
		Name:     cookieNameRefreshToken,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}

	sessionIDCookie := http.Cookie{
		Name:     cookieNameSessionID,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}

	if delete {
		idTokenCookie.Expires = time.Unix(0, 0)
		refreshTokenCookie.Expires = time.Unix(0, 0)
		sessionIDCookie.Expires = time.Unix(0, 0)

		http.SetCookie(w, &idTokenCookie)
		http.SetCookie(w, &refreshTokenCookie)
		http.SetCookie(w, &sessionIDCookie)
		return nil
	}

	encodedIDTokenCookie, err := secureCookie.Encode(cookieNameIDToken, idToken)
	if err != nil {
		return fmt.Errorf("Failed to encrypt ID token: %w", err)
	}

	encodedRefreshToken, err := secureCookie.Encode(cookieNameRefreshToken, refreshToken)
	if err != nil {
		return fmt.Errorf("Failed to encrypt refresh token: %w", err)
	}

	sessionIDCookie.Value = sessionID.String()
	idTokenCookie.Value = encodedIDTokenCookie
	refreshTokenCookie.Value = encodedRefreshToken

	http.SetCookie(w, &idTokenCookie)
	http.SetCookie(w, &refreshTokenCookie)
	http.SetCookie(w, &sessionIDCookie)
	return nil
}

// secureCookieFromSession returns a *securecookie.SecureCookie that is secure, unique to each client, and possible to
// decrypt on all cluster members.
//
// To do this we use the cluster private key as an input seed to HKDF (https://datatracker.ietf.org/doc/html/rfc5869) and
// use the given sessionID uuid.UUID as a salt. The session ID can then be stored as a plaintext cookie so that we can
// regenerate the keys upon the next request.
//
// Warning: Changes to this function might cause all existing OIDC users to be logged out of LXD (but not logged out of
// the IdP).
func (o *Verifier) secureCookieFromSession(sessionID uuid.UUID) (*securecookie.SecureCookie, error) {
	// Get the sessionID as a binary so that we can use it as a salt.
	salt, err := sessionID.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal session ID as binary: %w", err)
	}

	// Get the current cluster private key.
	clusterPrivateKey := o.clusterCert().PrivateKey()

	// Extract a pseudo-random key from the cluster private key.
	prk := hkdf.Extract(sha512.New, clusterPrivateKey, salt)

	// Get an io.Reader from which we can read a secure key. We will use this key as the hash key for the cookie.
	// The hash key is used to verify the integrity of decrypted values using HMAC. The HKDF "info" is set to "INTEGRITY"
	// to indicate the intended usage of the key and prevent decryption in other contexts
	// (see https://datatracker.ietf.org/doc/html/rfc5869#section-3.2).
	keyDerivationFunc := hkdf.Expand(sha512.New, prk, []byte("INTEGRITY"))

	// Read 64 bytes of the derived key. The securecookie library recommends 64 bytes for the hash key (https://github.com/gorilla/securecookie).
	cookieHashKey := make([]byte, 64)
	_, err = io.ReadFull(keyDerivationFunc, cookieHashKey)
	if err != nil {
		return nil, fmt.Errorf("Failed creating secure cookie hash key: %w", err)
	}

	// Get an io.Reader from which we can read a secure key. We will use this key as the block key for the cookie.
	// The block key is used by securecookie to perform AES encryption. The HKDF "info" is set to "ENCRYPTION"
	// to indicate the intended usage of the key and prevent decryption in other contexts
	// (see https://datatracker.ietf.org/doc/html/rfc5869#section-3.2).
	keyDerivationFunc = hkdf.Expand(sha512.New, prk, []byte("ENCRYPTION"))

	// Read 32 bytes of the derived key. Given 32 bytes for the block key the securecookie library will use AES-256 for encryption.
	cookieBlockKey := make([]byte, 32)
	_, err = io.ReadFull(keyDerivationFunc, cookieBlockKey)
	if err != nil {
		return nil, fmt.Errorf("Failed creating secure cookie block key: %w", err)
	}

	return securecookie.New(cookieHashKey, cookieBlockKey), nil
}

// Opts contains optional configurable fields for the Verifier.
type Opts struct {
	ConfigExpiryInterval time.Duration
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientID string, audience string, clusterCert func() *shared.CertInfo, options *Opts) (*Verifier, error) {
	opts := &Opts{
		ConfigExpiryInterval: defaultConfigExpiryInterval,
	}

	if options != nil {
		opts.ConfigExpiryInterval = options.ConfigExpiryInterval
	}

	verifier := &Verifier{
		issuer:               issuer,
		clientID:             clientID,
		audience:             audience,
		clusterCert:          clusterCert,
		configExpiryInterval: opts.ConfigExpiryInterval,
	}

	return verifier, nil
}
