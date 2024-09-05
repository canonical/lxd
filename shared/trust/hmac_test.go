package trust

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// errorCloser implements the Reader and Closer interfaces and allows to
// simulate errors during read and close operations.
type errorReaderCloser struct {
	readErr  error
	closeErr error
}

// Read simulates a read from errorReaderCloser.
// Return io.EOF to exit out any read operations.
func (e errorReaderCloser) Read(p []byte) (n int, err error) {
	return 0, e.readErr
}

// Close simulates a close of errorReaderCloser.
func (e errorReaderCloser) Close() error {
	return e.closeErr
}

func TestCreateHMAC(t *testing.T) {
	tests := []struct {
		name           string
		conf           HMACConf
		key            []byte
		password       []byte
		hexSalt        string
		payload        any
		expectedHeader string
		expectedErr    error
	}{
		{
			name:           "Create HMAC from simple payload",
			conf:           NewDefaultHMACConf("LXD1.0"),
			key:            []byte("foo"),
			payload:        map[string]string{"hello": "world"},
			expectedHeader: "LXD1.0 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717",
		},
		{
			name:           "Create HMAC from simple payload using argon2 as KDF",
			conf:           NewDefaultHMACConf("LXD1.0"),
			password:       []byte("foo"),
			hexSalt:        "caffee",
			payload:        map[string]string{"hello": "world"},
			expectedHeader: "LXD1.0 caffee:b4b19532928620a1d54e7d1c58e4baaa916a8e0023ed8a08b2b05038d6da189a",
		},
		{
			name:        "Reject creating HMAC from invalid payload",
			conf:        NewDefaultHMACConf("LXD1.0"),
			key:         []byte("foo"),
			payload:     make(chan bool),
			expectedErr: errors.New("Failed to marshal payload: json: unsupported type: chan bool"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			var hmac HMACFormatter
			if tt.key != nil {
				hmac = NewHMAC(tt.key, tt.conf)
			}

			if tt.password != nil {
				salt, err := hex.DecodeString(tt.hexSalt)
				require.NoError(t, err)

				hmac, err = NewHMACArgon2(tt.password, salt, tt.conf)
				require.NoError(t, err)
			}

			hmacStr, err := HMACAuthorizationHeader(hmac, tt.payload)
			if tt.expectedErr != nil {
				require.Equal(t, tt.expectedErr.Error(), err.Error())
			} else {
				require.Equal(t, tt.expectedHeader, hmacStr)
			}
		})
	}
}

func TestValidateHMAC(t *testing.T) {
	tests := []struct {
		name        string
		conf        HMACConf
		key         []byte
		password    []byte
		hexSalt     string
		request     *http.Request
		expectedErr error
	}{
		{
			name: "Validate HMAC from request header",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"hello":"world"}`)),
			},
		},
		{
			name: "Validate non-matching HMAC from request header",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"hello":"world","modified":"body"}`)),
			},
			expectedErr: errors.New("Invalid HMAC"),
		},
		{
			name:     "Validate HMAC from request header using argon2 as KDF",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 caffee:b4b19532928620a1d54e7d1c58e4baaa916a8e0023ed8a08b2b05038d6da189a"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"hello":"world"}`)),
			},
		},
		{
			name:     "Validate non-matching HMAC from request header using argon2 as KDF",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 caffee:b4b19532928620a1d54e7d1c58e4baaa916a8e0023ed8a08b2b05038d6da189a"},
				},
				Body: io.NopCloser(bytes.NewBufferString(`{"hello":"world","modified":"body"}`)),
			},
			expectedErr: errors.New("Invalid HMAC"),
		},
		{
			name: "Reject header missing the version",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"invalid"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Version or HMAC is missing"),
		},
		{
			name:     "Reject header missing the version using argon2 as KDF",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"invalid"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Version or HMAC is missing"),
		},
		{
			name:     "Reject header missing the HMAC and salt combination using argon2 as KDF",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 caffee"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Argon2 salt or HMAC is missing"),
		},
		{
			name:     "Reject header with a non hex salt using argon2 as KDF",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 nonhex:abc"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Failed to decode the argon2 salt: encoding/hex: invalid byte: U+006E 'n'"),
		},
		{
			name:     "Reject header with a non hex HMAC using argon2 as KDF",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 caffee:nonhex"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Failed to decode the argon2 HMAC: encoding/hex: invalid byte: U+006E 'n'"),
		},
		{
			name:        "Reject request with missing Authorization header",
			conf:        NewDefaultHMACConf("LXD1.0"),
			request:     &http.Request{},
			expectedErr: errors.New("Authorization header is missing"),
		},
		{
			name: "Reject request with non-matching HMAC version",
			conf: NewDefaultHMACConf("LXD2.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717"},
				},
			},
			expectedErr: errors.New(`Authorization header uses version "LXD1.0" but expected "LXD2.0"`),
		},
		{
			name: "Reject request with a non hex HMAC",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 nonhexcharacters"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Failed to decode the HMAC: encoding/hex: invalid byte: U+006E 'n'"),
		},
		{
			name: "Reject request whose body cannot be read",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717"},
				},
				// Use reader that errors out immediately.
				Body: errorReaderCloser{readErr: errors.New("Fail always")},
			},
			expectedErr: errors.New("Failed to calculate HMAC from request body: Failed to read request body: Fail always"),
		},
		{
			name: "Reject request whose body cannot be closed",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717"},
				},
				// Use reader that errors out immediately.
				// Return EOF from the reader to trigger a Close.
				Body: errorReaderCloser{readErr: io.EOF, closeErr: errors.New("Fail always")},
			},
			expectedErr: errors.New("Failed to calculate HMAC from request body: Failed to close the request body: Fail always"),
		},
		{
			name: "Reject request with empty HMAC version",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{" 4022ad4878aff5a3bbd815aec63cce26cb5e8abd4df69589312cd0dee25fd717"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Version cannot be empty"),
		},
		{
			name: "Reject request with empty HMAC",
			conf: NewDefaultHMACConf("LXD1.0"),
			key:  []byte("foo"),
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 "},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: HMAC cannot be empty"),
		},
		{
			name:     "Reject request with empty argon2 salt",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 :abc"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Argon2 salt cannot be empty"),
		},
		{
			name:     "Reject request with empty argon2 HMAC",
			conf:     NewDefaultHMACConf("LXD1.0"),
			password: []byte("foo"),
			hexSalt:  "caffee",
			request: &http.Request{
				Header: http.Header{
					"Authorization": []string{"LXD1.0 caffee:"},
				},
			},
			expectedErr: errors.New("Failed to parse Authorization header: Argon2 HMAC cannot be empty"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			var hmac HMACFormatter
			if tt.key != nil {
				hmac = NewHMAC(tt.key, tt.conf)
			}

			if tt.password != nil {
				salt, err := hex.DecodeString(tt.hexSalt)
				require.NoError(t, err)

				hmac, err = NewHMACArgon2(tt.password, salt, tt.conf)
				require.NoError(t, err)
			}

			err = HMACEqual(hmac, tt.request)
			if tt.expectedErr != nil {
				require.Equal(t, tt.expectedErr.Error(), err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
