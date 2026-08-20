package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatDeviceID(t *testing.T) {
	dir := t.TempDir()

	fileA := filepath.Join(dir, "a")
	fileB := filepath.Join(dir, "b")
	require.NoError(t, os.WriteFile(fileA, nil, 0600))
	require.NoError(t, os.WriteFile(fileB, nil, 0600))

	idA, err := StatDeviceID(fileA)
	require.NoError(t, err)
	assert.Regexp(t, `^\d+:\d+$`, idA)

	// Two paths on the same filesystem report the same device ID.
	idB, err := StatDeviceID(fileB)
	require.NoError(t, err)
	assert.Equal(t, idA, idB)

	_, err = StatDeviceID(filepath.Join(dir, "does-not-exist"))
	assert.Error(t, err)
}

func TestExtractFromMountInfo(t *testing.T) {
	mountinfo := "" +
		"66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n" +
		"91 25 0:73 / /dev/.lxd-mounts rw,relatime shared:47 - tmpfs tmpfs rw,size=2048k,mode=711\n"
	path := filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(path, []byte(mountinfo), 0600))

	// match/extract are generic over which field is being looked up and returned;
	// exercise that with something other than GetDeviceIDFromMountInfo's own
	// device-ID lookup, e.g. the fstype (field 8) for a match on mount ID.
	fields, err := extractFromMountInfo(path,
		func(fields []string) bool { return fields[0] == "91" },
		func(fields []string) []string { return fields[8:9] })
	require.NoError(t, err)
	assert.Equal(t, []string{"tmpfs"}, fields)

	_, err = extractFromMountInfo(path,
		func(fields []string) bool { return fields[0] == "does-not-exist" },
		func(fields []string) []string { return fields })
	assert.ErrorIs(t, err, ErrNoMountInfoEntry)

	_, err = extractFromMountInfo(filepath.Join(t.TempDir(), "does-not-exist"),
		func(fields []string) bool { return true },
		func(fields []string) []string { return fields })
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoMountInfoEntry)
}

func TestExtractFromMountInfoLongLine(t *testing.T) {
	// bufio.Scanner's default 64 KiB max line length is smaller than a real
	// overlayfs mount can produce (its superblock options field concatenates
	// one entry per lowerdir). An unrelated line this long, appearing before
	// the line being looked for, must not stop the scan from reaching it.
	longOptions := "rw,lowerdir=" + strings.Repeat("/var/lib/lxd/storage-pools/default/images/deadbeef/rootfs:", 2000)
	require.Greater(t, len(longOptions), 64*1024)

	mountinfo := "" +
		"12 25 0:11 / /some/unrelated/overlay rw,relatime shared:1 - overlay overlay " + longOptions + "\n" +
		"66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n"
	path := filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(path, []byte(mountinfo), 0600))

	fields, err := extractFromMountInfo(path,
		func(fields []string) bool { return fields[4] == "/dev/lxd" },
		func(fields []string) []string { return fields[2:3] })
	require.NoError(t, err)
	assert.Equal(t, []string{"0:58"}, fields)
}

func TestEscapeMountInfoField(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "no special characters", in: "/dev/lxd", want: "/dev/lxd"},
		{name: "space", in: "/mnt/my pool", want: `/mnt/my\040pool`},
		{name: "tab", in: "/mnt/a\tb", want: `/mnt/a\011b`},
		{name: "newline", in: "/mnt/a\nb", want: `/mnt/a\012b`},
		{name: "backslash", in: `/mnt/a\b`, want: `/mnt/a\134b`},
		{
			name: "all of them together",
			in:   "/mnt/a b\tc\nd\\e",
			want: `/mnt/a\040b\011c\012d\134e`,
		},
		{
			// A path whose literal bytes happen to spell out another field's
			// escape sequence must not come out looking like that sequence:
			// the backslash has to be escaped first, or this would collide
			// with escapeMountInfoField("/mnt/ ") (a trailing space).
			name: "literal backslash-zero-four-zero does not collide with an escaped space",
			in:   `/mnt/\040`,
			want: `/mnt/\134040`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, escapeMountInfoField(tt.in))
		})
	}

	// The two inputs distinguished by the case above must not escape to the
	// same string.
	assert.NotEqual(t, escapeMountInfoField(`/mnt/\040`), escapeMountInfoField("/mnt/ "))
}

func TestGetDeviceIDFromMountInfo(t *testing.T) {
	tests := []struct {
		name        string
		mountinfo   string
		mountPoint  string
		want        string
		wantErr     bool
		wantNoEntry bool // asserted via errors.Is(err, ErrNoMountInfoEntry)
	}{
		{
			name: "simple match",
			mountinfo: "" +
				"66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n",
			mountPoint: "/dev/lxd",
			want:       "0:58",
		},
		{
			name: "last match wins",
			mountinfo: "" +
				"66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n" +
				"91 25 0:73 / /dev/lxd rw,relatime shared:47 - tmpfs tmpfs rw,size=1024k,mode=711\n",
			mountPoint: "/dev/lxd",
			want:       "0:73",
		},
		{
			name: "no match",
			mountinfo: "" +
				"66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n",
			mountPoint:  "/dev/.lxd-mounts",
			wantErr:     true,
			wantNoEntry: true,
		},
		{
			name: "malformed lines are skipped, not matched",
			mountinfo: "" +
				"garbage short line\n" +
				"66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n",
			mountPoint: "/dev/lxd",
			want:       "0:58",
		},
		{
			name:        "empty file",
			mountinfo:   "",
			mountPoint:  "/dev/lxd",
			wantErr:     true,
			wantNoEntry: true,
		},
		{
			// The kernel octal-escapes space/tab/newline/backslash in mountinfo
			// path fields (fs/proc_namespace.c's mangle()); mountPoint is given
			// as a normal, unescaped path, same as any other caller would pass.
			name: "mount point containing a space",
			mountinfo: "" +
				`66 25 0:58 / /mnt/my\040pool rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711` + "\n",
			mountPoint: "/mnt/my pool",
			want:       "0:58",
		},
		{
			name: "mount point containing a tab",
			mountinfo: "" +
				`66 25 0:58 / /mnt/a\011b rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711` + "\n",
			mountPoint: "/mnt/a\tb",
			want:       "0:58",
		},
		{
			name: "mount point containing a newline",
			mountinfo: "" +
				`66 25 0:58 / /mnt/a\012b rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711` + "\n",
			mountPoint: "/mnt/a\nb",
			want:       "0:58",
		},
		{
			name: "mount point containing a backslash",
			mountinfo: "" +
				`66 25 0:58 / /mnt/a\134b rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711` + "\n",
			mountPoint: `/mnt/a\b`,
			want:       "0:58",
		},
		{
			// A mount point literally named "\040" (backslash, zero, four, zero
			// as real characters) is a different path from one named " " (a
			// single space); the kernel escapes the literal backslash too, so
			// it appears as "\134040", not "\040". Looking up the space must
			// not match the entry for the literal-backslash path.
			name: "escaped mount point is not confused with an unrelated literal backslash sequence",
			mountinfo: "" +
				`66 25 0:58 / /mnt/\134040 rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711` + "\n",
			mountPoint:  "/mnt/ ",
			wantErr:     true,
			wantNoEntry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mountinfo")
			require.NoError(t, os.WriteFile(path, []byte(tt.mountinfo), 0600))

			got, err := GetDeviceIDFromMountInfo(path, tt.mountPoint)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantNoEntry, errors.Is(err, ErrNoMountInfoEntry))
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("missing mountinfo file", func(t *testing.T) {
		// A real I/O error, as opposed to "not mounted": callers need to be able
		// to tell the two apart, so this must not satisfy errors.Is(err,
		// ErrNoMountInfoEntry).
		_, err := GetDeviceIDFromMountInfo(filepath.Join(t.TempDir(), "does-not-exist"), "/dev/lxd")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrNoMountInfoEntry)
	})
}
