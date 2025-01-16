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
	// ParseHTTPHeader expects an Authorization header and returns a new instance of HMACFormatter
	// using the current implementation.
	// This allows parsing an Authorization header based on information which is already set
	// in the parent HMACFormatter like the HMACVersion.
	// Furthermore it returns the actual HMAC.
	ParseHTTPHeader(header string) (HMACFormatter, []byte, error)
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

// splitVersionFromHMAC is a helper to separate the HMAC version from the actual HMAC.
// Depending on the used format the HMAC value has to be splitted further (see argon2).
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

// Version returns the used HMAC version.
func (h *HMAC) Version() HMACVersion {
	return h.conf.Version
}

// HTTPHeader returns the actual HMAC together with the used version.
func (h *HMAC) HTTPHeader(hmac []byte) string {
	return string(h.conf.Version) + " " + hex.EncodeToString(hmac)
}

// ParseHTTPHeader parses the given header and returns a new instance of the default formatter
// together with the actual HMAC.
// It's using the parent formatter's configuration.
func (h *HMAC) ParseHTTPHeader(header string) (HMACFormatter, []byte, error) {
	version, hmacStr, err := h.splitVersionFromHMAC(header)
	if err != nil {
		return nil, nil, err
	}

	hmacFromHeader, err := hex.DecodeString(hmacStr)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to decode the HMAC: %w", err)
	}

	hNew := NewHMAC(h.key, NewDefaultHMACConf(version))
	return hNew, hmacFromHeader, nil
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

// HMACAuthorizationHeader returns the HMAC as an Authorization header using the given formatter.
func HMACAuthorizationHeader(h HMACFormatter, v any) (string, error) {
	hmacBytes, err := h.WriteJSON(v)
	if err != nil {
		return "", api.StatusErrorf(http.StatusInternalServerError, "Failed to calculate HMAC from struct: %w", err)
	}

	return h.HTTPHeader(hmacBytes), nil
}

// HMACEqual checks whether or not the Authorization header matches the HMAC
// derived using the given formatter.
// The formatter indicates the used format together with some basic HMAC configuration (e.g. key and version).
func HMACEqual(h HMACFormatter, r *http.Request) error {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return api.NewStatusError(http.StatusBadRequest, "Authorization header is missing")
	}

	hFromHeader, hmacFromHeader, err := h.ParseHTTPHeader(authHeader)
	if err != nil {
		return api.StatusErrorf(http.StatusInternalServerError, "Failed to parse Authorization header: %w", err)
	}

	// Check if the HMAC version from the formatter matches the one found in the request header.
	headerVersion := hFromHeader.Version()
	expectedVersion := h.Version()
	if headerVersion != expectedVersion {
		return api.StatusErrorf(http.StatusBadRequest, "Authorization header uses version %q but expected %q", headerVersion, expectedVersion)
	}

	// Use the formatter derived from the header to re-create the HMAC from the request's body.
	hmacFromBody, err := hFromHeader.WriteRequest(r)
	if err != nil {
		return api.StatusErrorf(http.StatusInternalServerError, "Failed to calculate HMAC from request body: %w", err)
	}

	// Compare if the HMAC from the header matches the one computed over the body.
	if !hmac.Equal(hmacFromHeader, hmacFromBody) {
		return api.NewStatusError(http.StatusForbidden, "Invalid HMAC")
	}

	return nil
}
