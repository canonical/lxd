package encryption

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/canonical/lxd/shared"
)

const (
	// defaultNotBefore is added to time.Now and used as the "nbf" claim.
	// A token is not valid if it is used before this time.
	// It is used to allow for some time skew between cluster members.
	defaultNotBefore = -5 * time.Second

	// defaultExpiry is used to set an expiry of "never", meaning 10 years.
	defaultExpiry = "10y"
)

// GetSignedJWT generates and signs a token. If a UUID salt is provided, a unique signing key is derived from the secret using
// the salt and the UUID is set as the token ID (jti). Otherwise, the token is directly signed by the secret.
func GetSignedJWT(secret []byte, salt *uuid.UUID, subject string, clusterUUID string, expiry string) (string, error) {
	if expiry == "" {
		expiry = defaultExpiry
	}

	expiresAt, err := shared.GetExpiry(time.Now(), expiry)
	if err != nil {
		return "", err
	}

	issAndAud := "lxd:" + clusterUUID

	claims := jwt.RegisteredClaims{
		Issuer:   issAndAud,
		Subject:  subject,
		Audience: jwt.ClaimStrings{issAndAud},
		// Not before used in case of time-skew between members.
		NotBefore: jwt.NewNumericDate(time.Now().Add(defaultNotBefore)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}

	tokenSigningKey := secret
	if salt != nil {
		claims.ID = salt.String()
		tokenSigningKey, err = TokenSigningKey(secret, salt[:])
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	if err != nil {
		return "", err
	}

	signedToken, err := token.SignedString(tokenSigningKey)
	if err != nil {
		return "", fmt.Errorf("Failed to sign JWT: %w", err)
	}

	return signedToken, nil
}
