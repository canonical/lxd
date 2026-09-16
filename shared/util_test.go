package shared

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flosch/pongo2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurePathJoin(t *testing.T) {
	// Test with requireRegular=true (template file case).
	t.Run("RequireRegularFile", func(t *testing.T) {
		templatesDir, err := os.MkdirTemp("", "lxd-templates-")
		require.NoError(t, err)
		defer os.RemoveAll(templatesDir)
		templatesDir, err = filepath.EvalSymlinks(templatesDir)
		require.NoError(t, err)

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
			p, err := SecurePathJoin(templatesDir, name, true)
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
			_, err := SecurePathJoin(templatesDir, name, true)
			assert.Error(t, err, "expected traversal path %q to be rejected", name)
		}

		// Template files that are symlinks are rejected, even if the link name stays within the directory.
		_, err = SecurePathJoin(templatesDir, "passwd.tpl", true)
		assert.Error(t, err)

		// Template files reached through a symlinked directory are rejected, even if the final file
		// is a regular file.
		_, err = SecurePathJoin(templatesDir, "linkdir/test.tpl", true)
		assert.Error(t, err)
	})

	// Test with requireRegular=false (template output path case).
	t.Run("AllowNonExistentPath", func(t *testing.T) {
		rootfsDir, err := os.MkdirTemp("", "lxd-rootfs-")
		require.NoError(t, err)
		defer os.RemoveAll(rootfsDir)
		rootfsDir, err = filepath.EvalSymlinks(rootfsDir)
		require.NoError(t, err)

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
			"./",
		}

		for _, name := range validPaths {
			p, err := SecurePathJoin(rootfsDir, name, false)
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
			_, err := SecurePathJoin(rootfsDir, name, false)
			assert.Error(t, err, "expected traversal path %q to be rejected", name)
		}

		// Output paths with symlinked intermediate directories are rejected, even if they would
		// technically remain within the rootfs through the symlink.
		_, err = SecurePathJoin(rootfsDir, "linkdir/test", false)
		assert.Error(t, err, "expected path through symlink directory to be rejected")

		// Output paths where the target itself is a symlink pointing outside the rootfs are rejected.
		// This is critical for preventing template rendering from following escape symlinks.
		require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(rootfsDir, "escapedlink")))
		_, err = SecurePathJoin(rootfsDir, "escapedlink", false)
		assert.Error(t, err, "expected symlink path pointing outside to be rejected")
	})
}

func TestURLEncode(t *testing.T) {
	url, _ := URLEncode(
		"/path/with spaces",
		map[string]string{"param": "with spaces", "other": "without"})
	expected := "/path/with%20spaces?other=without&param=with+spaces"
	if url != expected {
		t.Error(fmt.Errorf("'%s' != '%s'", url, expected))
	}
}

func TestUrlsJoin(t *testing.T) {
	baseUrl := "http://images.lxd.canonical.com/streams/v1/"
	path := "../../image/root.tar.xz"

	res, err := JoinUrls(baseUrl, path)
	if err != nil {
		t.Error(err)
		return
	}

	expected := "http://images.lxd.canonical.com/image/root.tar.xz"
	if res != expected {
		t.Error(fmt.Errorf("'%s' != '%s'", res, expected))
	}
}

func TestFileCopy(t *testing.T) {
	helloWorld := []byte("hello world\n")
	source, err := ioutil.TempFile("", "")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.Remove(source.Name())

	if err := WriteAll(source, helloWorld); err != nil {
		source.Close()
		t.Error(err)
		return
	}
	source.Close()

	dest, err := ioutil.TempFile("", "")
	defer os.Remove(dest.Name())
	if err != nil {
		t.Error(err)
		return
	}
	dest.Close()

	if err := FileCopy(source.Name(), dest.Name()); err != nil {
		t.Error(err)
		return
	}

	dest2, err := os.Open(dest.Name())
	if err != nil {
		t.Error(err)
		return
	}

	content, err := ioutil.ReadAll(dest2)
	if err != nil {
		t.Error(err)
		return
	}

	if string(content) != string(helloWorld) {
		t.Error("content mismatch: ", string(content), "!=", string(helloWorld))
		return
	}
}

func TestDirCopy(t *testing.T) {
	dir, err := ioutil.TempDir("", "lxd-shared-util-")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	source := filepath.Join(dir, "source")
	dest := filepath.Join(dir, "dest")

	dir1 := "dir1"
	dir2 := "dir2"

	file1 := "file1"
	file2 := "dir1/file1"

	content1 := []byte("file1")
	content2 := []byte("file2")

	require.NoError(t, os.Mkdir(source, 0755))
	require.NoError(t, os.Mkdir(filepath.Join(source, dir1), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(source, dir2), 0755))
	require.NoError(t, ioutil.WriteFile(filepath.Join(source, file1), content1, 0755))
	require.NoError(t, ioutil.WriteFile(filepath.Join(source, file2), content2, 0755))

	require.NoError(t, DirCopy(source, dest))

	for _, path := range []string{dir1, dir2, file1, file2} {
		assert.True(t, PathExists(filepath.Join(dest, path)))
	}

	bytes, err := ioutil.ReadFile(filepath.Join(dest, file1))
	require.NoError(t, err)
	assert.Equal(t, content1, bytes)

	bytes, err = ioutil.ReadFile(filepath.Join(dest, file2))
	require.NoError(t, err)
	assert.Equal(t, content2, bytes)
}

func TestReaderToChannel(t *testing.T) {
	buf := make([]byte, 1*1024*1024)
	rand.Read(buf)

	offset := 0
	finished := false

	ch := ReaderToChannel(bytes.NewBuffer(buf), -1)
	for {
		data, ok := <-ch
		if len(data) > 0 {
			for i := 0; i < len(data); i++ {
				if buf[offset+i] != data[i] {
					t.Error(fmt.Sprintf("byte %d didn't match", offset+i))
					return
				}
			}

			offset += len(data)
			if offset > len(buf) {
				t.Error("read too much data")
				return
			}

			if offset == len(buf) {
				finished = true
			}
		}

		if !ok {
			if !finished {
				t.Error("connection closed too early")
				return
			} else {
				break
			}
		}
	}
}

func TestGetSnapshotExpiry(t *testing.T) {
	refDate := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiryDate, err := GetSnapshotExpiry(refDate, "1M 2H 3d 4w 5m 6y")
	expectedDate := time.Date(2006, time.July, 2, 2, 1, 0, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	refDate = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiryDate, err = GetSnapshotExpiry(refDate, "1M 2H 3d 4y")
	expectedDate = time.Date(2004, time.January, 4, 2, 1, 0, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	expiryDate, err = GetSnapshotExpiry(refDate, "0M 0H 0d 0w 0m 0y")
	require.NoError(t, err)
	require.Equal(t, expiryDate, expiryDate)

	expiryDate, err = GetSnapshotExpiry(refDate, "")
	require.NoError(t, err)
	require.Equal(t, time.Time{}, expiryDate)

	expiryDate, err = GetSnapshotExpiry(refDate, "1z")
	require.Error(t, err)
	require.Equal(t, time.Time{}, expiryDate)
}

func TestRenderTemplate(t *testing.T) {
	// Reject invalid templates.
	out, err := RenderTemplate(`{% include "/etc/hosts" %}`, nil)
	assert.Error(t, err)
	assert.Empty(t, out)

	out, err = RenderTemplate(`{{ "{"|escape }}{{ "%"|escape }} include "/etc/hosts" {{ "%"|escape }}{{ "}"|escape }}`, nil)
	assert.Error(t, err)
	assert.Empty(t, out)

	// Recursion limit hit.
	out, err = RenderTemplate(`{{ "{{ '{{ \"{{ 1 }}' }}" }}" }}`, nil)
	assert.Error(t, err)
	assert.Empty(t, out)

	// Render proper templates.
	out, err = RenderTemplate(`Hello, world!`, nil)
	assert.NoError(t, err)
	assert.Equal(t, `Hello, world!`, out)

	out, err = RenderTemplate(`{{ "Hello, world!" }}`, nil)
	assert.NoError(t, err)
	assert.Equal(t, `Hello, world!`, out)

	out, err = RenderTemplate(`mysnap%d`, nil)
	assert.NoError(t, err)
	assert.Equal(t, `mysnap%d`, out)

	out, err = RenderTemplate(`mysnap%`, nil)
	assert.NoError(t, err)
	assert.Equal(t, `mysnap%`, out)

	out, err = RenderTemplate(`{{ "h"|capfirst }}`, nil)
	assert.NoError(t, err)
	assert.Equal(t, `H`, out)

	// Recursion limit not hit.
	out, err = RenderTemplate(`{{ "{{ '{{ \"1\" }}' }}" }}`, nil)
	assert.NoError(t, err)
	assert.Equal(t, `1`, out)

	// Check pongo2 panics are handled.
	_, err = RenderTemplate(`{{ badsnap%d }}`, nil)
	assert.Error(t, err)
}

func TestRenderTemplateFile(t *testing.T) {
	// Render proper template.
	var buf bytes.Buffer
	err := RenderTemplateFile(&buf, `Hello, {{ name }}!`, pongo2.Context{"name": "world"})
	assert.NoError(t, err)
	assert.Equal(t, `Hello, world!`, buf.String())

	// Ban dangerous tags.
	for _, tag := range []string{"extends", "import", "include", "ssi"} {
		buf.Reset()
		err = RenderTemplateFile(&buf, fmt.Sprintf(`{%% %s "/etc/hosts" %%}`, tag), nil)
		assert.Error(t, err)
		assert.Empty(t, buf.String())
	}

	// Check pongo2 panics are handled.
	buf.Reset()
	err = RenderTemplateFile(&buf, `{{ badsnap%d }}`, nil)
	assert.Error(t, err)
}
