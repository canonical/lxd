package trust

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HMACArgon2 represents the tooling for creating and validating HMACs
// bundled with the key derivation function argon2.
type HMACArgon2 struct {
	HMAC
	salt []byte
}

// NewHMACArgon2 returns a new HMAC implementation using argon2.
// If the salt is nil a random one is generated.
// Recommended defaults according to https://www.rfc-editor.org/rfc/rfc9106#section-4-6.2.
// We use the second recommended option to not require a system having 2 GiB of memory.
func NewHMACArgon2(password []byte, salt []byte, conf HMACConf) (HMACFormatter, error) {
	if salt == nil {
		// 128 bit salt.
		salt = make([]byte, 16)
		_, err := rand.Read(salt)
		if err != nil {
			return nil, fmt.Errorf("Failed to create salt: %w", err)
		}
	}

	// 3 iterations.
	var time uint32 = 3

	// 64 MiB memory.
	var memory uint32 = 64 * 1024

	// 4 lanes.
	var threads uint8 = 4

	// 256 bit tag size.
	var keyLen uint32 = 32

	return &HMACArgon2{
		HMAC: HMAC{
			conf: conf,
			key:  argon2.IDKey(password, salt, time, memory, threads, keyLen),
		},

		salt: salt,
	}, nil
}

// HTTPHeader returns the actual HMAC alongside it's salt together with the used version.
func (h *HMACArgon2) HTTPHeader(hmac []byte) string {
	return fmt.Sprintf("%s %s:%s", h.conf.Version, hex.EncodeToString(h.salt), hex.EncodeToString(hmac))
}

// ParseHTTPHeader extracts the actual version, HMAC and it's salt from the Authorization header.
func (h *HMACArgon2) ParseHTTPHeader(header string) (HMACVersion, []byte, error) {
	version, hmacStr, err := h.splitVersionFromHMAC(header)
	if err != nil {
		return "", nil, err
	}

	// In case of argon2 the HMAC has the salt as prefix.
	authHeaderDetails := strings.Split(hmacStr, ":")
	if len(authHeaderDetails) != 2 {
		return "", nil, errors.New("Argon2 salt or HMAC is missing")
	}

	if authHeaderDetails[0] == "" {
		return "", nil, fmt.Errorf("Argon2 salt cannot be empty")
	}

	if authHeaderDetails[1] == "" {
		return "", nil, fmt.Errorf("Argon2 HMAC cannot be empty")
	}

	salt, err := hex.DecodeString(authHeaderDetails[0])
	if err != nil {
		return "", nil, fmt.Errorf("Failed to decode the argon2 salt: %w", err)
	}

	h.salt = salt

	hmac, err := hex.DecodeString(authHeaderDetails[1])
	if err != nil {
		return "", nil, fmt.Errorf("Failed to decode the argon2 HMAC: %w", err)
	}

	return version, hmac, nil
}
