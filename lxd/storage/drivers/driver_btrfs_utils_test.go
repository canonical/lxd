package drivers

import (
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
