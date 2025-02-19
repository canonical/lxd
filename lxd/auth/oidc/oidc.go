package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/canonical/lxd/lxd/db/cluster/secret"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// SessionHandler is used where session handling must call the database. Methods should only be called after a client
// session cookie or bearer token has been verified.
type SessionHandler interface {
	StartSession(ctx context.Context, r *http.Request, res AuthenticationResult) error
	GetIdentityBySessionID(ctx context.Context, sessionID uuid.UUID) (info *api.IdentityInfo, sessionRevoked bool, refreshToken string, err error)
}

type relyingParty struct {
	rp.RelyingParty
	outdatedAt time.Time
}

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	accessTokenVerifier *op.AccessTokenVerifier
	relyingParties      []relyingParty

	clientID               string
	issuer                 string
	scopes                 []string
	audience               string
	groupsClaim            string
	httpClientFunc         func() (*http.Client, error)
	sessionHandler         SessionHandler
	clusterCertFingerprint func() string
	clusterSecret          func(ctx context.Context) (secret.Secret, bool, error)
	sessionLifetime        time.Duration

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
func (o *Verifier) Auth(ctx context.Context, w http.ResponseWriter, r *http.Request) (*api.IdentityInfo, error) {
	err := o.ensureConfig(ctx, r.Host)
	if err != nil {
		return nil, fmt.Errorf("Authorization failed: %w", err)
	}

	sessionJWT, err := getSessionCookie(r)
	if err == nil && sessionJWT != "" {
		username, err := o.verifySession(ctx, r, w, sessionJWT)
		if err != nil {
			deleteSessionCookie(w)
			return nil, AuthError{Err: fmt.Errorf("Failed to verify session: %w", err)}
		}

		return username, nil
	}

	authorizationHeader := r.Header.Get("Authorization")
	if authorizationHeader != "" {
		bearer, accessToken, ok := strings.Cut(authorizationHeader, " ")
		if !ok || bearer != "Bearer" {
			// When a command line client wants to authenticate, it needs to set the Authorization HTTP header like this:
			//    Authorization Bearer <access_token>
			return nil, AuthError{fmt.Errorf("Bad authorization token, expected a Bearer token")}
		}

		username, err := o.verifyAccessToken(ctx, w, r, accessToken)
		if err != nil {
			return nil, AuthError{Err: fmt.Errorf("Failed to verify bearer token: %w", err)}
		}

		return username, nil
	}

	return nil, AuthError{Err: errors.New("No session cookie or access token found")}
}

func (o *Verifier) verifySession(ctx context.Context, r *http.Request, w http.ResponseWriter, sessionToken string) (*api.IdentityInfo, error) {
	var unverifiedClaims jwt.RegisteredClaims
	_, _, err := jwt.NewParser().ParseUnverified(sessionToken, &unverifiedClaims)
	if err != nil {
		// Token is not a JWT, and therefore is not a LXD token.
		return nil, fmt.Errorf("Session cookie is not a JWT")
	}

	issAndAud := "lxd:" + o.clusterCertFingerprint()
	if unverifiedClaims.Issuer != issAndAud || len(unverifiedClaims.Audience) != 1 || unverifiedClaims.Audience[0] != issAndAud {
		// Token was not issued by us, or has empty or multivalued audience, or the intended audience is not us.
		return nil, fmt.Errorf("Session cookie has invalid issuer or audience")
	}

	sessionID, err := uuid.Parse(unverifiedClaims.Subject)
	if err != nil {
		// Token has invalid session ID
		return nil, fmt.Errorf("Session cookie has invalid subject")
	}

	key, err := sessionKey(ctx, o.clusterSecret, sessionID)
	if err != nil {
		return nil, fmt.Errorf("Failed to generate session key: %w", err)
	}

	keyFunc := func(token *jwt.Token) (any, error) {
		return key, nil
	}

	parserOptions := []jwt.ParserOption{
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithIssuer(issAndAud),
		jwt.WithAudience(issAndAud),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS512.Name}),
		jwt.WithLeeway(5 * time.Minute),
	}

	var claims jwt.RegisteredClaims
	_, err = jwt.NewParser(parserOptions...).ParseWithClaims(sessionToken, &claims, keyFunc)
	if err != nil && !errors.Is(err, jwt.ErrTokenExpired) {
		return nil, fmt.Errorf("Invalid session: %w", err)
	} else if err != nil {
		// Signature was verified (claims are verified after signature) but session has timed out.
		// Try to refresh the session, otherwise fail.
		newSessionID, err := o.refreshSession(ctx, r, w, sessionID)
		if err != nil {
			return nil, fmt.Errorf("Failed to refresh session: %w", err)
		}

		sessionID = *newSessionID
	}

	id, revoked, _, err := o.sessionHandler.GetIdentityBySessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("Failed to check uesr infomatation: %w", err)
	}

	if revoked {
		return nil, AuthError{Err: errors.New("Session revoked")}
	}

	return id, nil
}

func (o *Verifier) refreshSession(ctx context.Context, r *http.Request, w http.ResponseWriter, currentSessionID uuid.UUID) (*uuid.UUID, error) {
	_, revoked, refreshToken, err := o.sessionHandler.GetIdentityBySessionID(ctx, currentSessionID)
	if err != nil {
		return nil, fmt.Errorf("Failed to get a refresh token: %w", err)
	}

	if revoked {
		return nil, errors.New("Session revoked")
	} else if refreshToken == "" {
		return nil, errors.New("No refresh token available")
	}

	// If ID token verification failed (or it wasn't provided, try refreshing the token).
	tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, o.relyingParties[0], refreshToken, "", "")
	if err != nil {
		return nil, fmt.Errorf("Failed to refresh ID token: %w", err)
	}

	res, err := o.getResultFromClaims(tokens.IDTokenClaims, tokens.IDTokenClaims.Claims)
	if err != nil {
		return nil, fmt.Errorf("Failed getting login information from refreshed token: %w", err)
	}

	if tokens.RefreshToken != "" {
		res.RefreshToken = tokens.RefreshToken
	} else {
		res.RefreshToken = refreshToken
	}

	res.SessionID = uuid.New()

	reverter := revert.New()
	defer reverter.Fail()

	err = setSessionCookie(ctx, w, o.clusterSecret, o.clusterCertFingerprint, res.SessionID, o.sessionLifetime)
	if err != nil {
		return nil, fmt.Errorf("Failed to set session cookie: %w", err)
	}

	reverter.Add(func() {
		deleteSessionCookie(w)
	})

	err = o.sessionHandler.StartSession(ctx, r, *res)
	if err != nil {
		return nil, fmt.Errorf("Failed to store login information from refreshed token: %w", err)
	}

	reverter.Success()
	return &res.SessionID, nil
}

// verifyAccessToken verifies the access token and checks that the configured audience is present the in access
// token claims. We do not attempt to refresh access tokens as this is performed client side. The access token subject
// is returned if no error occurs.
func (o *Verifier) verifyAccessToken(ctx context.Context, w http.ResponseWriter, req *http.Request, accessToken string) (*api.IdentityInfo, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, o.relyingParties[0].UserinfoEndpoint(), nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to create a user info request: %w", err)
	}

	r.Header.Set("Authorization", "Bearer "+accessToken)
	var userInfo oidc.UserInfo
	err = httphelper.HttpRequest(o.relyingParties[0].HttpClient(), r, &userInfo)
	if err != nil {
		return nil, fmt.Errorf("Failed to call user info endpoint with given access token: %w", err)
	}

	res, err := o.getResultFromClaims(&userInfo, userInfo.Claims)
	if err != nil {
		return nil, err
	}

	res.SessionID = uuid.New()
	err = setSessionCookie(ctx, w, o.clusterSecret, o.clusterCertFingerprint, res.SessionID, o.sessionLifetime)
	if err != nil {
		return nil, fmt.Errorf("Failed to start session: %w", err)
	}

	reverter := revert.New()
	defer reverter.Fail()
	reverter.Add(func() {
		deleteSessionCookie(w)
	})

	err = o.sessionHandler.StartSession(ctx, r, *res)
	if err != nil {
		return nil, fmt.Errorf("Failed to store authentication result: %w", err)
	}

	id, revoked, _, err := o.sessionHandler.GetIdentityBySessionID(ctx, res.SessionID)
	if err != nil {
		return nil, fmt.Errorf("Failed to check uesr infomatation: %w", err)
	}

	if revoked {
		return nil, AuthError{Err: errors.New("Session revoked")}
	}

	reverter.Success()
	return id, nil
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
	deleteSessionCookie(w)
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

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		res, err := o.getResultFromClaims(tokens.IDTokenClaims, tokens.IDTokenClaims.Claims)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed getting login information: %w", err).Error()).Render(w, r)
			return
		}

		res.RefreshToken = tokens.RefreshToken
		res.SessionID = uuid.New()

		reverter := revert.New()
		defer reverter.Fail()

		err = setSessionCookie(r.Context(), w, o.clusterSecret, o.clusterCertFingerprint, res.SessionID, o.sessionLifetime)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to set session cookie: %w", err).Error()).Render(w, r)
			return
		}

		reverter.Add(func() {
			deleteSessionCookie(w)
		})

		err = o.sessionHandler.StartSession(r.Context(), r, *res)
		if err != nil {
			_ = response.ErrorResponse(http.StatusInternalServerError, fmt.Errorf("Failed to store login result: %w", err).Error()).Render(w, r)
			return
		}

		// Send to the UI.
		// NOTE: Once the UI does the redirection on its own, we may be able to use the referer here instead.
		reverter.Success()
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

	hash, block, err := hashAndBlockKeys(clusterSecret)
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

// Opts contains optional configurable fields for the Verifier.
type Opts struct {
	GroupsClaim string
	Host        string
	Ctx         context.Context
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientID string, scopes []string, audience string, sessionLifetime time.Duration, clusterSecret func(ctx context.Context) (secret.Secret, bool, error), clusterCertFingerprint func() string, httpClientFunc func() (*http.Client, error), sessionHandler SessionHandler, options *Opts) (*Verifier, error) {
	opts := &Opts{}

	if options != nil && options.GroupsClaim != "" {
		opts.GroupsClaim = options.GroupsClaim
	}

	verifier := &Verifier{
		issuer:                 issuer,
		clientID:               clientID,
		scopes:                 scopes,
		audience:               audience,
		clusterCertFingerprint: clusterCertFingerprint,
		groupsClaim:            opts.GroupsClaim,
		clusterSecret:          clusterSecret,
		httpClientFunc:         httpClientFunc,
		sessionHandler:         sessionHandler,
		sessionLifetime:        sessionLifetime,
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
