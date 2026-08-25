package features

import (
	"testing"
)

func TestLoadFromEnv(t *testing.T) {
	const envVar = "LXD_TEST_FEATURES"

	tests := []struct {
		name         string
		value        string
		wantEnabled  []Feature
		wantDisabled []Feature
	}{
		{
			name:         "empty",
			wantDisabled: []Feature{"test_feature_a", "test_feature_b"},
		},
		{
			name:         "one feature",
			value:        "test_feature_a",
			wantEnabled:  []Feature{"test_feature_a"},
			wantDisabled: []Feature{"test_feature_b"},
		},
		{
			name:        "multiple features",
			value:       "test_feature_a,test_feature_b",
			wantEnabled: []Feature{"test_feature_a", "test_feature_b"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(envVar, test.value)

			err := loadFromEnv(envVar)
			if err != nil {
				t.Fatalf("loadFromEnv() returned an unexpected error: %v", err)
			}

			for _, feature := range test.wantEnabled {
				if !IsEnabled(feature) {
					t.Errorf("Expected feature %q to be enabled", feature)
				}
			}

			for _, feature := range test.wantDisabled {
				if IsEnabled(feature) {
					t.Errorf("Expected feature %q to be disabled", feature)
				}
			}
		})
	}
}

func TestLoadFromEnvRejectsInvalidNames(t *testing.T) {
	const envVar = "LXD_TEST_FEATURES"

	tests := []string{
		"test-feature",
		"test feature",
		",test_feature",
		"test_feature,",
		"test_feature_a,,test_feature_b",
	}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envVar, value)

			err := loadFromEnv(envVar)
			if err == nil {
				t.Fatalf("Expected loadFromEnv() to reject %q", value)
			}
		})
	}
}
