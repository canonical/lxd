package trust

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// HMACFormatter represents arbitrary formats to diplay and parse the actual HMAC.
// For example implementations like argon2 extend the format with an additional salt.
// Example using argon2: `Authorization: <version> <salt>:<HMAC>`.
type HMACFormatter interface {
	// The Write* methods allow the creation of an HMAC based on various inputs.
	WriteBytes(b []byte) ([]byte, error)
	WriteJSON(v any) ([]byte, error)
	WriteRequest(r *http.Request) ([]byte, error)

	// Version returns the current HMAC version set for the format.
	Version() HMACVersion
	// HTTPHeader expects the HMAC computed over the payload and returns the final Authorization header.
	HTTPHeader(hmac []byte) string
	// ParseHTTPHeader expects an Authorization header and returns the used version and HMAC.
	ParseHTTPHeader(header string) (HMACVersion, []byte, error)
	// Equal compares two HMACs and returns true in case of a match.
	Equal(hmac1 []byte, hmac2 []byte) bool
}

// HMAC represents the the tooling for creating and validating HMACs.
type HMAC struct {
	conf HMACConf
	key  []byte
}

// HMACVersion indicates the version used for the authorization header format.
// This allows to define a format used by the header so that the scheme can be modified
// in future implementations without breaking already existing versions.
// An example version can be `LXD1.0` which indicates that this is version 1.0 of the
// LXD HMAC authentication scheme.
// The format used after the version is dependant on the actual implementation:
// Example: `Authorization: <version> <format including the HMAC>`.
type HMACVersion string

// HMACConf represents the HMAC configuration.
type HMACConf struct {
	HashFunc func() hash.Hash
	Version  HMACVersion
}

// NewDefaultHMACConf returns the default configuration for HMAC.
func NewDefaultHMACConf(version HMACVersion) HMACConf {
	return HMACConf{
		HashFunc: sha256.New,
		Version:  version,
	}
}

// NewHMAC returns a new instance of HMAC.
func NewHMAC(key []byte, conf HMACConf) HMACFormatter {
	return &HMAC{
		conf: conf,
		key:  key,
	}
}

func (h *HMAC) splitVersionFromHMAC(header string) (HMACVersion, string, error) {
	authHeaderSplit := strings.Split(header, " ")
	if len(authHeaderSplit) != 2 {
		return "", "", errors.New("Version or HMAC is missing")
	}

	if authHeaderSplit[0] == "" {
		return "", "", errors.New("Version cannot be empty")
	}

	if authHeaderSplit[1] == "" {
		return "", "", errors.New("HMAC cannot be empty")
	}

	return HMACVersion(authHeaderSplit[0]), authHeaderSplit[1], nil
}

// HTTPHeader returns the actual HMAC together with the used version.
func (h *HMAC) HTTPHeader(hmac []byte) string {
	return fmt.Sprintf("%s %s", h.conf.Version, hex.EncodeToString(hmac))
}

// Version returns the used HMAC version.
func (h *HMAC) Version() HMACVersion {
	return h.conf.Version
}

// ParseHTTPHeader extracts the actual version and HMAC from the Authorization header.
func (h *HMAC) ParseHTTPHeader(header string) (HMACVersion, []byte, error) {
	version, hmacStr, err := h.splitVersionFromHMAC(header)
	if err != nil {
		return "", nil, err
	}

	hmac, err := hex.DecodeString(hmacStr)
	if err != nil {
		return "", nil, fmt.Errorf("Failed to decode the HMAC: %w", err)
	}

	return version, hmac, nil
}

// WriteBytes creates a new HMAC hash using the given bytes.
func (h *HMAC) WriteBytes(b []byte) ([]byte, error) {
	mac := hmac.New(h.conf.HashFunc, h.key)
	_, err := mac.Write(b)
	if err != nil {
		return nil, fmt.Errorf("Failed to create HMAC: %w", err)
	}

	return mac.Sum(nil), nil
}

// WriteJSON creates a new HMAC hash using the given struct.
func (h *HMAC) WriteJSON(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal payload: %w", err)
	}

	return h.WriteBytes(payload)
}

// WriteRequest creates a new HMAC hash using the given request.
// It will extract the requests body.
func (h *HMAC) WriteRequest(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read request body: %w", err)
	}

	defer func() {
		// Reset the request body for the actual handler.
		r.Body = io.NopCloser(bytes.NewBuffer(body))
	}()

	err = r.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("Failed to close the request body: %w", err)
	}

	return h.WriteBytes(body)
}

// Equal returns true in case hmac1 is identical to hmac2.
func (h *HMAC) Equal(hmac1 []byte, hmac2 []byte) bool {
	return hmac.Equal(hmac1, hmac2)
}

// HMACEqual extracts the HMAC from the Authorization header and
// validates if it is equal to the HMAC created from the request's body using the given formatter.
func HMACEqual(h HMACFormatter, r *http.Request) error {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return api.StatusErrorf(http.StatusBadRequest, "Authorization header is missing")
	}

	version, hmacFromHeader, err := h.ParseHTTPHeader(authHeader)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Failed to parse Authorization header: %w", err)
	}

	hmacVersion := h.Version()
	if version != hmacVersion {
		return api.StatusErrorf(http.StatusBadRequest, "Authorization header uses version %q but expected %q", version, hmacVersion)
	}

	hmacFromBody, err := h.WriteRequest(r)
	if err != nil {
		return api.StatusErrorf(http.StatusInternalServerError, "Failed to calculate HMAC from request body: %w", err)
	}

	if !hmac.Equal(hmacFromHeader, hmacFromBody) {
		return api.StatusErrorf(http.StatusForbidden, "Invalid HMAC")
	}

	return nil
}

// HMACAuthorizationHeader returns the HMAC as an Authorization header using the given formatter.
func HMACAuthorizationHeader(h HMACFormatter, v any) (string, error) {
	hmacBytes, err := h.WriteJSON(v)
	if err != nil {
		return "", err
	}

	return h.HTTPHeader(hmacBytes), nil
}
