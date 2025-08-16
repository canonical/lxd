package bearer

import (
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/canonical/lxd/lxd/auth/encryption"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// IsDevLXDRequest returns true if the caller sent a bearer token in the Authorization header that is a JWT and appears to
// have this LXD cluster as the issuer. If true, it returns the raw token, and the subject.
func IsDevLXDRequest(r *http.Request, clusterUUID string) (isRequest bool, token string, subject string) {
	// Check Authorization header for bearer token.
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return false, "", ""
	}

	// Check we can parse the token. If we can't parse it, it could be an opaque OAuth2.0 token.
	claims := jwt.MapClaims{}
	t, _, err := jwt.NewParser().ParseUnverified(token, claims)
	if err != nil {
		return false, "", ""
	}

	// There must be an issuer
	issuer, err := t.Claims.GetIssuer()
	if err != nil {
		return false, "", ""
	}

	// There must be a subject
	sub, err := t.Claims.GetSubject()
	if err != nil {
		return false, "", ""
	}

	// Expect the issuer to be "lxd:{cluster_uuid}".
	expectIssuer := encryption.Issuer(clusterUUID)
	if issuer != expectIssuer {
		return false, "", ""
	}

	// Expect the audience to be "devlxd:{cluster_uuid}".
	expectAudience := encryption.DevLXDAudience(clusterUUID)
	audience, err := t.Claims.GetAudience()
	if err != nil {
		return false, "", ""
	}

	if len(audience) != 1 || audience[0] != expectAudience {
		return false, "", ""
	}

	return true, token, sub
}

// Authenticate gets a bearer identity from the cache using the given subject, and verifies that it is of the expected
// type. It then verifies that the token was signed by the secret associated with that identity, and that the token has
// not expired.
func Authenticate(token string, subject string, identityCache *identity.Cache) (*request.RequestorArgs, error) {
	// Get the identity from the cache by the subject.
	entry, err := identityCache.Get(api.AuthenticationMethodBearer, subject)
	if err != nil {
		return nil, err
	}

	// Always use UTC time.
	timeFunc := func() time.Time {
		return time.Now().UTC()
	}

	// Get a parser. We don't need to verify the issuer or audience because we already validated that in `IsRequest`.
	// We do not use a leeway. This is so the expiry is exact. This might cause issues if there is time skew between
	// cluster members.
	parser := jwt.NewParser(
		jwt.WithIssuedAt(),           // Verify time now is not before the token was issued. The not before is automatically verified.
		jwt.WithExpirationRequired(), // Verify token has not expired.
		jwt.WithTimeFunc(timeFunc),   // Ensure the UTC time is used for comparison.
	)

	// Use the identity secret as the signing key.
	keyFunc := func(_ *jwt.Token) (any, error) {
		return entry.Secret, nil
	}

	// Verify the token.
	_, err = parser.Parse(token, keyFunc)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusForbidden, "Token is not valid: %w", err)
	}

	return &request.RequestorArgs{
		Trusted:  true,
		Protocol: api.AuthenticationMethodBearer,
		Username: entry.Identifier,
	}, nil
}
