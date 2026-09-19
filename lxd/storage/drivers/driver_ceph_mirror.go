package drivers

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/api"
)

// cephMirrorSnapshotNamespace is the namespace rbd reports for the snapshots mirroring creates, which
// separates them from user snapshots in the same listing.
const cephMirrorSnapshotNamespace = "mirror"

// cephMirrorPeerState is the replay state rbd embeds as JSON inside the peer site description
// rather than reporting as fields of its own.
type cephMirrorPeerState struct {
	LocalSnapshotTimestamp  int64  `json:"local_snapshot_timestamp"`
	RemoteSnapshotTimestamp int64  `json:"remote_snapshot_timestamp"`
	ReplayState             string `json:"replay_state"`
}

// cephMirrorPeerSite is one peer site's view of a mirrored image.
type cephMirrorPeerSite struct {
	SiteName    string `json:"site_name"`
	State       string `json:"state"`
	Description string `json:"description"`
}

// cephMirrorImageStatus is `rbd mirror image status --format json`, cut down to what is read.
type cephMirrorImageStatus struct {
	Name      string               `json:"name"`
	State     string               `json:"state"`
	PeerSites []cephMirrorPeerSite `json:"peer_sites"`
}

// cephSnapshotEntry is one entry of an rbd snapshot listing.
type cephSnapshotEntry struct {
	ID        uint64 `json:"id"`
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`
	Namespace struct {
		Type string `json:"type"`
	} `json:"namespace"`
}

// cephParseTimestamp converts the timestamp of an rbd snapshot listing into seconds since the epoch.
// rbd formats it with ctime in the local time zone of the host it runs on. It runs as a child of this
// daemon on the same host, so the daemon's local zone is the one to read it back in. The peer's replay
// status reports whole epoch seconds, which is what the result is compared against.
func cephParseTimestamp(value string) (int64, error) {
	parsed, err := time.ParseInLocation(time.ANSIC, value, time.Local)
	if err != nil {
		return 0, fmt.Errorf("Cannot parse Ceph timestamp %q: %w", value, err)
	}

	return parsed.Unix(), nil
}

// state extracts the replay state rbd embeds as JSON after the readable part of the description.
func (p cephMirrorPeerSite) state() (*cephMirrorPeerState, error) {
	_, encoded, found := strings.Cut(p.Description, ", ")
	if !found {
		return nil, fmt.Errorf("Peer site %q reports no replay state: %q", p.SiteName, p.Description)
	}

	var state cephMirrorPeerState

	err := json.Unmarshal([]byte(encoded), &state)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing the replay state of peer site %q: %w", p.SiteName, err)
	}

	return &state, nil
}

// hasReplayed reports whether the peer site has replayed a mirror snapshot at least as recent as
// snapshotTimestamp. Both timestamps have to pass, so an image whose transfer started but did not
// finish does not count. A peer that is missing or down is an error rather than "not yet", because
// waiting on it cannot succeed.
func (s cephMirrorImageStatus) hasReplayed(siteName string, snapshotTimestamp int64) (bool, error) {
	idx := slices.IndexFunc(s.PeerSites, func(peer cephMirrorPeerSite) bool { return peer.SiteName == siteName })
	if idx < 0 {
		return false, fmt.Errorf("Image %q reports no status for peer site %q, check that the peer is registered and rbd-mirror is running", s.Name, siteName)
	}

	peer := s.PeerSites[idx]
	if !strings.HasPrefix(peer.State, "up+") {
		return false, fmt.Errorf("Peer site %q reports %q for image %q", siteName, peer.State, s.Name)
	}

	state, err := peer.state()
	if err != nil {
		return false, err
	}

	return state.LocalSnapshotTimestamp >= snapshotTimestamp && state.RemoteSnapshotTimestamp >= snapshotTimestamp, nil
}

// cephNewestMirrorSnapshotTimestamp returns the timestamp of the newest mirror snapshot in an rbd
// snapshot listing.
// Ceph prunes mirror snapshots on its own, so the snapshot just triggered is identified by being
// the newest rather than by an identifier carried between the two calls. Landing on a newer
// snapshot only makes the replay check stricter, which is the safe direction to be wrong in.
func cephNewestMirrorSnapshotTimestamp(listing string) (int64, error) {
	var entries []cephSnapshotEntry

	err := json.Unmarshal([]byte(listing), &entries)
	if err != nil {
		return 0, fmt.Errorf("Failed parsing the snapshot listing: %w", err)
	}

	entries = slices.DeleteFunc(entries, func(entry cephSnapshotEntry) bool {
		return entry.Namespace.Type != cephMirrorSnapshotNamespace
	})

	if len(entries) == 0 {
		return 0, api.StatusErrorf(http.StatusNotFound, "Snapshot listing holds no mirror snapshot")
	}

	newest := slices.MaxFunc(entries, func(a cephSnapshotEntry, b cephSnapshotEntry) int {
		return cmp.Compare(a.ID, b.ID)
	})

	return cephParseTimestamp(newest.Timestamp)
}

// EnableVolumeMirroring enrolls a volume's RBD image into snapshot based mirroring.
// The command runs unbounded on purpose, like the driver's other mutating rbd calls: cutting it short
// could leave the image halfway.
func (d *ceph) EnableVolumeMirroring(vol Volume) error {
	_, err := d.rbd(context.Background(), "mirror", "image", "enable", "--image", d.getRBDVolumeName(vol, "", false, false), "snapshot")
	if err != nil {
		return fmt.Errorf("Failed enabling mirroring for volume %q: %w", vol.name, err)
	}

	// For VMs, also enroll the filesystem volume, or the peer holds half a VM.
	if vol.IsVMBlock() {
		return d.EnableVolumeMirroring(vol.NewVMBlockFilesystemVolume())
	}

	return nil
}

// CreateVolumeMirrorSnapshot triggers a mirror snapshot of a volume's RBD image.
// Unbounded on purpose, for the same reason as the enable.
func (d *ceph) CreateVolumeMirrorSnapshot(vol Volume) error {
	_, err := d.rbd(context.Background(), "mirror", "image", "snapshot", "--image", d.getRBDVolumeName(vol, "", false, false))
	if err != nil {
		return fmt.Errorf("Failed creating a mirror snapshot of volume %q: %w", vol.name, err)
	}

	if vol.IsVMBlock() {
		return d.CreateVolumeMirrorSnapshot(vol.NewVMBlockFilesystemVolume())
	}

	return nil
}

// VolumeMirrorReplayed reports whether the peer site has replayed the newest mirror snapshot of a
// volume's RBD image. The newest snapshot is the one this run triggered or a later one, and a later
// one only makes the check stricter.
func (d *ceph) VolumeMirrorReplayed(vol Volume, peerSite string) (bool, error) {
	imageName := d.getRBDVolumeName(vol, "", false, false)

	// Both queries are read-only, so they get the bound the driver's other read-only rbd calls have.
	// The enable and the snapshot trigger stay unbounded, since cutting a mutating command short can
	// leave the image halfway.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listing, err := d.rbd(ctx, "--format", "json", "snap", "ls", "--all", imageName)
	if err != nil {
		return false, fmt.Errorf("Failed listing the snapshots of volume %q: %w", vol.name, err)
	}

	snapshotTimestamp, err := cephNewestMirrorSnapshotTimestamp(listing)
	if err != nil {
		return false, fmt.Errorf("Failed identifying the mirror snapshot of volume %q: %w", vol.name, err)
	}

	msg, err := d.rbd(ctx, "--format", "json", "mirror", "image", "status", imageName)
	if err != nil {
		return false, fmt.Errorf("Failed getting the mirror status of volume %q: %w", vol.name, err)
	}

	var status cephMirrorImageStatus

	err = json.Unmarshal([]byte(msg), &status)
	if err != nil {
		return false, fmt.Errorf("Failed parsing the mirror status of volume %q: %w", vol.name, err)
	}

	replayed, err := status.hasReplayed(peerSite, snapshotTimestamp)
	if err != nil || !replayed {
		return false, err
	}

	if vol.IsVMBlock() {
		return d.VolumeMirrorReplayed(vol.NewVMBlockFilesystemVolume(), peerSite)
	}

	return true, nil
}
