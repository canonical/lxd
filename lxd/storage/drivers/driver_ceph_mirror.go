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

// cephMirrorPendingStates are the peer states rbd-mirror passes through on its own before it reports a
// replay state: bootstrapping the image, and replaying before it has seen a mirror snapshot on the
// remote. A peer in one of them has not replayed yet, while any other state without a replay state is
// stuck.
//
// The Ceph documentation shows the status command without listing the states it can report:
// https://docs.ceph.com/en/latest/rbd/rbd-mirroring/#mirror-status
// The names follow the rbd_mirror_image_status_state_t enum of librbd's public header, prefixed with
// whether the peer's daemon is up. Ceph makes no written promise about them, but that enum is public
// API and cannot change without breaking its users:
// https://github.com/ceph/ceph/blob/main/src/include/rbd/librbd.h
var cephMirrorPendingStates = []string{"up+starting_replay", "up+syncing", "up+replaying"}

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
	Name        string               `json:"name"`
	State       string               `json:"state"`
	Description string               `json:"description"`
	PeerSites   []cephMirrorPeerSite `json:"peer_sites"`
}

// cephMirrorSplitBrain is how rbd-mirror describes an image it stopped on because the image has a
// history the primary does not share.
const cephMirrorSplitBrain = "split-brain"

// inSplitBrain reports whether rbd-mirror stopped replaying onto the local image because of a
// split-brain. Only the local description counts. A peer site reporting one describes the peer's
// copy, and the local copy is then the one to keep.
func (s cephMirrorImageStatus) inSplitBrain() bool {
	return strings.Contains(s.Description, cephMirrorSplitBrain)
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

// state extracts the replay state rbd embeds as JSON after the "replaying, " lead of the description,
// or nil when the description carries none. Other leads are followed by plain text, such as the
// bootstrap progress of a peer that is still copying the image over.
func (p cephMirrorPeerSite) state() (*cephMirrorPeerState, error) {
	encoded, found := strings.CutPrefix(p.Description, "replaying, ")
	if !found {
		return nil, nil
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
// waiting on it cannot succeed. One that is still bootstrapping the image, or replaying before it has
// seen a mirror snapshot, has not reported a replay state yet and counts as "not yet". So does a
// peer that has not picked the image up yet.
func (s cephMirrorImageStatus) hasReplayed(siteName string, snapshotTimestamp int64) (bool, error) {
	idx := slices.IndexFunc(s.PeerSites, func(peer cephMirrorPeerSite) bool { return peer.SiteName == siteName })
	if idx < 0 {
		return false, fmt.Errorf("Image %q reports no status for peer site %q, check that the peer is registered and rbd-mirror is running", s.Name, siteName)
	}

	peer := s.PeerSites[idx]

	// A peer that has never reported the image shows down+unknown until its daemon picks the image
	// up, which happens on the daemon's own schedule after the enrollment. A daemon that died keeps
	// the state it last reported, so every other down state is one that waiting cannot clear.
	if peer.State == "down+unknown" {
		return false, nil
	}

	if !strings.HasPrefix(peer.State, "up+") {
		return false, fmt.Errorf("Peer site %q reports %q for image %q", siteName, peer.State, s.Name)
	}

	state, err := peer.state()
	if err != nil {
		return false, err
	}

	if state == nil {
		if slices.Contains(cephMirrorPendingStates, peer.State) {
			return false, nil
		}

		// rbd-mirror stops on an image whose peer copy has a history it does not share, which is
		// what a forced promotion leaves on the site that comes back. A second demotion on that
		// site rebuilds the image, so the message says so.
		if strings.Contains(peer.Description, cephMirrorSplitBrain) {
			return false, fmt.Errorf("Peer site %q reports a split-brain on image %q, demote the project there again to rebuild its volumes from this site", siteName, s.Name)
		}

		return false, fmt.Errorf("Peer site %q reports no replay state for image %q: %q", siteName, s.Name, peer.Description)
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

// mirrorImageStatus returns the mirror status rbd reports for one of a volume's RBD images.
func (d *ceph) mirrorImageStatus(ctx context.Context, vol Volume, imageName string) (*cephMirrorImageStatus, error) {
	msg, err := d.rbd(ctx, "--format", "json", "mirror", "image", "status", imageName)
	if err != nil {
		return nil, fmt.Errorf("Failed getting the mirror status of volume %q: %w", vol.name, err)
	}

	var status cephMirrorImageStatus

	err = json.Unmarshal([]byte(msg), &status)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing the mirror status of volume %q: %w", vol.name, err)
	}

	return &status, nil
}

// VolumeMirrorReplayed reports whether the peer site has replayed the newest mirror snapshot of a
// volume's RBD image. The newest snapshot is the one this run triggered or a later one, and a later
// one only makes the check stricter.
func (d *ceph) VolumeMirrorReplayed(ctx context.Context, vol Volume, peerSite string) (bool, error) {
	imageName := d.getRBDVolumeName(vol, "", false, false)

	// Both queries are read-only, so they get the bound the driver's other read-only rbd calls have.
	// The enable and the snapshot trigger stay unbounded, since cutting a mutating command short can
	// leave the image halfway. A caller that brings its own deadline keeps it, and the bound only
	// applies when it brings none. Either way a caller that gives up stops the query at once.
	queryCtx := ctx

	_, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		var cancel context.CancelFunc

		queryCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	listing, err := d.rbd(queryCtx, "--format", "json", "snap", "ls", "--all", imageName)
	if err != nil {
		return false, fmt.Errorf("Failed listing the snapshots of volume %q: %w", vol.name, err)
	}

	snapshotTimestamp, err := cephNewestMirrorSnapshotTimestamp(listing)
	if err != nil {
		return false, fmt.Errorf("Failed identifying the mirror snapshot of volume %q: %w", vol.name, err)
	}

	status, err := d.mirrorImageStatus(queryCtx, vol, imageName)
	if err != nil {
		return false, err
	}

	replayed, err := status.hasReplayed(peerSite, snapshotTimestamp)
	if err != nil || !replayed {
		return false, err
	}

	if vol.IsVMBlock() {
		return d.VolumeMirrorReplayed(ctx, vol.NewVMBlockFilesystemVolume(), peerSite)
	}

	return true, nil
}
