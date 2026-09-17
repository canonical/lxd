package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_btrfs_selectSubvolumesToSync asserts that a migration refresh target receives only the
// subvolumes it still needs, and rejects a snapshot that is neither negotiated nor on the target.
func Test_btrfs_selectSubvolumesToSync(t *testing.T) {
	driver := &btrfs{}

	tests := []struct {
		name                string
		subvolumes          []BTRFSSubVolume
		localSubvolumes     map[string]string
		negotiatedSnapshots []string
		wantSnapshots       []string
		wantSubvolumes      []BTRFSSubVolume
		wantError           string
	}{
		{
			name:            "Main volume is always synced",
			subvolumes:      []BTRFSSubVolume{{Snapshot: "", Path: "/", UUID: "uuid-root"}},
			localSubvolumes: map[string]string{},
			wantSnapshots:   []string{},
			wantSubvolumes:  []BTRFSSubVolume{{Snapshot: "", Path: "/", UUID: "uuid-root"}},
		},
		{
			name:            "Snapshot with a matching received UUID is skipped",
			subvolumes:      []BTRFSSubVolume{{Snapshot: "snap0", Path: "/", UUID: "uuid-snap0"}},
			localSubvolumes: map[string]string{"snap0": "uuid-snap0"},
			wantSnapshots:   []string{},
		},
		{
			name: "Snapshot missing on the target is synced with its nested subvolumes",
			subvolumes: []BTRFSSubVolume{
				{Snapshot: "snap1", Path: "/", UUID: "uuid-snap1"},
				{Snapshot: "snap1", Path: "/foo", UUID: "uuid-snap1-foo"},
			},
			localSubvolumes:     map[string]string{},
			negotiatedSnapshots: []string{"snap1"},
			wantSnapshots:       []string{"snap1"},
			wantSubvolumes: []BTRFSSubVolume{
				{Snapshot: "snap1", Path: "/", UUID: "uuid-snap1"},
				{Snapshot: "snap1", Path: "/foo", UUID: "uuid-snap1-foo"},
			},
		},
		{
			name:            "Snapshot on the target with a different received UUID is synced",
			subvolumes:      []BTRFSSubVolume{{Snapshot: "snap0", Path: "/", UUID: "uuid-snap0"}},
			localSubvolumes: map[string]string{"snap0": ""},
			wantSnapshots:   []string{"snap0"},
			wantSubvolumes:  []BTRFSSubVolume{{Snapshot: "snap0", Path: "/", UUID: "uuid-snap0"}},
		},
		{
			name:                "Snapshot missing on the target and not negotiated is rejected",
			subvolumes:          []BTRFSSubVolume{{Snapshot: "rogue", Path: "/", UUID: "uuid-rogue"}},
			localSubvolumes:     map[string]string{},
			negotiatedSnapshots: []string{"snap1"},
			wantError:           `Subvolume snapshot "rogue" was not negotiated for this migration`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshots, syncSubvolumes, err := driver.selectSubvolumesToSync(test.subvolumes, test.localSubvolumes, test.negotiatedSnapshots)
			if test.wantError != "" {
				require.EqualError(t, err, test.wantError)
				return
			}

			require.NoError(t, err)
			require.Equal(t, test.wantSnapshots, snapshots)
			require.Equal(t, test.wantSubvolumes, syncSubvolumes)
		})
	}
}

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

// Test_btrfs_validateReturnedSubvolumes asserts that a migration refresh reply may only include
// subvolumes the source offered in the first place.
func Test_btrfs_validateReturnedSubvolumes(t *testing.T) {
	driver := &btrfs{}

	sent := []BTRFSSubVolume{
		{Snapshot: "", Path: "/", UUID: "uuid-root", Readonly: false},
		{Snapshot: "", Path: "/foo", UUID: "uuid-foo", Readonly: true},
		{Snapshot: "snap0", Path: "/", UUID: "uuid-snap0", Readonly: true},
	}

	tests := []struct {
		name      string
		returned  []BTRFSSubVolume
		wantError string
	}{
		{
			name:     "Empty reply",
			returned: nil,
		},
		{
			name: "Subset of offered entries, readonly ignored",
			returned: []BTRFSSubVolume{
				{Snapshot: "", Path: "/", UUID: "uuid-root"},
				{Snapshot: "snap0", Path: "/", UUID: "uuid-snap0"},
			},
		},
		{
			name: "Lexically local path the source never offered is rejected",
			returned: []BTRFSSubVolume{
				{Snapshot: "", Path: "/evil", UUID: "uuid-root"},
			},
			wantError: `Returned subvolume path "/evil" (snapshot "") was not offered by the source`,
		},
		{
			name: "Offered path with a different UUID is rejected",
			returned: []BTRFSSubVolume{
				{Snapshot: "", Path: "/foo", UUID: "uuid-forged"},
			},
			wantError: `Returned subvolume path "/foo" (snapshot "") was not offered by the source`,
		},
		{
			name: "Offered path under a different snapshot is rejected",
			returned: []BTRFSSubVolume{
				{Snapshot: "snap0", Path: "/foo", UUID: "uuid-foo"},
			},
			wantError: `Returned subvolume path "/foo" (snapshot "snap0") was not offered by the source`,
		},
		{
			name: "One crafted entry among valid ones is rejected",
			returned: []BTRFSSubVolume{
				{Snapshot: "", Path: "/", UUID: "uuid-root"},
				{Snapshot: "", Path: "/foo", UUID: "uuid-foo"},
				{Snapshot: "", Path: "/foo/../../etc", UUID: "uuid-foo"},
			},
			wantError: `Returned subvolume path "/foo/../../etc" (snapshot "") was not offered by the source`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := driver.validateReturnedSubvolumes(sent, test.returned)
			if test.wantError != "" {
				require.EqualError(t, err, test.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Test_btrfs_resolveSubvolumeDest asserts that the destination resolves within the volume,
// and that a symlink cannot redirect it outside the volume.
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

	t.Run("In-bounds symlink is followed but stays within the volume", func(t *testing.T) {
		base := t.TempDir()
		volRoot := filepath.Join(base, "vol")
		require.NoError(t, os.Mkdir(volRoot, 0700))
		require.NoError(t, os.Mkdir(filepath.Join(volRoot, "real"), 0700))
		require.NoError(t, os.Symlink("real", filepath.Join(volRoot, "link")))

		src := filepath.Join(base, "src")
		require.NoError(t, os.Mkdir(src, 0700))

		dest, closer, err := driver.resolveSubvolumeDest(volRoot, "/link/target")
		require.NoError(t, err)
		defer closer()

		require.NoError(t, os.Rename(src, dest))
		require.DirExists(t, filepath.Join(volRoot, "real", "target"))
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
