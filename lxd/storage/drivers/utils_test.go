package drivers

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// sourceRecover=false or nonexistent file falls back to loopFileSizeDefault.
	size, err = loopFileSizeResolve(existingFile, false)
	assert.NoError(t, err)
	assert.NotEmpty(t, size)

	size, err = loopFileSizeResolve(filepath.Join(dir, "nonexistent.img"), true)
	assert.NoError(t, err)
	assert.NotEmpty(t, size)
}
