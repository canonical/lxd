package resources

import (
	"testing"
	"time"
)

func TestParseSystemdDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		hasError bool
	}{
		// Microseconds - raw integers
		{"500000", 500 * time.Millisecond, false},
		{"1000000", 1 * time.Second, false},
		{"60000000", 1 * time.Minute, false},

		// Explicit units
		{"10us", 10 * time.Microsecond, false},
		{"500ms", 500 * time.Millisecond, false},
		{"10s", 10 * time.Second, false},
		{"5min", 5 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"2d", 48 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"2m", 60 * 24 * time.Hour, false},
		{"1y", 365 * 24 * time.Hour, false},

		// No unit (defaults to microseconds)
		{"45", 45 * time.Microsecond, false},

		// Decimal values
		{"1.5s", 1500 * time.Millisecond, false},
		{"0.5min", 30 * time.Second, false},
		{"1.5h", 90 * time.Minute, false},

		// Composite values
		{"10min30s", 10*time.Minute + 30*time.Second, false},
		{"1h20min", 1*time.Hour + 20*time.Minute, false},
		{"1d12h", 36 * time.Hour, false},
		{"2m15d", 75 * 24 * time.Hour, false},
		{"1h30min45s", 1*time.Hour + 30*time.Minute + 45*time.Second, false},

		// Complex composite values
		{"1y2m3d4h5min6s", 365*24*time.Hour + 60*24*time.Hour + 3*24*time.Hour + 4*time.Hour + 5*time.Minute + 6*time.Second, false},
		{"1.5h30.5min", 90*time.Minute + 30*time.Minute + 30*time.Second, false},

		// Invalid formats
		{"invalid", 0, true},
		{"1.2.3s", 0, true},
		{"5seconds", 0, true},
		{"", 0, true},
		{"min10", 0, true},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result, err := parseSystemdDuration(test.input)
			if test.hasError && err == nil {
				t.Errorf("Expected error for input %s, but got none. Result: %v", test.input, result)
			}

			if !test.hasError && err != nil {
				t.Errorf("Unexpected error for input %s: %v", test.input, err)
			}

			if !test.hasError {
				if result != test.expected {
					t.Errorf("Input '%s': expected %v (%d ns), got %v (%d ns)", test.input, test.expected, test.expected, result, result)
				}
			}
		})
	}
}
