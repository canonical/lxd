package secret

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Secret encapsulates a cluster-wide secret allowing all members to share and rotate salts and keys.
// The caller should check if the current secret IsValid and use it if it is valid. Otherwise, they must call Update.
type Secret struct {
	key  key
	salt salt
}

// IsValid returns true if both the key and salt are set and have not expired.
func (s Secret) IsValid() bool {
	return s.key.isValid() && s.salt.isValid()
}

// KeyAndSalt returns the current key and salt.
func (s Secret) KeyAndSalt() (key []byte, salt []byte, err error) {
	if !s.key.isValid() {
		return nil, nil, errors.New("Invalid key")
	}

	if !s.salt.isValid() {
		return nil, nil, errors.New("Invalid salt")
	}

	return s.key.bytes, s.salt.u[:], nil
}

// UnsetKey should be called when `core.salt_lifetime` is changed so that the current key is invalidated.
func (s *Secret) UnsetKey(ctx context.Context, tx *sql.Tx) error {
	return s.key.unset(ctx, tx)
}

// UnsetSalt should be called when `core.salt_lifetime` is changed so that the current salt is invalidated.
func (s *Secret) UnsetSalt(ctx context.Context, tx *sql.Tx) error {
	return s.salt.unset(ctx, tx)
}

// Update updates the secret. This should only be called when the current secret is invalid. In both cases, it queries
// the database to check if any other members have updated the value first. If the key and salt in the database are valid,
// they are used. Otherwise, the key/salt is overwritten with a new valid one.
func (s *Secret) Update(ctx context.Context, tx *sql.Tx, keyLifetime time.Duration, saltLifetime time.Duration) error {
	if s == nil {
		return errors.New("Cannot update nil secret")
	}

	keyIsValid := s.key.isValid()
	saltIsValid := s.salt.isValid()
	if keyIsValid && saltIsValid {
		return nil
	}

	if !keyIsValid {
		key, err := getKey(ctx, tx, keyLifetime)
		if err != nil {
			return err
		}

		s.key = *key
	}

	if !saltIsValid {
		salt, err := getSalt(ctx, tx, saltLifetime)
		if err != nil {
			return err
		}

		s.salt = *salt
	}

	return nil
}

// getKey gets a secret key from the database or sets a new one with the given lifetime.
func getKey(ctx context.Context, tx *sql.Tx, lifetime time.Duration) (*key, error) {
	// Check if another cluster member has already updated the salt.
	r := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE key = 'volatile.secret.key'")
	err := r.Err()
	if err != nil {
		return nil, err
	}

	var dbSecretKey key
	err = r.Scan(&dbSecretKey)
	if err == nil && dbSecretKey.isValid() {
		// Another member updated the key, and it is in date, so use that.
		return &dbSecretKey, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	updatedSecret, err := newKey(lifetime)
	if err != nil {
		return nil, err
	}

	res, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('volatile.secret.key', ?)", updatedSecret)
	if err != nil {
		return nil, fmt.Errorf("Failed to set new shared secret: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("Failed to check key was correctly stored: %w", err)
	}

	if rowsAffected != 1 {
		return nil, fmt.Errorf("Failed to set new secret key - no rows affected")
	}

	return updatedSecret, nil
}

// newKey returns a key with the expiry set to the current UTC time, plus the lifetime in milliseconds.
func newKey(lifetime time.Duration) (*key, error) {
	buf := make([]byte, 512)
	n, err := rand.Read(buf)
	if err != nil {
		return nil, err
	}

	if n != 512 {
		return nil, errors.New("Not enough bytes to read")
	}

	return &key{
		expiry: time.Now().UTC().Add(lifetime).UnixMilli(),
		bytes:  buf,
	}, nil
}

// key is a combination of a timestamp (UTC Unix milliseconds) and 512 random bytes.
type key struct {
	expiry int64
	bytes  []byte
}

func (s key) unset(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM config WHERE key = 'volatile.secret.key'")
	return err
}

// String implements fmt.Stringer for key.
func (s key) String() string {
	if s.expiry == 0 || len(s.bytes) == 0 {
		return ""
	}

	return strconv.FormatInt(s.expiry, 10) + "." + base64.StdEncoding.EncodeToString(s.bytes)
}

// isValid returns true if the key is set and has not expired.
func (s key) isValid() bool {
	return len(s.bytes) == 512 && s.expiry > time.Now().UTC().UnixMilli()
}

// Scan implements sql.Scanner for key.
func (s *key) Scan(value any) error {
	if s == nil {
		return errors.New("Cannot scan secret key into nil value")
	}

	if value == nil {
		return nil
	}

	stringValue, err := driver.String.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid secret key type: %w", err)
	}

	secretStr, ok := stringValue.(string)
	if !ok {
		return fmt.Errorf("key should be a string, got `%v` (%T)", stringValue, stringValue)
	}

	tStr, secretStr, ok := strings.Cut(secretStr, ".")
	if !ok {
		return fmt.Errorf("Invalid cluster secret: Timestamp and secret must be separated by a '.'")
	}

	expiry, err := strconv.ParseInt(tStr, 10, 64)
	if err != nil {
		return err
	}

	secret, err := base64.StdEncoding.DecodeString(secretStr)
	if err != nil {
		return err
	}

	s.expiry = expiry
	s.bytes = secret

	return nil
}

// Value implements driver.Valuer for key.
func (s key) Value() (driver.Value, error) {
	if !s.isValid() {
		return nil, errors.New("Cannot write invalid secret to database")
	}

	return s.String(), nil
}

// getSalt gets a Salt from the database or sets a new one with the given lifetime.
func getSalt(ctx context.Context, tx *sql.Tx, lifetime time.Duration) (*salt, error) {
	// Check if another cluster member has already updated the salt.
	r := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE key = 'volatile.secret.salt'")
	err := r.Err()
	if err != nil {
		return nil, err
	}

	var dbSalt salt
	err = r.Scan(&dbSalt)
	if err == nil && dbSalt.isValid() {
		// Another member updated the salt, and it is in date, so use that.
		return &dbSalt, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	updatedSalt, err := newSalt(lifetime)
	if err != nil {
		return nil, err
	}

	res, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('volatile.secret.salt', ?)", updatedSalt)
	if err != nil {
		return nil, fmt.Errorf("Failed to set new shared salt: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("Failed to check salt was correctly stored: %w", err)
	}

	if rowsAffected != 1 {
		return nil, fmt.Errorf("Failed to set new salt - no rows affected")
	}

	return updatedSalt, nil
}

// newSalt returns a salt with the expiry set to the current UTC time, plus the lifetime in milliseconds.
func newSalt(lifetime time.Duration) (*salt, error) {
	u, err := ulid.New(uint64(time.Now().UTC().UnixMilli()+lifetime.Milliseconds()), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("Failed to create time embedded salt: %w", err)
	}

	return &salt{
		u: &u,
	}, nil
}

// salt is a wrapper for ulid.ULID for use as a random salt with a built-in expiry.
type salt struct {
	u *ulid.ULID
}

func (s salt) unset(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM config WHERE key = 'volatile.secret.salt'")
	return err
}

// String implements fmt.Stringer for salt.
func (s salt) String() string {
	if s.u == nil {
		return ""
	}

	return s.u.String()
}

// isValid returns true if the salt is set and has not expired.
func (s salt) isValid() bool {
	return s.u != nil && time.Now().UTC().UnixMilli() < ulid.Time(s.u.Time()).UTC().UnixMilli()
}

// Scan implements sql.Scanner for salt.
func (s *salt) Scan(value any) error {
	if s == nil {
		return errors.New("Cannot scan salt into nil value")
	}

	if value == nil {
		return nil
	}

	stringValue, err := driver.String.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid salt type: %w", err)
	}

	saltStr, ok := stringValue.(string)
	if !ok {
		return fmt.Errorf("salt should be a string, got `%v` (%T)", stringValue, stringValue)
	}

	u, err := ulid.Parse(saltStr)
	if err != nil {
		return fmt.Errorf("salt should be a ULID: %w", err)
	}

	s.u = &u

	return nil
}

// Value implements driver.Valuer for salt.
func (s salt) Value() (driver.Value, error) {
	if !s.isValid() {
		return nil, errors.New("Cannot write invalid salt to database")
	}

	return s.u.String(), nil
}
