package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// TestSnapshotBitmapFile checks that a snapshot bitmap file is read back as written when the
// instance snapshot UUID it records is asked for, that no file is returned for another UUID, for
// an empty UUID or from a directory without one, and that a snapshot UUID that is not a UUID or a
// symlink that resolves outside the directory is rejected.
func TestSnapshotBitmapFile(t *testing.T) {
	t.Setenv("LXD_DIR", t.TempDir())

	d := &qemu{
		dbType:  instancetype.VM,
		name:    "v1",
		project: api.Project{Name: api.ProjectDefaultName},
	}

	file := &snapshotBitmapFile{
		Snapshot: snapshotBitmapFileSnapshot{UUID: "7d3c9a1e-4b2f-4e8a-9c1d-2a6f8b3e5d70"},
		Volumes: map[string]snapshotBitmapFileVolume{
			"root": {
				UUID: "891bd2a3-7d4e-4c5a-9b1f-0e2d3c4b5a6f",
				Bitmaps: []snapshotBitmapFileBitmap{
					{Name: "snap1", UUID: "2f0a6b77-1c3d-4e5f-8a9b-0c1d2e3f4a5b", Granularity: 65536},
					{Name: "snap2", UUID: "5e114c02-9d8e-4f7a-b6c5-d4e3f2a1b0c9", Granularity: 65536},
				},
			},
			"data": {
				UUID:    "c4d2e8f0-3a5b-4c7d-9e1f-2a3b4c5d6e7f",
				Bitmaps: []snapshotBitmapFileBitmap{{Name: "snap2", UUID: "5e114c02-9d8e-4f7a-b6c5-d4e3f2a1b0c9", Granularity: 65536}},
			},
		},
	}

	read, err := d.readSnapshotBitmapFile(file.Snapshot.UUID)
	require.NoError(t, err)
	require.Nil(t, read, "A missing directory holds no file")

	bitmapsDir := filepath.Join(d.Path(), qemuBitmapsDir)
	require.NoError(t, os.MkdirAll(bitmapsDir, 0700))
	require.NoError(t, d.writeSnapshotBitmapFile(file))
	require.True(t, shared.PathExists(filepath.Join(bitmapsDir, "snapshot.7d3c9a1e-4b2f-4e8a-9c1d-2a6f8b3e5d70.yaml")), "The file is named after the instance snapshot UUID")

	read, err = d.readSnapshotBitmapFile(file.Snapshot.UUID)
	require.NoError(t, err)
	require.Equal(t, file, read)

	read, err = d.readSnapshotBitmapFile("00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	require.Nil(t, read, "A file recording another snapshot is not returned")

	read, err = d.readSnapshotBitmapFile("")
	require.NoError(t, err)
	require.Nil(t, read, "A snapshot without a UUID has no file")

	file.Snapshot.UUID = ""
	require.Error(t, d.writeSnapshotBitmapFile(file), "No file is written without a UUID")

	// Without the check the file is bitmaps/snapshot.x/../../escaped.yaml, which is escaped.yaml
	// in the instance directory.
	file.Snapshot.UUID = "x/../../escaped"
	require.Error(t, d.writeSnapshotBitmapFile(file))
	require.False(t, shared.PathExists(filepath.Join(d.Path(), "escaped.yaml")), "No file is written outside the directory")
	_, err = d.readSnapshotBitmapFile(file.Snapshot.UUID)
	require.Error(t, err, "No file is read from outside the directory")

	outsidePath := filepath.Join(d.Path(), "escaped.yaml")
	file.Snapshot.UUID = "e1a4c7d2-6b3f-4a8e-9d5c-3f2b1a0e9c8d"
	require.NoError(t, os.Symlink(outsidePath, filepath.Join(bitmapsDir, "snapshot.e1a4c7d2-6b3f-4a8e-9d5c-3f2b1a0e9c8d.yaml")))
	require.Error(t, d.writeSnapshotBitmapFile(file))
	require.False(t, shared.PathExists(outsidePath), "No file is written through a symlink outside the directory")

	data, err := yaml.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(outsidePath, data, 0600))
	_, err = d.readSnapshotBitmapFile(file.Snapshot.UUID)
	require.Error(t, err, "No file is read through a symlink outside the directory")
}

// TestMetadataImageNames checks that a volume UUID that is not a UUID is rejected rather than
// joined into the name of a volume metadata image or an overlay.
func TestMetadataImageNames(t *testing.T) {
	_, err := volumeMetadataImageName("../../escaped")
	require.Error(t, err)

	_, err = overlayFileName("../../escaped")
	require.Error(t, err)
}

// TestPruneMetadataImagesSymlink checks that a bitmaps directory that is a symlink to a directory
// outside the instance path fails the prune rather than having the entries of that directory
// deleted.
func TestPruneMetadataImagesSymlink(t *testing.T) {
	t.Setenv("LXD_DIR", t.TempDir())

	d := &qemu{
		dbType:  instancetype.VM,
		name:    "v1",
		project: api.Project{Name: api.ProjectDefaultName},
	}

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "keep")
	require.NoError(t, os.WriteFile(outsidePath, nil, 0600))
	require.NoError(t, os.MkdirAll(d.Path(), 0700))
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(d.Path(), qemuBitmapsDir)))

	require.Error(t, d.pruneMetadataImages(nil))
	require.True(t, shared.PathExists(outsidePath), "No entry outside the instance path is deleted")
}
