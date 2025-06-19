package cluster

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Secret contains a 512 byte slice and a creation time (in unix milliseconds).
type Secret struct {
	Value     []byte `json:"value"`
	CreatedAt int64  `json:"created_at"`
}

// IsValid returns true if the Secret is younger than the given lifetime.
func (s Secret) IsValid(lifetime int64) bool {
	return s.CreatedAt+lifetime > time.Now().UTC().UnixMilli()
}

// Secrets is a concrete type for a slice of Secret. This allows defining convenient methods.
type Secrets []Secret

// IsValid returns true if the first Secret in Secrets is younger than the given lifetime.
func (s Secrets) IsValid(lifetime int64) bool {
	return len(s) > 0 && s[0].IsValid(lifetime)
}

// Rotate prepends a new secret. Currently, the slice length is limited to 2.
func (s *Secrets) Rotate() {
	secretValue := make([]byte, 512)
	_, _ = rand.Read(secretValue)
	newSecret := Secret{
		Value:     secretValue,
		CreatedAt: time.Now().UTC().UnixMilli(),
	}

	secrets := append([]Secret{newSecret}, *s...)
	if len(secrets) > 2 {
		secrets = secrets[:2]
	}

	*s = secrets
}

// Value implements driver.Valuer for Secrets. This performs JSON marshalling of the slice.
// Note that since the Value field of Secret is a []byte, it is automatically base64 encoded (see
// https://pkg.go.dev/encoding/json#Marshal).
func (s Secrets) Value() (driver.Value, error) {
	jsonVal, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	return string(jsonVal), nil
}

// Scan implements sql.Scanner for Secrets. This unmarshals the JSON.
func (s *Secrets) Scan(value any) error {
	if value == nil {
		return errors.New("Secrets cannot be null")
	}

	strVal, err := driver.String.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid secret type: %w", err)
	}

	str, ok := strVal.(string)
	if !ok {
		return fmt.Errorf("Secrets should be a string, got `%v` (%T)", strVal, strVal)
	}

	newS := Secrets{}
	err = json.Unmarshal([]byte(str), &newS)
	if err != nil {
		return fmt.Errorf("Failed to scan cluster secret: %w", err)
	}

	*s = newS
	return nil
}
