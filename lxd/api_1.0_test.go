package main

import (
	"testing"
)

func TestGetProjectNameFromStorageConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name:     "empty config",
			config:   "",
			expected: "",
		},
		{
			name:     "valid project images storage config",
			config:   "storage.project.foo.images_volume",
			expected: "foo",
		},
		{
			name:     "valid project backups storage config",
			config:   "storage.project.foo.backups_volume",
			expected: "foo",
		},
		{
			name:     "missing project name",
			config:   "storage.project..backups_volume",
			expected: "",
		},
		{
			name:     "unknown volume type",
			config:   "storage.project.foo.notused_volume",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getProjectNameFromStorageConfig(tt.config)
			if result != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
