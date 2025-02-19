package oidc

import (
	"context"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
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

	"github.com/canonical/lxd/lxd/db/cluster/secret"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// SessionHandler is used where session handling must call the database. Methods should only be called after a client
// session cookie or bearer token has been verified.
type SessionHandler interface {
	StartSession(ctx context.Context, r *http.Request, res AuthenticationResult) error
	GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (info *api.IdentityInfo, sessionRevoked bool, refreshToken string, err error)
}

const (
	// cookieNameIDToken is the identifier used to set and retrieve the identity token.
	cookieNameIDToken = "oidc_identity"

	// cookieNameRefreshToken is the identifier used to set and retrieve the refresh token.
	cookieNameRefreshToken = "oidc_refresh"

	// cookieNameSessionID is used to identify the session. It does not need to be encrypted.
	cookieNameSessionID = "session_id"
)

type relyingParty struct {
	rp.RelyingParty
	outdatedAt time.Time
}

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	accessTokenVerifier *op.AccessTokenVerifier
	relyingParties      []relyingParty
	identityCache       *identity.Cache

	clientID       string
	issuer         string
	scopes         []string
	audience       string
	groupsClaim    string
	httpClientFunc func() (*http.Client, error)
	clusterSecret  func(ctx context.Context) (secret.Secret, bool, error)

	// host is used for setting a valid callback URL when setting the relyingParty.
	// When creating the relyingParty, the OIDC library performs discovery (e.g. it calls the /well-known/oidc-configuration endpoint).
	// We don't want to perform this on every request, so we only do it when the request host changes.
	host string

	// expireConfig is used to refresh configuration on the next usage. This forces the verifier to, for example, update
	// the http.Client used for communication with the IdP so that proxies can be used.
	expireConfig bool
}

// AuthenticationResult represents an authenticated OIDC client.
type AuthenticationResult struct {
	IdentityType           string
	Subject                string
	Email                  string
	Name                   string
	IdentityProviderGroups []string
	RefreshToken           string
	SessionID              uuid.UUID
}

// AuthError represents an authentication error. If an error of this type is returned, the caller should call
// WriteHeaders on the response so that the client has the necessary information to log in using the device flow.
type AuthError struct {
	Err error
}

// Error implements the error interface for AuthError.
func (e AuthError) Error() string {
	return "Failed to authenticate: " + e.Err.Error()
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

	userInfo, err := rp.Userinfo[*oidc.UserInfo](ctx, accessToken, oidc.BearerToken, claims.Subject, o.relyingParties[0])
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to call user info endpoint with given access token: %w", err)}
	}

	return o.getResultFromClaims(userInfo, userInfo.Claims)
}

// authenticateIDToken verifies the identity token and returns the ID token subject. If no identity token is given (or
// verification fails) it will attempt to refresh the ID token.
func (o *Verifier) authenticateIDToken(ctx context.Context, w http.ResponseWriter, idToken string, refreshToken string) (*AuthenticationResult, error) {
	var claims *oidc.IDTokenClaims
	var err error
	if idToken != "" {
		// Try to verify the ID token.
		claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](ctx, idToken, o.relyingParties[0].IDTokenVerifier())
		if err == nil {
			return o.getResultFromClaims(claims, claims.Claims)
		}
	}

	// If ID token verification failed (or it wasn't provided, try refreshing the token).
	tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, o.relyingParties[0], refreshToken, "", "")
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
	claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](ctx, idToken, o.relyingParties[0].IDTokenVerifier())
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to verify refreshed ID token: %w", err)}
	}

	sessionID := uuid.New()
	clusterSecret, _, err := o.clusterSecret(ctx)
	if err != nil {
		return nil, err
	}

	secureCookie, err := secureCookie(clusterSecret, sessionID[:])
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to create new session with refreshed token: %w", err)}
	}

	// Update the cookies.
	err = o.setCookies(w, secureCookie, sessionID, idToken, tokens.RefreshToken, false)
	if err != nil {
		return nil, AuthError{fmt.Errorf("Failed to update login cookies: %w", err)}
	}

	return o.getResultFromClaims(claims, claims.Claims)
}

// getResultFromClaims gets an AuthenticationResult from the given rp.SubjectGetter and claim map.
// It returns an error if any required values are not present or are invalid.
func (o *Verifier) getResultFromClaims(sg rp.SubjectGetter, claims map[string]any) (*AuthenticationResult, error) {
	email, err := o.getEmailFromClaims(claims)
	if err != nil {
		return nil, err
	}

	subject := sg.GetSubject()
	if subject == "" {
		return nil, fmt.Errorf("Token does not contain a subject")
	}

	var name string
	nameAny, ok := claims["name"]
	if ok {
		nameStr, ok := nameAny.(string)
		if ok {
			name = nameStr
		}
	}

	return &AuthenticationResult{
		IdentityType:           api.IdentityTypeOIDCClient,
		Subject:                subject,
		Email:                  email,
		Name:                   name,
		IdentityProviderGroups: o.getGroupsFromClaims(claims),
	}, nil
}

// getEmailFromClaims gets a valid email address from the claims or returns an error.
func (o *Verifier) getEmailFromClaims(claims map[string]any) (string, error) {
	emailAny, ok := claims[oidc.ScopeEmail]
	if !ok {
		return "", fmt.Errorf("Token does not contain an email address")
	}

	email, ok := emailAny.(string)
	if !ok {
		return "", fmt.Errorf("Token claim %q has incorrect type (expected %T, got %T)", "email", "", emailAny)
	}

	_, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("Token claim %q contains a value %q that is not a valid email address: %w", "email", email, err)
	}

	return email, nil
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
		return nil
	}

	groups := make([]string, 0, len(groupsArr))
	for _, groupNameAny := range groupsArr {
		groupName, ok := groupNameAny.(string)
		if !ok {
			logger.Warn("Unexpected type for OIDC groups custom claim", logger.Ctx{"claim_name": o.groupsClaim, "claim_value": groupsClaimAny})
			return nil
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

	handler := rp.AuthURLHandler(func() string { return uuid.New().String() }, o.relyingParties[0], rp.WithURLParam("audience", o.audience))
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

	var relyingParty rp.RelyingParty
	for _, p := range o.relyingParties {
		// Check if the relying party can read the state cookie.
		// If it can, then the auth flow was initiated by a relying party with the same encryption keys.
		// Old RPs are only kept valid for 5 minutes (see ensureConfig).
		_, err := p.CookieHandler().CheckQueryCookie(r, "state")
		if err != nil {
			continue
		}

		relyingParty = p
	}

	if relyingParty == nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("OIDC callback failed: No relying party available with applicable cookie handler").Error()).Render(w, r)
		return
	}

	clusterSecret, _, err := o.clusterSecret(r.Context())
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("OIDC callback failed: %w", err).Error()).Render(w, r)
		return
	}

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		sessionID := uuid.New()
		secureCookie, err := secureCookie(clusterSecret, sessionID[:])
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
	}, relyingParty)

	handler(w, r)
}

// WriteHeaders writes the OIDC configuration as HTTP headers so the client can initatiate the device code flow.
func (o *Verifier) WriteHeaders(w http.ResponseWriter) error {
	w.Header().Set("X-LXD-OIDC-issuer", o.issuer)
	w.Header().Set("X-LXD-OIDC-clientid", o.clientID)
	w.Header().Set("X-LXD-OIDC-audience", o.audience)

	// Continue to sent groups claim header for compatibility with older clients
	w.Header().Set("X-LXD-OIDC-groups-claim", o.groupsClaim)

	scopesJSON, err := json.Marshal(o.scopes)
	if err != nil {
		return fmt.Errorf("Failed to marshal OIDC scopes: %w", err)
	}

	w.Header().Set("X-LXD-OIDC-scopes", string(scopesJSON))

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
	o.expireConfig = true
}

// ensureConfig ensures that the relyingParties field of Verifier has at least one entry and that the accessTokenVerifier
// field is non-nil. Additionally, if the given host is different from the Verifier host we add a new relyingParty to
// relyingParties to ensure the callback URL is set correctly.
func (o *Verifier) ensureConfig(ctx context.Context, host string) error {
	// Clean up any old relying parties that became outdated more than 5 minutes ago.
	rps := make([]relyingParty, 0, len(o.relyingParties))
	for i, p := range o.relyingParties {
		if i == 0 || time.Now().Before(p.outdatedAt.Add(5*time.Minute)) {
			rps = append(rps, p)
		}
	}

	o.relyingParties = rps

	clusterSecret, secretUpdated, err := o.clusterSecret(ctx)
	if err != nil {
		return err
	}

	if len(o.relyingParties) == 0 || o.expireConfig || secretUpdated || host != o.host {
		err := o.setRelyingParties(ctx, clusterSecret, host)
		if err != nil {
			return err
		}

		o.expireConfig = false
		o.host = host
	}

	if o.accessTokenVerifier == nil {
		err := o.setAccessTokenVerifier(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// setRelyingParties sets the relyingParty on the Verifier. The host argument is used to set a valid callback URL.
func (o *Verifier) setRelyingParties(ctx context.Context, clusterSecret secret.Secret, host string) error {
	httpClient, err := o.httpClientFunc()
	if err != nil {
		return fmt.Errorf("Failed to get a HTTP client: %w", err)
	}

	hash, block, err := extractKeys(clusterSecret, nil)
	if err != nil {
		return err
	}

	cookieHandler := httphelper.NewCookieHandler(hash, block)
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
		rp.WithHTTPClient(httpClient),
	}

	newRP, err := rp.NewRelyingPartyOIDC(ctx, o.issuer, o.clientID, "", "https://"+host+"/oidc/callback", o.scopes, options...)
	if err != nil {
		return fmt.Errorf("Failed to get OIDC relying party: %w", err)
	}

	// Set the time that the last RP became outdated.
	if len(o.relyingParties) > 0 {
		o.relyingParties[0].outdatedAt = time.Now()
	}

	o.relyingParties = append([]relyingParty{{RelyingParty: newRP}}, o.relyingParties...)
	return nil
}

// setAccessTokenVerifier sets the accessTokenVerifier on the Verifier. It uses the oidc.KeySet from the relyingParty if
// it is set, otherwise it calls the discovery endpoint (/.well-known/openid-configuration).
func (o *Verifier) setAccessTokenVerifier(ctx context.Context) error {
	httpClient, err := o.httpClientFunc()
	if err != nil {
		return err
	}

	var keySet oidc.KeySet
	if len(o.relyingParties) > 0 && o.relyingParties[0].RelyingParty != nil {
		keySet = o.relyingParties[0].IDTokenVerifier().KeySet
	} else {
		discoveryConfig, err := client.Discover(ctx, o.issuer, httpClient)
		if err != nil {
			return fmt.Errorf("Failed calling OIDC discovery endpoint: %w", err)
		}

		keySet = rp.NewRemoteKeySet(httpClient, discoveryConfig.JwksURI)
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

	clusterSecret, _, err := o.clusterSecret(r.Context())
	if err != nil {
		return nil, "", "", err
	}

	secureCookie, err := secureCookie(clusterSecret, sessionID[:])
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
func (*Verifier) setCookies(w http.ResponseWriter, secureCookie *securecookie.SecureCookie, sessionID uuid.UUID, idToken string, refreshToken string, deleteCookies bool) error {
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

	if deleteCookies {
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

// secureCookie returns a *securecookie.SecureCookie that is secure, unique for each salt, and possible to
// decrypt on all cluster members.
//
// To do this we use the cluster-wide secret as an input seed to HKDF (https://datatracker.ietf.org/doc/html/rfc5869).
// If no salt is provided, the cluster-wide salt is used.
//
// Warning: Changes to this function might cause all existing OIDC users to be logged out of LXD (but not logged out of
// the IdP).
func secureCookie(s secret.Secret, salt []byte) (*securecookie.SecureCookie, error) {
	hash, block, err := extractKeys(s, salt)
	if err != nil {
		return nil, err
	}

	return securecookie.New(hash, block), nil
}

// extractKeys derives a hash and block key from the given secret and salt. If no salt is given, the cluster-wide salt is used.
func extractKeys(s secret.Secret, salt []byte) (hash []byte, block []byte, err error) {
	key, clusterSalt, err := s.KeyAndSalt()
	if err != nil {
		return nil, nil, err
	}

	if salt == nil {
		salt = clusterSalt
	}

	// Extract a pseudo-random key from the cluster private key.
	prk := hkdf.Extract(sha512.New, key, salt)

	// Get an io.Reader from which we can read a secure key. We will use this key as the hash key for the cookie.
	// The hash key is used to verify the integrity of decrypted values using HMAC. The HKDF "info" is set to "INTEGRITY"
	// to indicate the intended usage of the key and prevent decryption in other contexts
	// (see https://datatracker.ietf.org/doc/html/rfc5869#section-3.2).
	keyDerivationFunc := hkdf.Expand(sha512.New, prk, []byte("INTEGRITY"))

	// Read 64 bytes of the derived key. The securecookie library recommends 64 bytes for the hash key (https://github.com/gorilla/securecookie).
	cookieHashKey := make([]byte, 64)
	_, err = io.ReadFull(keyDerivationFunc, cookieHashKey)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed creating secure cookie hash key: %w", err)
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
		return nil, nil, fmt.Errorf("Failed creating secure cookie block key: %w", err)
	}

	return cookieHashKey, cookieBlockKey, nil
}

// Opts contains optional configurable fields for the Verifier.
type Opts struct {
	GroupsClaim string
	Host        string
	Ctx         context.Context
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientID string, scopes []string, audience string, clusterSecret func(ctx context.Context) (secret.Secret, bool, error), identityCache *identity.Cache, httpClientFunc func() (*http.Client, error), options *Opts) (*Verifier, error) {
	opts := &Opts{}

	if options != nil && options.GroupsClaim != "" {
		opts.GroupsClaim = options.GroupsClaim
	}

	verifier := &Verifier{
		issuer:         issuer,
		clientID:       clientID,
		scopes:         scopes,
		audience:       audience,
		identityCache:  identityCache,
		groupsClaim:    opts.GroupsClaim,
		clusterSecret:  clusterSecret,
		httpClientFunc: httpClientFunc,
	}

	if options != nil && options.Host != "" {
		ctx := context.Background()
		if opts.Ctx != nil {
			ctx = opts.Ctx
		}

		err := verifier.ensureConfig(ctx, opts.Host)
		if err != nil {
			return nil, fmt.Errorf("Failed to configure OIDC verifier: %w", err)
		}
	}

	return verifier, nil
}
