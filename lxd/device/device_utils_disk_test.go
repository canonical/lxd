package device

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/idmap"
)

func TestDiskVMVirtiofsdResolveIDMaps(t *testing.T) {
	t.Run("Use supplied idmaps", func(t *testing.T) {
		expected := []idmap.IdmapEntry{
			{
				Hostid:   1000,
				Isuid:    true,
				Nsid:     0,
				Maprange: 1,
			},
			{
				Hostid:   1000,
				Isgid:    true,
				Nsid:     0,
				Maprange: 1,
			},
		}

		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return nil, errors.New("should not be called")
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(expected, currentIDMapSetFunc)
		assert.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("Fallback to current idmap set", func(t *testing.T) {
		// The current idmap set maps current-namespace IDs (Nsid) to parent IDs (Hostid), here a
		// non-identity mapping (0 -> 100000). The resolved maps must be an identity map over the
		// current Nsid range (Hostid = Nsid) so nested LXD can start virtiofsd correctly.
		currentIDMapSet := &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
			{
				Hostid:   100000,
				Isuid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
			{
				Hostid:   100000,
				Isgid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
		}}

		expected := []idmap.IdmapEntry{
			{
				Hostid:   0,
				Isuid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
			{
				Hostid:   0,
				Isgid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
		}

		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return currentIDMapSet, nil
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("Fail if getting current idmap set", func(t *testing.T) {
		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return nil, errors.New("boom")
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Failed getting current idmap set: boom")
	})

	t.Run("Fail if current idmap set empty", func(t *testing.T) {
		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return &idmap.IdmapSet{}, nil
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Current idmap set cannot be empty")
	})

	t.Run("Fail if current idmap set has no gid map", func(t *testing.T) {
		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
				{
					Hostid:   100000,
					Isuid:    true,
					Nsid:     0,
					Maprange: 65536,
				},
			}}, nil
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Current idmap set must contain both UID and GID mappings")
	})

	t.Run("Fail if current idmap set function is nil", func(t *testing.T) {
		actual, err := diskVMVirtiofsdResolveIDMaps(nil, nil)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Current idmap set function is nil")
	})
}

func TestDiskAddRootUserNSEntry(t *testing.T) {
	// Check adds a combined uid/gid root entry to an empty list.
	var idmaps []idmap.IdmapEntry
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected := []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, expected, idmaps)

	// Check doesn't add another one if an existing combined entry exists.
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	assert.Equal(t, expected, idmaps)

	// Check adds a root gid entry if root uid entry already exists.
	idmaps = []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, expected, idmaps)

	// Check adds a root uid entry if root gid entry already exists.
	idmaps = []idmap.IdmapEntry{
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.IdmapEntry{
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, expected, idmaps)
}

func TestDiskPropagationFlags(t *testing.T) {
	tests := []struct {
		propagation string
		expected    uintptr
	}{
		{"", unix.MS_REC | unix.MS_SLAVE},
		{"private", unix.MS_PRIVATE},
		{"shared", unix.MS_SHARED},
		{"slave", unix.MS_SLAVE},
		{"unbindable", unix.MS_UNBINDABLE},
		{"rprivate", unix.MS_PRIVATE | unix.MS_REC},
		{"rshared", unix.MS_SHARED | unix.MS_REC},
		{"rslave", unix.MS_SLAVE | unix.MS_REC},
		{"runbindable", unix.MS_UNBINDABLE | unix.MS_REC},
	}

	for _, tt := range tests {
		t.Run("Mode "+tt.propagation, func(t *testing.T) {
			flags, err := diskPropagationFlags(tt.propagation, false)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, flags)

			// A recursive mount applies the propagation to its submounts too, whatever the mode.
			flags, err = diskPropagationFlags(tt.propagation, true)
			require.NoError(t, err)
			assert.Equal(t, tt.expected|unix.MS_REC, flags)
		})
	}

	t.Run("Invalid mode", func(t *testing.T) {
		_, err := diskPropagationFlags("bogus", false)
		assert.ErrorContains(t, err, `Invalid propagation mode "bogus"`)
	})
}

// mountInfo returns the mountinfo line of the mount at path in the calling thread's mount namespace.
func mountInfo(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile("/proc/thread-self/mountinfo")
	require.NoError(t, err)

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == path {
			return line
		}
	}

	t.Fatalf("No mount found at %q in:\n%s", path, data)
	return ""
}

// privateMountNamespace moves the test into its own mount namespace (and keeps it locked to this thread)
// so that the mounts it makes never touch the host. Requires root.
func privateMountNamespace(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("Requires root")
	}

	// The thread is intentionally never unlocked: once it has changed namespace, it must not be reused.
	runtime.LockOSThread()

	err := unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		t.Skipf("Cannot create a mount namespace: %v", err)
	}

	err = unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	require.NoError(t, err)
}

// mountPoint creates a directory to mount on and unmounts whatever is mounted there (and below) at the end.
func mountPoint(t *testing.T) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "dst")
	require.NoError(t, os.Mkdir(dst, 0o700))
	t.Cleanup(func() { _ = unix.Unmount(dst, unix.MNT_DETACH) })

	return dst
}

// sharedSource creates a directory that is a shared mount, as on a systemd host.
func sharedSource(t *testing.T) string {
	t.Helper()

	src := filepath.Join(t.TempDir(), "src")
	require.NoError(t, os.Mkdir(src, 0o700))
	require.NoError(t, unix.Mount(src, src, "", unix.MS_BIND, ""))
	t.Cleanup(func() { _ = unix.Unmount(src, unix.MNT_DETACH) })
	require.NoError(t, unix.Mount("", src, "", unix.MS_SHARED|unix.MS_REC, ""))

	return src
}

// optionalFields returns the propagation fields of the mountinfo line of the mount at path.
func optionalFields(t *testing.T, path string) string {
	t.Helper()

	return strings.SplitN(mountInfo(t, path), " - ", 2)[0]
}

// The subtests below deliberately avoid t.Run: subtests run in new goroutines that may be scheduled on a
// different OS thread than the one placed in the private mount namespace.
func TestDiskMountPropagation(t *testing.T) {
	privateMountNamespace(t)
	src := sharedSource(t)

	tests := []struct {
		propagation string
		fields      []string // mountinfo optional fields that must be present
		absent      []string // ... and that must not be
	}{
		{"", []string{"master:"}, []string{"shared:"}},
		{"private", nil, []string{"shared:", "master:", "unbindable"}},
		{"shared", []string{"shared:"}, []string{"master:"}},
		{"slave", []string{"master:"}, []string{"shared:"}},
		{"unbindable", []string{"unbindable"}, []string{"shared:"}},
		{"rprivate", nil, []string{"shared:", "master:", "unbindable"}},
		{"rshared", []string{"shared:"}, []string{"master:"}},
		{"rslave", []string{"master:"}, []string{"shared:"}},
		{"runbindable", []string{"unbindable"}, []string{"shared:"}},
	}

	for _, tt := range tests {
		dst := mountPoint(t)
		require.NoError(t, DiskMount(src, dst, false, tt.propagation, nil, "none"), "mode %q", tt.propagation)

		optional := optionalFields(t, dst)
		for _, f := range tt.fields {
			assert.Contains(t, optional, f, "mode %q", tt.propagation)
		}

		for _, f := range tt.absent {
			assert.NotContains(t, optional, f, "mode %q", tt.propagation)
		}
	}

	// Read-only is honoured together with propagation.
	dst := mountPoint(t)
	require.NoError(t, DiskMount(src, dst, false, "rshared", []string{"ro"}, "none"))
	assert.Contains(t, strings.Fields(mountInfo(t, dst))[5], "ro")
	assert.Contains(t, optionalFields(t, dst), "shared:")

	// A bind mount without ro stays writable.
	dst = mountPoint(t)
	require.NoError(t, DiskMount(src, dst, false, "rshared", nil, "none"))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "probe"), []byte("x"), 0o600))
	assert.NotContains(t, strings.Fields(mountInfo(t, dst))[5], "ro")

	// A read-only mount that is not a bind mount.
	dst = mountPoint(t)
	require.NoError(t, DiskMount("tmpfs", dst, false, "", []string{"ro"}, "tmpfs"))
	assert.Contains(t, strings.Fields(mountInfo(t, dst))[5], "ro")

	// An invalid propagation mounts nothing.
	dst = mountPoint(t)
	err := DiskMount(src, dst, false, "bogus", nil, "none")
	assert.ErrorContains(t, err, "Invalid propagation mode")

	data, readErr := os.ReadFile("/proc/thread-self/mountinfo")
	require.NoError(t, readErr)
	assert.NotContains(t, string(data), " "+dst+" ")

	// A missing source fails.
	err = DiskMount(filepath.Join(t.TempDir(), "missing"), mountPoint(t), false, "", nil, "none")
	assert.ErrorContains(t, err, "Cannot mount")
}

func TestDiskMountPropagationRecursive(t *testing.T) {
	privateMountNamespace(t)

	// A shared source with a submount, as with recursive=true on a directory that has mounts below it.
	src := sharedSource(t)
	nested := filepath.Join(src, "nested")
	require.NoError(t, os.Mkdir(nested, 0o700))
	require.NoError(t, unix.Mount("tmpfs", nested, "tmpfs", 0, ""))
	t.Cleanup(func() { _ = unix.Unmount(nested, unix.MNT_DETACH) })

	// Propagation modes that are not recursive by name must still apply to the submounts of a recursive mount,
	// so that a submount is never left a peer of the host's when the administrator did not ask for it.
	for _, mode := range []string{"", "private", "slave", "unbindable", "rprivate", "rslave"} {
		dst := mountPoint(t)
		require.NoError(t, DiskMount(src, dst, true, mode, nil, "none"), "mode %q", mode)
		assert.NotContains(t, optionalFields(t, filepath.Join(dst, "nested")), "shared:", "mode %q", mode)
		_ = unix.Unmount(filepath.Join(dst, "nested"), unix.MNT_DETACH)
	}

	dst := mountPoint(t)
	require.NoError(t, DiskMount(src, dst, true, "rshared", nil, "none"))
	assert.Contains(t, optionalFields(t, filepath.Join(dst, "nested")), "shared:")
	_ = unix.Unmount(filepath.Join(dst, "nested"), unix.MNT_DETACH)
}
