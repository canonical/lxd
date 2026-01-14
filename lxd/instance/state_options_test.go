package instance

import (
	"testing"
)

func TestParseRecursionFields(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectedRecursion int
		expectedDisk      bool
		expectedNetwork   bool
		expectError       bool
	}{
		{
			name:              "Empty string",
			input:             "",
			expectedRecursion: 0,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Recursion 0",
			input:             "0",
			expectedRecursion: 0,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Recursion 1",
			input:             "1",
			expectedRecursion: 1,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Recursion 2 (backward compatible)",
			input:             "2",
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Selective field: disk only",
			input:             "[state.disk]",
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   false,
			expectError:       false,
		},
		{
			name:              "Selective field: network only",
			input:             "[state.network]",
			expectedRecursion: 2,
			expectedDisk:      false,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Selective fields: both disk and network",
			input:             "[state.disk,state.network]",
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Selective fields: both network and disk (reversed order)",
			input:             "[state.network,state.disk]",
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Empty brackets (no fields)",
			input:             "[]",
			expectedRecursion: 2,
			expectedDisk:      false,
			expectedNetwork:   false,
			expectError:       false,
		},
		{
			name:              "Selective fields with spaces",
			input:             "[state.disk, state.network]",
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:        "Invalid field name",
			input:       "[state.invalid]",
			expectError: true,
		},
		{
			name:        "Mixed valid and invalid fields",
			input:       "[state.disk,state.invalid]",
			expectError: true,
		},
		{
			name:        "Invalid recursion value",
			input:       "invalid",
			expectError: true,
		},
		{
			name:        "Invalid field without state prefix",
			input:       "[disk]",
			expectError: true,
		},
		{
			name:        "Malformed brackets (missing closing)",
			input:       "[state.disk",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recursion, opts, err := ParseRecursionFields(tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if recursion != tt.expectedRecursion {
				t.Errorf("Expected recursion=%d, got %d", tt.expectedRecursion, recursion)
			}

			if opts.IncludeDisk != tt.expectedDisk {
				t.Errorf("Expected IncludeDisk=%v, got %v", tt.expectedDisk, opts.IncludeDisk)
			}

			if opts.IncludeNetwork != tt.expectedNetwork {
				t.Errorf("Expected IncludeNetwork=%v, got %v", tt.expectedNetwork, opts.IncludeNetwork)
			}
		})
	}
}

func TestDefaultStateRenderOptions(t *testing.T) {
	opts := DefaultStateRenderOptions()

	if !opts.IncludeDisk {
		t.Error("Expected IncludeDisk to be true by default")
	}

	if !opts.IncludeNetwork {
		t.Error("Expected IncludeNetwork to be true by default")
	}
}
