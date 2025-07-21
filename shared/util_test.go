package shared

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	baseURL := "https://cloud-images.ubuntu.com/releases/streams/v1/"
	path := "../../image/root.tar.xz"

	res, err := JoinUrls(baseURL, path)
	if err != nil {
		t.Error(err)
		return
	}

	expected := "https://cloud-images.ubuntu.com/releases/image/root.tar.xz"
	if res != expected {
		t.Error(fmt.Errorf("'%s' != '%s'", res, expected))
	}
}

func TestParseLXDFileHeader(t *testing.T) {
	header := map[string][]string{
		"X-Lxd-Uid":  {"1000"},
		"X-Lxd-Gid":  {"1001"},
		"X-Lxd-Mode": {"0700"},
	}

	headers, err := ParseLXDFileHeaders(header)
	if err != nil {
		t.Fatalf("Failed to parse headers %q: %s", header, err)
	}

	if headers.UID != 1000 || headers.GID != 1001 || headers.Mode != 0o700 {
		t.Fatalf("Mismatched UID (%d), GID (%d), or Mode (%d)", headers.UID, headers.GID, headers.Mode)
	}

	if headers.UIDModifyExisting || headers.GIDModifyExisting || headers.ModeModifyExisting {
		t.Fatalf("Mismatched `modify-perm` header")
	}

	header = map[string][]string{
		"X-Lxd-Uid":         {"0"},
		"X-Lxd-Gid":         {"99"},
		"X-Lxd-Mode":        {"420"},
		"X-Lxd-Modify-Perm": {"uid,gid,mode"},
	}

	headers, err = ParseLXDFileHeaders(header)
	if err != nil {
		t.Fatalf("Failed to parse headers %q: %s", header, err)
	}

	if headers.UID != 0 || headers.GID != 99 || headers.Mode != 0o644 {
		t.Fatalf("Mismatched UID (%d), GID (%d), or Mode (%d)", headers.UID, headers.GID, headers.Mode)
	}

	if !headers.UIDModifyExisting || !headers.GIDModifyExisting || !headers.ModeModifyExisting {
		t.Fatalf("Mismatched `modify-perm` header")
	}

	header = map[string][]string{
		"X-Lxd-Mode":        {"0640"},
		"X-Lxd-Modify-Perm": {"uid,gid"},
		"X-Lxd-Type":        {"file"},
		"X-Lxd-Write":       {"append"},
	}

	headers, err = ParseLXDFileHeaders(header)
	if err != nil {
		t.Fatalf("Failed to parse headers %q: %s", header, err)
	}

	if headers.Mode != 0o640 || headers.UID != -1 || headers.GID != -1 {
		t.Fatalf("Mismatched UID (%d), GID (%d), or Mode (%d)", headers.UID, headers.GID, headers.Mode)
	}

	if !headers.UIDModifyExisting || !headers.GIDModifyExisting || headers.ModeModifyExisting {
		t.Fatalf("Mismatched `modify-perm` header")
	}

	if headers.Type != "file" || headers.Write != "append" {
		t.Fatalf("Mismatched Type (%s) or Write (%s)", headers.Type, headers.Write)
	}

	invalidHeaderTests := []map[string][]string{
		{"X-Lxd-Uid": {"0xF4"}},
		{"X-Lxd-Gid": {"0b1101"}},
		{"X-Lxd-Mode": {"write"}},
		{"X-Lxd-Type": {"dir"}},
		{"X-Lxd-Write": {"Append"}},
		{"X-Lxd-Modify-Perm": {"GID"}},
		{"X-Lxd-Modify-Perm": {","}},
	}

	for _, header := range invalidHeaderTests {
		_, err = ParseLXDFileHeaders(header)
		if err == nil {
			t.Fatalf("Parsed invalid headers %q", header)
		}
	}
}

func TestFileCopy(t *testing.T) {
	helloWorld := []byte("hello world\n")
	source, err := os.CreateTemp("", "")
	if err != nil {
		t.Error(err)
		return
	}

	defer func() { _ = os.Remove(source.Name()) }()

	err = WriteAll(source, helloWorld)
	if err != nil {
		_ = source.Close()
		t.Error(err)
		return
	}

	_ = source.Close()

	dest, err := os.CreateTemp("", "")
	defer func() { _ = os.Remove(dest.Name()) }()
	if err != nil {
		t.Error(err)
		return
	}

	_ = dest.Close()

	err = FileCopy(source.Name(), dest.Name())
	if err != nil {
		t.Error(err)
		return
	}

	dest2, err := os.Open(dest.Name())
	if err != nil {
		t.Error(err)
		return
	}

	content, err := io.ReadAll(dest2)
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
	dir, err := os.MkdirTemp("", "lxd-shared-util-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()

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
	require.NoError(t, os.WriteFile(filepath.Join(source, file1), content1, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, file2), content2, 0755))

	require.NoError(t, DirCopy(source, dest))

	for _, path := range []string{dir1, dir2, file1, file2} {
		assert.True(t, PathExists(filepath.Join(dest, path)))
	}

	bytes, err := os.ReadFile(filepath.Join(dest, file1))
	require.NoError(t, err)
	assert.Equal(t, content1, bytes)

	bytes, err = os.ReadFile(filepath.Join(dest, file2))
	require.NoError(t, err)
	assert.Equal(t, content2, bytes)
}

func TestReaderToChannel(t *testing.T) {
	buf := make([]byte, 1*1024*1024)
	_, _ = rand.Read(buf)

	offset := 0
	finished := false

	ch := ReaderToChannel(bytes.NewBuffer(buf), -1)
	for {
		data, ok := <-ch
		if len(data) > 0 {
			for i := range data {
				if buf[offset+i] != data[i] {
					t.Errorf("byte %d didn't match", offset+i)
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
			}

			break
		}
	}
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
	assert.ErrorContains(t, err, "Recursion limit")
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

func TestGetExpiry(t *testing.T) {
	refDate := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiryDate, err := GetExpiry(refDate, "1M 2H 3d 4w 5m 6y")
	expectedDate := time.Date(2006, time.July, 2, 2, 1, 0, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	expiryDate, err = GetExpiry(refDate, "5S 1M 2H 3d 4y")
	expectedDate = time.Date(2004, time.January, 4, 2, 1, 5, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	expiryDate, err = GetExpiry(refDate, "0M 0H 0d 0w 0m 0y")
	expectedDate = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	expiryDate, err = GetExpiry(refDate, "")
	require.NoError(t, err)
	require.Equal(t, time.Time{}, expiryDate)

	expiryDate, err = GetExpiry(refDate, "1M 1M")
	require.Error(t, err)
	require.Equal(t, time.Time{}, expiryDate)

	expiryDate, err = GetExpiry(refDate, "1z")
	require.Error(t, err)
	require.Equal(t, time.Time{}, expiryDate)
}

func TestHasKey(t *testing.T) {
	m1 := map[string]string{
		"foo":   "bar",
		"empty": "",
	}

	m2 := map[int]string{
		1: "foo",
	}

	assert.True(t, HasKey("foo", m1))
	assert.True(t, HasKey("empty", m1))
	assert.False(t, HasKey("missing", m1))

	assert.True(t, HasKey(1, m2))
	assert.False(t, HasKey(0, m2))
}

func TestRemoveElementsFromStringSlice(t *testing.T) {
	type test struct {
		elementsToRemove []string
		list             []string
		expectedList     []string
	}

	tests := []test{
		{
			elementsToRemove: []string{"one", "two", "three"},
			list:             []string{"one", "two", "three", "four", "five"},
			expectedList:     []string{"four", "five"},
		},
		{
			elementsToRemove: []string{"two", "three", "four"},
			list:             []string{"one", "two", "three", "four", "five"},
			expectedList:     []string{"one", "five"},
		},
		{
			elementsToRemove: []string{"two", "three", "four"},
			list:             []string{"two", "three"},
			expectedList:     []string{},
		},
		{
			elementsToRemove: []string{"two", "two", "two"},
			list:             []string{"two"},
			expectedList:     []string{},
		},
		{
			elementsToRemove: []string{"two", "two", "two"},
			list:             []string{"one", "two", "three", "four", "five"},
			expectedList:     []string{"one", "three", "four", "five"},
		},
	}

	for _, tt := range tests {
		gotList := RemoveElementsFromSlice(tt.list, tt.elementsToRemove...)
		assert.ElementsMatch(t, tt.expectedList, gotList)
	}
}
