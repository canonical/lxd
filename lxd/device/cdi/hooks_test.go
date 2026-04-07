package cdi

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localFS implements containerFS using the local filesystem for testing.
// Paths are interpreted relative to rootFS, mirroring how sftpContainerFS treats
// them relative to the container root.
type localFS struct {
	rootFS string
}

func (l *localFS) MkdirAll(path string) error {
	return os.MkdirAll(l.rootFS+filepath.Clean(path), 0755)
}

func (l *localFS) Symlink(oldname, newname string) error {
	return os.Symlink(oldname, l.rootFS+filepath.Clean(newname))
}

func (l *localFS) OpenFile(path string, flags int) (io.ReadWriteCloser, error) {
	return os.OpenFile(l.rootFS+filepath.Clean(path), flags, 0644)
}

func (l *localFS) Remove(path string) error {
	return os.Remove(l.rootFS + filepath.Clean(path))
}

func (l *localFS) Chtimes(path string, atime time.Time, mtime time.Time) error {
	return os.Chtimes(l.rootFS+filepath.Clean(path), atime, mtime)
}

func (l *localFS) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(l.rootFS + filepath.Clean(path))
}

// TestApplyHooksToContainer tests the ApplyHooksToContainer function.
func TestApplyHooksToContainer(t *testing.T) {
	t.Run("invalid hooks file path", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := applyHooksWithFS("/nonexistent/path.json", &localFS{rootFS: tmpDir})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Failed opening the CDI hooks file")
	})

	t.Run("invalid JSON in hooks file", func(t *testing.T) {
		tmpDir := t.TempDir()
		hooksFile := filepath.Join(tmpDir, "hooks.json")
		err := os.WriteFile(hooksFile, []byte("not json"), 0644)
		require.NoError(t, err)

		err = applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Failed decoding the CDI hooks file")
	})

	t.Run("empty hooks", func(t *testing.T) {
		tmpDir := t.TempDir()

		hooks := Hooks{}
		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err := applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		assert.NoError(t, err)
	})

	t.Run("creates symlinks", func(t *testing.T) {
		tmpDir := t.TempDir()

		hooks := Hooks{
			Symlinks: []SymlinkEntry{
				{Target: "/usr/lib/libfoo.so.1", Link: "/usr/lib/libfoo.so"},
				{Target: "/usr/lib/libbar.so.2", Link: "/usr/lib/libbar.so"},
			},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err := applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		require.NoError(t, err)

		// Verify symlinks were created
		for _, sl := range hooks.Symlinks {
			linkPath := filepath.Join(tmpDir, sl.Link)
			target, err := os.Readlink(linkPath)
			require.NoError(t, err)

			expectedTarget, err := resolveTargetRelativeToLink(sl.Link, sl.Target)
			require.NoError(t, err)
			assert.Equal(t, expectedTarget, target)
		}
	})

	t.Run("symlink already exists", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Pre-create the symlink
		linkDir := filepath.Join(tmpDir, "usr", "lib")
		err := os.MkdirAll(linkDir, 0755)
		require.NoError(t, err)
		err = os.Symlink("libfoo.so.1", filepath.Join(linkDir, "libfoo.so"))
		require.NoError(t, err)

		hooks := Hooks{
			Symlinks: []SymlinkEntry{
				{Target: "/usr/lib/libfoo.so.1", Link: "/usr/lib/libfoo.so"},
			},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		// Should not error on existing symlink
		err = applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		assert.NoError(t, err)
	})

	t.Run("creates symlinks in nested directories", func(t *testing.T) {
		tmpDir := t.TempDir()

		hooks := Hooks{
			Symlinks: []SymlinkEntry{
				{Target: "/usr/lib/x86_64/libdeep.so.1", Link: "/usr/lib/x86_64/libdeep.so"},
			},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err := applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		require.NoError(t, err)

		linkPath := filepath.Join(tmpDir, "usr", "lib", "x86_64", "libdeep.so")
		_, err = os.Lstat(linkPath)
		assert.NoError(t, err)
	})

	t.Run("creates new ld conf file", func(t *testing.T) {
		tmpDir := t.TempDir()

		hooks := Hooks{
			LDCacheUpdates: []string{"/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu"},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err := applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		require.NoError(t, err)

		ldConfPath := filepath.Join(tmpDir, "etc", "ld.so.conf.d", customCDILinkerConfFile)
		content, err := os.ReadFile(ldConfPath)
		require.NoError(t, err)

		assert.Contains(t, string(content), "/usr/lib/x86_64-linux-gnu\n")
		assert.Contains(t, string(content), "/usr/lib/aarch64-linux-gnu\n")
	})

	t.Run("appends to existing ld conf file without duplicates", func(t *testing.T) {
		tmpDir := t.TempDir()

		ldConfDir := filepath.Join(tmpDir, "etc", "ld.so.conf.d")
		err := os.MkdirAll(ldConfDir, 0755)
		require.NoError(t, err)

		// Pre-create the conf file with one entry
		ldConfPath := filepath.Join(ldConfDir, customCDILinkerConfFile)
		err = os.WriteFile(ldConfPath, []byte("/usr/lib/existing\n"), 0644)
		require.NoError(t, err)

		hooks := Hooks{
			LDCacheUpdates: []string{"/usr/lib/existing", "/usr/lib/new-entry"},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err = applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		require.NoError(t, err)

		content, err := os.ReadFile(ldConfPath)
		require.NoError(t, err)

		// Should contain the existing entry once and the new entry
		assert.Equal(t, "/usr/lib/existing\n/usr/lib/new-entry\n", string(content))
	})

	t.Run("symlinks and ld cache combined", func(t *testing.T) {
		tmpDir := t.TempDir()

		hooks := Hooks{
			Symlinks: []SymlinkEntry{
				{Target: "/usr/lib/libfoo.so.1", Link: "/usr/lib/libfoo.so"},
			},
			LDCacheUpdates: []string{"/usr/lib"},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err := applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		require.NoError(t, err)

		// Verify symlink
		linkPath := filepath.Join(tmpDir, "usr", "lib", "libfoo.so")
		_, err = os.Lstat(linkPath)
		assert.NoError(t, err)

		// Verify ld conf
		ldConfPath := filepath.Join(tmpDir, "etc", "ld.so.conf.d", customCDILinkerConfFile)
		content, err := os.ReadFile(ldConfPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "/usr/lib\n")
	})

	t.Run("symlink with relative link path errors", func(t *testing.T) {
		tmpDir := t.TempDir()

		hooks := Hooks{
			Symlinks: []SymlinkEntry{
				{Target: "/usr/lib/libfoo.so.1", Link: "relative/path"},
			},
		}

		hooksFile := writeHooksFile(t, tmpDir, hooks)

		err := applyHooksWithFS(hooksFile, &localFS{rootFS: tmpDir})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Failed resolving a CDI symlink")
	})
}

func writeHooksFile(t *testing.T, dir string, hooks Hooks) string {
	t.Helper()
	data, err := json.Marshal(hooks)
	require.NoError(t, err)

	path := filepath.Join(dir, "hooks.json")
	err = os.WriteFile(path, data, 0644)
	require.NoError(t, err)

	return path
}

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
