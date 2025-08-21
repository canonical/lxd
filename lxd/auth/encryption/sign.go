package encryption

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	audienceDevLXD = "devlxd"
)

// DevLXDAudience returns the aud claim for all DevLXD tokens issued by this cluster.
func DevLXDAudience(clusterUUID string) string {
	return strings.Join([]string{audienceDevLXD, clusterUUID}, ":")
}

// Issuer returns the iss claim for all tokens issued by this LXD cluster.
func Issuer(clusterUUID string) string {
	return strings.Join([]string{"lxd", clusterUUID}, ":")
}

// GetDevLXDBearerToken generates and signs a token for use with the DevLXD API. For claims it has:
// - Subject (sub): Identity identifier (UUID)
// - Issuer (iss): "lxd:{cluster_uuid}"
// - Audience (aud): "devlxd:{cluster_uuid}"
// - Not before (nbf): time now (UTC)
// - Issued at (iat): time now (UTC)
// - Expiry (exp): The given time (UTC).
func GetDevLXDBearerToken(secret []byte, identityIdentifier string, clusterUUID string, expiresAt time.Time) (string, error) {
	if expiresAt.Location() != time.UTC {
		expiresAt = expiresAt.UTC()
	}

	claims := jwt.RegisteredClaims{
		Issuer:    Issuer(clusterUUID),
		Subject:   identityIdentifier,
		Audience:  jwt.ClaimStrings{DevLXDAudience(clusterUUID)},
		NotBefore: jwt.NewNumericDate(time.Now().UTC()),
		IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}

	signedToken, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("Failed to sign JWT: %w", err)
	}

	return signedToken, nil
}
