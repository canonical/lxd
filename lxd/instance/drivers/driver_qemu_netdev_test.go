package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
)

// TestAddNetDevConfigVDPAOpenError checks that a vDPA device file that cannot be opened is reported as an error.
func TestAddNetDevConfigVDPAOpenError(t *testing.T) {
	// A dangling symlink exists for shared.PathExists but fails to open, even as root.
	vdpaPath := filepath.Join(t.TempDir(), "vhost-vdpa-0")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing"), vdpaPath))

	d := &qemu{}
	nicConfig := []deviceConfig.RunConfigItem{
		{Key: "devName", Value: "vdpa0"},
		{Key: "vhostVDPAPath", Value: vdpaPath},
	}

	monHook, err := d.addNetDevConfig("", nil, nil, nicConfig)
	require.NoError(t, err)

	// The hook fails before using the monitor, so none is needed.
	err = monHook(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Contains(t, err.Error(), vdpaPath)
}
