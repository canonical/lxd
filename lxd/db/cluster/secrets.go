package cluster

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// authSecretLength defines the length of an AuthSecretValue.
const authSecretLength = 64

// AuthSecretValue is used to store byte slice of length authSecretLength in a text column as a base64 encoded string.
type AuthSecretValue []byte

// String implements [fmt.Stringer] for AuthSecretValue so that a human-readable format can be easily displayed.
// Note however that this is likely to contain sensitive information, and should never be logged.
func (s AuthSecretValue) String() string {
	return base64.StdEncoding.EncodeToString(s)
}

// newAuthSecretValue returns a AuthSecretValue, read from [rand.Reader].
func newAuthSecretValue() AuthSecretValue {
	secretValue := make([]byte, authSecretLength)
	_, _ = rand.Read(secretValue)
	return secretValue
}

// Validate checks that the AuthSecretValue has length authSecretLength.
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

	return s.String(), nil
}

// ScanText implements [query.TextScanner] for AuthSecretValue to simplify the [sql.Scanner] implementation.
//
// Note: This method must have a pointer receiver so that e.g. `row.Scan(&secretVal)` will work, whereas the Value
// implementation does not require a pointer receiver. This is so that a non-pointer AuthSecretValue can be written to
// the database.
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

	// SecretTypeBearerSigningKey is the SecretType for bearer identity signing keys.
	SecretTypeBearerSigningKey SecretType = "bearer_signing_key"
)

const (
	// secretTypeCodeCoreAuth is the database code for SecretTypeCoreAuth.
	secretTypeCodeCoreAuth         int64 = 1
	secretTypeCodeBearerSigningKey int64 = 2
)

// Value implements [driver.Valuer] for SecretType.
func (s SecretType) Value() (driver.Value, error) {
	switch s {
	case SecretTypeCoreAuth:
		return secretTypeCodeCoreAuth, nil
	case SecretTypeBearerSigningKey:
		return secretTypeCodeBearerSigningKey, nil
	}

	return nil, fmt.Errorf("Invalid secret type %q", s)
}

// ScanInteger implements [query.IntegerScanner] for SecretType to simplify the [sql.Scanner] implementation.
func (s *SecretType) ScanInteger(code int64) error {
	switch code {
	case secretTypeCodeCoreAuth:
		*s = SecretTypeCoreAuth
	case secretTypeCodeBearerSigningKey:
		*s = SecretTypeBearerSigningKey
	default:
		return fmt.Errorf("Invalid secret type code %d", code)
	}

	return nil
}

// Scan implements sql.Scanner for SecretType.
//
// Note: This method must have a pointer receiver because only a pointer to a SecretType implements
// [query.IntegerScanner]. The Value implementation does not require a pointer receiver. This is so that a non-pointer
// SecretType can be written to the database.
func (s *SecretType) Scan(value any) error {
	return query.ScanValue(value, s, false)
}

// AuthSecret contains an AuthSecretValue and a creation time.
type AuthSecret struct {
	ID           int
	Value        AuthSecretValue
	CreationDate time.Time
}

// newAuthSecret mints a new AuthSecret.
func newAuthSecret() AuthSecret {
	return AuthSecret{
		Value:        newAuthSecretValue(),
		CreationDate: time.Now().UTC(),
	}
}

// Validate checks that the secret is of the correct length and has not expired.
func (s AuthSecret) Validate(expiry string) error {
	err := s.Value.Validate()
	if err != nil {
		return err
	}

	expiresAt, err := shared.GetExpiry(s.CreationDate, expiry)
	if err != nil {
		return fmt.Errorf("Failed to check auth secret expiry: %w", err)
	}

	if time.Now().UTC().After(expiresAt) {
		return errors.New("Secret has expired")
	}

	return nil
}

// AuthSecrets is a concrete type for a slice of AuthSecret.
type AuthSecrets []AuthSecret

// Validate checks that there is at least one secret in the slice, and that the first (most recent) is valid and has not
// expired.
func (s AuthSecrets) Validate(expiry string) error {
	if len(s) == 0 {
		return errors.New("No secrets are defined")
	}

	return s[0].Validate(expiry)
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
	var oldSecretIDs []int
	if len(rotatedSecrets) > 2 {
		for _, authSecret := range rotatedSecrets[2:] {
			oldSecretIDs = append(oldSecretIDs, authSecret.ID)
		}

		rotatedSecrets = rotatedSecrets[:2]
	}

	// Add the new value to the database
	id, err := createCoreAuthSecret(ctx, tx, newSecret)
	if err != nil {
		return nil, fmt.Errorf("Failed to rotate secrets: %w", err)
	}

	// Set the ID of the new value.
	rotatedSecrets[0].ID = id

	// If the secrets were truncated, delete any secrets that are not in our new slice.
	err = deleteSecretsByID(ctx, tx, oldSecretIDs...)
	if err != nil {
		return nil, fmt.Errorf("Failed to delete expired core secrets: %w", err)
	}

	return rotatedSecrets, nil
}

// GetCoreAuthSecrets returns a slice of AuthSecrets.
func GetCoreAuthSecrets(ctx context.Context, tx *sql.Tx) (AuthSecrets, error) {
	q := `SELECT id, value, creation_date FROM secrets WHERE entity_type = ? AND entity_id = ? AND type = ? ORDER BY creation_date DESC`

	var secrets AuthSecrets
	scanFunc := func(scan func(dest ...any) error) error {
		var secret AuthSecret
		err := scan(&secret.ID, &secret.Value, &secret.CreationDate)
		if err != nil {
			return err
		}

		secrets = append(secrets, secret)
		return nil
	}

	err := query.Scan(ctx, tx, q, scanFunc, EntityType(entity.TypeServer), 0, SecretTypeCoreAuth)
	if err != nil {
		return nil, fmt.Errorf("Failed to get auth secrets: %w", err)
	}

	return secrets, nil
}

// createCoreAuthSecret creates an AuthSecret.
func createCoreAuthSecret(ctx context.Context, tx *sql.Tx, secret AuthSecret) (int, error) {
	return createSecret(ctx, tx, entity.TypeServer, 0, SecretTypeCoreAuth, secret.Value, secret.CreationDate)
}

// createSecret is a general method for creating a secret.
func createSecret(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int, secretType SecretType, value any, createdAt time.Time) (int, error) {
	// Add the new secret to the database.
	res, err := tx.ExecContext(ctx, `INSERT INTO secrets (entity_type, entity_id, type, value, creation_date) VALUES (?, ?, ?, ?, ?)`, EntityType(entityType), entityID, secretType, value, createdAt)
	if err != nil {
		return -1, err
	}

	// Enforce that we added the new secret successfully.
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return -1, err
	}

	if rowsAffected != 1 {
		return -1, fmt.Errorf("Failed to write new %q secret", secretType)
	}

	lastInsertID, err := res.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed to get last insert ID: %w", err)
	}

	return int(lastInsertID), nil
}

// deleteSecretsByID deletes all secrets where the ID is present in the given list. Returns an error if any secrets
// were not deleted (e.g. not present).
func deleteSecretsByID(ctx context.Context, tx *sql.Tx, ids ...int) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]any, 0, len(ids))
	for _, value := range ids {
		args = append(args, value)
	}

	q := "DELETE FROM secrets WHERE id IN " + query.Params(len(ids))

	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("Failed to delete secrets: %w", err)
	}

	nDeleted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to verify secret deletion: %w", err)
	}

	if int(nDeleted) != len(ids) {
		return fmt.Errorf("Failed to delete expected number of secrets: %w", err)
	}

	return nil
}

// GetAllBearerIdentitySigningKeys returns a map of identity ID to token signing keys.
// It should only be used to refresh the identity cache.
func GetAllBearerIdentitySigningKeys(ctx context.Context, tx *sql.Tx) (map[int]AuthSecretValue, error) {
	q := `SELECT entity_id, value FROM secrets WHERE entity_type = ? AND type = ?`

	identityIDToSigningKey := make(map[int]AuthSecretValue)
	scanFunc := func(scan func(dest ...any) error) error {
		var identityID int
		var value AuthSecretValue
		err := scan(&identityID, &value)
		if err != nil {
			return err
		}

		identityIDToSigningKey[identityID] = value
		return nil
	}

	err := query.Scan(ctx, tx, q, scanFunc, entityTypeCodeIdentity, SecretTypeBearerSigningKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to get bearer identity signing keys: %w", err)
	}

	return identityIDToSigningKey, nil
}

// DeleteBearerIdentitySigningKey deletes any signing keys for the identity. It returns an [api.StatusError] with
// [http.StatusNotFound] if no key exists.
func DeleteBearerIdentitySigningKey(ctx context.Context, tx *sql.Tx, identityID int) error {
	q := "DELETE FROM secrets WHERE entity_type = ? AND entity_id = ? AND type = ?"
	res, err := tx.ExecContext(ctx, q, EntityType(entity.TypeIdentity), identityID, SecretTypeBearerSigningKey)
	if err != nil {
		return fmt.Errorf("Failed to delete bearer identity signing key: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to verify deletion of bearer identity signing key: %w", err)
	}

	switch rowsAffected {
	case 0:
		// No signing key, and therefore no token
		return api.NewStatusError(http.StatusNotFound, "No token exists for the identity")
	case 1:
		// Happy path
		return nil
	}

	// Unhappy path. We deleted all of the secrets for the identity - but there should only ever have been one.
	return errors.New("Encountered more than one signing key for an identity")
}

// RotateBearerIdentitySigningKey deletes any existing signing keys for the identity and creates a new one.
func RotateBearerIdentitySigningKey(ctx context.Context, tx *sql.Tx, identityID int) (AuthSecretValue, error) {
	// Delete any existing key, continuing if there was no key.
	// If any other error occurs, including the identity having more than one existing signing key, it will not be possible
	// to rotate the keys.
	err := DeleteBearerIdentitySigningKey(ctx, tx, identityID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, err
	}

	// Get new signing key value.
	signingKey := newAuthSecretValue()

	// Create the signing key.
	_, err = createSecret(ctx, tx, entity.TypeIdentity, identityID, SecretTypeBearerSigningKey, signingKey, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("Failed to create bearer identity initial key material: %w", err)
	}

	return signingKey, nil
}
