package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_btrfs_validateSubVolumeHeader asserts that subvolume paths from an optimized backup or
// migration header which escape the volume mount point are rejected.
func Test_btrfs_validateSubVolumeHeader(t *testing.T) {
	driver := &btrfs{}

	tests := []struct {
		name      string
		paths     []string
		wantError string
	}{
		{
			name:  "Volume top only",
			paths: []string{"/"},
		},
		{
			name:  "Nested subvolumes within the volume",
			paths: []string{"/", "/foo", "/foo/bar"},
		},
		{
			name:      "Relative traversal escapes the mount point",
			paths:     []string{"/", "../../../../etc/cron.d"},
			wantError: `Subvolume path "../../../../etc/cron.d" must be within the volume`,
		},
		{
			name:      "Rooted traversal escapes a nested mount point",
			paths:     []string{"/foo/../../../etc"},
			wantError: `Subvolume path "/foo/../../../etc" must be within the volume`,
		},
		{
			name:      "Empty path is rejected",
			paths:     []string{""},
			wantError: `Subvolume path "" must be within the volume`,
		},
		{
			name:      "One crafted entry among valid ones is rejected",
			paths:     []string{"/", "/foo", "../etc/cron.d"},
			wantError: `Subvolume path "../etc/cron.d" must be within the volume`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := BTRFSMetaDataHeader{}
			for _, path := range test.paths {
				header.Subvolumes = append(header.Subvolumes, BTRFSSubVolume{Path: path})
			}

			err := driver.validateSubVolumeHeader(header, nil)
			if test.wantError != "" {
				require.EqualError(t, err, test.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Test_btrfs_validateSubVolumeHeader_snapshots asserts that invalid snapshot names provided by
// the client in a header are rejected.
func Test_btrfs_validateSubVolumeHeader_snapshots(t *testing.T) {
	driver := &btrfs{}

	tests := []struct {
		name              string
		snapshot          string
		expectedSnapshots []string
		wantError         string
	}{
		{
			name:              "Snapshot within the expected set",
			snapshot:          "snap0",
			expectedSnapshots: []string{"snap0", "snap1"},
		},
		{
			name:              "Traversal in the snapshot name is rejected",
			snapshot:          "../../containers/victim",
			expectedSnapshots: []string{"../../containers/victim"},
			wantError:         `Invalid subvolume snapshot name "../../containers/victim": Invalid instance snapshot name "../../containers/victim": Cannot contain *, spaces, forward or back slashes`,
		},
		{
			name:              "Valid snapshot outside the expected set is rejected",
			snapshot:          "rogue",
			expectedSnapshots: []string{"snap0"},
			wantError:         `Subvolume snapshot "rogue" does not belong to the volume`,
		},
		{
			name:      "Nil expected set skips containment but keeps name validation",
			snapshot:  "snap0",
			wantError: "",
		},
		{
			name:      "Dot as the snapshot name is rejected",
			snapshot:  ".",
			wantError: `Invalid subvolume snapshot name "."`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := BTRFSMetaDataHeader{Subvolumes: []BTRFSSubVolume{
				{Path: string(filepath.Separator), Snapshot: ""},
				{Path: string(filepath.Separator), Snapshot: test.snapshot},
			}}

			err := driver.validateSubVolumeHeader(header, test.expectedSnapshots)
			if test.wantError != "" {
				require.EqualError(t, err, test.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Test_btrfs_resolveSubvolumeDest asserts that a symlink in the restored content cannot redirect a
// subvolume to a destination outside the volume.
func Test_btrfs_resolveSubvolumeDest(t *testing.T) {
	driver := &btrfs{}

	t.Run("Nested destination within the volume", func(t *testing.T) {
		base := t.TempDir()
		volRoot := filepath.Join(base, "vol")
		require.NoError(t, os.Mkdir(volRoot, 0700))
		require.NoError(t, os.Mkdir(filepath.Join(volRoot, "sub"), 0700))

		src := filepath.Join(base, "src")
		require.NoError(t, os.Mkdir(src, 0700))

		dest, closer, err := driver.resolveSubvolumeDest(volRoot, "/sub/target")
		require.NoError(t, err)
		defer closer()

		require.NoError(t, os.Rename(src, dest))
		require.DirExists(t, filepath.Join(volRoot, "sub", "target"))
	})

	t.Run("Symlink escaping the volume is rejected", func(t *testing.T) {
		base := t.TempDir()
		volRoot := filepath.Join(base, "vol")
		require.NoError(t, os.Mkdir(volRoot, 0700))

		outside := filepath.Join(base, "outside")
		require.NoError(t, os.Mkdir(outside, 0700))

		require.NoError(t, os.Symlink("../outside", filepath.Join(volRoot, "evil")))

		_, _, err := driver.resolveSubvolumeDest(volRoot, "/evil/planted")
		require.Error(t, err)
		require.NoDirExists(t, filepath.Join(outside, "planted"))
	})

	t.Run("In-bounds symlink is rejected", func(t *testing.T) {
		base := t.TempDir()
		volRoot := filepath.Join(base, "vol")
		require.NoError(t, os.Mkdir(volRoot, 0700))
		require.NoError(t, os.Mkdir(filepath.Join(volRoot, "real"), 0700))
		require.NoError(t, os.Symlink("real", filepath.Join(volRoot, "link")))

		_, _, err := driver.resolveSubvolumeDest(volRoot, "/link/target")
		require.Error(t, err)
	})

	t.Run("Volume top destination", func(t *testing.T) {
		base := t.TempDir()
		volRoot := filepath.Join(base, "vol")
		require.NoError(t, os.Mkdir(volRoot, 0700))

		dest, closer, err := driver.resolveSubvolumeDest(volRoot, "/")
		require.NoError(t, err)
		defer closer()

		require.Equal(t, volRoot, dest)
	})
}
