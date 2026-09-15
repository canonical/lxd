package drivers

import (
	"encoding/json"
	"testing"
	"time"
)

// cephMirrorImageStatusJSON is the output of `rbd mirror image status <image> --format json` for a
// primary image whose peer has replayed its newest mirror snapshot, cut down to the fields that are
// read.
const cephMirrorImageStatusJSON = `{
  "name": "container_dr_web01",
  "global_id": "8b1f0d0e-6f0f-4e5f-9c6d-1f3f7a2b4c5d",
  "state": "up+stopped",
  "description": "local image is primary",
  "peer_sites": [
    {
      "site_name": "site-b",
      "state": "up+replaying",
      "description": "replaying, {\"bytes_per_second\":0.0,\"local_snapshot_timestamp\":1786366837,\"remote_snapshot_timestamp\":1786366837,\"replay_state\":\"idle\"}",
      "last_update": "2026-08-20 07:20:44"
    }
  ],
  "last_update": "2026-08-20 07:20:44"
}`

func Test_ceph_cephParseTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int64
		wantErr bool
	}{
		// rbd formats the timestamp with ctime, in the local time zone.
		{"Timestamp as rbd emits it", "Tue Sep 15 09:38:33 2026", time.Date(2026, time.September, 15, 9, 38, 33, 0, time.Local).Unix(), false},
		{"Timestamp of a single digit day, which ctime pads with a space", "Sat Sep  5 09:38:33 2026", time.Date(2026, time.September, 5, 9, 38, 33, 0, time.Local).Unix(), false},
		{"Timestamp in a format rbd does not emit", "2026-09-15T09:38:33.000000+0000", 0, true},
		{"Empty timestamp, which rbd emits for a snapshot without one", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cephParseTimestamp(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Unexpected error state for %q: %v", tt.value, err)
			}

			if got != tt.want {
				t.Errorf("Parsed the wrong timestamp: %d != %d", got, tt.want)
			}
		})
	}
}

func Test_ceph_cephNewestMirrorSnapshotTimestamp(t *testing.T) {
	// A listing holding a user snapshot and two mirror snapshots, the newest of which is not last.
	listing := `[
	  {"id": 8, "name": "snapshot_daily", "size": 10737418240, "protected": "false", "timestamp": "Thu Aug 20 07:10:00 2026", "namespace": {"type": "user"}},
	  {"id": 12, "name": ".mirror.primary.8b1f0d0e.2f", "size": 10737418240, "protected": "false", "timestamp": "Thu Aug 20 07:20:37 2026", "namespace": {"type": "mirror", "state": "primary", "complete": true}},
	  {"id": 9, "name": ".mirror.primary.8b1f0d0e.1a", "size": 10737418240, "protected": "false", "timestamp": "Thu Aug 20 07:15:00 2026", "namespace": {"type": "mirror", "state": "primary", "complete": true}}
	]`

	got, err := cephNewestMirrorSnapshotTimestamp(listing)
	if err != nil {
		t.Fatalf("Failed reading the newest mirror snapshot: %v", err)
	}

	want := time.Date(2026, time.August, 20, 7, 20, 37, 0, time.Local).Unix()
	if got != want {
		t.Errorf("Picked the wrong mirror snapshot: %d != %d", got, want)
	}

	// A volume that has never been mirrored must not silently report a timestamp.
	_, err = cephNewestMirrorSnapshotTimestamp(`[{"id": 8, "name": "snapshot_daily", "timestamp": "Thu Aug 20 07:10:00 2026", "namespace": {"type": "user"}}]`)
	if err == nil {
		t.Error("A listing without a mirror snapshot was accepted")
	}
}

func Test_ceph_cephMirrorImageStatus_hasReplayed(t *testing.T) {
	var replayed cephMirrorImageStatus

	err := json.Unmarshal([]byte(cephMirrorImageStatusJSON), &replayed)
	if err != nil {
		t.Fatalf("Failed parsing the image mirror status: %v", err)
	}

	if replayed.Name != "container_dr_web01" || len(replayed.PeerSites) != 1 {
		t.Fatalf("Parsed the image mirror status incorrectly: %+v", replayed)
	}

	// The peer is registered but no rbd-mirror daemon has picked the image up.
	peerDown := cephMirrorImageStatus{
		Name:      "custom_dr_backups.block",
		State:     "up+stopped",
		PeerSites: []cephMirrorPeerSite{{SiteName: "site-b", State: "down+unknown", Description: "status not found"}},
	}

	// The peer is replaying but has not seen a mirror snapshot on the remote yet, so it reports no
	// replay state.
	noState := cephMirrorImageStatus{
		Name:      "custom_dr_backups.block",
		State:     "up+stopped",
		PeerSites: []cephMirrorPeerSite{{SiteName: "site-b", State: "up+replaying", Description: "replaying"}},
	}

	// The peer is still bootstrapping the image.
	bootstrapping := cephMirrorImageStatus{
		Name:      "custom_dr_backups.block",
		State:     "up+stopped",
		PeerSites: []cephMirrorPeerSite{{SiteName: "site-b", State: "up+starting_replay", Description: "starting replay"}},
	}

	// The peer hit an error it reports without a replay state, which waiting does not clear.
	splitBrain := cephMirrorImageStatus{
		Name:      "custom_dr_backups.block",
		State:     "up+stopped",
		PeerSites: []cephMirrorPeerSite{{SiteName: "site-b", State: "up+error", Description: "split-brain"}},
	}

	tests := []struct {
		name              string
		image             cephMirrorImageStatus
		siteName          string
		snapshotTimestamp int64
		want              bool
		wantErr           bool
	}{
		{"Replayed snapshot", replayed, "site-b", 1786366837, true, false},
		// The peer reports equal timestamps with an idle replay state while still describing the
		// previous run's snapshot, which is the case the comparison exists to catch.
		{"Snapshot of a later run", replayed, "site-b", 1786366900, false, false},
		{"Unconfigured peer site", replayed, "site-c", 1786366837, false, true},
		{"Peer site that is down", peerDown, "site-b", 1786366837, false, true},
		{"Peer site not reporting a replay state yet", noState, "site-b", 1786366837, false, false},
		{"Peer site bootstrapping the image", bootstrapping, "site-b", 1786366837, false, false},
		{"Peer site in error without a replay state", splitBrain, "site-b", 1786366837, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.image.hasReplayed(tt.siteName, tt.snapshotTimestamp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Unexpected error state: %v", err)
			}

			if got != tt.want {
				t.Errorf("Reached the wrong replay verdict: %t != %t", got, tt.want)
			}
		})
	}
}
