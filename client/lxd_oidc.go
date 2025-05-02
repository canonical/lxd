package lxd

import (
	"context"
	"crypto/rand"
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

var errRefreshAccessToken = errors.New("Failed refreshing access token")

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

// getAccessToken returns the Access Token from the oidcClient's tokens, or an empty string if no tokens are present.
func (o *oidcClient) getAccessToken() string {
	if o.tokens == nil || o.tokens.Token == nil {
		return ""
	}

	return o.tokens.AccessToken
}

// do function executes an HTTP request using the oidcClient's http client, and manages authorization by refreshing or authenticating as needed.
// If the request fails with an HTTP Unauthorized status, it attempts to refresh the access token, or perform an OIDC authentication if refresh fails.
// The oidcScopesExtensionPresent argument changes the behaviour of this function based on the presence of an API extension.
func (o *oidcClient) do(req *http.Request, oidcScopesExtensionPresent bool) (*http.Response, error) {
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

	var scopes []string
	if oidcScopesExtensionPresent {
		// If we have the `oidc_scopes` extension, get the scopes from the header and ignore the groups claim header.
		scopesJSON := resp.Header.Get("X-LXD-OIDC-scopes")
		if scopesJSON == "" {
			return nil, errors.New("LXD server did not return OIDC scopes")
		}

		err = json.Unmarshal([]byte(scopesJSON), &scopes)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse OIDC scopes: %w", err)
		}
	} else {
		// Otherwise, use the default scopes from before the API extension was added, and append the groups claim header
		// if set.
		scopes = []string{oidc.ScopeOpenID, oidc.ScopeEmail, oidc.ScopeOfflineAccess, oidc.ScopeProfile}
		groupsClaim := resp.Header.Get("X-LXD-OIDC-groups-claim")
		if groupsClaim != "" {
			scopes = append(scopes, groupsClaim)
		}
	}

	err = o.refresh(issuer, clientID, scopes)
	if err != nil {
		err = o.authenticate(issuer, clientID, audience, scopes)
		if err != nil {
			return nil, err
		}
	}

	// Set the new access token in the header.
	req.Header.Set("Authorization", "Bearer "+o.tokens.AccessToken)

	resp, err = o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// getProvider initializes a new OpenID Connect Relying Party for a given issuer and clientID.
// The function also creates a secure CookieHandler with random encryption and hash keys, and applies a series of configurations on the Relying Party.
func (o *oidcClient) getProvider(issuer string, clientID string, scopes []string) (rp.RelyingParty, error) {
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
func (o *oidcClient) refresh(issuer string, clientID string, scopes []string) error {
	if o.tokens.Token == nil || o.tokens.RefreshToken == "" {
		return errRefreshAccessToken
	}

	provider, err := o.getProvider(issuer, clientID, scopes)
	if err != nil {
		return errRefreshAccessToken
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	oauthTokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, provider, o.tokens.RefreshToken, "", "")
	if err != nil {
		return errRefreshAccessToken
	}

	o.tokens.AccessToken = oauthTokens.AccessToken
	o.tokens.TokenType = oauthTokens.TokenType
	o.tokens.Expiry = oauthTokens.Expiry

	if oauthTokens.RefreshToken != "" {
		o.tokens.RefreshToken = oauthTokens.RefreshToken
	}

	return nil
}

// authenticate initiates the OpenID Connect device flow authentication process for the client.
// It presents a user code for the end user to input in the device that has web access and waits for them to complete the authentication,
// subsequently updating the client's tokens upon successful authentication.
func (o *oidcClient) authenticate(issuer string, clientID string, audience string, scopes []string) error {
	// Store the old transport and restore it in the end.
	oldTransport := o.httpClient.Transport
	o.oidcTransport.audience = audience
	o.httpClient.Transport = o.oidcTransport

	defer func() {
		o.httpClient.Transport = oldTransport
	}()

	provider, err := o.getProvider(issuer, clientID, scopes)
	if err != nil {
		return err
	}

	o.oidcTransport.deviceAuthorizationEndpoint = provider.GetDeviceAuthorizationEndpoint()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT)
	defer stop()

	resp, err := rp.DeviceAuthorization(ctx, scopes, provider, nil)
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
	o.tokens.AccessToken = token.AccessToken
	o.tokens.TokenType = token.TokenType

	if token.RefreshToken != "" {
		o.tokens.RefreshToken = token.RefreshToken
	}

	return nil
}
