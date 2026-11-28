package util

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/api"
)

func TestUnprivilegedCopy(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	tests := []struct {
		name    string
		content []byte
	}{
		{name: "empty", content: []byte{}},
		{name: "small", content: []byte("hello world")},
		{name: "multi block", content: bytes.Repeat([]byte("a"), 1024*1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("Failed creating temporary file: %v", err)
			}

			defer func() { _ = f.Close() }()

			written, err := UnprivilegedCopy(t.Context(), f, bytes.NewReader(tt.content), uid, gid)
			if err != nil {
				t.Fatalf("Failed copying: %v", err)
			}

			if written != int64(len(tt.content)) {
				t.Fatalf("Expected %d bytes written, got %d", len(tt.content), written)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed reading back file: %v", err)
			}

			if !bytes.Equal(got, tt.content) {
				t.Fatalf("Copied content does not match the source")
			}

			// The file has to be owned by the unprivileged user, as filesystem quotas are
			// accounted against the owner of the file.
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Failed stating file: %v", err)
			}

			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				t.Fatalf("Failed reading file ownership")
			}

			if st.Uid != uid || st.Gid != gid {
				t.Fatalf("Expected file owned by %d:%d, got %d:%d", uid, gid, st.Uid, st.Gid)
			}
		})
	}
}

func TestUnprivilegedCopyRootFallback(t *testing.T) {
	tests := []struct {
		name string
		uid  uint32
		gid  uint32
	}{
		{name: "no unprivileged user or group", uid: 0, gid: 0},
		{name: "no unprivileged group", uid: 65534, gid: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("Failed creating temporary file: %v", err)
			}

			defer func() { _ = f.Close() }()

			content := []byte("fallback")

			// There is nothing to switch to, so a regular copy is expected.
			written, err := UnprivilegedCopy(t.Context(), f, bytes.NewReader(content), tt.uid, tt.gid)
			if err != nil {
				t.Fatalf("Failed copying: %v", err)
			}

			if written != int64(len(content)) {
				t.Fatalf("Expected %d bytes written, got %d", len(content), written)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed reading back file: %v", err)
			}

			if !bytes.Equal(got, content) {
				t.Fatalf("Copied content does not match the source")
			}
		})
	}
}

// TestUnprivilegedCopyQuota checks that a filesystem quota for the unprivileged user bounds the copy, and
// that exceeding it is reported as http.StatusRequestEntityTooLarge.
func TestUnprivilegedCopyQuota(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	// A tmpfs with a 1MiB default block limit for every user.
	dir := t.TempDir()
	err := unix.Mount("tmpfs", dir, "tmpfs", 0, "size=16M,usrquota,usrquota_block_hardlimit=1M")
	if err != nil {
		t.Skipf("Test requires tmpfs with user quota support: %v", err)
	}

	t.Cleanup(func() { _ = unix.Unmount(dir, unix.MNT_DETACH) })

	tests := []struct {
		name       string
		size       int
		wantStatus int
	}{
		{name: "within quota", size: 512 * 1024},
		{name: "exceeding quota", size: 4 * 1024 * 1024, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp(dir, "upload")
			if err != nil {
				t.Fatalf("Failed creating temporary file: %v", err)
			}

			defer func() {
				_ = f.Close()
				_ = os.Remove(f.Name())
			}()

			written, err := UnprivilegedCopy(t.Context(), f, bytes.NewReader(make([]byte, tt.size)), uid, gid)
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("Failed copying: %v", err)
				}

				if written != int64(tt.size) {
					t.Fatalf("Expected %d bytes written, got %d", tt.size, written)
				}

				return
			}

			if !api.StatusErrorCheck(err, tt.wantStatus) {
				t.Fatalf("Expected an error with status %d, got: %v", tt.wantStatus, err)
			}

			if written >= int64(tt.size) {
				t.Fatalf("Expected the copy to stop at the quota, but %d bytes were written", written)
			}
		})
	}
}
