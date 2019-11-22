package instance

import (
	"time"

	"github.com/lxc/lxd/shared"
)

// CompareSnapshots returns a list of snapshots to sync to the target and a list of
// snapshots to remove from the target. A snapshot will be marked as "to sync" if it either doesn't
// exist in the target or its creation date is different to the source. A snapshot will be marked
// as "to delete" if it doesn't exist in the source or creation date is different to the source.
func CompareSnapshots(source Instance, target Instance) ([]Instance, []Instance, error) {
	// Get the source snapshots.
	sourceSnapshots, err := source.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Get the target snapshots.
	targetSnapshots, err := target.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	// Compare source and target.
	sourceSnapshotsTime := map[string]time.Time{}
	targetSnapshotsTime := map[string]time.Time{}

	toDelete := []Instance{}
	toSync := []Instance{}

	// Generate a list of source snapshot creation dates.
	for _, snap := range sourceSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		sourceSnapshotsTime[snapName] = snap.CreationDate()
	}

	// Generate a list of target snapshot creation times, if the source doesn't contain the
	// the snapshot or the creation time is different on the source then add the target snapshot
	// to the "to delete" list.
	for _, snap := range targetSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		targetSnapshotsTime[snapName] = snap.CreationDate()
		existDate, exists := sourceSnapshotsTime[snapName]
		if !exists {
			// Snapshot doesn't exist in source, mark it for deletion on target.
			toDelete = append(toDelete, snap)
		} else if existDate != snap.CreationDate() {
			// Snapshot creation date is different in source, mark it for deletion on
			// target.
			toDelete = append(toDelete, snap)
		}
	}

	// For each of the source snapshots, decide whether it needs to be synced or not based on
	// whether it already exists in the target and whether the creation dates match.
	for _, snap := range sourceSnapshots {
		_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())

		existDate, exists := targetSnapshotsTime[snapName]
		if !exists || existDate != snap.CreationDate() {
			toSync = append(toSync, snap)
		}
	}

	return toSync, toDelete, nil
}
