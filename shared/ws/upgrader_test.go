package ws

import (
	"net/http"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{
			name:   "No origin header allows request",
			origin: "",
			host:   "localhost:8443",
			want:   true,
		},
		{
			name:   "Same host and port accepted",
			origin: "https://localhost:8443",
			host:   "localhost:8443",
			want:   true,
		},
		{
			name:   "Standard scheme without port in origin matches host without port",
			origin: "https://localhost",
			host:   "localhost",
			want:   true,
		},
		{
			name:   "Standard scheme different port rejected",
			origin: "https://localhost:1234",
			host:   "localhost:8443",
			want:   false,
		},
		{
			name:   "Standard scheme without port in origin rejected when host has port",
			origin: "https://localhost",
			host:   "localhost:8443",
			want:   false,
		},
		{
			// This matches what pylxd/ws4py sends over Unix sockets.
			name:   "ws4py-style unix socket origin",
			origin: "ws+unix://localhost",
			host:   "localhost:None",
			want:   true,
		},
		{
			name:   "Non-standard scheme hostname match",
			origin: "ws+unix://localhost",
			host:   "localhost:8443",
			want:   true,
		},
		{
			name:   "Case-insensitive hostname match",
			origin: "https://LocalHost:8443",
			host:   "localhost:8443",
			want:   true,
		},
		{
			name:   "Different origin rejected",
			origin: "https://evil.com",
			host:   "localhost:8443",
			want:   false,
		},
		{
			name:   "Different origin without port rejected",
			origin: "https://evil.com",
			host:   "localhost",
			want:   false,
		},
		{
			name:   "Malformed origin URL rejected",
			origin: "://invalid",
			host:   "localhost:8443",
			want:   false,
		},
		{
			name:   "Subdomain mismatch rejected",
			origin: "https://sub.localhost",
			host:   "localhost:8443",
			want:   false,
		},
		{
			name:   "Userinfo bypass attempt rejected",
			origin: "https://localhost@evil.com:8443",
			host:   "localhost:8443",
			want:   false,
		},
		{
			name:   "Empty origin host rejected",
			origin: "https://",
			host:   "localhost:8443",
			want:   false,
		},
		{
			name:   "Non-standard scheme different hostname rejected",
			origin: "ws+unix://evil.com",
			host:   "localhost:None",
			want:   false,
		},
		{
			name:   "IPv6 same host and port accepted",
			origin: "https://[::1]:8443",
			host:   "[::1]:8443",
			want:   true,
		},
		{
			name:   "IPv6 different port rejected",
			origin: "https://[::1]:1234",
			host:   "[::1]:8443",
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{
				Host:   tc.host,
				Header: http.Header{},
			}

			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}

			got := checkOrigin(r)
			if got != tc.want {
				t.Errorf("checkOrigin() with origin=%q host=%q = %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}
}
