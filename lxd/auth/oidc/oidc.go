package oidc

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zitadel/oidc/v2/pkg/client"
	"github.com/zitadel/oidc/v2/pkg/client/rp"
	"github.com/zitadel/oidc/v2/pkg/oidc"
	"github.com/zitadel/oidc/v2/pkg/op"

	"github.com/lxc/lxd/shared"
)

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	keySet   oidc.KeySet
	verifier op.AccessTokenVerifier

	clientID string
	issuer   string
	audience string
}

// Auth extracts the token, validates it and returns the user information.
func (o *Verifier) Auth(ctx context.Context, r *http.Request) (string, error) {
	// When a client wants to authenticate, it needs to set the Authorization HTTP header like this:
	//    Authorization Bearer <access_token>
	// If set correctly, LXD will attempt to verify the access token, and grant access if it's valid.
	// If the verification fails, LXD will return an InvalidToken error. The client should then either use its refresh token to get a new valid access token, or log in again.
	// If the Authorization header is missing, LXD returns an AuthenticationRequired error.
	// Both returned errors contain information which are needed for the client to authenticate.
	parts := strings.Split(r.Header.Get("Authorization"), "Bearer ")
	if len(parts) != 2 {
		return "", fmt.Errorf("Bad authorization token, expected a Bearer token")
	}

	claims, err := o.VerifyAccessToken(ctx, parts[1])
	if err != nil {
		return "", err
	}

	user, ok := claims.Claims["email"]
	if ok && user != nil && user.(string) != "" {
		return user.(string), nil
	}

	return claims.Subject, nil
}

// VerifyAccessToken is a wrapper around op.VerifyAccessToken which avoids having to deal with Go generics elsewhere. It validates the access token (issuer, signature and expiration).
func (o *Verifier) VerifyAccessToken(ctx context.Context, token string) (*oidc.AccessTokenClaims, error) {
	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, token, o.verifier)
	if err != nil {
		return nil, err
	}

	// Check that the token includes the configured audience.
	audience := claims.GetAudience()
	if o.audience != "" && !shared.StringInSlice(o.audience, audience) {
		return nil, fmt.Errorf("Provided OIDC token doesn't allow the configured audience")
	}

	return claims, nil
}

// WriteHeaders writes the OIDC configuration as HTTP headers so the client can initatiate the device code flow.
func (o *Verifier) WriteHeaders(w http.ResponseWriter) error {
	w.Header().Set("X-LXD-OIDC-issuer", o.issuer)
	w.Header().Set("X-LXD-OIDC-clientid", o.clientID)
	w.Header().Set("X-LXD-OIDC-audience", o.audience)

	return nil
}

// IsRequest checks if the request is using OIDC authentication.
func (o *Verifier) IsRequest(r *http.Request) bool {
	return r.Header.Get("Authorization") != ""
}

// NewVerifier returns a Verifier. It calls the OIDC discovery endpoint in order to get the issuer's remote keys which are needed to verify an issued access token.
func NewVerifier(issuer string, clientid string, audience string) (*Verifier, error) {
	discoveryConfig, err := client.Discover(issuer, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("Failed calling OIDC discovery endpoint: %w", err)
	}

	keySet := rp.NewRemoteKeySet(http.DefaultClient, discoveryConfig.JwksURI)
	verifier := op.NewAccessTokenVerifier(issuer, keySet)

	return &Verifier{keySet: keySet, verifier: verifier, issuer: issuer, clientID: clientid, audience: audience}, nil
}
