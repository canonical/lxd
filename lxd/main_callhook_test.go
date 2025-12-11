package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveTargetRelativeToLink(t *testing.T) {
	tests := []struct {
		name      string
		link      string
		target    string
		expected  string
		expectErr bool
	}{
		{
			name:      "relative target returned as-is",
			link:      "/home/user/link",
			target:    "relative/path",
			expected:  "relative/path",
			expectErr: false,
		},
		{
			name:      "absolute target in same directory",
			link:      "/home/user/link",
			target:    "/home/user/target",
			expected:  "target",
			expectErr: false,
		},
		{
			name:      "absolute target in subdirectory",
			link:      "/home/user/link",
			target:    "/home/user/subdir/target",
			expected:  filepath.Join("subdir", "target"),
			expectErr: false,
		},
		{
			name:      "absolute target in parent directory",
			link:      "/home/user/subdir/link",
			target:    "/home/user/target",
			expected:  filepath.Join("..", "target"),
			expectErr: false,
		},
		{
			name:      "absolute target in sibling directory",
			link:      "/home/user/dir1/link",
			target:    "/home/user/dir2/target",
			expected:  filepath.Join("..", "dir2", "target"),
			expectErr: false,
		},
		{
			name:      "absolute target at root level",
			link:      "/home/user/link",
			target:    "/target",
			expected:  filepath.Join("..", "..", "target"),
			expectErr: false,
		},
		{
			name:      "paths with trailing slashes get cleaned",
			link:      "/home/user/link",
			target:    "/home/user/target/",
			expected:  "target",
			expectErr: false,
		},
		{
			name:      "paths with redundant separators get cleaned",
			link:      "/home//user///link",
			target:    "/home//user//target",
			expected:  "target",
			expectErr: false,
		},
		{
			name:      "paths with dot components get cleaned",
			link:      "/home/./user/link",
			target:    "/home/user/./target",
			expected:  "target",
			expectErr: false,
		},
		{
			name:      "relative link path returns error",
			link:      "relative/link",
			target:    "/absolute/target",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "empty link path returns error",
			link:      "",
			target:    "/absolute/target",
			expected:  "",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := resolveTargetRelativeToLink(tc.link, tc.target)

			if tc.expectErr {
				assert.Error(t, err, "Expected an error for link=%q and target=%q", tc.link, tc.target)
			} else {
				assert.NoError(t, err, "Expected no error for link=%q and target=%q", tc.link, tc.target)
			}

			assert.Equal(t, tc.expected, result, "Unexpected result value %q for link=%q and target=%q", result, tc.link, tc.target)
		})
	}
}
