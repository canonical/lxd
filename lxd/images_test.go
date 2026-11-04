package main

import (
	"net/http"
	"net/url"
	"testing"
)

func TestImageEndpointResolver(t *testing.T) {
	tests := []struct {
		name              string
		path              string
		rawPath           string // If set, simulates URL-encoded path
		wantEndpoint      *APIEndpoint
		wantPathValueKey  string
		wantPathValueVal  string
		wantPathValueKey2 string
		wantPathValueVal2 string
	}{
		// Alias routes
		{
			name:             "alias simple name",
			path:             "/1.0/images/aliases/myalias",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "myalias",
		},
		{
			name:         "alias with unescaped slash in name (path is too long)",
			path:         "/1.0/images/aliases/my/alias",
			wantEndpoint: nil,
		},
		{
			name:             "alias with encoded slash",
			path:             "/1.0/images/aliases/my/alias",
			rawPath:          "/1.0/images/aliases/my%2Falias",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "my/alias",
		},
		{
			name:             "alias with spaces",
			path:             "/1.0/images/aliases/my alias",
			rawPath:          "/1.0/images/aliases/my%20alias",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "my alias",
		},
		{
			name:             "alias ubuntu/noble",
			path:             "/1.0/images/aliases/ubuntu/noble",
			rawPath:          "/1.0/images/aliases/ubuntu%2Fnoble",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "ubuntu/noble",
		},

		// Fingerprint routes
		{
			name:             "fingerprint only",
			path:             "/1.0/images/abc123def456",
			wantEndpoint:     &imageCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},
		{
			name:             "fingerprint export",
			path:             "/1.0/images/abc123def456/export",
			wantEndpoint:     &imageExportCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},
		{
			name:             "fingerprint secret",
			path:             "/1.0/images/abc123def456/secret",
			wantEndpoint:     &imageSecretCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},
		{
			name:             "fingerprint refresh",
			path:             "/1.0/images/abc123def456/refresh",
			wantEndpoint:     &imageRefreshCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},

		// Unknown action returns nil
		{
			name:             "fingerprint unknown action",
			path:             "/1.0/images/abc123def456/unknown",
			wantEndpoint:     nil,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},

		// Base path returns nil (404)
		{
			name:         "base images path",
			path:         "/1.0/images/",
			wantEndpoint: nil,
		},
		{
			name:         "base aliases path",
			path:         "/1.0/images/aliases/",
			wantEndpoint: nil,
		},

		// Invalid URL escape sequences are treated as literals
		{
			name:             "typed alias collection",
			path:             "/1.0/images/container/aliases",
			wantEndpoint:     &imageTypeAliasesCmd,
			wantPathValueKey: "imageType",
			wantPathValueVal: "container",
		},
		{
			name:              "typed alias name",
			path:              "/1.0/images/container/aliases/jammy",
			wantEndpoint:      &imageTypeAliasCmd,
			wantPathValueKey:  "imageType",
			wantPathValueVal:  "container",
			wantPathValueKey2: "name",
			wantPathValueVal2: "jammy",
		},
		{
			name:              "typed alias with encoded slash",
			path:              "/1.0/images/virtual-machine/aliases/ubuntu/noble",
			rawPath:           "/1.0/images/virtual-machine/aliases/ubuntu%2Fnoble",
			wantEndpoint:      &imageTypeAliasCmd,
			wantPathValueKey:  "imageType",
			wantPathValueVal:  "virtual-machine",
			wantPathValueKey2: "name",
			wantPathValueVal2: "ubuntu/noble",
		},
		{
			name:             "untyped alias whose name starts with container",
			path:             "/1.0/images/aliases/container/jammy",
			rawPath:          "/1.0/images/aliases/container%2Fjammy",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "container/jammy",
		},
		{
			name:         "image type without aliases",
			path:         "/1.0/images/container",
			wantEndpoint: nil,
		},

		{
			name:             "invalid escape in alias",
			path:             "/1.0/images/aliases/bad%2",
			rawPath:          "/1.0/images/aliases/bad%2",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "bad%2",
		},
		{
			name:             "invalid escape in fingerprint",
			path:             "/1.0/images/bad%2/export",
			rawPath:          "/1.0/images/bad%2/export",
			wantEndpoint:     &imageExportCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "bad%2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &url.URL{Path: tt.path, RawPath: tt.rawPath}
			r := &http.Request{URL: u}

			got := imageEndpointResolver(r)

			if got != tt.wantEndpoint {
				var gotPath, wantPath string
				if got != nil {
					gotPath = got.Path
				}

				if tt.wantEndpoint != nil {
					wantPath = tt.wantEndpoint.Path
				}

				t.Errorf("imageEndpointResolver() = %s: %v (Path: %q), want %v (Path: %q)", tt.name, got, gotPath, tt.wantEndpoint, wantPath)
			}

			if tt.wantPathValueKey != "" && tt.wantEndpoint != nil {
				gotVal := r.PathValue(tt.wantPathValueKey)
				if gotVal != tt.wantPathValueVal {
					t.Errorf("PathValue(%q) = %q, want %q", tt.wantPathValueKey, gotVal, tt.wantPathValueVal)
				}
			}

			if tt.wantPathValueKey2 != "" && tt.wantEndpoint != nil {
				gotVal := r.PathValue(tt.wantPathValueKey2)
				if gotVal != tt.wantPathValueVal2 {
					t.Errorf("PathValue(%q) = %q, want %q", tt.wantPathValueKey2, gotVal, tt.wantPathValueVal2)
				}
			}
		})
	}
}
