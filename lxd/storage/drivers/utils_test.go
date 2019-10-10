package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test GetVolumeMountPoint
func TestGetVolumeMountPoint(t *testing.T) {
	poolName := "testpool"

	// Test custom volume.
	path := GetVolumeMountPoint(poolName, VolumeTypeCustom, "testvol")
	expected := GetPoolMountPoint(poolName) + "/custom/testvol"
	assert.Equal(t, expected, path)

	// Test custom volume snapshot.
	path = GetVolumeMountPoint(poolName, VolumeTypeCustom, "testvol/snap1")
	expected = GetPoolMountPoint(poolName) + "/custom-snapshots/testvol/snap1"
	assert.Equal(t, expected, path)

	// Test image volume.
	path = GetVolumeMountPoint(poolName, VolumeTypeImage, "fingerprint")
	expected = GetPoolMountPoint(poolName) + "/images/fingerprint"
	assert.Equal(t, expected, path)

	// Test container volume.
	path = GetVolumeMountPoint(poolName, VolumeTypeContainer, "testvol")
	expected = GetPoolMountPoint(poolName) + "/containers/testvol"
	assert.Equal(t, expected, path)

	// Test virtual-machine volume.
	path = GetVolumeMountPoint(poolName, VolumeTypeVM, "testvol")
	expected = GetPoolMountPoint(poolName) + "/virtual-machines/testvol"
	assert.Equal(t, expected, path)
}
