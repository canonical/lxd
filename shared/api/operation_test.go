package api

import (
	"reflect"
	"testing"
	"time"
)

func TestOperation_ToCertificateAddToken(t *testing.T) {
	expiresAtStr := "2023-01-01T12:00:00.123456789Z"
	// We use time.UTC to ensure consistency regardless of local timezone settings,
	// because the input string ends with Z.
	expiresAt, _ := time.Parse(time.RFC3339Nano, expiresAtStr)

	tests := []struct {
		name     string
		metadata map[string]any
		want     *CertificateAddToken
		wantErr  bool
	}{
		{
			name: "valid token with expiry",
			metadata: map[string]any{
				"request": map[string]any{
					"name": "my-client",
				},
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
				"expiresAt":   expiresAtStr,
			},
			want: &CertificateAddToken{
				ClientName:  "my-client",
				Secret:      "my-secret",
				Fingerprint: "my-fingerprint",
				Addresses:   []string{"1.2.3.4"},
				ExpiresAt:   expiresAt,
			},
			wantErr: false,
		},
		{
			name: "valid token without expiry",
			metadata: map[string]any{
				"request": map[string]any{
					"name": "my-client",
				},
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			want: &CertificateAddToken{
				ClientName:  "my-client",
				Secret:      "my-secret",
				Fingerprint: "my-fingerprint",
				Addresses:   []string{"1.2.3.4"},
			},
			wantErr: false,
		},
		{
			name: "missing request map",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "missing client name",
			metadata: map[string]any{
				"request":     map[string]any{},
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "invalid common fields",
			metadata: map[string]any{
				"request": map[string]any{
					"name": "my-client",
				},
				// missing secret
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "invalid expiry format",
			metadata: map[string]any{
				"request": map[string]any{
					"name": "my-client",
				},
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
				"expiresAt":   "invalid-date",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := Operation{
				Metadata: tt.metadata,
			}

			got, err := op.ToCertificateAddToken()
			if (err != nil) != tt.wantErr {
				t.Errorf("ToCertificateAddToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ToCertificateAddToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOperation_ToClusterJoinToken(t *testing.T) {
	expiresAtStr := "2023-01-01T12:00:00.123456789Z"
	// We use time.UTC to ensure consistency regardless of local timezone settings.
	expiresAt, _ := time.Parse(time.RFC3339Nano, expiresAtStr)

	tests := []struct {
		name     string
		metadata map[string]any
		want     *ClusterMemberJoinToken
		wantErr  bool
	}{
		{
			name: "valid token",
			metadata: map[string]any{
				"serverName":  "my-server",
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
				"expiresAt":   expiresAtStr,
			},
			want: &ClusterMemberJoinToken{
				ServerName:  "my-server",
				Secret:      "my-secret",
				Fingerprint: "my-fingerprint",
				Addresses:   []string{"1.2.3.4"},
				ExpiresAt:   expiresAt,
			},
			wantErr: false,
		},
		{
			name: "missing server name",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
				"expiresAt":   expiresAtStr,
			},
			wantErr: true,
		},
		{
			name: "invalid common fields",
			metadata: map[string]any{
				"serverName": "my-server",
				// missing secret
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
				"expiresAt":   expiresAtStr,
			},
			wantErr: true,
		},
		{
			name: "missing expiry",
			metadata: map[string]any{
				"serverName":  "my-server",
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "invalid expiry format",
			metadata: map[string]any{
				"serverName":  "my-server",
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
				"expiresAt":   "invalid-date",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := Operation{
				Metadata: tt.metadata,
			}

			got, err := op.ToClusterJoinToken()
			if (err != nil) != tt.wantErr {
				t.Errorf("ToClusterJoinToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ToClusterJoinToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOperation_parseCommonTokenFields(t *testing.T) {
	tests := []struct {
		name            string
		metadata        map[string]any
		wantSecret      string
		wantFingerprint string
		wantAddresses   []string
		wantErr         bool
	}{
		{
			name: "valid common fields",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4", "5.6.7.8"},
			},
			wantSecret:      "my-secret",
			wantFingerprint: "my-fingerprint",
			wantAddresses:   []string{"1.2.3.4", "5.6.7.8"},
			wantErr:         false,
		},
		{
			name: "missing secret",
			metadata: map[string]any{
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "invalid secret type",
			metadata: map[string]any{
				"secret":      123,
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "missing fingerprint",
			metadata: map[string]any{
				"secret":    "my-secret",
				"addresses": []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "invalid fingerprint type",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": 123,
				"addresses":   []any{"1.2.3.4"},
			},
			wantErr: true,
		},
		{
			name: "missing addresses",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
			},
			wantErr: true,
		},
		{
			name: "invalid addresses type",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   "not-an-array",
			},
			wantErr: true,
		},
		{
			name: "invalid address element type",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{"1.2.3.4", 123},
			},
			wantErr: true,
		},
		{
			name: "empty addresses",
			metadata: map[string]any{
				"secret":      "my-secret",
				"fingerprint": "my-fingerprint",
				"addresses":   []any{},
			},
			wantSecret:      "my-secret",
			wantFingerprint: "my-fingerprint",
			wantAddresses:   []string{},
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := Operation{
				Metadata: tt.metadata,
			}

			secret, fingerprint, addresses, gotErr := op.parseCommonTokenFields()
			if (gotErr != nil) != tt.wantErr {
				t.Errorf("parseCommonTokenFields() error = %v, wantErr %v", gotErr, tt.wantErr)
				return
			}

			if secret != tt.wantSecret {
				t.Errorf("parseCommonTokenFields() secret = %v, want %v", secret, tt.wantSecret)
			}

			if fingerprint != tt.wantFingerprint {
				t.Errorf("parseCommonTokenFields() fingerprint = %v, want %v", fingerprint, tt.wantFingerprint)
			}

			if !reflect.DeepEqual(addresses, tt.wantAddresses) {
				t.Errorf("parseCommonTokenFields() addresses = %v, want %v", addresses, tt.wantAddresses)
			}
		})
	}
}
