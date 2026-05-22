package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSysFile writes content to a file under the given base directory,
// creating any necessary parent directories.
func writeSysFile(t *testing.T, base, rel, content string) {
	t.Helper()
	full := filepath.Join(base, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// addCPUEntry creates the minimum sysfs topology entries for a CPU thread.
func addCPUEntry(t *testing.T, sysDir string, cpuID, socket, core, die int) {
	t.Helper()
	prefix := fmt.Sprintf("cpu%d", cpuID)
	writeSysFile(t, sysDir, filepath.Join(prefix, "topology", "physical_package_id"), fmt.Sprintf("%d\n", socket))
	writeSysFile(t, sysDir, filepath.Join(prefix, "topology", "core_id"), fmt.Sprintf("%d\n", core))
	writeSysFile(t, sysDir, filepath.Join(prefix, "topology", "die_id"), fmt.Sprintf("%d\n", die))
}

// buildCPUInfo builds a /proc/cpuinfo-like string for a set of processors.
// Each entry is identified by its processor index, with optional vendor and model fields.
type cpuInfoEntry struct {
	processor int
	vendor    string
	model     string
	// cpuField is the PowerPC/SPARC-style "cpu" key (used instead of "model name").
	cpuField string
}

func buildCPUInfo(entries []cpuInfoEntry) string {
	out := ""
	var outSb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&outSb, "processor\t: %d\n", e.processor)
		if e.vendor != "" {
			fmt.Fprintf(&outSb, "vendor_id\t: %s\n", e.vendor)
		}

		if e.model != "" {
			fmt.Fprintf(&outSb, "model name\t: %s\n", e.model)
		}

		if e.cpuField != "" {
			fmt.Fprintf(&outSb, "cpu\t\t: %s\n", e.cpuField)
		}

		outSb.WriteString("\n")
	}

	out += outSb.String()
	return out
}

func TestGetCPU_SingleSocketDualCoreTwoThreads(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	// Two physical cores, two threads each = 4 logical CPUs.
	addCPUEntry(t, sysDir, 0, 0, 0, 0) // socket 0, core 0, thread 0
	addCPUEntry(t, sysDir, 1, 0, 0, 0) // socket 0, core 0, thread 1
	addCPUEntry(t, sysDir, 2, 0, 1, 0) // socket 0, core 1, thread 0
	addCPUEntry(t, sysDir, 3, 0, 1, 0) // socket 0, core 1, thread 1

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Intel(R) Core(TM) i7"},
		{processor: 1, vendor: "GenuineIntel", model: "Intel(R) Core(TM) i7"},
		{processor: 2, vendor: "GenuineIntel", model: "Intel(R) Core(TM) i7"},
		{processor: 3, vendor: "GenuineIntel", model: "Intel(R) Core(TM) i7"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	assert.EqualValues(t, 4, cpu.Total)
	require.Len(t, cpu.Sockets, 1)

	socket := cpu.Sockets[0]
	assert.EqualValues(t, 0, socket.Socket)
	assert.Equal(t, "GenuineIntel", socket.Vendor)
	assert.Equal(t, "Intel(R) Core(TM) i7", socket.Name)
	require.Len(t, socket.Cores, 2)

	// Each core must have exactly 2 threads.
	for _, core := range socket.Cores {
		assert.Len(t, core.Threads, 2)
	}
}

func TestGetCPU_TwoSocketsSingleCore(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0) // socket 0
	addCPUEntry(t, sysDir, 1, 1, 0, 0) // socket 1

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "AuthenticAMD", model: "AMD EPYC 7763"},
		{processor: 1, vendor: "AuthenticAMD", model: "AMD EPYC 7763"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	assert.EqualValues(t, 2, cpu.Total)
	require.Len(t, cpu.Sockets, 2)

	assert.EqualValues(t, 0, cpu.Sockets[0].Socket)
	assert.EqualValues(t, 1, cpu.Sockets[1].Socket)

	for _, socket := range cpu.Sockets {
		assert.Equal(t, "AuthenticAMD", socket.Vendor)
		assert.Equal(t, "AMD EPYC 7763", socket.Name)
		require.Len(t, socket.Cores, 1)
		assert.Len(t, socket.Cores[0].Threads, 1)
	}
}

func TestGetCPU_PowerPCStyleCPUField(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)

	// PowerPC/SPARC-style cpuinfo uses "cpu" rather than "model name" and has
	// no "vendor_id" field.
	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, cpuField: "POWER9, altivec supported"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	assert.EqualValues(t, 1, cpu.Total)
	require.Len(t, cpu.Sockets, 1)
	assert.Equal(t, "POWER9, altivec supported", cpu.Sockets[0].Name)
}

func TestGetCPU_IsolatedCPU(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)
	addCPUEntry(t, sysDir, 1, 0, 1, 0)

	// Mark cpu1 as isolated.
	writeSysFile(t, sysDir, "isolated", "1\n")

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Test CPU"},
		{processor: 1, vendor: "GenuineIntel", model: "Test CPU"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	assert.EqualValues(t, 2, cpu.Total)
	require.Len(t, cpu.Sockets, 1)

	// Collect all threads across cores and verify isolation flags.
	isolated := map[int64]bool{}
	for _, core := range cpu.Sockets[0].Cores {
		for _, thread := range core.Threads {
			isolated[thread.ID] = thread.Isolated
		}
	}

	assert.False(t, isolated[0], "cpu0 should not be isolated")
	assert.True(t, isolated[1], "cpu1 should be isolated")
}

func TestGetCPU_FrequencyFromCpufreq(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)

	// cpufreq files — values are in kHz.
	writeSysFile(t, sysDir, "cpu0/cpufreq/cpuinfo_min_freq", "800000\n")  // 800 MHz
	writeSysFile(t, sysDir, "cpu0/cpufreq/cpuinfo_max_freq", "3600000\n") // 3600 MHz
	writeSysFile(t, sysDir, "cpu0/cpufreq/scaling_cur_freq", "2400000\n") // 2400 MHz

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Test CPU"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	require.Len(t, cpu.Sockets, 1)
	socket := cpu.Sockets[0]

	assert.EqualValues(t, 800, socket.FrequencyMinimum)
	assert.EqualValues(t, 3600, socket.FrequencyTurbo)
	assert.EqualValues(t, 2400, socket.Frequency)
}

func TestGetCPU_FrequencyFallbackToScaling(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)

	// Only scaling_min/max_freq is present (no cpuinfo_min/max_freq).
	writeSysFile(t, sysDir, "cpu0/cpufreq/scaling_min_freq", "1000000\n") // 1000 MHz
	writeSysFile(t, sysDir, "cpu0/cpufreq/scaling_max_freq", "4000000\n") // 4000 MHz

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Test CPU"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	require.Len(t, cpu.Sockets, 1)
	socket := cpu.Sockets[0]

	assert.EqualValues(t, 1000, socket.FrequencyMinimum)
	assert.EqualValues(t, 4000, socket.FrequencyTurbo)
}

func TestGetCPU_CacheInfo(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)

	// L1d cache entry.
	writeSysFile(t, sysDir, "cpu0/cache/index0/level", "1\n")
	writeSysFile(t, sysDir, "cpu0/cache/index0/size", "32K\n")
	writeSysFile(t, sysDir, "cpu0/cache/index0/type", "Data\n")

	// L2 cache entry.
	writeSysFile(t, sysDir, "cpu0/cache/index1/level", "2\n")
	writeSysFile(t, sysDir, "cpu0/cache/index1/size", "256K\n")
	writeSysFile(t, sysDir, "cpu0/cache/index1/type", "Unified\n")

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Test CPU"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	require.Len(t, cpu.Sockets, 1)
	caches := cpu.Sockets[0].Cache
	require.Len(t, caches, 2)

	l1 := caches[0]
	assert.EqualValues(t, 1, l1.Level)
	assert.EqualValues(t, 32*1024, l1.Size)
	assert.Equal(t, "Data", l1.Type)

	l2 := caches[1]
	assert.EqualValues(t, 2, l2.Level)
	assert.EqualValues(t, 256*1024, l2.Size)
	assert.Equal(t, "Unified", l2.Type)
}

func TestGetCPU_NUMANode(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)
	addCPUEntry(t, sysDir, 1, 0, 1, 0)

	// Attach cpu0 to NUMA node 0 and cpu1 to NUMA node 1.
	writeSysFile(t, sysDir, "cpu0/node0/numastat", "")
	writeSysFile(t, sysDir, "cpu1/node1/numastat", "")

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Test CPU"},
		{processor: 1, vendor: "GenuineIntel", model: "Test CPU"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	require.Len(t, cpu.Sockets, 1)
	require.Len(t, cpu.Sockets[0].Cores, 2)

	numaByCore := map[uint64]uint64{}
	for _, core := range cpu.Sockets[0].Cores {
		require.Len(t, core.Threads, 1)
		numaByCore[core.Core] = core.Threads[0].NUMANode
	}

	assert.EqualValues(t, 0, numaByCore[0])
	assert.EqualValues(t, 1, numaByCore[1])
}

func TestGetCPU_OfflineThread(t *testing.T) {
	sysDir := t.TempDir()
	cpuInfoFile := filepath.Join(t.TempDir(), "cpuinfo")

	addCPUEntry(t, sysDir, 0, 0, 0, 0)
	addCPUEntry(t, sysDir, 1, 0, 1, 0)

	// Mark cpu1 as offline.
	writeSysFile(t, sysDir, "cpu1/online", "0\n")

	require.NoError(t, os.WriteFile(cpuInfoFile, []byte(buildCPUInfo([]cpuInfoEntry{
		{processor: 0, vendor: "GenuineIntel", model: "Test CPU"},
		{processor: 1, vendor: "GenuineIntel", model: "Test CPU"},
	})), 0o644))

	orig := sysDevicesCPU
	origCPUInfo := cpuInfoPath
	t.Cleanup(func() {
		sysDevicesCPU = orig
		cpuInfoPath = origCPUInfo
	})

	sysDevicesCPU = sysDir
	cpuInfoPath = cpuInfoFile

	cpu, err := GetCPU()
	require.NoError(t, err)

	assert.EqualValues(t, 2, cpu.Total)

	online := map[int64]bool{}
	for _, core := range cpu.Sockets[0].Cores {
		for _, thread := range core.Threads {
			online[thread.ID] = thread.Online
		}
	}

	assert.True(t, online[0], "cpu0 should be online")
	assert.False(t, online[1], "cpu1 should be offline")
}
