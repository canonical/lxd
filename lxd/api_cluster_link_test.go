package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/api"
)

func TestValidateClusterLinkName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names.
		{name: "simple", input: "primary", wantErr: false},
		{name: "hyphenated", input: "backup-cluster", wantErr: false},
		{name: "numeric suffix", input: "link123", wantErr: false},
		{name: "single char", input: "a", wantErr: false},

		// Empty.
		{name: "empty", input: "", wantErr: true},

		// Path traversal.
		{name: "dotdot prefix", input: "../evil", wantErr: true},
		{name: "consecutive dots", input: "link..bad", wantErr: true},
		{name: "slash", input: "a/b", wantErr: true},

		// URL-unsafe characters (only /, ?, &, + are rejected by IsURLSegmentSafe).
		{name: "question mark", input: "link?param=1", wantErr: true},
		{name: "ampersand", input: "link&other", wantErr: true},
		{name: "plus", input: "link+plus", wantErr: true},

		// Non-ASCII (rejected by IsEntityName).
		{name: "non-ASCII", input: "lïnk", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClusterLinkName(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckVolatileConfig(t *testing.T) {
	tests := []struct {
		name    string
		current map[string]string
		updated map[string]string
		strict  bool
		wantErr bool
	}{
		// Strict (PUT) tests.
		{
			name:    "strict: no volatile keys, unchanged",
			current: map[string]string{"user.key": "val"},
			updated: map[string]string{"user.key": "val"},
			strict:  true,
			wantErr: false,
		},
		{
			name:    "strict: volatile key unchanged",
			current: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			strict:  true,
			wantErr: false,
		},
		{
			name:    "strict: volatile key value changed",
			current: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{"volatile.addresses": "10.0.0.2:8443"},
			strict:  true,
			wantErr: true,
		},
		{
			name:    "strict: volatile key removed",
			current: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{},
			strict:  true,
			wantErr: true,
		},
		{
			name:    "strict: volatile key added",
			current: map[string]string{},
			updated: map[string]string{"volatile.new": "val"},
			strict:  true,
			wantErr: true,
		},

		// Non-strict (PATCH) tests.
		{
			name:    "non-strict: volatile key omitted (allowed)",
			current: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{},
			strict:  false,
			wantErr: false,
		},
		{
			name:    "non-strict: volatile key unchanged",
			current: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			strict:  false,
			wantErr: false,
		},
		{
			name:    "non-strict: volatile key value changed",
			current: map[string]string{"volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{"volatile.addresses": "10.0.0.2:8443"},
			strict:  false,
			wantErr: true,
		},
		{
			name:    "non-strict: new volatile key added",
			current: map[string]string{},
			updated: map[string]string{"volatile.new": "val"},
			strict:  false,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkVolatileConfig(tc.current, tc.updated, tc.strict)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateClusterLinkType(t *testing.T) {
	tests := []struct {
		name    string
		reqType string
		want    string
		wantErr bool
	}{
		{name: "empty type", reqType: "", wantErr: true},
		{name: "explicit bidirectional", reqType: api.ClusterLinkTypeBidirectional, want: api.ClusterLinkTypeBidirectional, wantErr: false},
		{name: "invalid type", reqType: "one-way", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateClusterLinkType(tc.reqType)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestMergeClusterLinkActivationConfig(t *testing.T) {
	tests := []struct {
		name    string
		current map[string]string
		updated map[string]string
		want    map[string]string
	}{
		{
			name:    "preserve user config while updating volatile addresses",
			current: map[string]string{"user.description": "primary link", "volatile.addresses": "10.0.0.1:8443"},
			updated: map[string]string{"user.description": "ignored", "volatile.addresses": "10.0.0.2:8443"},
			want:    map[string]string{"user.description": "primary link", "volatile.addresses": "10.0.0.2:8443"},
		},
		{
			name:    "initialize empty config when only volatile keys are provided",
			current: nil,
			updated: map[string]string{"volatile.addresses": "10.0.0.3:8443"},
			want:    map[string]string{"volatile.addresses": "10.0.0.3:8443"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeClusterLinkActivationConfig(tc.current, tc.updated)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClusterLinkValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
	}{
		// Valid configs.
		{name: "empty config", config: map[string]string{}, wantErr: false},
		{name: "nil config", config: nil, wantErr: false},
		{name: "single IPv4 address", config: map[string]string{"volatile.addresses": "1.2.3.4:8443"}, wantErr: false},
		{name: "multiple addresses", config: map[string]string{"volatile.addresses": "1.2.3.4:8443,5.6.7.8:8443"}, wantErr: false},
		{name: "IPv6 address", config: map[string]string{"volatile.addresses": "[::1]:8443"}, wantErr: false},
		{name: "hostname address", config: map[string]string{"volatile.addresses": "server.example.com:8443"}, wantErr: false},
		{name: "max valid port", config: map[string]string{"volatile.addresses": "1.2.3.4:65535"}, wantErr: false},
		{name: "min valid port", config: map[string]string{"volatile.addresses": "1.2.3.4:1"}, wantErr: false},
		{name: "empty volatile.addresses", config: map[string]string{"volatile.addresses": ""}, wantErr: false},
		{name: "user key", config: map[string]string{"user.description": "my link"}, wantErr: false},
		{name: "user key with any value", config: map[string]string{"user.foo": "bar=baz, anything goes"}, wantErr: false},

		// Invalid volatile.addresses values.
		{name: "missing port", config: map[string]string{"volatile.addresses": "1.2.3.4"}, wantErr: true},
		{name: "port too high", config: map[string]string{"volatile.addresses": "1.2.3.4:65536"}, wantErr: true},
		{name: "non-numeric port", config: map[string]string{"volatile.addresses": "1.2.3.4:notaport"}, wantErr: true},
		{name: "empty host", config: map[string]string{"volatile.addresses": ":8443"}, wantErr: true},
		{name: "hostname with underscore", config: map[string]string{"volatile.addresses": "bad_host.example.com:8443"}, wantErr: true},

		// Unknown keys.
		{name: "unknown key", config: map[string]string{"invalid.key": "value"}, wantErr: true},
		{name: "bare key no namespace", config: map[string]string{"foo": "bar"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := clusterLinkValidateConfig(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
