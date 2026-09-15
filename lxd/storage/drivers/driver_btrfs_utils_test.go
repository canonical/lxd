package drivers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_btrfs_selectSubvolumesToSync asserts that a migration refresh target receives only the
// subvolumes it still needs.
func Test_btrfs_selectSubvolumesToSync(t *testing.T) {
	driver := &btrfs{}

	tests := []struct {
		name            string
		subvolumes      []BTRFSSubVolume
		localSubvolumes map[string]string
		wantSnapshots   []string
		wantSubvolumes  []BTRFSSubVolume
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
			localSubvolumes: map[string]string{},
			wantSnapshots:   []string{"snap1"},
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshots, syncSubvolumes := driver.selectSubvolumesToSync(test.subvolumes, test.localSubvolumes)
			require.Equal(t, test.wantSnapshots, snapshots)
			require.Equal(t, test.wantSubvolumes, syncSubvolumes)
		})
	}
}
