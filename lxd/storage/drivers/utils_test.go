package drivers

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Test GetVolumeMountPath.
func TestGetVolumeMountPath(t *testing.T) {
	poolName := "testpool"

	// Test custom volume.
	path := GetVolumeMountPath(poolName, VolumeTypeCustom, "testvol")
	expected := GetPoolMountPath(poolName) + "/custom/testvol"
	assert.Equal(t, expected, path)

	// Test custom volume snapshot.
	path = GetVolumeMountPath(poolName, VolumeTypeCustom, "testvol/snap1")
	expected = GetPoolMountPath(poolName) + "/custom-snapshots/testvol/snap1"
	assert.Equal(t, expected, path)

	// Test image volume.
	path = GetVolumeMountPath(poolName, VolumeTypeImage, "fingerprint")
	expected = GetPoolMountPath(poolName) + "/images/fingerprint"
	assert.Equal(t, expected, path)

	// Test container volume.
	path = GetVolumeMountPath(poolName, VolumeTypeContainer, "testvol")
	expected = GetPoolMountPath(poolName) + "/containers/testvol"
	assert.Equal(t, expected, path)

	// Test virtual-machine volume.
	path = GetVolumeMountPath(poolName, VolumeTypeVM, "testvol")
	expected = GetPoolMountPath(poolName) + "/virtual-machines/testvol"
	assert.Equal(t, expected, path)
}

func TestEnsureMountPathRejectsUnsafeNames(t *testing.T) {
	testCases := []struct {
		name          string
		poolName      string
		expectedError string
	}{
		{name: "../outside", poolName: "testpool", expectedError: `Invalid volume name ".."`},
		{name: "volume/../outside", poolName: "testpool", expectedError: `Invalid volume snapshot name "../outside"`},
		{name: "volume/snapshot", poolName: "../outside", expectedError: `Invalid storage pool name`},
	}

	for _, testCase := range testCases {
		volume := NewVolume(nil, testCase.poolName, VolumeTypeCustom, ContentTypeFS, testCase.name, nil, nil)
		assert.ErrorContains(t, volume.EnsureMountPath(), testCase.expectedError)
	}
}

// Test addNoRecoveryMountOption.
func TestAddNoRecoveryMountOption(t *testing.T) {
	// Test unsupported FS.
	options := "ro"
	result := addNoRecoveryMountOption(options, "vfat")
	assert.Equal(t, "ro", result)

	// Test without options.
	options = ""
	result = addNoRecoveryMountOption(options, "ext4")
	assert.Equal(t, "norecovery", result)

	// Test noload being a synonym for norecovery.
	options = "ro,noatime,noload"
	result = addNoRecoveryMountOption(options, "ext4")
	assert.Equal(t, "ro,noatime,norecovery", result)

	// Test with existing options.
	options = "ro,noatime"
	result = addNoRecoveryMountOption(options, "btrfs")
	assert.Equal(t, "ro,noatime,norecovery", result)

	// Test with existing norecovery option.
	options = "norecovery"
	result = addNoRecoveryMountOption(options, "xfs")
	assert.Equal(t, "norecovery", result)
}

// Test ValidPoolName.
func TestValidPoolName(t *testing.T) {
	// Test valid pool name.
	assert.NoError(t, ValidPoolName("valid-pool"))

	// Test valid pool name with special characters.
	assert.NoError(t, ValidPoolName("valid@pool"))
	assert.NoError(t, ValidPoolName("valid#pool"))
	assert.NoError(t, ValidPoolName("valid;pool"))
	assert.NoError(t, ValidPoolName("valid&pool"))
	assert.NoError(t, ValidPoolName("valid,pool"))
	assert.NoError(t, ValidPoolName("valid..pool"))
	assert.NoError(t, ValidPoolName("valid_pool"))

	// Test invalid pool names.
	assert.Error(t, ValidPoolName(""))
	assert.Error(t, ValidPoolName("-invalid-pool"))
	assert.Error(t, ValidPoolName(".invalid-pool"))
	assert.Error(t, ValidPoolName("."))
	assert.Error(t, ValidPoolName(".."))
	assert.Error(t, ValidPoolName("invalid pool"))
	assert.Error(t, ValidPoolName("invalid/pool"))
}

// Test ValidVolumeName.
func TestValidVolumeName(t *testing.T) {
	// Test valid volume name.
	assert.NoError(t, ValidVolumeName("validvolume"))

	// Test valid volume name with special characters.
	assert.NoError(t, ValidVolumeName("valid@volume"))
	assert.NoError(t, ValidVolumeName("valid#volume"))
	assert.NoError(t, ValidVolumeName("valid;volume"))
	assert.NoError(t, ValidVolumeName("valid&volume"))
	assert.NoError(t, ValidVolumeName("valid,volume"))
	assert.NoError(t, ValidVolumeName("valid..volume"))
	assert.NoError(t, ValidVolumeName("valid_volume"))
	assert.NoError(t, ValidVolumeName("-valid-volume"))

	// Test invalid volume names.
	assert.Error(t, ValidVolumeName(""))
	assert.Error(t, ValidVolumeName("."))
	assert.Error(t, ValidVolumeName(".."))
	assert.Error(t, ValidVolumeName("invalid volume"))
	assert.Error(t, ValidVolumeName("invalid/volume"))
}

// Test TryMount early exit.
func TestTryMountEarlyExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Check that TryMount returns an error when being called with an already cancelled context.
	assert.ErrorIs(t, TryMount(ctx, "", "", "", 0, ""), context.Canceled)
}

// Test loopFileSizeResolve.
func TestLoopFileSizeResolve(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LXD_DIR", dir)

	existingFile := filepath.Join(dir, "test.img")

	f, err := os.Create(existingFile)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(3*1024*1024*1024))
	require.NoError(t, f.Close())

	// sourceRecover=true with a GiB-aligned file: size is expressed in GiB.
	size, err := loopFileSizeResolve(existingFile, true)
	assert.NoError(t, err)
	assert.Equal(t, "3GiB", size)

	// sourceRecover=true with a non-GiB-aligned file: size is expressed in bytes.
	nonAlignedFile := filepath.Join(dir, "nonaligned.img")
	g, err := os.Create(nonAlignedFile)
	require.NoError(t, err)
	require.NoError(t, g.Truncate(3*1024*1024*1024+512))
	require.NoError(t, g.Close())

	size, err = loopFileSizeResolve(nonAlignedFile, true)
	assert.NoError(t, err)
	assert.Equal(t, strconv.FormatInt(3*1024*1024*1024+512, 10)+"B", size)

	// sourceRecover=false or nonexistent file falls back to loopFileSizeDefault
	// which requires at least 5GiB free on the LXD_DIR filesystem.
	var st unix.Statfs_t
	err = unix.Statfs(dir, &st)
	require.NoError(t, err)

	gibFree := uint64(st.Frsize) * st.Bavail / (1024 * 1024 * 1024)
	if gibFree < 5 {
		t.Skipf("Skipping loopFileSizeDefault tests: only %d GiB free, need at least 5 GiB", gibFree)
	}

	size, err = loopFileSizeResolve(existingFile, false)
	assert.NoError(t, err)
	assert.NotEmpty(t, size)

	size, err = loopFileSizeResolve(filepath.Join(dir, "nonexistent.img"), true)
	assert.NoError(t, err)
	assert.NotEmpty(t, size)
}

func TestRemoveEmptyDirectories(t *testing.T) {
	poolPath := t.TempDir()
	for _, dir := range []string{"containers/c1", "containers-snapshots", "custom", "images/abc", "virtual-machines"} {
		require.NoError(t, os.MkdirAll(filepath.Join(poolPath, dir), 0o711))
	}

	require.NoError(t, removeEmptyDirectories(poolPath))

	entries, err := os.ReadDir(poolPath)
	require.NoError(t, err)
	assert.Empty(t, entries)

	// Anything other than an empty directory is left untouched.
	filePath := filepath.Join(poolPath, "containers", "c1", "file")
	require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0o711))
	require.NoError(t, os.WriteFile(filePath, []byte("data"), 0o600))

	assert.Error(t, removeEmptyDirectories(poolPath))
	assert.FileExists(t, filePath)

	// A missing directory is not an error.
	assert.NoError(t, removeEmptyDirectories(filepath.Join(poolPath, "missing")))
}
