package cluster

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/entity"
)

// authSecretLength defines the length of an AuthSecretValue.
const authSecretLength = 64

// AuthSecretValue is used to store byte slice of length authSecretLength in a text column as a base64 encoded string.
type AuthSecretValue []byte

// newAuthSecretValue returns a AuthSecretValue, read from [rand.Reader].
func newAuthSecretValue() AuthSecretValue {
	secretValue := make([]byte, authSecretLength)
	_, _ = rand.Read(secretValue)
	return secretValue
}

// Validate enforces that the AuthSecretValue has length authSecretLength.
func (s AuthSecretValue) Validate() error {
	if len(s) != authSecretLength {
		return fmt.Errorf("Secret must have length %d", authSecretLength)
	}

	return nil
}

// Value implements [driver.Valuer] for AuthSecretValue.
func (s AuthSecretValue) Value() (driver.Value, error) {
	err := s.Validate()
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.EncodeToString(s), nil
}

// ScanText implements [query.TextScanner] for AuthSecretValue to simplify the [sql.Scanner] implementation.
func (s *AuthSecretValue) ScanText(str string) error {
	out, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return err
	}

	value := AuthSecretValue(out)
	err = value.Validate()
	if err != nil {
		return err
	}

	*s = value
	return nil
}

// Scan implements [sql.Scanner] for AuthSecretValue.
func (s *AuthSecretValue) Scan(value any) error {
	return query.ScanValue(value, s, false)
}

// SecretType represents the "type" column in the secrets table.
type SecretType string

const (
	// SecretTypeCoreAuth is the SecretType for core auth secrets.
	SecretTypeCoreAuth SecretType = "core_auth"
)

const (
	// secretTypeCodeCoreAuth is the database code for SecretTypeCoreAuth.
	secretTypeCodeCoreAuth int64 = 1
)

// Value implements [driver.Valuer] for SecretType.
func (s SecretType) Value() (driver.Value, error) {
	switch s {
	case SecretTypeCoreAuth:
		return secretTypeCodeCoreAuth, nil
	}

	return nil, fmt.Errorf("Invalid secret type %q", s)
}

// ScanInteger implements [query.IntegerScanner] for SecretType to simplify the [sql.Scanner] implementation.
func (s *SecretType) ScanInteger(code int64) error {
	switch code {
	case secretTypeCodeCoreAuth:
		*s = SecretTypeCoreAuth
	}

	return fmt.Errorf("Invalid secret type code %d", code)
}

// Scan implements sql.Scanner for SecretType.
func (s *SecretType) Scan(value any) error {
	return query.ScanValue(value, s, false)
}

// AuthSecret contains an AuthSecretValue and a creation time.
type AuthSecret struct {
	Value     AuthSecretValue
	CreatedAt time.Time
}

// newAuthSecret mints a new AuthSecret.
func newAuthSecret() AuthSecret {
	return AuthSecret{
		Value:     newAuthSecretValue(),
		CreatedAt: time.Now().UTC(),
	}
}

// IsExpired returns true if the time now is greater than the time that the secret was created, plus the expiry.
func (s AuthSecret) IsExpired(expirySeconds int64) bool {
	return time.Now().UTC().After(s.CreatedAt.Add(time.Duration(expirySeconds) * time.Second))
}

// AuthSecrets is a concrete type for a slice of AuthSecret.
type AuthSecrets []AuthSecret

// IsExpired returns true if the length of AuthSecrets is zero, or if the first AuthSecret in AuthSecrets has expired.
func (s AuthSecrets) IsExpired(lifetime int64) bool {
	return len(s) == 0 || s[0].IsExpired(lifetime)
}

// Rotate returns a new AuthSecrets, with a new AuthSecret prepended to the slice. The slice is always truncated to a
// maximum of two elements. The new AuthSecret is written to the database, and any AuthSecret values older than the new
// oldest in-memory value are deleted from the database.
func (s AuthSecrets) Rotate(ctx context.Context, tx *sql.Tx) (AuthSecrets, error) {
	// Get new secret.
	newSecret := newAuthSecret()

	// Initial in-memory rotation.
	rotatedSecrets := append([]AuthSecret{newSecret}, s...)

	// Truncation.
	truncated := len(rotatedSecrets) > 2
	if truncated {
		rotatedSecrets = rotatedSecrets[:2]
	}

	// Add the new value to the database
	err := createCoreAuthSecret(ctx, tx, newSecret)
	if err != nil {
		return nil, fmt.Errorf("Failed to rotate secrets: %w", err)
	}

	// If the secrets were truncated, then there should be at most one old secret in the database. Delete it and assert
	// that we deleted one row.
	if truncated {
		nDeleted, err := deleteCoreAuthSecretsOlderThanTimestamp(ctx, tx, rotatedSecrets[len(rotatedSecrets)-1].CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("Failed to delete out of date secrets: %w", err)
		}

		if nDeleted != 1 {
			return nil, fmt.Errorf("Failed to delete expected number of out of date secrets: %w", err)
		}
	}

	return rotatedSecrets, nil
}

// GetCoreAuthSecrets returns the server core auth secrets.
func GetCoreAuthSecrets(ctx context.Context, tx *sql.Tx) (AuthSecrets, error) {
	return getAuthSecrets(ctx, tx, entity.TypeServer, 0, SecretTypeCoreAuth)
}

// getAuthSecrets returns a slice of AuthSecrets. It scans the "secrets" table for rows of the given [entity.Type], entity
// ID, and SecretType. Since we are scanning for AuthSecrets, the "value" column must be an AuthSecretValue.
func getAuthSecrets(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int, secretType SecretType) (AuthSecrets, error) {
	q := `SELECT value, created_at FROM secrets WHERE entity_type = ? AND entity_id = ? AND type = ? ORDER BY created_at DESC`

	var secrets AuthSecrets
	scanFunc := func(scan func(dest ...any) error) error {
		var secret AuthSecret
		err := scan(&secret.Value, &secret.CreatedAt)
		if err != nil {
			return err
		}

		secrets = append(secrets, secret)
		return nil
	}

	err := query.Scan(ctx, tx, q, scanFunc, EntityType(entityType), entityID, secretType)
	if err != nil {
		return nil, fmt.Errorf("Failed to get auth secrets: %w", err)
	}

	return secrets, nil
}

// createCoreAuthSecret creates an AuthSecret.
func createCoreAuthSecret(ctx context.Context, tx *sql.Tx, secret AuthSecret) error {
	return createSecret(ctx, tx, entity.TypeServer, 0, SecretTypeCoreAuth, secret.Value, secret.CreatedAt)
}

// createSecret is a general method for creating a secret.
func createSecret(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int, secretType SecretType, value any, createdAt time.Time) error {
	// Add the new secret to the database.
	res, err := tx.ExecContext(ctx, `INSERT INTO secrets (entity_type, entity_id, type, value, created_at) VALUES (?, ?, ?, ?, ?)`, EntityType(entityType), entityID, secretType, value, createdAt)
	if err != nil {
		return err
	}

	// Enforce that we added the new secret successfully.
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return fmt.Errorf("Failed to write new %q secret", secretType)
	}

	return nil
}

// deleteCoreAuthSecretsOlderThanTimestamp deletes core auth secrets older than the given timestamp. It returns the
// number of affected rows.
func deleteCoreAuthSecretsOlderThanTimestamp(ctx context.Context, tx *sql.Tx, timestamp time.Time) (int64, error) {
	return deleteSecretsOlderThanTimestamp(ctx, tx, entity.TypeServer, 0, SecretTypeCoreAuth, timestamp)
}

// deleteSecretsOlderThanTimestamp deletes secrets with the given [entity.Type], entity ID, and SecretType that are
// older than the given timestamp. It returns the number of affected rows.
func deleteSecretsOlderThanTimestamp(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int, secretType SecretType, timestamp time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE entity_type = ? AND entity_id = ? AND type = ? AND created_at < ?`, EntityType(entityType), entityID, secretType, timestamp)
	if err != nil {
		return 0, fmt.Errorf("Failed to remove old secret values of type %q: %w", entityType, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("Failed to determine the number of rows affected by secret deletion: %w", err)
	}

	return rowsAffected, nil
}
