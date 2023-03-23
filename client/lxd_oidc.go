package lxd

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/zitadel/oidc/v2/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v2/pkg/http"
	"github.com/zitadel/oidc/v2/pkg/oidc"
	"golang.org/x/oauth2"
)

func (r *ProtocolLXD) setupOIDCClient(token *oidc.Tokens[*oidc.IDTokenClaims]) {
	if r.oidcClient != nil {
		return
	}

	r.oidcClient = newOIDCClient(token)
	r.oidcClient.httpClient = r.http
}

// Custom transport that modifies requests to inject the audience field.
type oidcTransport struct {
	deviceAuthorizationEndpoint string
	audience                    string
}

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
var oidcScopes = []string{oidc.ScopeOpenID, oidc.ScopeOfflineAccess}

type oidcClient struct {
	httpClient    *http.Client
	oidcTransport *oidcTransport
	tokens        *oidc.Tokens[*oidc.IDTokenClaims]
}

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

func (o *oidcClient) getAccessToken() string {
	if o.tokens == nil || o.tokens.Token == nil {
		return ""
	}

	return o.tokens.AccessToken
}

func (o *oidcClient) do(req *http.Request) (*http.Response, error) {
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Since the response body can only be read once (io.ReadCloser), we store it, and feed it back
	// to the response before returning it. This way, the caller won't have an empty body after
	// we're done processing it.
	var bodyBytes []byte
	if resp.Body != nil {
		bodyBytes, _ = io.ReadAll(resp.Body)
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	_, _, err = lxdParseResponse(resp)
	if err == nil {
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		return resp, nil
	}

	issuer := resp.Header.Get("X-LXD-OIDC-issuer")
	clientID := resp.Header.Get("X-LXD-OIDC-clientid")
	audience := resp.Header.Get("X-LXD-OIDC-audience")

	err = o.refresh(issuer, clientID)
	if err != nil {
		err = o.authenticate(issuer, clientID, audience)
		if err != nil {
			return nil, err
		}
	}

	// Set the new access token in the header.
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.tokens.AccessToken))

	resp, err = o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (o *oidcClient) getProvider(issuer string, clientID string) (rp.RelyingParty, error) {
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

	cookieHandler := httphelper.NewCookieHandler(hashKey, encryptKey, httphelper.WithUnsecure())
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
		rp.WithHTTPClient(o.httpClient),
	}

	provider, err := rp.NewRelyingPartyOIDC(issuer, clientID, "", "", oidcScopes, options...)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

func (o *oidcClient) refresh(issuer string, clientID string) error {
	if o.tokens.Token == nil || o.tokens.RefreshToken == "" {
		return errRefreshAccessToken
	}

	provider, err := o.getProvider(issuer, clientID)
	if err != nil {
		return errRefreshAccessToken
	}

	oauthTokens, err := rp.RefreshAccessToken(provider, o.tokens.RefreshToken, "", "")
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

func (o *oidcClient) authenticate(issuer string, clientID string, audience string) error {
	// Store the old transport and restore it in the end.
	oldTransport := o.httpClient.Transport
	o.oidcTransport.audience = audience
	o.httpClient.Transport = o.oidcTransport

	defer func() {
		o.httpClient.Transport = oldTransport
	}()

	provider, err := o.getProvider(issuer, clientID)
	if err != nil {
		return err
	}

	o.oidcTransport.deviceAuthorizationEndpoint = provider.GetDeviceAuthorizationEndpoint()

	resp, err := rp.DeviceAuthorization(oidcScopes, provider)
	if err != nil {
		return err
	}

	fmt.Printf("Code: %s\n\n", resp.UserCode)

	u, _ := url.Parse(resp.VerificationURIComplete)

	err = httpbakery.OpenWebBrowser(u)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT)
	defer stop()

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
