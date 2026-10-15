package resources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupGPUPaths points the GPU sysfs and procfs paths at temporary directories.
func setupGPUPaths(t *testing.T) (drmDir string) {
	t.Helper()

	drmDir = t.TempDir()
	pciDir := t.TempDir()
	nvidiaDir := filepath.Join(t.TempDir(), "nvidia")

	origDrm := sysClassDrm
	origPci := sysBusPci
	origNvidia := procDriverNvidia
	t.Cleanup(func() {
		sysClassDrm = origDrm
		sysBusPci = origPci
		procDriverNvidia = origNvidia
	})

	sysClassDrm = drmDir
	sysBusPci = pciDir
	procDriverNvidia = nvidiaDir

	return drmDir
}

func TestGetGPU_DanglingDeviceSymlink(t *testing.T) {
	drmDir := setupGPUPaths(t)

	// card0 belongs to a disconnected device: its "device" symlink is dangling.
	writeSysFile(t, drmDir, filepath.Join("card0", "dev"), "226:0\n")
	require.NoError(t, os.MkdirAll(filepath.Join(drmDir, "card0", "device"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(drmDir, "missing"), filepath.Join(drmDir, "card0", "device", "device")))

	// card1 is a regular card.
	writeSysFile(t, drmDir, filepath.Join("card1", "dev"), "226:1\n")
	writeSysFile(t, drmDir, filepath.Join("card1", "device", "vendor"), "0x8086\n")
	writeSysFile(t, drmDir, filepath.Join("card1", "device", "device"), "0x1234\n")

	gpu, err := GetGPU()
	require.NoError(t, err)
	require.Len(t, gpu.Cards, 1)
	assert.Equal(t, "8086", gpu.Cards[0].VendorID)
	assert.Equal(t, "1234", gpu.Cards[0].ProductID)
	assert.Equal(t, uint64(1), gpu.Total)
}
