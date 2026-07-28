package drivers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurePathJoin(t *testing.T) {
	// Test with requireRegular=true (template file case).
	t.Run("RequireRegularFile", func(t *testing.T) {
		templatesDir, err := os.MkdirTemp("", "lxd-templates-")
		require.NoError(t, err)
		defer os.RemoveAll(templatesDir)

		// Regular template file within the templates directory.
		require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "hostname.tpl"), []byte("{{ instance.name }}"), 0644))

		// Nested template file within the templates directory.
		require.NoError(t, os.MkdirAll(filepath.Join(templatesDir, "sub"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "sub", "nested.tpl"), []byte("nested"), 0644))

		// Template file that is a symlink pointing outside the templates directory.
		require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(templatesDir, "passwd.tpl")))

		// Intermediate directory that is a symlink pointing outside the templates directory.
		// A template referenced through it must not be able to escape via the parent.
		outsideDir, err := os.MkdirTemp("", "lxd-outside-")
		require.NoError(t, err)
		defer os.RemoveAll(outsideDir)
		require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "test.tpl"), []byte("outside"), 0644))
		require.NoError(t, os.Symlink(outsideDir, filepath.Join(templatesDir, "linkdir")))

		// Valid template files are joined onto the templates directory.
		regularPaths := []string{
			"hostname.tpl",
			"sub/nested.tpl",
		}

		for _, name := range regularPaths {
			p, err := securePathJoin(templatesDir, name, true)
			assert.NoError(t, err)
			assert.Equal(t, filepath.Join(templatesDir, name), p)
		}

		// Template files using directory traversal to escape the templates directory are rejected.
		escapePaths := []string{
			"../../../../../../../../etc/passwd",
			"../metadata.yaml",
			"sub/../../escape",
		}

		for _, name := range escapePaths {
			_, err := securePathJoin(templatesDir, name, true)
			assert.Error(t, err, "expected traversal path %q to be rejected", name)
		}

		// Template files that are symlinks are rejected, even if the link name stays within the directory.
		_, err = securePathJoin(templatesDir, "passwd.tpl", true)
		assert.Error(t, err)

		// Template files reached through a symlinked directory are rejected, even if the final file
		// is a regular file.
		_, err = securePathJoin(templatesDir, "linkdir/test.tpl", true)
		assert.Error(t, err)
	})

	// Test with requireRegular=false (template output path case).
	t.Run("AllowNonExistentPath", func(t *testing.T) {
		rootfsDir, err := os.MkdirTemp("", "lxd-rootfs-")
		require.NoError(t, err)
		defer os.RemoveAll(rootfsDir)

		// Create some nested directories in the rootfs for valid test paths.
		require.NoError(t, os.MkdirAll(filepath.Join(rootfsDir, "etc"), 0755))
		require.NoError(t, os.MkdirAll(filepath.Join(rootfsDir, "etc", "sub"), 0755))

		// Create a symlink that points outside the rootfs.
		outsideDir, err := os.MkdirTemp("", "lxd-outside-")
		require.NoError(t, err)
		defer os.RemoveAll(outsideDir)
		require.NoError(t, os.Symlink(outsideDir, filepath.Join(rootfsDir, "linkdir")))

		// Valid output paths within the rootfs are accepted.
		validPaths := []string{
			"etc/hostname",
			"etc/sub/nested",
			"/etc/resolv.conf",
			"/root/.bashrc",
		}

		for _, name := range validPaths {
			p, err := securePathJoin(rootfsDir, name, false)
			assert.NoError(t, err, "expected valid path %q to be accepted", name)
			// Verify the result is within rootfsDir
			if err == nil {
				assert.True(t, strings.HasPrefix(p, rootfsDir), "expected path %q to be within rootfs", p)
			}
		}

		// Output paths using directory traversal to escape the rootfs are rejected.
		escapePaths := []string{
			"../../../../../../../../etc/passwd",
			"../../../metadata.yaml",
			"etc/../../escape",
			"/etc/../../escape",
		}

		for _, name := range escapePaths {
			_, err := securePathJoin(rootfsDir, name, false)
			assert.Error(t, err, "expected traversal path %q to be rejected", name)
		}

		// Output paths with symlinked intermediate directories are rejected, even if they would
		// technically remain within the rootfs through the symlink.
		_, err = securePathJoin(rootfsDir, "linkdir/test", false)
		assert.Error(t, err, "expected path through symlink directory to be rejected")

		// Output paths where the target itself is a symlink pointing outside the rootfs are rejected.
		// This is critical for preventing template rendering from following escape symlinks.
		require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(rootfsDir, "escapedlink")))
		_, err = securePathJoin(rootfsDir, "escapedlink", false)
		assert.Error(t, err, "expected symlink path pointing outside to be rejected")
	})
}
