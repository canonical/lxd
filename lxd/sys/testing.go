package sys

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// NewTestOS returns a new OS instance initialized with test values.
func NewTestOS(t *testing.T) (*OS, func()) {
	dir, err := ioutil.TempDir("", "lxd-sys-os-test-")
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, os.RemoveAll(dir))
	}

	os := &OS{
		VarDir:   dir,
		CacheDir: filepath.Join(dir, "cache"),
		LogDir:   filepath.Join(dir, "log"),
	}

	return os, cleanup
}
