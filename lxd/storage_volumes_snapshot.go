package main

import (
	"fmt"
	"net/http"
)

var storagePoolVolumeSnapshotsTypeCmd = Command{
	name: "storage-pools/{pool}/volumes/{type}/{name}/snapshots",
	post: storagePoolVolumeSnapshotsTypePost,
	get:  storagePoolVolumeSnapshotsTypeGet,
}

var storagePoolVolumeSnapshotTypeCmd = Command{
	name:   "storage-pools/{pool}/volumes/{type}/{name}/snapshots/{snapshotName}",
	post:   storagePoolVolumeSnapshotTypePost,
	get:    storagePoolVolumeSnapshotTypeGet,
	delete: storagePoolVolumeSnapshotTypeDelete,
}

func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Creating storage pool volume snapshots is not implemented"))
}

func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Retrieving storage pool volume snapshots is not implemented"))
}

func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Updating storage pool volume snapshots is not implemented"))
}

func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Retrieving a storage pool volume snapshot is not implemented"))
}

func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Deleting storage pool volume snapshots is not implemented"))
}
