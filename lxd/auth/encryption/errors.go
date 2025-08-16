package encryption

import (
	"errors"
	"strconv"
)

var (
	// errSecretTooShort is returned when the given secret is too short to extract a key of the given length.
	errSecretTooShort = errors.New("Secret too short for key length")

	// errInsufficientSalt is returned if a provided salt is less than 16 bytes.
	errInsufficientSalt = errors.New("Minimum salt length is 16 bytes")

	// errKeyTooShort is returned if a provided length is less than 32 bytes. This is the minimum value supported and is
	// used for AES-256 encryption.
	errKeyTooShort = errors.New("Minimum key length is 32 bytes")

	// errKeyTooLong is returned if the provided length is greater than the hash size.
	errKeyTooLong = errors.New("Maximum key length is " + strconv.Itoa(hashFunc().Size()) + " bytes")

	// errNoUsage is returned if the caller did not specify key usage.
	errNoUsage = errors.New("Usage must be specified")
)
