package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/canonical/lxd/lxd/auth/encryption"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const (
	// cookieNameLoginID is used to identify a single login flow.
	cookieNameLoginID = "login_id"

	// cookieNameIDToken is the identifier used to set and retrieve the identity token.
	cookieNameIDToken = "oidc_identity"

	// cookieNameRefreshToken is the identifier used to set and retrieve the refresh token.
	cookieNameRefreshToken = "oidc_refresh"

	// cookieNameSessionID is used to identify the session. It does not need to be encrypted.
	cookieNameSessionID = "session_id"
)

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	accessTokenVerifier *op.AccessTokenVerifier
	relyingParty        rp.RelyingParty
	identityCache       *identity.Cache

	clientID       string
	clientSecret   string
	issuer         string
	scopes         []string
	audience       string
	groupsClaim    string
	secretsFunc    func(ctx context.Context) (cluster.AuthSecrets, error)
	httpClientFunc func() (*http.Client, error)

	// host is used for setting a valid callback URL when setting the relyingParty.
	// When creating the relyingParty, the OIDC library performs discovery (e.g. it calls the /well-known/oidc-configuration endpoint).
	// We don't want to perform this on every request, so we only do it when the request host changes.
	host string

	// expireConfig is used to expiry the relying party configuration before it is next used. This is so that proxy
	// configurations (core.https_proxy) can be applied to the HTTP client used to call the IdP.
	expireConfig bool
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
	return "Failed to authenticate: " + e.Err.Error()
}

// Unwrap implements the xerrors.Wrapper interface for AuthError.
func (e AuthError) Unwrap() error {
	return e.Err
}

// Auth extracts OIDC tokens from the request, verifies them, and returns an AuthenticationResult or an error.
func (o *Verifier) Auth(w http.ResponseWriter, r *http.Request) (*AuthenticationResult, error) {
	err := o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		return nil, fmt.Errorf("Authorization failed: %w", err)
	}

	// If a bearer token is provided, it must be valid.
	bearerToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok {
		return o.authenticateAccessToken(r.Context(), bearerToken)
	}

	// Otherwise, it must be a browser.
	return o.authenticateIDToken(w, r)
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
	if o.audience != "" && !slices.Contains(audience, o.audience) {
		return nil, AuthError{Err: errors.New("Provided OIDC token doesn't allow the configured audience")}
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

	return o.getResultFromClaims(userInfo, userInfo.Claims)
}

// authenticateIDToken gets the ID token from the request cookies and validates it. If it is not present or not valid, it
// attempts to refresh the ID token (with a refresh token also taken from the request cookies).
func (o *Verifier) authenticateIDToken(w http.ResponseWriter, r *http.Request) (*AuthenticationResult, error) {
	idToken, refreshToken, startNewSession, err := o.getCookies(r)
	if err != nil {
		// Cookies are present but we failed to decrypt them. They may have been tampered with, so delete them to force
		// the user to log in again.
		_ = o.setCookies(w, nil, uuid.UUID{}, "", "", true)
		return nil, fmt.Errorf("Failed to retrieve login information: %w", err)
	} else if idToken == "" && refreshToken == "" {
		// The IsRequest function gates calls to the OIDC verifier. We should not reach this block.
		return nil, AuthError{Err: errors.New("No credentials found")}
	}

	var claims *oidc.IDTokenClaims
	if idToken != "" {
		// Try to verify the ID token.
		claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](r.Context(), idToken, o.relyingParty.IDTokenVerifier())
		if err == nil {
			if startNewSession {
				err = o.startSession(r.Context(), w, idToken, refreshToken)
				if err != nil {
					return nil, AuthError{Err: fmt.Errorf("Failed to refresh session: %w", err)}
				}
			}

			return o.getResultFromClaims(claims, claims.Claims)
		}
	}

	// If ID token verification failed (or it wasn't provided, try refreshing the token).
	tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](r.Context(), o.relyingParty, refreshToken, "", "")
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
	claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](r.Context(), idToken, o.relyingParty.IDTokenVerifier())
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to verify refreshed ID token: %w", err)}
	}

	err = o.startSession(r.Context(), w, idToken, tokens.RefreshToken)
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to create new session with refreshed token: %w", err)}
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
		return nil, errors.New("Token does not contain a subject")
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
		return "", errors.New("Token does not contain an email address")
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

	// Use a v7 UUID for the login ID. Encoding the current unix epoch into the ID allows us to determine if an
	// outdated secret was used for encryption key generation.
	loginID, err := uuid.NewV7()
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Login failed: Failed to create a login identifier: %w", err).Error()).Render(w, r)
		return
	}

	// Create a login ID cookie. This will be deleted after the login flow is completed in /oidc/callback.
	loginIDCookie := &http.Cookie{
		Name:     cookieNameLoginID,
		Path:     "/",
		Value:    loginID.String(),
		Secure:   true,
		HttpOnly: true,
		// Lax mode is required because the auth flow ends in a redirect to /oidc/callback. In Strict mode, even though
		// we're being redirected to the same URL, the browser doesn't send this cookie to the callback because of the
		// redirect (making it cross-origin).
		SameSite: http.SameSiteLaxMode,
	}

	// Set the login cookie on the request. This is required so that the AuthURLHandler below is able to use it to derive
	// cookie encryption keys that are unique to this login flow and can be recreated on any cluster member (see
	// [Verifier.setRelyingParty] and https://github.com/canonical/lxd/issues/13644).
	r.AddCookie(loginIDCookie)

	// Set the login cookie on the response. This stores the salt for cookie encryption key derivation on the client,
	// for use in /oidc/callback (see [Verifier.setRelyingParty] and https://github.com/canonical/lxd/issues/13644). We
	// must set this on the response now, because the AuthURLHandler below will send a HTTP redirect.
	http.SetCookie(w, loginIDCookie)

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
	// Always delete the login_id cookie on callback. If the callback fails and this is not deleted, the same login ID is
	// resent. The login handler then sets an additional login_id cookie, and it isn't clear to the callback which one
	// to use. So it uses the first one, and fails to decrypt the state and PKCE cookies.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieNameLoginID,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
	})

	err := o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("OIDC callback failed: %w", err).Error()).Render(w, r)
		return
	}

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		err := o.startSession(r.Context(), w, tokens.IDToken, tokens.RefreshToken)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to start a new session: %w", err).Error()).Render(w, r)
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

// ensureConfig ensures that the relyingParty and accessTokenVerifier fields of the Verifier are non-nil. Additionally,
// if the given host is different from the Verifier host we reset the relyingParty to ensure the callback URL is set
// correctly.
func (o *Verifier) ensureConfig(ctx context.Context, host string) error {
	if o.relyingParty == nil || host != o.host || o.expireConfig {
		err := o.setRelyingParty(ctx, host)
		if err != nil {
			return err
		}

		o.host = host
		o.expireConfig = false
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
	//
	// If LXD is deployed behind a load balancer, it's possible that the IdP will redirect the caller to a different
	// cluster member than the member that initiated the flow. To handle this, we set a "login_id" cookie at the start
	// of the flow, then derive cookie encryption keys from that login ID from the cluster secret using HMAC (the
	// same way that we do for OIDC tokens). See https://github.com/canonical/lxd/issues/13644.
	cookieHandler := httphelper.NewRequestAwareCookieHandler(func(r *http.Request) (*securecookie.SecureCookie, error) {
		loginID, err := r.Cookie(cookieNameLoginID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get login ID cookie: %w", err)
		}

		loginUUID, err := uuid.Parse(loginID.Value)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse login ID cookie: %w", err)
		}

		// For the auth code flow, ignore the boolean which tells us to start a new session. We only care that
		// we are able to decrypt cookies when the flow is complete. These cookies don't need to persist.
		secureCookie, _, err := o.secureCookieFromSession(r.Context(), loginUUID)
		if err != nil {
			return nil, err
		}

		return secureCookie, nil
	})

	httpClient, err := o.httpClientFunc()
	if err != nil {
		return fmt.Errorf("Failed to get a HTTP client: %w", err)
	}

	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
		rp.WithHTTPClient(httpClient),
	}

	relyingParty, err := rp.NewRelyingPartyOIDC(ctx, o.issuer, o.clientID, o.clientSecret, "https://"+host+"/oidc/callback", o.scopes, options...)
	if err != nil {
		return fmt.Errorf("Failed to get OIDC relying party: %w", err)
	}

	o.relyingParty = relyingParty
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
	if o.relyingParty != nil {
		keySet = o.relyingParty.IDTokenVerifier().KeySet
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

// startSession creates a session ID, then derives encryption keys with it. The ID and refresh token are encrypted
// with the derived key, and then the session ID and encrypted ID and refresh tokens are all saved as cookies.
func (o *Verifier) startSession(ctx context.Context, w http.ResponseWriter, idToken string, refreshToken string) error {
	// Use a v7 UUID for the session ID. Encoding the current unix epoch into the ID allows us to determine if an
	// outdated secret was used for encryption key generation.
	sessionID, err := uuid.NewV7()
	if err != nil {
		return err
	}

	secureCookie, _, err := o.secureCookieFromSession(ctx, sessionID)
	if err != nil {
		return err
	}

	err = o.setCookies(w, secureCookie, sessionID, idToken, refreshToken, false)
	if err != nil {
		return err
	}

	return nil
}

// getCookies gets the sessionID, identity and refresh tokens from the request cookies and decrypts them. The
// startNewSession boolean indicates that the cookes were encrypted with keys derived from an old secret that has since
// been rotated out of use. A new session should be started so that the user is not logged out.
func (o *Verifier) getCookies(r *http.Request) (idToken string, refreshToken string, startNewSession bool, err error) {
	sessionIDCookie, err := r.Cookie(cookieNameSessionID)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return "", "", false, fmt.Errorf("Failed to get session ID cookie from request: %w", err)
	} else if sessionIDCookie == nil {
		return "", "", false, nil
	}

	sessionID, err := uuid.Parse(sessionIDCookie.Value)
	if err != nil {
		return "", "", false, fmt.Errorf("Invalid session ID cookie: %w", err)
	}

	secureCookie, startNewSession, err := o.secureCookieFromSession(r.Context(), sessionID)
	if err != nil {
		return "", "", false, fmt.Errorf("Failed to decrypt cookies: %w", err)
	}

	idTokenCookie, err := r.Cookie(cookieNameIDToken)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return "", "", false, fmt.Errorf("Failed to get ID token cookie from request: %w", err)
	}

	if idTokenCookie != nil {
		err = secureCookie.Decode(cookieNameIDToken, idTokenCookie.Value, &idToken)
		if err != nil {
			return "", "", false, fmt.Errorf("Failed to decrypt ID token cookie: %w", err)
		}
	}

	refreshTokenCookie, err := r.Cookie(cookieNameRefreshToken)
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return "", "", false, fmt.Errorf("Failed to get refresh token cookie from request: %w", err)
	}

	if refreshTokenCookie != nil {
		err = secureCookie.Decode(cookieNameRefreshToken, refreshTokenCookie.Value, &refreshToken)
		if err != nil {
			return "", "", false, fmt.Errorf("Failed to decrypt refresh token cookie: %w", err)
		}
	}

	return idToken, refreshToken, startNewSession, nil
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

// secureCookieFromSession returns a *securecookie.SecureCookie that is secure, unique to each client, and possible to
// decrypt on all cluster members. To do this we use the cluster secret as an input seed to HMAC and use the given
// sessionID [uuid.UUID] as a salt. The session ID can then be stored as a plaintext cookie so that we can regenerate
// the keys upon the next request.
//
// A boolean value is returned that indicates if a new session should be started. This is calculated by comparing the
// session start time (extracted from the v7 UUID) against the most recent core auth secret. If the session was started
// before the most recent secret was created, then the cookies are encrypted with keys derived from an out of date
// secret. We need to start a new session so that the user is not logged out when that secret is eventually deleted.
//
// Warning: Changes to this function might cause all existing OIDC users to be logged out of LXD (but not logged out of
// the IdP).
func (o *Verifier) secureCookieFromSession(ctx context.Context, sessionID uuid.UUID) (*securecookie.SecureCookie, bool, error) {
	// Get the sessionID as a binary so that we can use it as a salt.
	salt, err := sessionID.MarshalBinary()
	if err != nil {
		return nil, false, fmt.Errorf("Failed to marshal session ID as binary: %w", err)
	}

	// Get the secret used when the session was created
	sessionStartedAtSeconds, _ := sessionID.Time().UnixTime()
	secrets, err := o.secretsFunc(ctx)
	if err != nil {
		return nil, false, err
	}

	var secret cluster.AuthSecret
	var startNewSession bool
	for i := range secrets {
		// If the secret was created after the session started, skip.
		if secrets[i].CreationDate.Unix() > sessionStartedAtSeconds {
			continue
		}

		// Take the first secret that was created before the session started.
		secret = secrets[i]
		if i > 0 {
			// If this isn't the most recent secret, indicate that a new session should be started.
			startNewSession = true
		}

		break
	}

	// Derive a hash key. The hash key is used to verify the integrity of decrypted values using HMAC.
	// Use a key length of 64. This instructs the securecookie library to use HMAC-SHA512.
	cookieHashKey, err := encryption.CookieHashKey(secret.Value, salt)
	if err != nil {
		return nil, false, fmt.Errorf("Failed creating secure cookie hash key: %w", err)
	}

	// Derive a block key. The block key is used to perform AES encryption on the cookie contents.
	// Use a key length of 32. This instructs the securecookie library to use AES-256.
	cookieBlockKey, err := encryption.CookieBlockKey(secret.Value, salt)
	if err != nil {
		return nil, false, fmt.Errorf("Failed creating secure cookie block key: %w", err)
	}

	return securecookie.New(cookieHashKey, cookieBlockKey), startNewSession, nil
}

// Opts contains optional configurable fields for the Verifier.
type Opts struct {
	GroupsClaim string
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientID string, clientSecret string, scopes []string, audience string, secretsFunc func(ctx context.Context) (cluster.AuthSecrets, error), identityCache *identity.Cache, httpClientFunc func() (*http.Client, error), options *Opts) (*Verifier, error) {
	opts := &Opts{}

	if options != nil && options.GroupsClaim != "" {
		opts.GroupsClaim = options.GroupsClaim
	}

	verifier := &Verifier{
		issuer:         issuer,
		clientID:       clientID,
		clientSecret:   clientSecret,
		scopes:         scopes,
		audience:       audience,
		identityCache:  identityCache,
		groupsClaim:    opts.GroupsClaim,
		secretsFunc:    secretsFunc,
		httpClientFunc: httpClientFunc,
	}

	return verifier, nil
}
