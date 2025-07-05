package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
