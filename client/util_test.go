package lxd

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared"
)

func Test_setQueryParam(t *testing.T) {
	type args struct {
		uri   string
		param string
		value string
	}

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "no existing params",
			args: args{
				uri:   "http://example.com",
				param: "foo",
				value: "bar",
			},
			want:    "http://example.com?foo=bar",
			wantErr: false,
		},
		{
			name: "existing params",
			args: args{
				uri:   "http://example.com?baz=qux",
				param: "foo",
				value: "bar",
			},
			want:    "http://example.com?baz=qux&foo=bar",
			wantErr: false,
		},
		{
			name: "overwrite existing param",
			args: args{
				uri:   "http://example.com?foo=old",
				param: "foo",
				value: "new",
			},
			want:    "http://example.com?foo=new",
			wantErr: false,
		},
		{
			name: "invalid URI",
			args: args{
				uri:   "http://%41:8080/", // Invalid percent-encoding
				param: "foo",
				value: "bar",
			},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := setQueryParam(tt.args.uri, tt.args.param, tt.args.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("setQueryParam() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("setQueryParam() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_urlsToResourceNames(t *testing.T) {
	type args struct {
		matchPrefix string
		urls        []string
	}

	tests := []struct {
		name        string
		args        args
		want        []string
		expectError bool
		err         error
	}{
		{
			name: "simple tests",
			args: args{
				matchPrefix: "1.0",
				urls: []string{
					"http://example.com/1.0/instances",
					"http://example.com/1.0/instances?recursion=1",
					"http://example.com/1.0/instances?recursion=1&project=default",
				},
			},
			want: []string{"instances", "instances", "instances"},
		},
		{
			name: "empty list",
			args: args{
				matchPrefix: "",
				urls:        []string{},
			},
			want: []string{},
		},
		{
			name: "no matching prefix",
			args: args{
				matchPrefix: "2.0",
				urls: []string{
					"http://example.com/1.0/instances",
					"http://example.com/1.0/instances?recursion=1",
				},
			},
			want:        []string{},
			expectError: true,
			err:         errors.New("Unexpected URL path"),
		},
		{
			name: "invalid URL",
			args: args{
				matchPrefix: "1.0",
				urls: []string{
					"http://%41/1.0/instances",
				},
			},
			want:        []string{},
			expectError: true,
			err:         errors.New("Failed parsing URL"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := urlsToResourceNames(tt.args.matchPrefix, tt.args.urls...)
			if (err != nil) != tt.expectError {
				t.Errorf("urlsToResourceNames() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError && err != nil && !strings.Contains(err.Error(), tt.err.Error()) {
				t.Errorf("urlsToResourceNames() error = %v, want %v", err, tt.err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("urlsToResourceNames() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_parseFilters(t *testing.T) {
	tests := []struct {
		name    string
		filters []string
		want    string
	}{
		{
			name:    "single filter",
			filters: []string{"key=value"},
			want:    "key eq value",
		},
		{
			name:    "multiple filters",
			filters: []string{"key=value", "foo=bar", "ignored"},
			want:    "key eq value and foo eq bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFilters(tt.filters)
			if got != tt.want {
				t.Errorf("parseFilters() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_openBrowser(t *testing.T) {
	tests := []struct {
		name string
		env  string
		url  string
	}{
		{
			name: "valid URL but none browser",
			env:  "none",
			url:  "http://example.com",
		},
		{
			name: "valid URL for a fake browser command",
			env:  "echo",
			url:  "http://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BROWSER", tt.env)
			err := openBrowser(tt.url)
			if err != nil {
				t.Errorf("openBrowser() unexpected error = %v", err)
			}
		})
	}
}

func Test_tlsHTTPClient_Fingerprint(t *testing.T) {
	// Setup a test TLS server
	certInfo := shared.TestingKeyPair()
	pubKey, err := certInfo.PublicKeyX509()
	require.NoError(t, err)

	tlsConfig, err := shared.GetTLSConfig(pubKey)
	require.NoError(t, err)

	tlsConfig.Certificates = []tls.Certificate{certInfo.KeyPair()}

	// Create a local TCP listener on any available port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	// Start a simple server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.TLS = tlsConfig
	server.StartTLS()
	defer server.Close()

	// Calculate fingerprint
	fingerprint := certInfo.Fingerprint()
	require.NotEmpty(t, fingerprint)

	tests := []struct {
		name         string
		fingerprint  string
		serverCert   string
		insecureSkip bool
		expectError  error
	}{
		{
			name:         "Valid fingerprint",
			fingerprint:  fingerprint,
			insecureSkip: false,
		},
		{
			name:         "Invalid fingerprint",
			fingerprint:  "badfingerprint",
			insecureSkip: false,
			expectError:  errors.New("Server certificate fingerprint mismatch"),
		},
		{
			name:         "Invalid fingerprint, valid server cert (takes precedence)",
			fingerprint:  "badfingerprint",
			serverCert:   string(certInfo.PublicKey()),
			insecureSkip: false,
		},
		{
			name:         "No fingerprint (should fail verification of self-signed cert)",
			fingerprint:  "",
			insecureSkip: false,
			expectError:  errors.New("failed to verify certificate"),
		},
		{
			name:         "No fingerprint but insecure skip (should pass)",
			fingerprint:  "",
			insecureSkip: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := tlsHTTPClient(nil, "", "", "", test.serverCert, test.insecureSkip, false, nil, nil, test.fingerprint)
			require.NoError(t, err)

			resp, err := client.Get(server.URL)
			if err != nil {
				if test.expectError == nil {
					require.FailNow(t, fmt.Sprintf("Expected no error but got: %v", err))
				}

				require.ErrorContains(t, err, test.expectError.Error())
			} else {
				resp.Body.Close()

				if test.expectError != nil {
					require.FailNow(t, fmt.Sprintf("Expected error %v but got nil", test.expectError))
				}

				require.Equal(t, http.StatusOK, resp.StatusCode, "Expected HTTP 200 OK")
			}
		})
	}
}

func Test_tlsHTTPClient_CertificateValidity(t *testing.T) {
	tests := []struct {
		name         string
		validAfter   time.Time
		validBefore  time.Time
		insecureSkip bool
		expectError  error
	}{
		{
			name:        "Valid certificate",
			validAfter:  time.Now().Add(-time.Hour),
			validBefore: time.Now().Add(time.Hour),
		},
		{
			name:         "Valid certificate | Skip verification",
			validAfter:   time.Now().Add(-time.Hour),
			validBefore:  time.Now().Add(time.Hour),
			insecureSkip: true,
		},
		{
			name:        "Invalid certificate | Expired",
			validAfter:  time.Now().Add(-6 * time.Hour),
			validBefore: time.Now().Add(-4 * time.Hour),
			expectError: errors.New("expired"),
		},
		{
			name:        "Invalid certificate | Not valid yet",
			validAfter:  time.Now().Add(2 * time.Hour),
			validBefore: time.Now().Add(4 * time.Hour),
			expectError: errors.New("valid after"),
		},
		{
			name:         "Invalid certificate | Skip verification",
			validAfter:   time.Now().Add(2 * time.Hour),
			validBefore:  time.Now().Add(4 * time.Hour),
			insecureSkip: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Setup a test TLS server
			certInfo := shared.TestingKeyPairWithValidity(test.validAfter, test.validBefore)
			pubKey, err := certInfo.PublicKeyX509()
			require.NoError(t, err)

			tlsConfig, err := shared.GetTLSConfig(pubKey)
			require.NoError(t, err)

			tlsConfig.Certificates = []tls.Certificate{certInfo.KeyPair()}

			// Create a local TCP listener on any available port.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer func() { _ = listener.Close() }()

			// Start a simple server
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			server := httptest.NewUnstartedServer(handler)
			server.Listener = listener
			server.TLS = tlsConfig
			server.StartTLS()
			defer server.Close()

			// Calculate certificate fingerprint.
			fingerprint := certInfo.Fingerprint()
			require.NotEmpty(t, fingerprint)

			client, err := tlsHTTPClient(nil, "", "", "", "", test.insecureSkip, false, nil, nil, fingerprint)
			require.NoError(t, err)

			// Try connecting to the server.
			_, err = client.Get(server.URL)
			if test.expectError != nil {
				require.ErrorContains(t, err, test.expectError.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
