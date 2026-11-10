package encryption

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared"
)

// NewEphemeralSecret returns a short but verifiable secret, for use in "untrusted" API calls with "?secret=<secret>".
// It should be used when setting a secret in operation metadata.
func NewEphemeralSecret(ctx context.Context, secretsFunc func(ctx context.Context) (dbCluster.AuthSecrets, error)) (string, error) {
	plaintextSecret, err := shared.RandomCryptoString()
	if err != nil {
		return "", fmt.Errorf("Failed generating secret: %w", err)
	}

	clusterSecrets, err := secretsFunc(ctx)
	if err != nil {
		return "", err
	} else if len(clusterSecrets) == 0 {
		return "", errors.New("No cluster secrets available")
	}

	signingKey, err := TokenSigningKey(clusterSecrets[0].Value, []byte(plaintextSecret))
	if err != nil {
		return "", fmt.Errorf("Failed creating a new token signing key: %w", err)
	}

	mac := hmac.New(sha512.New, signingKey)
	mac.Write([]byte(plaintextSecret))
	signature := mac.Sum(nil)

	encodedSig := base64.RawURLEncoding.EncodeToString(signature)
	return plaintextSecret + "." + encodedSig, nil
}

// VerifyEphemeralSecret checks whether a signed token secret was generated using the current or previous cluster secret.
func VerifyEphemeralSecret(ctx context.Context, secretsFunc func(ctx context.Context) (dbCluster.AuthSecrets, error), secret string) error {
	plaintextSecret, signature, ok := strings.Cut(secret, ".")
	if !ok {
		return errors.New("Invalid secret format")
	}

	receivedSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return err
	}

	clusterSecrets, err := secretsFunc(ctx)
	if err != nil {
		return fmt.Errorf("Failed loading cluster secret: %w", err)
	}

	for _, clusterSecret := range clusterSecrets {
		signingKey, err := TokenSigningKey(clusterSecret.Value, []byte(plaintextSecret))
		if err != nil {
			return fmt.Errorf("Failed deriving signing key for secret verification: %w", err)
		}

		mac := hmac.New(sha512.New, signingKey)
		mac.Write([]byte(plaintextSecret))
		expectedSignature := mac.Sum(nil)

		if hmac.Equal(receivedSignature, expectedSignature) {
			return nil
		}
	}

	return errors.New("Invalid token secret signature")
}
