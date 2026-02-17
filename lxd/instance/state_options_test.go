package instance

import (
	"testing"
)

func TestParseRecursionFields(t *testing.T) {
	tests := []struct {
		name            string
		fields          []string // nil = no fields specified, [] = empty fields, ["state.disk"] = specific field
		expectedDisk    bool
		expectedNetwork bool
		expectError     bool
	}{
		{
			name:            "Nil fields (default behavior)",
			fields:          nil,
			expectedDisk:    true,
			expectedNetwork: true,
			expectError:     false,
		},
		{
			name:            "Empty fields (no expensive fields)",
			fields:          []string{},
			expectedDisk:    false,
			expectedNetwork: false,
			expectError:     false,
		},
		{
			name:            "Selective field: disk only",
			fields:          []string{"state.disk"},
			expectedDisk:    true,
			expectedNetwork: false,
			expectError:     false,
		},
		{
			name:            "Selective field: network only",
			fields:          []string{"state.network"},
			expectedDisk:    false,
			expectedNetwork: true,
			expectError:     false,
		},
		{
			name:            "Selective fields: both disk and network",
			fields:          []string{"state.disk", "state.network"},
			expectedDisk:    true,
			expectedNetwork: true,
			expectError:     false,
		},
		{
			name:            "Selective fields: both network and disk reversed",
			fields:          []string{"state.network", "state.disk"},
			expectedDisk:    true,
			expectedNetwork: true,
			expectError:     false,
		},
		{
			name:        "Invalid field name",
			fields:      []string{"state.invalid"},
			expectError: true,
		},
		{
			name:        "Mixed valid and invalid fields",
			fields:      []string{"state.disk", "state.invalid"},
			expectError: true,
		},
		{
			name:        "Invalid field without state prefix",
			fields:      []string{"disk"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := ParseRecursionFields(tt.fields)

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
