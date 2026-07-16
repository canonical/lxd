package drivers

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateFileSafePath(t *testing.T) {
	templatesDir, err := ioutil.TempDir("", "lxd-templates-")
	require.NoError(t, err)
	defer os.RemoveAll(templatesDir)

	// Regular template file within the templates directory.
	require.NoError(t, ioutil.WriteFile(filepath.Join(templatesDir, "hostname.tpl"), []byte("{{ instance.name }}"), 0644))

	// Nested template file within the templates directory.
	require.NoError(t, os.MkdirAll(filepath.Join(templatesDir, "sub"), 0755))
	require.NoError(t, ioutil.WriteFile(filepath.Join(templatesDir, "sub", "nested.tpl"), []byte("nested"), 0644))

	// Template file that is a symlink pointing outside the templates directory.
	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(templatesDir, "passwd.tpl")))

	// Intermediate directory that is a symlink pointing outside the templates directory.
	// A template referenced through it must not be able to escape via the parent.
	outsideDir, err := ioutil.TempDir("", "lxd-outside-")
	require.NoError(t, err)
	defer os.RemoveAll(outsideDir)
	require.NoError(t, ioutil.WriteFile(filepath.Join(outsideDir, "test.tpl"), []byte("outside"), 0644))
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(templatesDir, "linkdir")))

	// Valid template files are joined onto the templates directory.
	regularPaths := []string{
		"hostname.tpl",
		"sub/nested.tpl",
	}

	for _, name := range regularPaths {
		p, err := templateFileSafePath(templatesDir, name)
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
		_, err := templateFileSafePath(templatesDir, name)
		assert.Error(t, err, "expected traversal path %q to be rejected", name)
	}

	// Template files that are symlinks are rejected, even if the link name stays within the directory.
	_, err = templateFileSafePath(templatesDir, "passwd.tpl")
	assert.Error(t, err)

	// Template files reached through a symlinked directory are rejected, even if the final file
	// is a regular file.
	_, err = templateFileSafePath(templatesDir, "linkdir/test.tpl")
	assert.Error(t, err)
}
