package resources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMeminfo_Host(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	content := `MemTotal:       16318412 kB
MemFree:         1203364 kB
MemAvailable:    9876544 kB
Buffers:          412300 kB
Cached:          7654320 kB
SwapCached:         1024 kB
Shmem:            823456 kB
SwapTotal:       4194300 kB
SwapFree:        3145724 kB
HugePages_Total:       4
HugePages_Free:        1
Hugepagesize:       2048 kB
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	info, err := parseMeminfo(path)
	require.NoError(t, err)

	assert.Equal(t, uint64(16318412*1024), info.Total)
	assert.Equal(t, uint64(1203364*1024), info.Free)
	assert.Equal(t, uint64(9876544*1024), info.Available)
	assert.Equal(t, uint64(412300*1024), info.Buffers)
	assert.Equal(t, uint64(7654320*1024), info.Cached)
	assert.Equal(t, uint64(823456*1024), info.Shmem)
	assert.Equal(t, uint64(4194300*1024), info.SwapTotal)
	assert.Equal(t, uint64(3145724*1024), info.SwapFree)
	assert.Equal(t, uint64(4), info.HugepagesTotal)
	assert.Equal(t, uint64(1), info.HugepagesFree)
	assert.Equal(t, uint64(2048*1024), info.HugepagesSize)
}

func TestParseMeminfo_NUMANode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	content := `Node 0 MemTotal:        8155472 kB
Node 0 MemFree:          601232 kB
Node 0 MemUsed:         7554240 kB
Node 0 Active:          3012344 kB
Node 0 FilePages:       3911204 kB
Node 0 Shmem:            512000 kB
Node 0 SReclaimable:     204800 kB
Node 0 HugePages_Total:     2
Node 0 HugePages_Free:      1
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	info, err := parseMeminfo(path)
	require.NoError(t, err)

	assert.Equal(t, uint64(8155472*1024), info.Total)
	assert.Equal(t, uint64(601232*1024), info.Free)
	assert.Equal(t, uint64(3911204*1024), info.FilePages)
	assert.Equal(t, uint64(512000*1024), info.Shmem)
	assert.Equal(t, uint64(204800*1024), info.SReclaimable)
	assert.Equal(t, uint64(2), info.HugepagesTotal)
	assert.Equal(t, uint64(1), info.HugepagesFree)
	assert.Zero(t, info.Available)
	assert.Zero(t, info.SwapTotal)
	assert.Zero(t, info.SwapFree)
}

func TestParseMeminfo_InvalidValue(t *testing.T) {
	for _, key := range []string{"MemTotal", "MemAvailable", "Buffers", "Cached", "Shmem", "SwapTotal", "SwapFree"} {
		t.Run(key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meminfo")
			content := "MemFree:         1203364 kB\n" + key + ":       invalid kB\n"
			require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

			_, err := parseMeminfo(path)
			assert.ErrorContains(t, err, "Failed parsing "+key)
		})
	}
}
