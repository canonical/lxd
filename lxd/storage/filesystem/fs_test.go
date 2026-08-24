package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
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
