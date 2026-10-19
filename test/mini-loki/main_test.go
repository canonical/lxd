package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/test/testutils/servemock"
)

func TestMain_ServeHTTP(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "loki-test-log")
	if err != nil {
		t.Fatalf("Failed creating temp file: %v", err)
	}

	defer os.Remove(tmpFile.Name())

	l := &loki{
		logfile: tmpFile,
	}

	tests := []struct {
		name           string
		method         string
		url            string
		body           string
		expectedStatus int
		expectedLog    string
	}{
		{
			name:           "Ready check",
			method:         http.MethodGet,
			url:            "/ready",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Push logs",
			method:         http.MethodPost,
			url:            "/loki/api/v1/push",
			body:           `{"streams": [{"stream": {"foo": "bar"}, "values": [["1570838961892966000", "fizzbuzz"]]}]}`,
			expectedStatus: http.StatusOK,
			expectedLog:    `{"streams": [{"stream": {"foo": "bar"}, "values": [["1570838961892966000", "fizzbuzz"]]}]}` + "\n",
		},
		{
			name:           "Not found",
			method:         http.MethodGet,
			url:            "/unknown",
			expectedStatus: http.StatusNotFound,
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	res, err := servemock.API(ctx, servemock.Config{
		Address:  "127.0.0.1:0",
		Handlers: l.handlers(),
	})
	require.NoError(t, err)

	_, port, err := net.SplitHostPort(res.Listener.(*net.TCPListener).Addr().String())
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear the file for each test case
			err := tmpFile.Truncate(0)
			if err != nil {
				t.Fatalf("Failed truncating file: %v", err)
			}

			_, err = tmpFile.Seek(0, 0)
			if err != nil {
				t.Fatalf("Failed seeking file: %v", err)
			}

			var req *http.Request
			if tt.body != "" {
				req, err = http.NewRequest(tt.method, "http://127.0.0.1:"+port+tt.url, bytes.NewBufferString(tt.body))
				require.NoError(t, err)
			} else {
				req, err = http.NewRequest(tt.method, "http://127.0.0.1:"+port+tt.url, nil)
				require.NoError(t, err)
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			if tt.expectedLog != "" {
				_, err := tmpFile.Seek(0, 0)
				if err != nil {
					t.Fatalf("Failed seeking file: %v", err)
				}

				content, err := io.ReadAll(tmpFile)
				if err != nil {
					t.Fatalf("Failed reading file: %v", err)
				}

				if string(content) != tt.expectedLog {
					t.Errorf("Expected log %q, got %q", tt.expectedLog, string(content))
				}
			} else {
				// Verify file is empty (optional, but good practice if we expect no logs)
				stat, err := tmpFile.Stat()
				if err != nil {
					t.Fatalf("Failed getting file info: %v", err)
				}

				if stat.Size() != 0 {
					t.Errorf("Expected empty log file, got size %d", stat.Size())
				}
			}
		})
	}
}
