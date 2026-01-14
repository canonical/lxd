package instance

import (
	"testing"
)

func TestParseRecursionFields(t *testing.T) {
	tests := []struct {
		name              string
		recursion         string   // Can include semicolon syntax: "2;fields=state.disk"
		fields            []string // Separate fields parameter (for backward compatibility)
		expectedRecursion int
		expectedDisk      bool
		expectedNetwork   bool
		expectError       bool
	}{
		{
			name:              "Empty recursion string",
			recursion:         "",
			fields:            nil,
			expectedRecursion: 0,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Recursion 0",
			recursion:         "0",
			fields:            nil,
			expectedRecursion: 0,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Recursion 1",
			recursion:         "1",
			fields:            nil,
			expectedRecursion: 1,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Recursion 2 (backward compatible)",
			recursion:         "2",
			fields:            nil,
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Selective field: disk only (semicolon syntax)",
			recursion:         "2;fields=state.disk",
			fields:            nil,
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   false,
			expectError:       false,
		},
		{
			name:              "Selective field: network only (semicolon syntax)",
			recursion:         "2;fields=state.network",
			fields:            nil,
			expectedRecursion: 2,
			expectedDisk:      false,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Selective fields: both disk and network (semicolon syntax)",
			recursion:         "2;fields=state.disk,state.network",
			fields:            nil,
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Selective fields: both network and disk reversed (semicolon syntax)",
			recursion:         "2;fields=state.network,state.disk",
			fields:            nil,
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:              "Empty fields (no expensive fields, semicolon syntax)",
			recursion:         "2;fields=",
			fields:            nil,
			expectedRecursion: 2,
			expectedDisk:      false,
			expectedNetwork:   false,
			expectError:       false,
		},
		{
			name:              "Backward compatibility: separate fields parameter disk only",
			recursion:         "2",
			fields:            []string{"state.disk"},
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   false,
			expectError:       false,
		},
		{
			name:              "Backward compatibility: separate fields parameter both",
			recursion:         "2",
			fields:            []string{"state.disk", "state.network"},
			expectedRecursion: 2,
			expectedDisk:      true,
			expectedNetwork:   true,
			expectError:       false,
		},
		{
			name:        "Invalid field name (semicolon syntax)",
			recursion:   "2;fields=state.invalid",
			fields:      nil,
			expectError: true,
		},
		{
			name:        "Mixed valid and invalid fields (semicolon syntax)",
			recursion:   "2;fields=state.disk,state.invalid",
			fields:      nil,
			expectError: true,
		},
		{
			name:        "Invalid recursion value",
			recursion:   "invalid",
			fields:      nil,
			expectError: true,
		},
		{
			name:        "Invalid field without state prefix (semicolon syntax)",
			recursion:   "2;fields=disk",
			fields:      nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recursion, opts, err := ParseRecursionFields(tt.recursion, tt.fields)

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
