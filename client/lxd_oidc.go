package lxd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"golang.org/x/oauth2"
)

// setupOIDCClient initializes the OIDC (OpenID Connect) client with given tokens if it hasn't been set up already.
// It also assigns the protocol's http client to the oidcClient's httpClient.
func (r *ProtocolLXD) setupOIDCClient(token *oidc.Tokens[*oidc.IDTokenClaims]) {
	if r.oidcClient != nil {
		return
	}

	r.oidcClient = newOIDCClient(token)
	r.oidcClient.httpClient = r.http
}

// oidcTransport is a custom HTTP transport that injects the audience field into requests directed at the device
// authorization endpoint.
type oidcTransport struct {
	deviceAuthorizationEndpoint string
	audience                    string
}

// RoundTrip the oidcTransport implementation of http.RoundTripper. It modifies the request, adds the audience parameter
// if appropriate, and sends it along.
func (o *oidcTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Don't modify the request if it's not to the device authorization endpoint, or there are no
	// URL parameters which need to be set.
	if r.URL.String() != o.deviceAuthorizationEndpoint || len(o.audience) == 0 {
		return http.DefaultTransport.RoundTrip(r)
	}

	err := r.ParseForm()
	if err != nil {
		return nil, err
	}

	if o.audience != "" {
		r.Form.Add("audience", o.audience)
	}

	// Update the body with the new URL parameters.
	body := r.Form.Encode()
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))

	return http.DefaultTransport.RoundTrip(r)
}

var errRefreshAccessToken = fmt.Errorf("Failed refreshing access token")
var oidcScopes = []string{oidc.ScopeOpenID, oidc.ScopeOfflineAccess, oidc.ScopeEmail, oidc.ScopeProfile}

type oidcClient struct {
	httpClient    *http.Client
	oidcTransport *oidcTransport
	tokens        *oidc.Tokens[*oidc.IDTokenClaims]
}

// oidcClient is a structure encapsulating an HTTP client, OIDC transport, and a token for OpenID Connect (OIDC) operations.
// newOIDCClient constructs a new oidcClient, ensuring the token field is non-nil to prevent panics during authentication.
func newOIDCClient(tokens *oidc.Tokens[*oidc.IDTokenClaims]) *oidcClient {
	client := oidcClient{
		tokens:        tokens,
		httpClient:    &http.Client{},
		oidcTransport: &oidcTransport{},
	}

	// Ensure client.tokens is never nil otherwise authenticate() will panic.
	if client.tokens == nil {
		client.tokens = &oidc.Tokens[*oidc.IDTokenClaims]{}
	}

	return &client
}

// setHeaders sets the `Authorization: Bearer <token>` header on the request.
// It parses the tokens locally to send either the ID token or Access token if we know the server will be unable to
// accept it. This is due to Entra ID issuing access tokens from a different issuer that are intended for the Microsoft
// graph API. These access tokens contain a "nonce" header that prevent them from being validated by standard OIDC libraries.
func (o *oidcClient) setHeaders(r *http.Request) {
	// Always set the Authorization header. This tells the server that we're trying to authenticate with OIDC.
	// The server then knows to return OIDC configuration headers to the client, which we need to perform the device flow.
	var token string
	defer func() {
		r.Header.Set("Authorization", "Bearer "+token)
	}()

	if o.tokens == nil || o.tokens.Token == nil {
		return
	}

	// We should really be using the access token, so we'll set that as a default.
	token = o.tokens.AccessToken

	// If there's no ID token, we have to send the access token.
	if o.tokens.IDToken == "" {
		return
	}

	// Return now if there is nothing to send.
	if o.tokens.AccessToken == "" {
		return
	}

	_, idTokenPayload, err := getTokenHeaderAndPayload(o.tokens.IDToken)
	if err != nil {
		return
	}

	// If the ID token doesn't contain an email, the server cannot use it.
	if getMapValue[string](idTokenPayload, "email") == "" {
		return
	}

	accessTokenHeader, accessTokenPayload, err := getTokenHeaderAndPayload(o.tokens.AccessToken)
	if err != nil {
		return
	}

	// If the access token contains a "nonce" key, we can't validate it on the server side.
	// Send the ID token instead.
	if getMapValue[string](accessTokenHeader, "nonce") != "" {
		token = o.tokens.IDToken
		return
	}

	// If the issuer does not match, we can't validate the access token on the server side.
	// Send the ID token instead.
	if getMapValue[string](accessTokenPayload, "iss") != getMapValue[string](idTokenPayload, "iss") {
		token = o.tokens.IDToken
		return
	}

	// If the subject does not match, we'll conflict with the subject that is set when using the UI.
	// Send the ID token instead.
	if getMapValue[string](accessTokenPayload, "sub") != getMapValue[string](idTokenPayload, "sub") {
		token = o.tokens.IDToken
		return
	}
}

// getMapValue gets the value with the given key, from the given map, of the given type.
func getMapValue[T any](m map[string]any, key string) T {
	var empty T
	valueAny, ok := m[key]
	if !ok {
		return empty
	}

	value, ok := valueAny.(T)
	if !ok {
		return empty
	}

	return value
}

// getTokenHeaderAndPayload parses a JWT and returns the header and payload as maps.
func getTokenHeaderAndPayload(token string) (header map[string]any, payload map[string]any, err error) {
	getMap := func(inBase64 string) (map[string]any, error) {
		inJSON, err := base64.RawURLEncoding.DecodeString(inBase64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse access token payload: %w", err)
		}

		var inMap map[string]any
		err = json.Unmarshal(inJSON, &inMap)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse access token payload: %w", err)
		}

		return inMap, nil
	}

	fields := strings.Split(token, ".")
	if len(fields) != 3 {
		return nil, nil, fmt.Errorf("Failed to parse access token, expected 3 fields delimited by a '.'")
	}

	header, err = getMap(fields[0])
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to parse access token header: %w", err)
	}

	payload, err = getMap(fields[1])
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to parse access token payload: %w", err)
	}

	return header, payload, nil
}

// do function executes an HTTP request using the oidcClient's http client, and manages authorization by refreshing or authenticating as needed.
// If the request fails with an HTTP Unauthorized status, it attempts to refresh the access token, or perform an OIDC authentication if refresh fails.
func (o *oidcClient) do(req *http.Request) (*http.Response, error) {
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Return immediately if the error is not HTTP status unauthorized.
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	issuer := resp.Header.Get("X-LXD-OIDC-issuer")
	clientID := resp.Header.Get("X-LXD-OIDC-clientid")
	audience := resp.Header.Get("X-LXD-OIDC-audience")
	groupsClaim := resp.Header.Get("X-LXD-OIDC-groups-claim")

	err = o.refresh(issuer, clientID, groupsClaim)
	if err != nil {
		err = o.authenticate(issuer, clientID, audience, groupsClaim)
		if err != nil {
			return nil, err
		}
	}

	// Set the new token in the header.
	o.setHeaders(req)

	resp, err = o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// getProvider initializes a new OpenID Connect Relying Party for a given issuer and clientID.
// The function also creates a secure CookieHandler with random encryption and hash keys, and applies a series of configurations on the Relying Party.
func (o *oidcClient) getProvider(issuer string, clientID string, groupsClaim string) (rp.RelyingParty, error) {
	hashKey := make([]byte, 16)
	encryptKey := make([]byte, 16)

	_, err := rand.Read(hashKey)
	if err != nil {
		return nil, err
	}

	_, err = rand.Read(encryptKey)
	if err != nil {
		return nil, err
	}

	cookieHandler := httphelper.NewCookieHandler(hashKey, encryptKey)
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
		rp.WithHTTPClient(o.httpClient),
	}

	scopes := oidcScopes
	if groupsClaim != "" {
		scopes = append(oidcScopes, groupsClaim)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider, err := rp.NewRelyingPartyOIDC(ctx, issuer, clientID, "", "", scopes, options...)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

// refresh attempts to refresh the OpenID Connect access token for the client using the refresh token.
// If no token is present or the refresh token is empty, it returns an error. If successful, it updates the access token and other relevant token fields.
func (o *oidcClient) refresh(issuer string, clientID string, groupsClaim string) error {
	if o.tokens.Token == nil || o.tokens.RefreshToken == "" {
		return errRefreshAccessToken
	}

	provider, err := o.getProvider(issuer, clientID, groupsClaim)
	if err != nil {
		return errRefreshAccessToken
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	oauthTokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, provider, o.tokens.RefreshToken, "", "")
	if err != nil {
		return errRefreshAccessToken
	}

	o.tokens.Token.AccessToken = oauthTokens.AccessToken
	o.tokens.TokenType = oauthTokens.TokenType
	o.tokens.Expiry = oauthTokens.Expiry

	if oauthTokens.RefreshToken != "" {
		o.tokens.Token.RefreshToken = oauthTokens.RefreshToken
	}

	return nil
}

// authenticate initiates the OpenID Connect device flow authentication process for the client.
// It presents a user code for the end user to input in the device that has web access and waits for them to complete the authentication,
// subsequently updating the client's tokens upon successful authentication.
func (o *oidcClient) authenticate(issuer string, clientID string, audience string, groupsClaim string) error {
	// Store the old transport and restore it in the end.
	oldTransport := o.httpClient.Transport
	o.oidcTransport.audience = audience
	o.httpClient.Transport = o.oidcTransport

	defer func() {
		o.httpClient.Transport = oldTransport
	}()

	provider, err := o.getProvider(issuer, clientID, groupsClaim)
	if err != nil {
		return err
	}

	o.oidcTransport.deviceAuthorizationEndpoint = provider.GetDeviceAuthorizationEndpoint()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT)
	defer stop()

	resp, err := rp.DeviceAuthorization(ctx, oidcScopes, provider, nil)
	if err != nil {
		return err
	}

	// Check if `verification_uri_complete` is present (this auto fills the code in the browser but is marked as optional https://www.rfc-editor.org/rfc/rfc8628#section-3.2)
	var u *url.URL
	if resp.VerificationURIComplete != "" {
		u, _ = url.Parse(resp.VerificationURIComplete)
	}

	// Fall back to `verification_uri` (marked as required in specification).
	if u == nil {
		if resp.VerificationURI == "" {
			return errors.New("Identity provider did not return a verification URI")
		}

		u, err = url.Parse(resp.VerificationURI)
		if err != nil {
			return fmt.Errorf("Identity provider returned an invalid verification URI %q: %w", resp.VerificationURI, err)
		}
	}

	fmt.Printf("URL: %s\n", u.String())
	fmt.Printf("Code: %s\n\n", resp.UserCode)

	_ = openBrowser(u.String())

	token, err := rp.DeviceAccessToken(ctx, resp.DeviceCode, time.Duration(resp.Interval)*time.Second, provider)
	if err != nil {
		return err
	}

	if o.tokens.Token == nil {
		o.tokens.Token = &oauth2.Token{}
	}

	o.tokens.Expiry = time.Now().Add(time.Duration(token.ExpiresIn))
	o.tokens.IDToken = token.IDToken
	o.tokens.Token.AccessToken = token.AccessToken
	o.tokens.TokenType = token.TokenType

	if token.RefreshToken != "" {
		o.tokens.Token.RefreshToken = token.RefreshToken
	}

	return nil
}
