package util

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestIsRecursionRequest(t *testing.T) {
	tests := []struct {
		name               string
		queryString        string // raw query string for the request
		expectedRecursion  int
		expectedFields     []string // nil means no fields, [] means empty fields
		expectedFieldIsNil bool     // true if fields should be nil (not just empty)
	}{
		{
			name:               "No recursion parameter",
			queryString:        "",
			expectedRecursion:  0,
			expectedFields:     nil,
			expectedFieldIsNil: true,
		},
		{
			name:               "Recursion 0",
			queryString:        "recursion=0",
			expectedRecursion:  0,
			expectedFields:     nil,
			expectedFieldIsNil: true,
		},
		{
			name:               "Recursion 1",
			queryString:        "recursion=1",
			expectedRecursion:  1,
			expectedFields:     nil,
			expectedFieldIsNil: true,
		},
		{
			name:               "Recursion 2",
			queryString:        "recursion=2",
			expectedRecursion:  2,
			expectedFields:     nil,
			expectedFieldIsNil: true,
		},
		{
			name:              "Semicolon syntax: disk only",
			queryString:       "recursion=2%3Bfields%3Dstate.disk",
			expectedRecursion: 2,
			expectedFields:    []string{"state.disk"},
		},
		{
			name:              "Semicolon syntax: network only",
			queryString:       "recursion=2%3Bfields%3Dstate.network",
			expectedRecursion: 2,
			expectedFields:    []string{"state.network"},
		},
		{
			name:              "Semicolon syntax: both fields",
			queryString:       "recursion=2%3Bfields%3Dstate.disk%2Cstate.network",
			expectedRecursion: 2,
			expectedFields:    []string{"state.disk", "state.network"},
		},
		{
			name:              "Semicolon syntax: empty fields (no expensive fields)",
			queryString:       "recursion=2%3Bfields%3D",
			expectedRecursion: 2,
			expectedFields:    []string{},
		},
		{
			name:              "Separate fields parameter: disk only",
			queryString:       "recursion=2&fields=state.disk",
			expectedRecursion: 2,
			expectedFields:    []string{"state.disk"},
		},
		{
			name:              "Separate fields parameter: multiple values",
			queryString:       "recursion=2&fields=state.disk&fields=state.network",
			expectedRecursion: 2,
			expectedFields:    []string{"state.disk", "state.network"},
		},
		{
			name:              "Semicolon syntax takes priority over separate fields",
			queryString:       "recursion=2%3Bfields%3Dstate.disk&fields=state.network",
			expectedRecursion: 2,
			expectedFields:    []string{"state.disk"},
		},
		{
			name:               "Invalid recursion value",
			queryString:        "recursion=invalid",
			expectedRecursion:  0,
			expectedFields:     nil,
			expectedFieldIsNil: true,
		},
		{
			name:               "Negative recursion value",
			queryString:        "recursion=-1",
			expectedRecursion:  -1,
			expectedFields:     nil,
			expectedFieldIsNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL, err := url.Parse("http://localhost/1.0/instances?" + tt.queryString)
			if err != nil {
				t.Fatalf("Failed to parse URL: %v", err)
			}

			r := &http.Request{URL: reqURL}
			r.Form = reqURL.Query()

			recursion, fields := IsRecursionRequest(r)

			if recursion != tt.expectedRecursion {
				t.Errorf("Expected recursion=%d, got %d", tt.expectedRecursion, recursion)
			}

			if tt.expectedFieldIsNil {
				if fields != nil {
					t.Errorf("Expected fields to be nil, got %v", fields)
				}

				return
			}

			if fields == nil && tt.expectedFields != nil {
				t.Errorf("Expected fields=%v, got nil", tt.expectedFields)
				return
			}

			if len(fields) != len(tt.expectedFields) {
				t.Errorf("Expected %d fields, got %d: %v", len(tt.expectedFields), len(fields), fields)
				return
			}

			for i, f := range fields {
				if f != tt.expectedFields[i] {
					t.Errorf("Expected fields[%d]=%q, got %q", i, tt.expectedFields[i], f)
				}
			}
		})
	}
}

func ExampleListenAddresses() {
	listenAddressConfigs := []string{
		"",
		"127.0.0.1:8000",   // Valid IPv4 address with port.
		"127.0.0.1",        // Valid IPv4 address without port.
		"[127.0.0.1]",      // Valid wrapped IPv4 address without port.
		"[::1]:8000",       // Valid IPv6 address with port.
		"::1:8000",         // Valid IPv6 address without port (that might look like a port).
		"::1",              // Valid IPv6 address without port.
		"[::1]",            // Valid wrapped IPv6 address without port.
		"example.com",      // Valid hostname without port.
		"example.com:8000", // Valid hostname with port.
		"foo:8000:9000",    // Invalid host and port combination.
		":::8000",          // Invalid host and port combination.
	}

	for _, listlistenAddressConfig := range listenAddressConfigs {
		listenAddress, err := ListenAddresses(listlistenAddressConfig)
		fmt.Printf("%q: %v %v\n", listlistenAddressConfig, listenAddress, err)
	}

	// Output: "": [] <nil>
	// "127.0.0.1:8000": [127.0.0.1:8000] <nil>
	// "127.0.0.1": [127.0.0.1:8443] <nil>
	// "[127.0.0.1]": [127.0.0.1:8443] <nil>
	// "[::1]:8000": [[::1]:8000] <nil>
	// "::1:8000": [[::1:8000]:8443] <nil>
	// "::1": [[::1]:8443] <nil>
	// "[::1]": [[::1]:8443] <nil>
	// "example.com": [example.com:8443] <nil>
	// "example.com:8000": [example.com:8000] <nil>
	// "foo:8000:9000": [] address foo:8000:9000: too many colons in address
	// ":::8000": [] address :::8000: too many colons in address
}
