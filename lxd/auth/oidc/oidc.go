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
	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/canonical/lxd/lxd/auth/bearer"
	"github.com/canonical/lxd/lxd/auth/encryption"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

// SessionHandler is used where session handling must call the database. Methods should only be called after a client
// session cookie or bearer token has been verified.
type SessionHandler interface {
	StartSession(r *http.Request, res AuthenticationResult, idToken string, accessToken string, refreshToken string) (sessionID *uuid.UUID, expiry *time.Time, err error)
	GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (res *AuthenticationResult, idToken string, accessToken string, refreshToken string, err error)
}

const (
	// cookieNameLoginID is used to identify a single login flow.
	cookieNameLoginID = "login_id"

	// cookieNameLXDSession references a cookie that is a JWT containing the session information.
	cookieNameLXDSession = "session"
)

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	accessTokenVerifier *op.AccessTokenVerifier
	relyingParty        rp.RelyingParty

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

// Auth extracts OIDC tokens from the request, verifies them, and returns an AuthenticationResult or an error.
func (o *Verifier) Auth(w http.ResponseWriter, r *http.Request) (*AuthenticationResult, error) {
	cookie, err := r.Cookie(cookieNameLXDSession)
	if err == nil {
		return o.verifySession(r, w, cookie.Value)
	}

	// If a bearer token is provided, it must be valid.
	bearerToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok && bearerToken != "" {
		return o.authenticateBearerToken(r, w, bearerToken)
	}

	return nil, AuthError{Err: errors.New("No credentials found")}
}

func (o *Verifier) userInfo(ctx context.Context, token string) (*oidc.UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.relyingParty.UserinfoEndpoint(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	var userinfo oidc.UserInfo
	err = httphelper.HttpRequest(o.relyingParty.HttpClient(), req, &userinfo)
	if err != nil {
		return nil, err
	}

	return &userinfo, nil
}

func (o *Verifier) authenticateBearerToken(r *http.Request, w http.ResponseWriter, bearerToken string) (*AuthenticationResult, error) {
	userAgent := r.UserAgent()
	agent, err := version.ParseClientUserAgent(userAgent)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "OIDC authentication requires cookie support. Please update your client")
	}

	if !slices.Contains(agent.Capabilities, "cookiejar") {
		return nil, api.StatusErrorf(http.StatusBadRequest, "OIDC authentication requires cookie support. Please update your client")
	}

	err = o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		return nil, err
	}

	userInfo, err := o.userInfo(r.Context(), bearerToken)
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to call user info endpoint with given access token: %w", err)}
	}

	res, err := o.getResultFromClaims(userInfo, userInfo.Claims)
	if err != nil {
		return nil, err
	}

	err = o.startSession(r, w, *res, "", "", "")
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (o *Verifier) verifySession(r *http.Request, w http.ResponseWriter, sessionToken string) (*AuthenticationResult, error) {
	sessionID, issuedAt, err := bearer.IsSessionToken(sessionToken, o.clusterUUID)
	if err != nil {
		return nil, fmt.Errorf("Invalid session cookie: %w", err)
	}

	secrets, err := o.secretsFunc(r.Context())
	if err != nil {
		return nil, err
	}

	secret, startNewSession, err := o.getSigningSecret(issuedAt.Unix(), secrets)
	if err != nil {
		return nil, fmt.Errorf("Failed to get session token signing key: %w", err)
	}

	err = bearer.VerifySessionToken(sessionToken, secret.Value, *sessionID)
	if err != nil {
		if !errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("Session token invalid: %w", err)
		}

		return o.handleExpiredSession(r, w, *sessionID)
	}

	res, idToken, accessToken, refreshToken, err := o.sessionHandler.GetIdentityBySessionID(r.Context(), *sessionID)
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, fmt.Errorf("Failed to get session information: %w", err)
		}

		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && token != "" {
			return o.authenticateBearerToken(r, w, token)
		}

		return nil, AuthError{Err: errors.New("Session revoked, please log in again")}
	}

	if startNewSession {
		err = o.startSession(r, w, *res, idToken, accessToken, refreshToken)
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

func (o *Verifier) handleExpiredSession(r *http.Request, w http.ResponseWriter, sessionID uuid.UUID) (*AuthenticationResult, error) {
	reverter := revert.New()
	defer reverter.Fail()
	reverter.Add(func() {
		o.endSession(w)
	})

	_, idToken, accessToken, refreshToken, err := o.sessionHandler.GetIdentityBySessionID(r.Context(), sessionID)
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

	// If no IDToken, this is a CLI client. Return an auth error so that the oidc headers are sent for them to obtain a new token from the IdP.
	if idToken == "" {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && token != "" {
			return o.authenticateBearerToken(r, w, token)
		}

		return nil, AuthError{Err: errors.New("Session expired, please log in again")}
	}

	err = o.ensureConfig(r.Context(), r.Host)
	if err != nil {
		return nil, err
	}

	// Reverify ID token.
	claims, err := rp.VerifyIDToken[*oidc.IDTokenClaims](r.Context(), idToken, o.relyingParty.IDTokenVerifier())
	if err == nil {
		res, err := o.getResultFromClaims(claims, claims.Claims)
		if err != nil {
			return nil, err
		}

		err = o.startSession(r, w, *res, idToken, accessToken, refreshToken)
		if err != nil {
			return nil, err
		}

		return res, nil
	}

	// If ID token verification failed try refreshing the token.
	if refreshToken == "" {
		return nil, errors.New("Cannot refresh session")
	}

	tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](r.Context(), o.relyingParty, refreshToken, "", "")
	if err != nil {
		return nil, AuthError{Err: fmt.Errorf("Failed to refresh ID tokens: %w", err)}
	}

	// Verify the refreshed ID token.
	claims, err = rp.VerifyIDToken[*oidc.IDTokenClaims](r.Context(), tokens.IDToken, o.relyingParty.IDTokenVerifier())
	if err != nil {
		return nil, err
	}

	res, err := o.getResultFromClaims(claims, claims.Claims)
	if err != nil {
		return nil, err
	}

	err = o.startSession(r, w, *res, tokens.IDToken, tokens.AccessToken, tokens.RefreshToken)
	if err != nil {
		return nil, err
	}

	reverter.Success()
	return res, err
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
	// Just delete the session cookie, the session itself will be cleaned up later.
	// TODO: Immediately delete session so that it doesn't show up via API anymore.
	o.endSession(w)
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

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, relyingParty rp.RelyingParty) {
		claims, err := rp.VerifyIDToken[*oidc.IDTokenClaims](r.Context(), tokens.IDToken, o.relyingParty.IDTokenVerifier())
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to start a new session: %w", err).Error()).Render(w, r)
			return
		}

		res, err := o.getResultFromClaims(claims, claims.Claims)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to start a new session: %w", err).Error()).Render(w, r)
			return
		}

		err = o.startSession(r, w, *res, tokens.IDToken, tokens.AccessToken, tokens.RefreshToken)
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
// or the session cookie.
func (*Verifier) IsRequest(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}

	c, err := r.Cookie(cookieNameLXDSession)
	return err == nil && c != nil && c.Value != ""
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
		secureCookie, err := o.getSecureCookie(r.Context(), loginUUID)
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
func (o *Verifier) startSession(r *http.Request, w http.ResponseWriter, res AuthenticationResult, idToken string, accessToken string, refreshToken string) error {
	secrets, err := o.secretsFunc(r.Context())
	if err != nil {
		return err
	}

	sessionID, expiry, err := o.sessionHandler.StartSession(r, res, idToken, accessToken, refreshToken)
	if err != nil {
		return err
	}

	token, err := encryption.GetSessionToken(secrets[0].Value, *sessionID, o.clusterUUID, *expiry)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieNameLXDSession,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Value:    token,
		// Cookie has to have an expiry for the persistent cookie jar to save it.
		// This sets the cookie to expire one month after the token expiry.
		// This is so that the browser will still send the expired token
		Expires: expiry.AddDate(0, 1, 0),
	})

	return nil
}

func (o *Verifier) endSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieNameLXDSession,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
	})
}

func (o *Verifier) getSigningSecret(sessionStartedSeconds int64, secrets cluster.AuthSecrets) (*cluster.AuthSecret, bool, error) {
	var secret cluster.AuthSecret
	var startNewSession bool
	for i := range secrets {
		// If the secret was created after the session started, skip.
		if secrets[i].CreationDate.Unix() > sessionStartedSeconds {
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

	// If the found secret was created after the session started, return an error
	if secret.CreationDate.Unix() > sessionStartedSeconds {
		return nil, false, errors.New("Signing key out of date")
	}

	return &secret, startNewSession, nil
}

// getSecureCookie returns a *securecookie.SecureCookie that is secure, unique to each client, and possible to
// decrypt on all cluster members. To do this we use the cluster secret as an input seed to HMAC and use the given
// [uuid.UUID] as a salt. The salt must be set as a plaintext cookie alongside the encrypted cookies so that they can be
// decrypted. This is used in the relying party cookie handler to encrypt cookies used for the UI auth flow.
func (o *Verifier) getSecureCookie(ctx context.Context, v7UUID uuid.UUID) (*securecookie.SecureCookie, error) {
	// Get the uuid as a binary so that we can use it as a salt.
	salt, err := v7UUID.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal session ID as binary: %w", err)
	}

	// Get the secret used when the given uuid was created
	uuidCreatedAt, _ := v7UUID.Time().UnixTime()
	secrets, err := o.secretsFunc(ctx)
	if err != nil {
		return nil, err
	}

	secret, _, err := o.getSigningSecret(uuidCreatedAt, secrets)
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
func NewVerifier(issuer string, clientID string, clientSecret string, scopes []string, audience string, groupsClaim string, clusterUUID string, secretsFunc func(ctx context.Context) (cluster.AuthSecrets, error), httpClientFunc func() (*http.Client, error), sessionHandler SessionHandler) (*Verifier, error) {
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

	return verifier, nil
}
