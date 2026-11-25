package resources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupNetworkPaths points the network sysfs paths at temporary directories.
func setupNetworkPaths(t *testing.T) (netDir string) {
	t.Helper()

	netDir = t.TempDir()

	origNet := sysClassNet
	origPci := sysBusPci
	t.Cleanup(func() {
		sysClassNet = origNet
		sysBusPci = origPci
	})

	sysClassNet = netDir
	sysBusPci = t.TempDir()

	return netDir
}

func TestGetNetwork_SharedNonPCIDevice(t *testing.T) {
	netDir := setupNetworkPaths(t)
	devicesDir := t.TempDir()

	// Two interfaces backed by the same platform device (no PCI address) and one standalone device.
	ifaces := map[string]string{
		"lxdtest0": "switch",
		"lxdtest1": "switch",
		"lxdtest2": "other",
	}

	for iface, device := range ifaces {
		writeSysFile(t, devicesDir, filepath.Join(device, "net", iface, "type"), "1\n")
		require.NoError(t, os.MkdirAll(filepath.Join(netDir, iface), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(devicesDir, device), filepath.Join(netDir, iface, "device")))
	}

	network, err := GetNetwork()
	require.NoError(t, err)
	assert.Len(t, network.Cards, 2)
	assert.Equal(t, uint64(2), network.Total)
}
