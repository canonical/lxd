package shared

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/flosch/pongo2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestURLEncode(t *testing.T) {
	url, _ := URLEncode(
		"/path/with spaces",
		map[string]string{"param": "with spaces", "other": "without"})
	expected := "/path/with%20spaces?other=without&param=with+spaces"
	if url != expected {
		t.Error(fmt.Errorf("%q != %q", url, expected))
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
		t.Error(fmt.Errorf("%q != %q", res, expected))
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
		t.Fatalf("Failed parsing headers %q: %s", header, err)
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
		t.Fatalf("Failed parsing headers %q: %s", header, err)
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
		t.Fatalf("Failed parsing headers %q: %s", header, err)
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
					t.Errorf("byte %d did not match", offset+i)
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

func TestResolveSnapPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("This test is only relevant on Linux")
	}

	tests := []struct {
		name     string
		snap     string
		snapName string
		unset    bool
		input    string
		want     string
		isSuffix bool
		wantOk   bool
	}{
		{
			name:   "Not in snap",
			unset:  true,
			input:  "/foo/bar",
			want:   "/foo/bar",
			wantOk: false,
		},
		{
			name:     "In snap but not LXD",
			snap:     "/snap/other/current",
			snapName: "other",
			input:    "/foo/bar",
			want:     "/foo/bar",
			wantOk:   false,
		},
		{
			name:     "In LXD snap - empty path",
			snap:     "/snap/lxd/current",
			snapName: "lxd",
			input:    "",
			want:     "",
			wantOk:   false,
		},
		{
			name:     "In LXD snap - dash",
			snap:     "/snap/lxd/current",
			snapName: "lxd",
			input:    "-",
			want:     "-",
			wantOk:   false,
		},
		{
			name:     "In LXD snap - absolute path",
			snap:     "/snap/lxd/current",
			snapName: "lxd",
			input:    "/foo/bar",
			want:     "/foo/bar",
			wantOk:   true,
		},
		{
			name:     "In LXD snap - relative path",
			snap:     "/snap/lxd/current",
			snapName: "lxd",
			input:    "foo/bar",
			want:     "/foo/bar",
			isSuffix: true,
			wantOk:   true,
		},
	}

	// Helper to reset env
	resetEnv := func(key, val string, set bool) {
		if set {
			_ = os.Setenv(key, val)
		} else {
			_ = os.Unsetenv(key)
		}
	}

	origSnap, snapSet := os.LookupEnv("SNAP")
	origSnapName, snapNameSet := os.LookupEnv("SNAP_NAME")
	defer func() {
		resetEnv("SNAP", origSnap, snapSet)
		resetEnv("SNAP_NAME", origSnapName, snapNameSet)
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unset {
				_ = os.Unsetenv("SNAP")
				_ = os.Unsetenv("SNAP_NAME")
			} else {
				_ = os.Setenv("SNAP", tt.snap)
				_ = os.Setenv("SNAP_NAME", tt.snapName)
			}

			p, ok := resolveSnapPath(tt.input)

			if tt.isSuffix {
				assert.True(t, strings.HasSuffix(p, tt.want), "expected %q to have suffix %q", p, tt.want)
				assert.True(t, filepath.IsAbs(p), "expected absolute path")
			} else {
				assert.Equal(t, tt.want, p)
			}

			assert.Equal(t, tt.wantOk, ok)
		})
	}
}

func TestUnique(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		given []string
		want  []string
	}{
		{name: "nil-slice", given: nil, want: nil},
		{name: "empty-slice", given: []string{}, want: []string{}},
		{name: "single-item", given: []string{"aaa"}, want: []string{"aaa"}},
		{name: "multiple-items-no-duplicates", given: []string{"aaa", "bbb", "ccc"}, want: []string{"aaa", "bbb", "ccc"}},
		{name: "multiple-items-consecutive-duplicates", given: []string{"aaa", "aaa", "bbb", "bbb", "ccc", "ccc"}, want: []string{"aaa", "bbb", "ccc"}},
		{name: "multiple-items-random-duplicates", given: []string{"aaa", "bbb", "ccc", "bbb", "ccc", "aaa", "ccc"}, want: []string{"aaa", "bbb", "ccc"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := Unique(test.given)
			require.Equal(t, test.want, got, "unexpected Unique function result")
		})
	}
}

func TestEnsurePort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		addr string
		port string
		want string
	}{
		{name: "empty", addr: "", port: "", want: ":"},
		{name: "empty-address", addr: "", port: "1234", want: ":1234"},
		{name: "empty-port", addr: "host", port: "", want: "host:"},
		{name: "host-without-port", addr: "host", port: "1234", want: "host:1234"},
		{name: "host-with-port", addr: "host:9876", port: "1234", want: "host:9876"},
		{name: "host-in-brackets", addr: "[host]", port: "1234", want: "host:1234"},
		{name: "host-in-brackets-with-port", addr: "[host]:9876", port: "1234", want: "host:9876"},
		{name: "ipv4-without-port", addr: "123.123.123.123", port: "1234", want: "123.123.123.123:1234"},
		{name: "ipv4-with-port", addr: "123.123.123.123:9876", port: "1234", want: "123.123.123.123:9876"},
		{name: "ipv4-in-brackets", addr: "[123.123.123.123]", port: "1234", want: "123.123.123.123:1234"},
		{name: "ipv4-in-brackets-with-port", addr: "[123.123.123.123]:9876", port: "1234", want: "123.123.123.123:9876"},
		{name: "ipv6-without-port", addr: "1234:567::89ab:cd:ef01", port: "1234", want: "[1234:567::89ab:cd:ef01]:1234"},
		{name: "ipv6-with-port", addr: "1234:567::89ab:cd:ef01:9876", port: "1234", want: "[1234:567::89ab:cd:ef01:9876]:1234"},
		{name: "ipv6-in-brackets", addr: "[1234:567::89ab:cd:ef01]", port: "1234", want: "[1234:567::89ab:cd:ef01]:1234"},
		{name: "ipv6-in-brackets-with-port", addr: "[1234:567::89ab:cd:ef01]:9876", port: "1234", want: "[1234:567::89ab:cd:ef01]:9876"},
		{name: "ipv6-full-without-port", addr: "1234:5678:9abc:def0:1234:5678:9abc:def0", port: "1234", want: "[1234:5678:9abc:def0:1234:5678:9abc:def0]:1234"},
		{name: "ipv6-full-with-port", addr: "1234:5678:9abc:def0:1234:5678:9abc:def0:9876", port: "1234", want: "[1234:5678:9abc:def0:1234:5678:9abc:def0:9876]:1234"},
		{name: "ipv6-full-in-brackets", addr: "[1234:5678:9abc:def0:1234:5678:9abc:def0]", port: "1234", want: "[1234:5678:9abc:def0:1234:5678:9abc:def0]:1234"},
		{name: "ipv6-full-in-brackets-with-port", addr: "[1234:5678:9abc:def0:1234:5678:9abc:def0]:9876", port: "1234", want: "[1234:5678:9abc:def0:1234:5678:9abc:def0]:9876"},
		{name: "ipv6-min-without-port", addr: "1234::5678", port: "1234", want: "[1234::5678]:1234"},
		{name: "ipv6-min-with-port", addr: "1234::5678:9876", port: "1234", want: "[1234::5678:9876]:1234"},
		{name: "ipv6-min-in-brackets", addr: "[1234::5678]", port: "1234", want: "[1234::5678]:1234"},
		{name: "ipv6-min-in-brackets-with-port", addr: "[1234::5678]:9876", port: "1234", want: "[1234::5678]:9876"},
		{name: "ipv6-loopback-without-port", addr: "::1", port: "1234", want: "[::1]:1234"},
		{name: "ipv6-loopback-with-port", addr: "::1:9876", port: "1234", want: "[::1:9876]:1234"},
		{name: "ipv6-loopback-in-brackets", addr: "[::1]", port: "1234", want: "[::1]:1234"},
		{name: "ipv6-loopback-in-brackets-with-port", addr: "[::1]:9876", port: "1234", want: "[::1]:9876"},
		{name: "colon-without-port", addr: ":", port: "1234", want: ":1234"},
		{name: "colon-with-port", addr: "::9876", port: "1234", want: "[::9876]:1234"},
		{name: "colon-in-brackets", addr: "[:]", port: "1234", want: "[:]:1234"},
		{name: "colon-in-brackets-with-port", addr: "[:]:9876", port: "1234", want: "[:]:9876"},
		{name: "colons-without-port", addr: "::", port: "1234", want: "[::]:1234"},
		{name: "colons-with-port", addr: ":::9876", port: "1234", want: "[:::9876]:1234"},
		{name: "colons-in-brackets", addr: "[::]", port: "1234", want: "[::]:1234"},
		{name: "colons-in-brackets-with-port", addr: "[::]:9876", port: "1234", want: "[::]:9876"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := EnsurePort(test.addr, test.port)
			require.Equal(t, test.want, got, "unexpected EnsurePort function result")
		})
	}
}
