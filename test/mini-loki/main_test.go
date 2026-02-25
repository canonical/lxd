package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMain_ServeHTTP(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "loki-test-log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear the file for each test case
			err := tmpFile.Truncate(0)
			if err != nil {
				t.Fatalf("Failed to truncate file: %v", err)
			}

			_, err = tmpFile.Seek(0, 0)
			if err != nil {
				t.Fatalf("Failed to seek file: %v", err)
			}

			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.url, bytes.NewBufferString(tt.body))
			} else {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			}

			w := httptest.NewRecorder()

			l.ServeHTTP(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			if tt.expectedLog != "" {
				_, err := tmpFile.Seek(0, 0)
				if err != nil {
					t.Fatalf("Failed to seek file: %v", err)
				}

				content, err := io.ReadAll(tmpFile)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}

				if string(content) != tt.expectedLog {
					t.Errorf("Expected log %q, got %q", tt.expectedLog, string(content))
				}
			} else {
				// Verify file is empty (optional, but good practice if we expect no logs)
				stat, err := tmpFile.Stat()
				if err != nil {
					t.Fatalf("Failed to stat file: %v", err)
				}

				if stat.Size() != 0 {
					t.Errorf("Expected empty log file, got size %d", stat.Size())
				}
			}
		})
	}
}
