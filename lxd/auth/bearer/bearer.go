package bearer

import (
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/shared/api"
)

// IsRequest returns true if the caller sent a bearer token in the Authorization header that is a JWT and appears to
// have this LXD cluster as the issuer. If true, it returns the raw token, and the subject.
func IsRequest(r *http.Request, clusterUUID string) (isRequest bool, token string, subject string) {
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
	expectIssuer := "lxd:" + clusterUUID
	if issuer != expectIssuer {
		return false, "", ""
	}

	// Expect the audience to be "lxd:{cluster_uuid}" (the token is signed symmetrically).
	gotAudience, err := t.Claims.GetAudience()
	if err != nil {
		return false, "", ""
	}

	if len(gotAudience) != 1 || gotAudience[0] != expectIssuer {
		return false, "", ""
	}

	return true, token, sub
}

// Authenticate verifies that the token was signed by the secret held by the identity matching the given subject and that
// it has not expired.
func Authenticate(token string, subject string, identityCache *identity.Cache) error {
	entry, err := identityCache.Get(api.AuthenticationMethodBearer, subject)
	if err != nil {
		return err
	}

	_, err = jwt.NewParser(jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute), jwt.WithExpirationRequired()).Parse(token, func(token *jwt.Token) (any, error) {
		return entry.Secret, nil
	})
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Token is not valid: %w", err)
	}

	return nil
}
