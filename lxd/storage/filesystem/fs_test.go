package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMkdirAllOwner(t *testing.T) {
	tmpDir := t.TempDir()
	uid := os.Getuid()
	gid := os.Getgid()

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("Failed opening root %q: %v", tmpDir, err)
	}

	defer func() { _ = root.Close() }()

	err = MkdirAllOwner(root, "a/b/c", 0755, uid, gid)
	if err != nil {
		t.Fatalf("Failed creating nested directories: %v", err)
	}

	// Ensure repeated calls are idempotent for existing directories.
	err = MkdirAllOwner(root, "a/b/c", 0755, uid, gid)
	if err != nil {
		t.Fatalf("Failed on idempotent nested directory creation: %v", err)
	}

	createdDir, err := root.Stat("a/b/c")
	if err != nil {
		t.Fatalf("Failed statting created directory: %v", err)
	}

	if !createdDir.IsDir() {
		t.Fatalf("Expected created path to be a directory")
	}

	ownershipChecks := []string{"a", "a/b", "a/b/c"}
	for i := range ownershipChecks {
		info, err := root.Lstat(ownershipChecks[i])
		if err != nil {
			t.Fatalf("Failed statting %q: %v", ownershipChecks[i], err)
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("Expected syscall.Stat_t for %q", ownershipChecks[i])
		}

		if int(stat.Uid) != uid || int(stat.Gid) != gid {
			t.Fatalf("Expected %q ownership uid=%d gid=%d, got uid=%d gid=%d", ownershipChecks[i], uid, gid, stat.Uid, stat.Gid)
		}
	}
}

func TestMkdirAllOwnerPathExistsAsFile(t *testing.T) {
	tmpDir := t.TempDir()

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("Failed opening root %q: %v", tmpDir, err)
	}

	defer func() { _ = root.Close() }()

	err = os.WriteFile(filepath.Join(tmpDir, "existing-file"), []byte("data"), 0600)
	if err != nil {
		t.Fatalf("Failed creating existing file: %v", err)
	}

	err = MkdirAllOwner(root, "existing-file", 0755, -1, -1)
	if err == nil {
		t.Fatalf("Expected error when path exists as file")
	}

	// The error should match os.MkdirAll's behaviour: a *os.PathError wrapping syscall.ENOTDIR.
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("Expected error to wrap syscall.ENOTDIR, got: %v", err)
	}

	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("Expected error to be a *os.PathError, got: %T", err)
	}

	if pathErr.Op != "mkdir" || pathErr.Path != "existing-file" {
		t.Fatalf("Expected PathError{Op: mkdir, Path: existing-file}, got Op=%q Path=%q", pathErr.Op, pathErr.Path)
	}
}

func TestMkdirAllOwnerExistingDirNotChowned(t *testing.T) {
	tmpDir := t.TempDir()

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("Failed opening root %q: %v", tmpDir, err)
	}

	defer func() { _ = root.Close() }()

	// Pre-create the directory so it already exists before the call.
	err = root.Mkdir("existing-dir", 0755)
	if err != nil {
		t.Fatalf("Failed creating existing directory: %v", err)
	}

	info, err := root.Lstat("existing-dir")
	if err != nil {
		t.Fatalf("Failed statting existing directory: %v", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("Expected syscall.Stat_t for existing directory")
	}

	// Request ownership that differs from the current owner. Because the directory already
	// exists, MkdirAllOwner must take the fast path and skip Chown entirely, so this must
	// not fail even when running unprivileged (where the Chown would otherwise be denied).
	err = MkdirAllOwner(root, "existing-dir", 0755, int(stat.Uid)+1, int(stat.Gid)+1)
	if err != nil {
		t.Fatalf("Expected no error for existing directory, got: %v", err)
	}

	// Confirm ownership was left untouched.
	after, err := root.Lstat("existing-dir")
	if err != nil {
		t.Fatalf("Failed statting existing directory after call: %v", err)
	}

	afterStat, ok := after.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("Expected syscall.Stat_t for existing directory after call")
	}

	if afterStat.Uid != stat.Uid || afterStat.Gid != stat.Gid {
		t.Fatalf("Expected ownership to remain uid=%d gid=%d, got uid=%d gid=%d", stat.Uid, stat.Gid, afterStat.Uid, afterStat.Gid)
	}
}

func TestMkdirAllOwnerRootEscapeProtection(t *testing.T) {
	baseDir := t.TempDir()
	rootDir := filepath.Join(baseDir, "root")

	err := os.Mkdir(rootDir, 0755)
	if err != nil {
		t.Fatalf("Failed creating root directory %q: %v", rootDir, err)
	}

	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("Failed opening root %q: %v", rootDir, err)
	}

	defer func() { _ = root.Close() }()

	escapeTargets := []string{"../outside-a", "safe/../../outside-b"}
	outsidePaths := []string{filepath.Join(baseDir, "outside-a"), filepath.Join(baseDir, "outside-b")}

	for i := range escapeTargets {
		err = MkdirAllOwner(root, escapeTargets[i], 0755, -1, -1)
		if err == nil {
			t.Fatalf("Expected escape attempt %q to fail", escapeTargets[i])
		}

		_, statErr := os.Stat(outsidePaths[i])
		if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("Expected no outside directory at %q, got stat error: %v", outsidePaths[i], statErr)
		}
	}
}

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

func TestGetMountinfo(t *testing.T) {
	dir := t.TempDir()

	fields, err := GetMountinfo("/proc/self/mountinfo", dir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(fields), 5)

	// dir is not itself a mount point, so the entry found is the mount it lives
	// in and its mount point (field 4) is an ancestor of dir.
	assert.True(t, strings.HasPrefix(dir, fields[4]), "mount point %q is not an ancestor of %q", fields[4], dir)

	t.Run("path not in the given mountinfo file", func(t *testing.T) {
		// A well-formed mountinfo file that simply holds no entry for dir's mount,
		// which is what reading another mount namespace's file would look like if
		// the path had been resolved in the caller's namespace instead.
		path := filepath.Join(t.TempDir(), "mountinfo")
		require.NoError(t, os.WriteFile(path, []byte("66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n"), 0600))

		_, err := GetMountinfo(path, dir)
		assert.ErrorIs(t, err, ErrNoMountInfoEntry)
	})

	t.Run("missing mountinfo file", func(t *testing.T) {
		// A real I/O error, as opposed to "no entry": callers need to be able to
		// tell the two apart.
		_, err := GetMountinfo(filepath.Join(t.TempDir(), "does-not-exist"), dir)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrNoMountInfoEntry)
	})

	t.Run("missing path", func(t *testing.T) {
		_, err := GetMountinfo("/proc/self/mountinfo", filepath.Join(dir, "does-not-exist"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestGetMountinfoLongLine(t *testing.T) {
	dir := t.TempDir()

	// Take the real mount ID for dir so the synthetic file below can hold an
	// entry that the stat inside GetMountinfo will match.
	current, err := GetMountinfo("/proc/self/mountinfo", dir)
	require.NoError(t, err)
	mountID := current[0]

	// A real overlayfs mount concatenates one entry per lowerdir into its
	// superblock options field, so its mountinfo line can be far longer than
	// bufio.Scanner's default 64 KiB. An unrelated line that long, appearing
	// before the line being looked for, must not stop the scan from reaching it.
	// Deliberately past 1 MiB as well, so this stays a test of "no line length
	// limit" rather than of some particular larger buffer size.
	longOptions := "rw,lowerdir=" + strings.Repeat("/var/lib/lxd/storage-pools/default/images/deadbeef/rootfs:", 40000)
	require.Greater(t, len(longOptions), 1024*1024)

	// Appending a digit keeps this distinct from dir's own mount ID.
	unrelatedID := mountID + "0"

	mountinfo := "" +
		unrelatedID + " 25 0:11 / /some/unrelated/overlay rw,relatime shared:1 - overlay overlay " + longOptions + "\n" +
		mountID + " 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n"
	path := filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(path, []byte(mountinfo), 0600))

	fields, err := GetMountinfo(path, dir)
	require.NoError(t, err)
	assert.Equal(t, "0:58", fields[2])
}

func TestGetMountinfoNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()

	current, err := GetMountinfo("/proc/self/mountinfo", dir)
	require.NoError(t, err)
	mountID := current[0]

	// The matching entry is the last line and has no trailing newline, so it is
	// only seen if the final short read is examined rather than discarded.
	mountinfo := mountID + " 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711"
	path := filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(path, []byte(mountinfo), 0600))

	fields, err := GetMountinfo(path, dir)
	require.NoError(t, err)
	assert.Equal(t, "0:58", fields[2])
}

func TestGetMountinfoMalformedLines(t *testing.T) {
	dir := t.TempDir()

	current, err := GetMountinfo("/proc/self/mountinfo", dir)
	require.NoError(t, err)
	mountID := current[0]

	mountinfo := "" +
		"garbage short line\n" +
		"\n" +
		mountID + " 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711\n"
	path := filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(path, []byte(mountinfo), 0600))

	fields, err := GetMountinfo(path, dir)
	require.NoError(t, err)
	assert.Equal(t, "0:58", fields[2])
}
