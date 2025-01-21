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
	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/crypto/hkdf"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
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
	accessTokenVerifier *op.AccessTokenVerifier
	relyingParty        rp.RelyingParty
	identityCache       *identity.Cache

	clientID       string
	issuer         string
	audience       string
	groupsClaim    string
	scopes         string
	clusterCert    func() *shared.CertInfo
	httpClientFunc func() (*http.Client, error)

	// host is used for setting a valid callback URL when setting the relyingParty.
	// When creating the relyingParty, the OIDC library performs discovery (e.g. it calls the /well-known/oidc-configuration endpoint).
	// We don't want to perform this on every request, so we only do it when the request host changes.
	host string

	// configExpiry is the next time at which the relying party and access token verifier will be considered out of date
	// and will be refreshed. This refreshes the cookie encryption keys that the relying party uses.
	configExpiry         time.Time
	configExpiryInterval time.Duration
}

// AuthenticationResult represents an authenticated OIDC client.
type AuthenticationResult struct {
	IdentityType           string
	Subject                string
	Email                  string
	Name                   string
	IdentityProviderGroups []string
}

// AuthError represents an authentication error. If an error of this type is returned, the caller should call
// WriteHeaders on the response so that the client has the necessary information to log in using the device flow.
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
func (o *Verifier) Auth(ctx context.Context, w http.ResponseWriter, r *http.Request) (*AuthenticationResult, error) {
	err := o.ensureConfig(ctx, r.Host)
	if err != nil {
		return nil, fmt.Errorf("Authorization failed: %w", err)
	}

	authorizationHeader := r.Header.Get("Authorization")

	_, idToken, refreshToken, err := o.getCookies(r)
	if err != nil {
		// Cookies are present but we failed to decrypt them. They may have been tampered with, so delete them to force
		// the user to log in again.
		_ = o.setCookies(w, nil, uuid.UUID{}, "", "", true)
		return nil, fmt.Errorf("Failed to retrieve login information: %w", err)
	}

	var result *AuthenticationResult
	if authorizationHeader != "" {
		// When a command line client wants to authenticate, it needs to set the Authorization HTTP header like this:
		//    Authorization Bearer <access_token>
		parts := strings.Split(authorizationHeader, "Bearer ")
		if len(parts) != 2 {
			return nil, AuthError{fmt.Errorf("Bad authorization token, expected a Bearer token")}
		}

		// Bearer tokens should always be access tokens.
		result, err = o.authenticateAccessToken(ctx, parts[1])
		if err != nil {
			return nil, err
		}
	} else if idToken != "" || refreshToken != "" {
		// When authenticating via the UI, we expect that there will be ID and refresh tokens present in the request cookies.
		result, err = o.authenticateIDToken(ctx, w, idToken, refreshToken)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

// authenticateAccessToken verifies the access token and checks that the configured audience is present the in access
// token claims. We do not attempt to refresh access tokens as this is performed client side. The access token subject
// is returned if no error occurs.
func (o *Verifier) authenticateAccessToken(ctx context.Context, accessToken string) (*AuthenticationResult, error) {
	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, accessToken, o.accessTokenVerifier)
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to verify access token: %w", err)}
	}

	// Check that the token includes the configured audience.
	audience := claims.GetAudience()
	if o.audience != "" && !shared.ValueInSlice(o.audience, audience) {
		return nil, AuthError{Err: fmt.Errorf("Provided OIDC token doesn't allow the configured audience")}
	}

	id, err := o.identityCache.GetByOIDCSubject(claims.Subject)
	if err == nil {
		return &AuthenticationResult{
			IdentityType:           api.IdentityTypeOIDCClient,
			Email:                  id.Identifier,
			Name:                   id.Name,
			Subject:                claims.Subject,
			IdentityProviderGroups: o.getGroupsFromClaims(claims.Claims),
		}, nil
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, fmt.Errorf("Failed to get OIDC identity from identity cache by their subject (%s): %w", claims.Subject, err)
	}

	userInfo, err := rp.Userinfo[*oidc.UserInfo](ctx, accessToken, oidc.BearerToken, claims.Subject, o.relyingParty)
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to call user info endpoint with given access token: %w", err)}
	}

	if userInfo.Email == "" {
		return nil, AuthError{Err: fmt.Errorf("Could not get email address of oidc user with subject %q", claims.Subject)}
	}

	return &AuthenticationResult{
		IdentityType:           api.IdentityTypeOIDCClient,
		Email:                  userInfo.Email,
		Name:                   userInfo.Name,
		Subject:                claims.Subject,
		IdentityProviderGroups: o.getGroupsFromClaims(claims.Claims),
	}, nil
}

// authenticateIDToken verifies the identity token and returns the ID token subject. If no identity token is given (or
// verification fails) it will attempt to refresh the ID token.
func (o *Verifier) authenticateIDToken(ctx context.Context, w http.ResponseWriter, idToken string, refreshToken string) (*AuthenticationResult, error) {
	var claims *oidc.IDTokenClaims
	var err error
	if idToken != "" {
		// Try to verify the ID token.
		claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](ctx, idToken, o.relyingParty.IDTokenVerifier())
		if err == nil {
			return &AuthenticationResult{
				IdentityType:           api.IdentityTypeOIDCClient,
				Subject:                claims.Subject,
				Email:                  claims.Email,
				Name:                   claims.Name,
				IdentityProviderGroups: o.getGroupsFromClaims(claims.Claims),
			}, nil
		}
	}

	// If ID token verification failed (or it wasn't provided, try refreshing the token).
	tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, o.relyingParty, refreshToken, "", "")
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to refresh ID tokens: %w", err)}
	}

	idTokenAny := tokens.Extra("id_token")
	if idTokenAny == nil {
		return nil, AuthError{Err: errors.New("ID tokens missing from OIDC refresh response")}
	}

	idToken, ok := idTokenAny.(string)
	if !ok {
		return nil, AuthError{Err: errors.New("Malformed ID tokens in OIDC refresh response")}
	}

	// Verify the refreshed ID token.
	claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](ctx, idToken, o.relyingParty.IDTokenVerifier())
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to verify refreshed ID token: %w", err)}
	}

	sessionID := uuid.New()
	secureCookie, err := o.secureCookieFromSession(sessionID)
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to create new session with refreshed token: %w", err)}
	}

	// Update the cookies.
	err = o.setCookies(w, secureCookie, sessionID, idToken, tokens.RefreshToken, false)
	if err != nil {
		return nil, AuthError{fmt.Errorf("Failed to update login cookies: %w", err)}
	}

	return &AuthenticationResult{
		IdentityType:           api.IdentityTypeOIDCClient,
		Subject:                claims.Subject,
		Email:                  claims.Email,
		Name:                   claims.Name,
		IdentityProviderGroups: o.getGroupsFromClaims(claims.Claims),
	}, nil
}

// getGroupsFromClaims attempts to get the configured groups claim from the token claims and warns if it is not present
// or is not a valid type. The custom claims are an unmarshalled JSON object.
func (o *Verifier) getGroupsFromClaims(customClaims map[string]any) []string {
	if o.groupsClaim == "" {
		return nil
	}

	groupsClaimAny, ok := customClaims[o.groupsClaim]
	if !ok {
		logger.Warn("OIDC groups custom claim not found", logger.Ctx{"claim_name": o.groupsClaim})
		return nil
	}

	groupsArr, ok := groupsClaimAny.([]any)
	if !ok {
		logger.Warn("Unexpected type for OIDC groups custom claim", logger.Ctx{"claim_name": o.groupsClaim, "claim_value": groupsClaimAny})
	}

	groups := make([]string, 0, len(groupsArr))
	for _, groupNameAny := range groupsArr {
		groupName, ok := groupNameAny.(string)
		if !ok {
			logger.Warn("Unexpected type for OIDC groups custom claim", logger.Ctx{"claim_name": o.groupsClaim, "claim_value": groupsClaimAny})
		}

		groups = append(groups, groupName)
	}

	return groups
}

// Login is a http.Handler than initiates the login flow for the UI.
func (o *Verifier) Login(w http.ResponseWriter, r *http.Request) {
	err := o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Login failed: %w", err).Error()).Render(w, r)
		return
	}

	handler := rp.AuthURLHandler(func() string { return uuid.New().String() }, o.relyingParty, rp.WithURLParam("audience", o.audience))
	handler(w, r)
}

// Logout deletes the ID and refresh token cookies and redirects the user to the login page.
func (o *Verifier) Logout(w http.ResponseWriter, r *http.Request) {
	err := o.setCookies(w, nil, uuid.UUID{}, "", "", true)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to delete login information: %w", err).Error()).Render(w, r)
		return
	}

	http.Redirect(w, r, "/ui/login/", http.StatusFound)
}

// Callback is a http.HandlerFunc which implements the code exchange required on the /oidc/callback endpoint.
func (o *Verifier) Callback(w http.ResponseWriter, r *http.Request) {
	err := o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("OIDC callback failed: %w", err).Error()).Render(w, r)
		return
	}

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		sessionID := uuid.New()
		secureCookie, err := o.secureCookieFromSession(sessionID)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to start a new session: %w", err).Error()).Render(w, r)
			return
		}

		err = o.setCookies(w, secureCookie, sessionID, tokens.IDToken, tokens.RefreshToken, false)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to set login information: %w", err).Error()).Render(w, r)
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
	w.Header().Set("X-LXD-OIDC-groups-claim", o.groupsClaim)
	w.Header().Set("X-LXD-OIDC-scopes", o.scopes)

	return nil
}

// IsRequest checks if the request is using OIDC authentication. We check for the presence of the Authorization header
// or one of the ID or refresh tokens and the session cookie.
func (*Verifier) IsRequest(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}

	_, err := r.Cookie(cookieNameSessionID)
	if err != nil {
		return false
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

// ExpireConfig sets the expiry time of the current configuration to zero. This forces the verifier to reconfigure the
// relying party the next time a user authenticates.
func (o *Verifier) ExpireConfig() {
	o.configExpiry = time.Now()
}

// ensureConfig ensures that the relyingParty and accessTokenVerifier fields of the Verifier are non-nil. Additionally,
// if the given host is different from the Verifier host we reset the relyingParty to ensure the callback URL is set
// correctly.
func (o *Verifier) ensureConfig(ctx context.Context, host string) error {
	if o.relyingParty == nil || host != o.host || time.Now().After(o.configExpiry) {
		err := o.setRelyingParty(ctx, host)
		if err != nil {
			return err
		}

		o.host = host
		o.configExpiry = time.Now().Add(o.configExpiryInterval)
	}

	if o.accessTokenVerifier == nil {
		err := o.setAccessTokenVerifier(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// setRelyingParty sets the relyingParty on the Verifier. The host argument is used to set a valid callback URL.
func (o *Verifier) setRelyingParty(ctx context.Context, host string) error {
	// The relying party sets cookies for the following values:
	// - "state": Used to prevent CSRF attacks (https://datatracker.ietf.org/doc/html/rfc6749#section-10.12).
	// - "pkce": Used to prevent authorization code interception attacks (https://datatracker.ietf.org/doc/html/rfc7636).
	// Both should be stored securely. However, these cookies do not need to be decrypted by other cluster members, so
	// it is ok to use the secure key generation that is built in to the securecookie library. This also reduces the
	// exposure of our private key.

	// The hash key should be 64 bytes (https://github.com/gorilla/securecookie).
	cookieHashKey := securecookie.GenerateRandomKey(64)
	if cookieHashKey == nil {
		return errors.New("Failed to generate a secure cookie hash key")
	}

	// The block key should 32 bytes for AES-256 encryption.
	cookieBlockKey := securecookie.GenerateRandomKey(32)
	if cookieBlockKey == nil {
		return errors.New("Failed to generate a secure cookie hash key")
	}

	httpClient, err := o.httpClientFunc()
	if err != nil {
		return fmt.Errorf("Failed to get a HTTP client: %w", err)
	}

	cookieHandler := httphelper.NewCookieHandler(cookieHashKey, cookieBlockKey)
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
		rp.WithHTTPClient(httpClient),
	}

	oidcScopes := []string{oidc.ScopeOpenID, oidc.ScopeOfflineAccess, oidc.ScopeEmail, oidc.ScopeProfile}
	if o.groupsClaim != "" {
		oidcScopes = append(oidcScopes, o.groupsClaim)
	}

	if o.scopes != "" {
		oidcScopes = strings.Split(o.scopes, " ")
	}

	relyingParty, err := rp.NewRelyingPartyOIDC(ctx, o.issuer, o.clientID, "", fmt.Sprintf("https://%s/oidc/callback", host), oidcScopes, options...)
	if err != nil {
		return fmt.Errorf("Failed to get OIDC relying party: %w", err)
	}

	o.relyingParty = relyingParty
	return nil
}

// setAccessTokenVerifier sets the accessTokenVerifier on the Verifier. It uses the oidc.KeySet from the relyingParty if
// it is set, otherwise it calls the discovery endpoint (/.well-known/openid-configuration).
func (o *Verifier) setAccessTokenVerifier(ctx context.Context) error {
	var keySet oidc.KeySet
	if o.relyingParty != nil {
		keySet = o.relyingParty.IDTokenVerifier().KeySet
	} else {
		discoveryConfig, err := client.Discover(ctx, o.issuer, http.DefaultClient)
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
	GroupsClaim string
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientID string, audience string, scopes string, clusterCert func() *shared.CertInfo, identityCache *identity.Cache, httpClientFunc func() (*http.Client, error), options *Opts) (*Verifier, error) {
	opts := &Opts{}

	if options != nil && options.GroupsClaim != "" {
		opts.GroupsClaim = options.GroupsClaim
	}

	verifier := &Verifier{
		issuer:               issuer,
		clientID:             clientID,
		audience:             audience,
		scopes:               scopes,
		identityCache:        identityCache,
		groupsClaim:          opts.GroupsClaim,
		clusterCert:          clusterCert,
		configExpiryInterval: defaultConfigExpiryInterval,
		httpClientFunc:       httpClientFunc,
	}

	return verifier, nil
}
