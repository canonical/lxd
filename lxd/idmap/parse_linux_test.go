//go:build linux && cgo

package idmap

import (
	"reflect"
	"strings"
	"testing"
)

func Test_ParseRawIdmap(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []IdmapEntry
		wantErr  bool
		errMsg   string
	}{
		{
			name:  "single uid entry",
			input: "uid 1000 0",
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: true, Isgid: false},
			},
		},
		{
			name:  "single gid entry",
			input: "gid 1000 0",
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: false, Isgid: true},
			},
		},
		{
			name:  "single both entry",
			input: "both 1000 0",
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: true, Isgid: false},
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: false, Isgid: true},
			},
		},
		{
			name:  "range mapping",
			input: "uid 1000-1009 0-9",
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 10, Isuid: true, Isgid: false},
			},
		},
		{
			name: "multiple entries",
			input: `uid 1000 0
gid 1000 0
both 2000-2009 100-109`,
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: true, Isgid: false},
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: false, Isgid: true},
				{Hostid: 2000, Nsid: 100, Maprange: 10, Isuid: true, Isgid: false},
				{Hostid: 2000, Nsid: 100, Maprange: 10, Isuid: false, Isgid: true},
			},
		},
		{
			name:    "size mismatch input",
			input:   "uid 1000-999 1-999",
			wantErr: true,
			errMsg:  `The ID map ranges are of different sizes "uid 1000-999 1-999"`,
		},
		{
			name:    "invalid input",
			input:   "invalid 1000 0\n",
			wantErr: true,
			errMsg:  `Invalid ID map type "invalid 1000 0"`,
		},
		{
			name:    "negative range",
			input:   "uid 1000--1 0\n",
			wantErr: true,
			errMsg:  `Invalid ID map range "1000--1"`,
		},
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "empty lines ignored",
			input: "uid 1000 0\n\ngid 1000 0",
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: true, Isgid: false},
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: false, Isgid: true},
			},
		},
		{
			name:    "invalid type",
			input:   "invalid 1000 0",
			wantErr: true,
			errMsg:  "Invalid ID map type",
		},
		{
			name:    "too few fields",
			input:   "uid 1000",
			wantErr: true,
			errMsg:  "Invalid ID map line",
		},
		{
			name:    "too many fields",
			input:   "uid 1000 0 extra",
			wantErr: true,
			errMsg:  "Invalid ID map line",
		},
		{
			name:    "invalid range format",
			input:   "uid 1000-2000-3000 0",
			wantErr: true,
			errMsg:  `Invalid ID map range "1000-2000-3000"`,
		},
		{
			name:    "invalid number in hostid",
			input:   "uid invalid 0",
			wantErr: true,
		},
		{
			name:    "invalid number in nsid",
			input:   "uid 1000 invalid",
			wantErr: true,
		},
		{
			name:    "invalid number in range",
			input:   "uid 1000-invalid 0",
			wantErr: true,
		},
		{
			name:    "mismatched range sizes",
			input:   "uid 1000-1009 0-4",
			wantErr: true,
			errMsg:  "different sizes",
		},
		{
			name:  "large ranges",
			input: "uid 100000-199999 0-99999",
			expected: []IdmapEntry{
				{Hostid: 100000, Nsid: 0, Maprange: 100000, Isuid: true, Isgid: false},
			},
		},
		{
			name:  "single number treated as range of 1",
			input: "uid 1000 0",
			expected: []IdmapEntry{
				{Hostid: 1000, Nsid: 0, Maprange: 1, Isuid: true, Isgid: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseRawIdmap(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}

				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}
