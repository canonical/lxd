package apparmor

import (
	"slices"
	"testing"
)

func TestLDLibraryPaths(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{"empty", "", []string{}},
		{"only separators", ":::", []string{}},
		{"absolute entries", "/a/b:/c", []string{"/a/b", "/c"}},
		{"trailing slash is kept", "/a/b/", []string{"/a/b/"}},
		{"empty entries", ":/a::/b:", []string{"/a", "/b"}},
		{"relative entries", ".:a/b:../c:/d", []string{"/d"}},
		{"root directory", "/://:/.:/a", []string{"/a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LD_LIBRARY_PATH", tt.value)

			got := ldLibraryPaths()
			if !slices.Equal(got, tt.want) {
				t.Errorf("ldLibraryPaths() = %q, want %q", got, tt.want)
			}
		})
	}
}
