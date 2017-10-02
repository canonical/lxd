package debug_test

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/lxc/lxd/lxd/debug"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCPU(t *testing.T) {
	// Create a temporary file.
	file, err := ioutil.TempFile("", "lxd-util-")
	assert.NoError(t, err)
	file.Close()
	defer os.Remove(file.Name())

	stop, err := debug.Start(debug.CPU(file.Name()))
	require.NoError(t, err)
	stop()

	// The CPU profiling data actually exists on disk.
	_, err = os.Stat(file.Name())
	assert.NoError(t, err)
}

func TestCPU_CannotCreateFile(t *testing.T) {
	stop, err := debug.Start(debug.CPU("/a/path/that/does/not/exists"))
	assert.Contains(t, err.Error(), "Error opening cpu profile file")
	stop()
}
