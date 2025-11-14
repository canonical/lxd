package template

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestChrootLoaderAbs(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		base     string
		filename string
		expected string
	}{
		{
			name:     "Simple relative path",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "test.txt",
			expected: "/var/lib/lxd/containers/c1/test.txt",
		},
		{
			name:     "Nested relative path",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "etc/hosts",
			expected: "/var/lib/lxd/containers/c1/etc/hosts",
		},
		{
			name:     "Path with dot segments",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "./test.txt",
			expected: "/var/lib/lxd/containers/c1/test.txt",
		},
		{
			name:     "Path with parent directory references",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "../test.txt",
			expected: "/var/lib/lxd/containers/test.txt",
		},
		{
			name:     "Absolute path in filename",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "/etc/hosts",
			expected: "/var/lib/lxd/containers/c1/etc/hosts",
		},
		{
			name:     "Empty filename",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "",
			expected: "/var/lib/lxd/containers/c1",
		},
		{
			name:     "Multiple slashes",
			path:     "/var/lib/lxd/containers/c1",
			base:     "",
			filename: "//etc///hosts//",
			expected: "/var/lib/lxd/containers/c1/etc/hosts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := ChrootLoader{Path: tt.path}
			result := loader.Abs(tt.base, tt.filename)

			if result != tt.expected {
				t.Errorf("Abs() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestChrootLoaderGet(t *testing.T) {
	tmpDir := t.TempDir()

	testContent := []byte("test file content")
	testFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatalf("Failed creating test file: %v", err)
	}

	nestedDir := filepath.Join(tmpDir, "subdir")
	err = os.Mkdir(nestedDir, 0755)
	if err != nil {
		t.Fatalf("Failed creating nested directory: %v", err)
	}

	nestedContent := []byte("nested file content")
	nestedFile := filepath.Join(nestedDir, "nested.txt")
	err = os.WriteFile(nestedFile, nestedContent, 0644)
	if err != nil {
		t.Fatalf("Failed creating nested file: %v", err)
	}

	outsideDir := t.TempDir()
	outsideContent := []byte("outside file content")
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	err = os.WriteFile(outsideFile, outsideContent, 0644)
	if err != nil {
		t.Fatalf("Failed creating outside file: %v", err)
	}

	t.Run("Read file within chroot", func(t *testing.T) {
		loader := ChrootLoader{Path: tmpDir}
		reader, err := loader.Get(testFile)
		if err != nil {
			t.Fatalf("Get() error = %v, want nil", err)
		}

		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}

		if string(content) != string(testContent) {
			t.Errorf("Get() content = %q, want %q", content, testContent)
		}
	})

	t.Run("Read nested file within chroot", func(t *testing.T) {
		loader := ChrootLoader{Path: tmpDir}
		reader, err := loader.Get(nestedFile)
		if err != nil {
			t.Fatalf("Get() error = %v, want nil", err)
		}

		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}

		if string(content) != string(nestedContent) {
			t.Errorf("Get() content = %q, want %q", content, nestedContent)
		}
	})

	t.Run("Reject file outside chroot", func(t *testing.T) {
		loader := ChrootLoader{Path: tmpDir}
		_, err := loader.Get(outsideFile)
		if err == nil {
			t.Error("Get() error = nil, want error for file outside chroot")
		}

		expectedMsg := "Attempting to access a file outside the instance"
		if err.Error() != expectedMsg {
			t.Errorf("Get() error = %q, want %q", err.Error(), expectedMsg)
		}
	})

	t.Run("Reject file accessed via symlink escape", func(t *testing.T) {
		symlinkPath := filepath.Join(tmpDir, "escape")
		err := os.Symlink(outsideFile, symlinkPath)
		if err != nil {
			t.Fatalf("Failed creating symlink: %v", err)
		}

		loader := ChrootLoader{Path: tmpDir}
		_, err = loader.Get(symlinkPath)
		if err == nil {
			t.Error("Get() error = nil, want error for symlink escape attempt")
		}

		expectedMsg := "Attempting to access a file outside the instance"
		if err.Error() != expectedMsg {
			t.Errorf("Get() error = %q, want %q", err.Error(), expectedMsg)
		}
	})

	t.Run("Handle non-existent file", func(t *testing.T) {
		loader := ChrootLoader{Path: tmpDir}
		nonExistentPath := filepath.Join(tmpDir, "nonexistent.txt")
		_, err := loader.Get(nonExistentPath)
		if err == nil {
			t.Error("Get() error = nil, want error for non-existent file")
		}
	})

	t.Run("Handle non-existent base path", func(t *testing.T) {
		loader := ChrootLoader{Path: "/nonexistent/path"}
		_, err := loader.Get(testFile)
		if err == nil {
			t.Error("Get() error = nil, want error for non-existent base path")
		}
	})
}

func TestChrootLoaderGetSymlinkWithinChroot(t *testing.T) {
	tmpDir := t.TempDir()

	targetContent := []byte("target file content")
	targetFile := filepath.Join(tmpDir, "target.txt")
	err := os.WriteFile(targetFile, targetContent, 0644)
	if err != nil {
		t.Fatalf("Failed creating target file: %v", err)
	}

	symlinkPath := filepath.Join(tmpDir, "link.txt")
	err = os.Symlink(targetFile, symlinkPath)
	if err != nil {
		t.Fatalf("Failed creating symlink: %v", err)
	}

	t.Run("Follow symlink within chroot", func(t *testing.T) {
		loader := ChrootLoader{Path: tmpDir}
		reader, err := loader.Get(symlinkPath)
		if err != nil {
			t.Fatalf("Get() error = %v, want nil", err)
		}

		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}

		if string(content) != string(targetContent) {
			t.Errorf("Get() content = %q, want %q", content, targetContent)
		}
	})
}
