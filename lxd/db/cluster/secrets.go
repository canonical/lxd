package cluster

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// secretValueLength defines the length of the secret value.
const secretValueLength = 64

// newSecretValue returns a byte slice of length secretValueLength, read from [rand.Reader].
func newSecretValue() []byte {
	secretValue := make([]byte, secretValueLength)
	_, _ = rand.Read(secretValue)
	return secretValue
}

// newSecret mints a new Secret.
func newSecret() Secret {
	return Secret{
		Value:     newSecretValue(),
		CreatedAt: time.Now().UTC().Unix(),
	}
}

// Secret contains a byte slice of length secretValueLength and a creation time as a unix epoch (seconds).
type Secret struct {
	Value     []byte `json:"value"`
	CreatedAt int64  `json:"created_at"`
}

// IsValid returns true if the time now is less than the time that the secret was created, plus the expiry.
func (s Secret) IsValid(expiry int64) bool {
	return s.CreatedAt+expiry > time.Now().UTC().Unix()
}

// Secrets is a concrete type for a slice of Secret.
type Secrets []Secret

// IsValid returns true if the first Secret in Secrets is valid.
func (s Secrets) IsValid(lifetime int64) bool {
	return len(s) > 0 && s[0].IsValid(lifetime)
}

// Rotate returns a new Secrets, with a new Secret prepended to the slice.
// The slice is always truncated to a maximum of two elements.
func (s Secrets) Rotate() Secrets {
	secrets := append([]Secret{newSecret()}, s...)
	if len(secrets) > 2 {
		secrets = secrets[:2]
	}

	return secrets
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
