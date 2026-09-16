package util

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func TestProcStatFields(t *testing.T) {
	// Trailing fields shared by the well-formed cases, as they would follow the state field in /proc/<pid>/stat.
	tail := []string{"S", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "21", "22"}
	tailStr := " S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22\n"

	tests := []struct {
		name     string
		content  string
		expected []string
		wantErr  bool
	}{
		{
			name:     "plain comm",
			content:  "1234 (bash)" + tailStr,
			expected: append([]string{"1234", "bash"}, tail...),
		},
		{
			name:     "comm with spaces",
			content:  "1234 (my prog)" + tailStr,
			expected: append([]string{"1234", "my prog"}, tail...),
		},
		{
			name:     "comm with parentheses",
			content:  "1234 (a) b))" + tailStr,
			expected: append([]string{"1234", "a) b)"}, tail...),
		},
		{
			name:     "empty comm",
			content:  "1234 ()" + tailStr,
			expected: append([]string{"1234", ""}, tail...),
		},
		{
			name:    "empty file",
			content: "",
			wantErr: true,
		},
		{
			name:    "nothing before comm",
			content: "(bash)" + tailStr,
			wantErr: true,
		},
		{
			name:    "nothing after comm",
			content: "1234 (bash)\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stat")
			err := os.WriteFile(path, []byte(tt.content), 0o600)
			if err != nil {
				t.Fatalf("Failed writing stat file: %v", err)
			}

			fields, err := ProcStatFields(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Expected error, got fields %q", fields)
				}

				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !slices.Equal(fields, tt.expected) {
				t.Fatalf("Expected fields %q, got %q", tt.expected, fields)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		_, err := ProcStatFields(filepath.Join(t.TempDir(), "missing"))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Expected os.ErrNotExist, got %v", err)
		}
	})

	t.Run("self", func(t *testing.T) {
		fields, err := ProcStatFields("/proc/self/stat")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// proc(5) documents 52 fields as of Linux 3.5.
		if len(fields) < 52 {
			t.Fatalf("Expected at least 52 fields, got %d", len(fields))
		}

		if fields[0] != strconv.Itoa(os.Getpid()) {
			t.Fatalf("Expected pid %d, got %q", os.Getpid(), fields[0])
		}
	})
}

func TestProcessStartTime(t *testing.T) {
	starttime, err := ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if starttime <= 0 {
		t.Fatalf("Expected positive starttime, got %d", starttime)
	}

	// PID 0 has no /proc entry.
	_, err = ProcessStartTime(0)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Expected os.ErrNotExist, got %v", err)
	}
}
