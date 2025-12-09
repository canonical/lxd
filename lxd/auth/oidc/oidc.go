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

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/canonical/lxd/lxd/auth/bearer"
	"github.com/canonical/lxd/lxd/auth/encryption"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// SessionHandler is used where session handling must call the database.
//
// It is important that these methods are only called after the caller has successfully authenticated via session token
// or via the IdP. This is to enforce that unauthenticated callers cannot DoS the database by sending bogus tokens.
type SessionHandler interface {
	StartSession(r *http.Request, res AuthenticationResult, tokens *oidc.Tokens[*oidc.IDTokenClaims], expiryOverride *time.Time) (sessionID *uuid.UUID, expiry *time.Time, err error)
	GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (res *AuthenticationResult, tokens *oidc.Tokens[*oidc.IDTokenClaims], sessionExpiry *time.Time, err error)
	DeleteSession(ctx context.Context, sessionID uuid.UUID) error
}

const (
	// cookieNameLoginID is used to identify a single login flow.
	cookieNameLoginID = "login_id"

	// cookieNameSession references a cookie that is a JWT containing the session information.
	cookieNameSession = "session"

	// SessionCookieExpiryBuffer denotes the time taken for a session cookie to expire AFTER the token within the cookie expires.
	// This buffer is necessary so that clients continue to send the session cookie after the token expires, so that we can
	// refresh their session by contacting the IdP.
	SessionCookieExpiryBuffer = time.Hour * 24 * 7
)

// Verifier holds all information needed to verify and manage OIDC logins and sessions.
type Verifier struct {
	relyingParty rp.RelyingParty

	clientID       string
	clientSecret   string
	issuer         string
	scopes         []string
	audience       string
	groupsClaim    string
	secretsFunc    func(ctx context.Context) (cluster.AuthSecrets, error)
	httpClientFunc func() (*http.Client, error)
	clusterUUID    string
	sessionHandler SessionHandler

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

// Auth checks if a session cookie is present and tries to verify it. Otherwise, it checks if a bearer token was sent
// and verifies that instead (for the CLI).
func (o *Verifier) Auth(w http.ResponseWriter, r *http.Request) (*AuthenticationResult, error) {
	cookie, err := r.Cookie(cookieNameSession)
	if err == nil {
		// Session cookie exists, verify it.
		res, err := o.verifySession(r, w, cookie.Value)
		if err != nil {
			// If anything fails, delete the cookie.
			o.deleteSessionCookie(w)
			return nil, err
		}

		return res, nil
	}

	// If a bearer token is provided, it must be valid.
	bearerToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok && bearerToken != "" {
		return o.authenticateBearerToken(r, w, bearerToken)
	}

	// Return an AuthError, instructing the daemon to write OIDC headers for the client to use to obtain a token via the
	// device flow.
	return nil, AuthError{Err: errors.New("No credentials found")}
}

// userInfo calls the /userinfo endpoint of the configured issuer with the given access token.
// Note that this implementation is required because the zitadel library implementation asserts that the endpoint returns
// a value with a specific subject, which we can't do for opaque tokens.
func (o *Verifier) userInfo(ctx context.Context, token string) (*oidc.UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.relyingParty.UserinfoEndpoint(), nil)
	if err != nil {
		return nil, err
	}

	// We only expect bearer tokens.
	req.Header.Set("Authorization", "Bearer "+token)
	var userinfo oidc.UserInfo
	err = httphelper.HttpRequest(o.relyingParty.HttpClient(), req, &userinfo)
	if err != nil {
		return nil, fmt.Errorf("Failed to get user info: %w", err)
	}

	return &userinfo, nil
}

// authenticateBearerToken calls the /userinfo endpoint with the given token to retrieve information about the user. Then
// starts a new session.
func (o *Verifier) authenticateBearerToken(r *http.Request, w http.ResponseWriter, accessToken string) (*AuthenticationResult, error) {
	agent, err := request.ParseUserAgent(r.UserAgent())
	if err != nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Failed to parse user agent to determine persistent cookie feature: %w", err)
	}

	if !slices.Contains(agent.Features, api.ClientFeatureCookieJar) {
		return nil, api.StatusErrorf(http.StatusBadRequest, "OIDC authentication requires persistent cookie support. Please update your client")
	}

	err = o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		return nil, fmt.Errorf("Could not verify OIDC configuration: %w", err)
	}

	userInfo, err := o.userInfo(r.Context(), accessToken)
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to call user info endpoint with given access token: %w", err)}
	}

	res, err := o.getResultFromClaims(userInfo, userInfo.Claims)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse user info response: %w", err)
	}

	err = o.startSession(r, w, *res, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to start a new session: %w", err)
	}

	return res, nil
}

// verifySession verifies the given session token in-memory, then gets the session details via the [SessionHandler] to
// return an [AuthenticationResult]. If the session token is expired but otherwise valid, the login will be re-confirmed
// with the IdP and a new session will be started. If the session token was signed by a key derived from an out-of-date
// cluster secret, a new session will be started without verifying the login, but the expiry will be the same as the
// expiry of the existing session.
func (o *Verifier) verifySession(r *http.Request, w http.ResponseWriter, sessionToken string) (*AuthenticationResult, error) {
	// Verify the token.
	sessionID, startNewSession, err := o.verifySessionToken(r.Context(), sessionToken)
	if err != nil {
		if !errors.Is(err, jwt.ErrTokenExpired) {
			// For any error other than expiry, the token is invalid (e.g. tampered with).
			return nil, fmt.Errorf("Session token invalid: %w", err)
		}

		return o.handleExpiredSession(r, w, *sessionID)
	}

	// Get the session details.
	res, tokens, expiry, err := o.sessionHandler.GetIdentityBySessionID(r.Context(), *sessionID)
	if err != nil {
		// Unexpected error.
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, fmt.Errorf("Failed to get session information: %w", err)
		}

		// If not found, check if the caller already sent a bearer token to reverify.
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && token != "" {
			return o.authenticateBearerToken(r, w, token)
		}

		// Otherwise, the session was revoked.
		return nil, AuthError{Err: errors.New("Session revoked, please log in again")}
	}

	// If we signed the session with a key derived from an old cluster secret, start a new session and override the expiry.
	if startNewSession {
		err = o.startSession(r, w, *res, tokens, expiry)
		if err != nil {
			return nil, fmt.Errorf("Failed to start a new session: %w", err)
		}

		// Delete the old session from the database.
		err = o.sessionHandler.DeleteSession(r.Context(), *sessionID)
		if err != nil {
			logger.Warn("Failed to delete session with stale signing key from database", logger.Ctx{"session_uuid": sessionID.String(), "err": err})
		}
	}

	return res, nil
}

// handleExpiredSession attempts to start a new session based on details saved in an existing session. If no tokens are
// saved in the session, it checks if the caller sent their access token as an authorization header (CLI). If tokens are
// present, the identity is reverified using the access token. If this fails, the refresh token is used (if present) to
// get a new set of tokens.
func (o *Verifier) handleExpiredSession(r *http.Request, w http.ResponseWriter, sessionID uuid.UUID) (*AuthenticationResult, error) {
	defer func() {
		// Always delete the old session from the database.
		err := o.sessionHandler.DeleteSession(r.Context(), sessionID)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			logger.Warn("Failed to delete session with stale signing key from database", logger.Ctx{"session_uuid": sessionID.String(), "err": err})
		}
	}()

	_, tokens, _, err := o.sessionHandler.GetIdentityBySessionID(r.Context(), sessionID)
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, fmt.Errorf("Failed to get session information: %w", err)
		}

		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && token != "" {
			return o.authenticateBearerToken(r, w, token)
		}

		return nil, AuthError{Err: errors.New("Session expired or revoked, please log in again")}
	}

	// If no access token in session. Return an auth error so that the oidc headers are sent for them to obtain a new token from the IdP.
	if tokens.AccessToken == "" {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && token != "" {
			return o.authenticateBearerToken(r, w, token)
		}

		return nil, AuthError{Err: errors.New("Session expired, please log in again")}
	}

	err = o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		return nil, fmt.Errorf("Failed to verify OIDC configuration: %w", err)
	}

	// Reverify access token.
	userInfo, err := o.userInfo(r.Context(), tokens.AccessToken)
	if err == nil {
		res, err := o.getResultFromClaims(userInfo, userInfo.Claims)
		if err != nil {
			return nil, err
		}

		err = o.startSession(r, w, *res, tokens, nil)
		if err != nil {
			return nil, err
		}

		return res, nil
	}

	// If access token verification failed try refreshing the token.
	if tokens.RefreshToken == "" {
		return nil, errors.New("Cannot refresh session")
	}

	refreshedTokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](r.Context(), o.relyingParty, tokens.RefreshToken, "", "")
	if err != nil {
		return nil, fmt.Errorf("Failed to refresh ID tokens: %w", err)
	}

	// Verify the refreshed access token.
	userInfo, err = o.userInfo(r.Context(), refreshedTokens.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("Failed to verify refreshed access token: %w", err)
	}

	res, err := o.getResultFromClaims(userInfo, userInfo.Claims)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse user info response: %w", err)
	}

	err = o.startSession(r, w, *res, refreshedTokens, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to start a new session: %w", err)
	}

	return res, err
}

// verifySessionToken verifies the given session token. If the token is valid, it returns the session ID and a boolean
// indicating whether the token was signed by a key derived from an out-of-date cluster secret.
func (o *Verifier) verifySessionToken(ctx context.Context, sessionToken string) (sessionID *uuid.UUID, staleSigningKey bool, err error) {
	// Check the cookie contents are as expected. We need to do this to get the session ID, this gives us a session
	// creation date from which we can determine which cluster secret was used to create the token signing key.
	sessionID, issuedAt, err := bearer.IsSessionToken(sessionToken, o.clusterUUID)
	if err != nil {
		return nil, false, fmt.Errorf("Invalid session token: %w", err)
	}

	// Find the secret that was used to obtain the signing key.
	secret, staleSigningKey, err := o.getSecretFromUsedAtTime(ctx, issuedAt.Unix())
	if err != nil {
		return nil, false, fmt.Errorf("Failed to get session token signing key: %w", err)
	}

	// Verify the token.
	err = bearer.VerifySessionToken(sessionToken, secret.Value, *sessionID)
	if err != nil {
		return nil, false, fmt.Errorf("Session token is not valid: %w", err)
	}

	return sessionID, staleSigningKey, nil
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

// Logout always deletes the session cookie. If the caller is logged in with a valid session cookie, then that session
// deleted from the database.
func (o *Verifier) Logout(w http.ResponseWriter, r *http.Request) {
	defer func() {
		// Always delete session cookie and redirect to the login page.
		o.deleteSessionCookie(w)
		http.Redirect(w, r, "/ui/login/", http.StatusFound)
	}()

	sessionCookie, err := r.Cookie(cookieNameSession)
	if err != nil {
		// Not logged in.
		return
	}

	sessionID, _, err := o.verifySessionToken(r.Context(), sessionCookie.Value)
	if err != nil {
		// Not logged in.
		return
	}

	// Delete the current session
	err = o.sessionHandler.DeleteSession(r.Context(), *sessionID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		logger.Warn("Unable to delete session after OIDC logout", logger.Ctx{"session_uuid": sessionID.String(), "err": err})
	}
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

	callback := func(ctx context.Context, tokens *oidc.Tokens[*oidc.IDTokenClaims]) error {
		userInfo, err := o.userInfo(r.Context(), tokens.AccessToken)
		if err != nil {
			return fmt.Errorf("Failed to get caller identity: %w", err)
		}

		res, err := o.getResultFromClaims(userInfo, userInfo.Claims)
		if err != nil {
			return fmt.Errorf("Failed to parse user info response: %w", err)
		}

		err = o.startSession(r, w, *res, tokens, nil)
		if err != nil {
			return fmt.Errorf("Failed to start a new session: %w", err)
		}

		return nil
	}

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		err = callback(r.Context(), tokens)
		if err != nil {
			_ = response.SmartError(fmt.Errorf("Failed to run OIDC callback: %w", err)).Render(w, r)
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

	_, err := r.Cookie(cookieNameSession)
	return err == nil
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
		secureCookie, err := o.secureCookieFromV7UUID(r.Context(), loginUUID)
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

// startSession starts a new session via the [SessionHandler]. It then issues a token and sets it as a cookie for future
// authentication.
func (o *Verifier) startSession(r *http.Request, w http.ResponseWriter, res AuthenticationResult, tokens *oidc.Tokens[*oidc.IDTokenClaims], expiryOverride *time.Time) error {
	secrets, err := o.secretsFunc(r.Context())
	if err != nil {
		return err
	}

	sessionID, expiry, err := o.sessionHandler.StartSession(r, res, tokens, expiryOverride)
	if err != nil {
		return err
	}

	token, err := encryption.GetOIDCSessionToken(secrets[0].Value, *sessionID, o.clusterUUID, *expiry)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieNameSession,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Value:    token,
		// This sets the cookie to expire [SessionCookieExpiryBuffer] after the token within the cookie expires.
		// This allows sessions to be refreshed.
		Expires: expiry.Add(SessionCookieExpiryBuffer),
	})

	return nil
}

// deleteSessionCookie deletes the session cookie.
func (o *Verifier) deleteSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieNameSession,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
	})
}

// getSecretFromUsedAtTime returns the secret that should have been used to encrypt a cookie or sign a token at the
// given unix time in seconds. A "needsRefresh" boolean is returned to indicate if the returned secret is not the most
// recent, this prompts the Verifier to refresh the session token, so that it doesn't become unverifiable.
func (o *Verifier) getSecretFromUsedAtTime(ctx context.Context, usedAtTimeUnixSeconds int64) (secret *cluster.AuthSecret, needsRefresh bool, err error) {
	secrets, err := o.secretsFunc(ctx)
	if err != nil {
		return nil, false, err
	}

	for i := range secrets {
		// If this secret was created after the used at, skip.
		if secrets[i].CreationDate.Unix() > usedAtTimeUnixSeconds {
			continue
		}

		// Take the first secret that was created before the used at time.
		secret = &secrets[i]
		if i > 0 {
			// If this isn't the most recent secret, indicate that the newest secret should be used next time.
			needsRefresh = true
		}

		break
	}

	// If the found secret was created after the session started, return an error
	if secret == nil {
		return nil, false, errors.New("No secrets were in date")
	}

	return secret, needsRefresh, nil
}

// secureCookieFromV7UUID returns a *securecookie.SecureCookie that is secure, unique to each client, and possible to
// decrypt on all cluster members. To do this we use the cluster secret as an input seed to HMAC and use the given
// [uuid.UUID] as a salt. The UUID must be stored as a plaintext cookie so that we can regenerate the keys upon the
// next request. The UUID must be a v7 UUID so that we are able to determine the cluster secret that was used as a seed
// when decrypting.
func (o *Verifier) secureCookieFromV7UUID(ctx context.Context, sessionID uuid.UUID) (*securecookie.SecureCookie, error) {
	// Get the sessionID as a binary so that we can use it as a salt.
	salt, err := sessionID.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal session ID as binary: %w", err)
	}

	// Get the secret used when the session was created
	sessionStartedAtSeconds, _ := sessionID.Time().UnixTime()
	secret, _, err := o.getSecretFromUsedAtTime(ctx, sessionStartedAtSeconds)
	if err != nil {
		return nil, err
	}

	// Derive a hash key. The hash key is used to verify the integrity of decrypted values using HMAC.
	// Use a key length of 64. This instructs the securecookie library to use HMAC-SHA512.
	cookieHashKey, err := encryption.CookieHashKey(secret.Value, salt)
	if err != nil {
		return nil, fmt.Errorf("Failed creating secure cookie hash key: %w", err)
	}

	// Derive a block key. The block key is used to perform AES encryption on the cookie contents.
	// Use a key length of 32. This instructs the securecookie library to use AES-256.
	cookieBlockKey, err := encryption.CookieBlockKey(secret.Value, salt)
	if err != nil {
		return nil, fmt.Errorf("Failed creating secure cookie block key: %w", err)
	}

	return securecookie.New(cookieHashKey, cookieBlockKey), nil
}

// NewVerifier returns a Verifier.
func NewVerifier(ctx context.Context, issuer string, clientID string, clientSecret string, scopes []string, audience string, groupsClaim string, clusterUUID string, networkAddress string, secretsFunc func(ctx context.Context) (cluster.AuthSecrets, error), httpClientFunc func() (*http.Client, error), sessionHandler SessionHandler) (*Verifier, error) {
	verifier := &Verifier{
		issuer:         issuer,
		clientID:       clientID,
		clientSecret:   clientSecret,
		scopes:         scopes,
		audience:       audience,
		groupsClaim:    groupsClaim,
		clusterUUID:    clusterUUID,
		secretsFunc:    secretsFunc,
		httpClientFunc: httpClientFunc,
		sessionHandler: sessionHandler,
	}

	// Ensure configuration is valid with daemon's network address.
	err := verifier.ensureConfig(ctx, networkAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed to ensure new verifier's configuration: %w", err)
	}

	return verifier, nil
}
