package drivers

import (
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
