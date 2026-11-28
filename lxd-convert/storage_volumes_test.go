package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseStorageVolumeArg(t *testing.T) {
	name, config, err := parseStorageVolumeArg("data,pool=default,source=vol1,path=/data")
	require.NoError(t, err)
	require.Equal(t, "data", name)
	require.Equal(t, map[string]string{"pool": "default", "source": "vol1", "path": "/data"}, config)

	_, _, err = parseStorageVolumeArg("data")
	require.Error(t, err)

	_, _, err = parseStorageVolumeArg("data,pool")
	require.Error(t, err)
}
