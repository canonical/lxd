package resources

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMountInfoMountedIDs(t *testing.T) {
	validMountInfo := "22 27 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw\n" +
		"23 27 0:4 / /proc rw,nosuid,nodev,noexec,relatime shared:13 - proc proc rw\n"

	mountedIDs, err := parseMountInfoMountedIDs([]byte(validMountInfo))
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"0:21": true, "0:4": true}, mountedIDs)

	// A torn read can leave a line with too few fields.
	tornMountInfo := "22 27 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw\n" +
		"ntinue,threads=single\n"

	_, err = parseMountInfoMountedIDs([]byte(tornMountInfo))
	require.Error(t, err)
}

func TestGetMountedIDsRetriesOnTornRead(t *testing.T) {
	origReadMountInfoFile := readMountInfoFile
	defer func() { readMountInfoFile = origReadMountInfoFile }()

	validMountInfo := []byte("22 27 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw\n")
	tornMountInfo := []byte("ntinue,threads=single\n")

	calls := 0
	readMountInfoFile = func() ([]byte, error) {
		calls++
		if calls == 1 {
			return tornMountInfo, nil
		}

		return validMountInfo, nil
	}

	mountedIDs, err := getMountedIDs()
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"0:21": true}, mountedIDs)
	assert.Equal(t, 2, calls)
}

func TestGetMountedIDsExhaustsRetries(t *testing.T) {
	origReadMountInfoFile := readMountInfoFile
	defer func() { readMountInfoFile = origReadMountInfoFile }()

	calls := 0
	readMountInfoFile = func() ([]byte, error) {
		calls++
		return []byte("ntinue,threads=single\n"), nil
	}

	_, err := getMountedIDs()
	require.Error(t, err)
	assert.Equal(t, 3, calls)
}

func TestGetMountedIDsReadFailure(t *testing.T) {
	origReadMountInfoFile := readMountInfoFile
	defer func() { readMountInfoFile = origReadMountInfoFile }()

	readErr := errors.New("boom")
	readMountInfoFile = func() ([]byte, error) {
		return nil, readErr
	}

	_, err := getMountedIDs()
	require.ErrorIs(t, err, readErr)
}
