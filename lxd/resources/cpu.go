package resources

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/digitalocean/go-smbios/smbios"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

var sysDevicesCPU = "/sys/devices/system/cpu"

// GetCPUIsolated returns a slice of IDs corresponding to isolated threads.
func GetCPUIsolated() []int64 {
	isolatedPath := filepath.Join(sysDevicesCPU, "isolated")

	isolatedCpusInt := []int64{}
	if sysfsExists(isolatedPath) {
		buf, err := os.ReadFile(isolatedPath)
		if err != nil {
			return isolatedCpusInt
		}

		// File might exist even though there are no isolated cpus.
		isolatedCpus := strings.TrimSpace(string(buf))
		if isolatedCpus != "" {
			isolatedCpusInt, err = ParseCpuset(isolatedCpus)
			if err != nil {
				return isolatedCpusInt
			}
		}
	}

	return isolatedCpusInt
}

// parseRangedListToInt64Slice takes an `input` of the form "1,2,8-10,5-7" and returns a slice of int64s
// containing the expanded list of numbers. In this example, the returned slice would be [1,2,8,9,10,5,6,7].
// The elements in the output slice are meant to represent hardware entity identifiers (e.g, either CPU or NUMA node IDs).
func parseRangedListToInt64Slice(input string) ([]int64, error) {
	res := []int64{}
	chunks := strings.Split(input, ",")
	for _, chunk := range chunks {
		if strings.Contains(chunk, "-") {
			// Range
			before, after, _ := strings.Cut(chunk, "-")
			if after == "" {
				return nil, fmt.Errorf("Invalid CPU/NUMA set value: %q", input)
			}

			low, err := strconv.ParseInt(before, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid CPU/NUMA set value: %w", err)
			}

			high, err := strconv.ParseInt(after, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid CPU/NUMA set value: %w", err)
			}

			for i := low; i <= high; i++ {
				res = append(res, i)
			}
		} else {
			// Simple entry
			nr, err := strconv.ParseInt(chunk, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid CPU/NUMA set value: %w", err)
			}

			res = append(res, nr)
		}
	}

	return res, nil
}

// ParseCpuset parses a `limits.cpu` range into a list of CPU ids.
func ParseCpuset(cpu string) ([]int64, error) {
	cpus, err := parseRangedListToInt64Slice(cpu)
	if err != nil {
		return nil, fmt.Errorf("Invalid cpuset value %q: %w", cpu, err)
	}

	return cpus, nil
}

// ParseNumaNodeSet parses a `limits.cpu.nodes` into a list of NUMA node ids.
func ParseNumaNodeSet(numaNodeSet string) ([]int64, error) {
	nodes, err := parseRangedListToInt64Slice(numaNodeSet)
	if err != nil {
		return nil, fmt.Errorf("Invalid NUMA node set value %q: %w", numaNodeSet, err)
	}

	return nodes, nil
}

func getCPUCache(path string) ([]api.ResourcesCPUCache, error) {
	caches := []api.ResourcesCPUCache{}

	// List all the caches
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to list %q: %w", path, err)
	}

	// Iterate and add to our list
	for _, entry := range entries {
		entryName := entry.Name()
		entryPath := filepath.Join(path, entryName)

		if !sysfsExists(filepath.Join(entryPath, "level")) {
			continue
		}

		// Setup the cache entry
		cache := api.ResourcesCPUCache{}
		cache.Type = "Unknown"

		// Get the cache level
		cacheLevel, err := readUint(filepath.Join(entryPath, "level"))
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "level"), err)
		}

		cache.Level = cacheLevel

		// Get the cache size
		content, err := os.ReadFile(filepath.Join(entryPath, "size"))
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "size"), err)
			}
		} else {
			cacheSizeStr := strings.TrimSpace(string(content))

			// Handle cache sizes in KiB
			cacheSizeMultiplier := uint64(1)
			if strings.HasSuffix(cacheSizeStr, "K") {
				cacheSizeMultiplier = 1024
				cacheSizeStr = strings.TrimSuffix(cacheSizeStr, "K")
			}

			cacheSize, err := strconv.ParseUint((cacheSizeStr), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse cache size: %w", err)
			}

			cache.Size = cacheSize * cacheSizeMultiplier
		}

		// Get the cache type
		cacheType, err := os.ReadFile(filepath.Join(entryPath, "type"))
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "type"), err)
			}
		} else {
			cache.Type = strings.TrimSpace(string(cacheType))
		}

		// Add to the list
		caches = append(caches, cache)
	}

	return caches, nil
}

func getCPUdmi() (vendor string, model string, err error) {
	// Open the system DMI tables.
	stream, _, err := smbios.Stream()
	if err != nil {
		return "", "", err
	}

	defer func() { _ = stream.Close() }()

	// Decode SMBIOS structures.
	d := smbios.NewDecoder(stream)
	tables, err := d.Decode()
	if err != nil {
		return "", "", err
	}

	for _, e := range tables {
		// Only care about the CPU table.
		if e.Header.Type != 4 {
			continue
		}

		if len(e.Strings) >= 3 {
			if e.Strings[1] != "" && e.Strings[2] != "" {
				return e.Strings[1], e.Strings[2], nil
			}
		}
	}

	return "", "", fmt.Errorf("No DMI table found")
}

type cpuInfo struct {
	Name   string
	Vendor string
}

// GetCPU returns a filled api.ResourcesCPU struct ready for use by LXD.
func GetCPU() (*api.ResourcesCPU, error) {
	cpu := api.ResourcesCPU{}

	// Get the isolated CPUs
	isolated := GetCPUIsolated()

	// Temporary storage
	cpuSockets := map[int64]*api.ResourcesCPUSocket{}
	cpuCores := map[int64]map[string]*api.ResourcesCPUCore{}

	// Get the DMI data
	dmiVendor, dmiModel, _ := getCPUdmi()

	// Open cpuinfo
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, fmt.Errorf("Failed to open /proc/cpuinfo: %w", err)
	}

	defer func() { _ = f.Close() }()
	cpuInfoScanner := bufio.NewScanner(f)
	cpuInfoMap := map[int64]*cpuInfo{}
	flagList := []string{}

	// CPU information
	for cpuInfoScanner.Scan() {
		line := strings.TrimSpace(cpuInfoScanner.Text())
		if !strings.HasPrefix(line, "processor") {
			return nil, fmt.Errorf("Failed to parse /proc/cpuinfo: Unexpected line %q", line)
		}

		// Extract cpu index
		_, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("Failed to parse /proc/cpuinfo: Missing separator")
		}

		value = strings.TrimSpace(value)
		cpuSocket, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse cpu index %q in /proc/cpuinfo: %w", value, err)
		}

		_, ok := cpuInfoMap[cpuSocket]
		if ok {
			return nil, fmt.Errorf("Failed to parse /proc/cpuinfo: duplicate CPU block in cpuinfo?")
		}

		cpuInfo := &cpuInfo{}

		// Iterate until we hit the separator line
		for cpuInfoScanner.Scan() {
			line := strings.TrimSpace(cpuInfoScanner.Text())

			// End of processor section
			if line == "" {
				break
			}

			// Check if we already have the data and seek to next
			if cpuInfo.Vendor != "" && cpuInfo.Name != "" && len(flagList) > 0 {
				continue
			}

			// Get key/value
			key, value, found := strings.Cut(line, ":")
			if !found {
				return nil, fmt.Errorf("Failed to parse /proc/cpuinfo: Missing separator")
			}

			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)

			if key == "vendor_id" {
				cpuInfo.Vendor = value
				continue
			}

			if key == "model name" {
				cpuInfo.Name = value
				continue
			}

			if key == "flags" {
				flagList = strings.SplitN(value, " ", -1)
				continue
			}

			if key == "cpu" {
				cpuInfo.Name = value
				continue
			}
		}

		cpuInfoMap[cpuSocket] = cpuInfo
	}

	// List all the CPUs
	entries, err := os.ReadDir(sysDevicesCPU)
	if err != nil {
		return nil, fmt.Errorf("Failed to list %q: %w", sysDevicesCPU, err)
	}

	// Process all entries
	cpu.Total = 0
	for _, entry := range entries {
		entryName := entry.Name()
		entryPath := filepath.Join(sysDevicesCPU, entryName)

		// Skip any non-CPU entry
		if !sysfsExists(filepath.Join(entryPath, "topology")) {
			continue
		}

		// Get topology
		cpuSocket, err := readInt(filepath.Join(entryPath, "topology", "physical_package_id"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "topology", "physical_package_id"), err)
		}

		cpuCore, err := readInt(filepath.Join(entryPath, "topology", "core_id"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "topology", "core_id"), err)
		}

		cpuDie, err := readInt(filepath.Join(entryPath, "topology", "die_id"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "topology", "die_id"), err)
		}

		// Handle missing architecture support.
		if cpuSocket == -1 {
			cpuSocket = 0
		}

		if cpuCore == -1 {
			cpuCore = 0
		}

		if cpuDie == -1 {
			cpuDie = 0
		}

		// Grab socket data if needed
		_, ok := cpuSockets[cpuSocket]
		if !ok {
			resSocket := &api.ResourcesCPUSocket{}

			// Socket number
			resSocket.Socket = uint64(cpuSocket)

			cpuInfo, ok := cpuInfoMap[cpuSocket]
			if ok {
				resSocket.Vendor = cpuInfo.Vendor
				resSocket.Name = cpuInfo.Name
			}

			// Fill in model/vendor from DMI if missing.
			if resSocket.Vendor == "" {
				resSocket.Vendor = dmiVendor
			}

			if resSocket.Name == "" {
				resSocket.Name = dmiModel
			}

			// Cache information
			if sysfsExists(filepath.Join(entryPath, "cache")) {
				socketCache, err := getCPUCache(filepath.Join(entryPath, "cache"))
				if err != nil {
					return nil, fmt.Errorf("Failed to get CPU cache information: %w", err)
				}

				resSocket.Cache = socketCache
			}

			// Frequency
			if sysfsExists(filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq")) {
				freqMinimum, err := readUint(filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq"), err)
				}

				resSocket.FrequencyMinimum = freqMinimum / 1000
			} else if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_min_freq")) {
				freqMinimum, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_min_freq"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "cpufreq", "scaling_min_freq"), err)
				}

				resSocket.FrequencyMinimum = freqMinimum / 1000
			}

			if sysfsExists(filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq")) {
				freqTurbo, err := readUint(filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq"), err)
				}

				resSocket.FrequencyTurbo = freqTurbo / 1000
			} else if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_max_freq")) {
				freqTurbo, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_max_freq"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "cpufreq", "scaling_max_freq"), err)
				}

				resSocket.FrequencyTurbo = freqTurbo / 1000
			}

			// Record the data
			cpuSockets[cpuSocket] = resSocket
			cpuCores[cpuSocket] = map[string]*api.ResourcesCPUCore{}
		}

		// Grab core data if needed
		coreIndex := fmt.Sprintf("%d_%d", cpuDie, cpuCore)
		resCore, ok := cpuCores[cpuSocket][coreIndex]
		if !ok {
			resCore = &api.ResourcesCPUCore{}

			// Core number
			resCore.Core = uint64(cpuCore)

			// Die number
			resCore.Die = uint64(cpuDie)

			// flag List
			resCore.Flags = flagList

			// Frequency
			if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_cur_freq")) {
				freqCurrent, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_cur_freq"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "cpufreq", "scaling_cur_freq"), err)
				}

				resCore.Frequency = freqCurrent / 1000
			}

			// Initialize thread list
			resCore.Threads = []api.ResourcesCPUThread{}

			// Record the data
			cpuCores[cpuSocket][coreIndex] = resCore
		}

		// Grab thread data
		threadNumber, err := strconv.ParseInt(strings.TrimPrefix(entryName, "cpu"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse thread number: %w", err)
		}

		thread := api.ResourcesCPUThread{}
		thread.Online = true
		if sysfsExists(filepath.Join(entryPath, "online")) {
			online, err := readUint(filepath.Join(entryPath, "online"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "online"), err)
			}

			if online == 0 {
				thread.Online = false
			}
		}
		thread.ID = threadNumber
		thread.Isolated = shared.ValueInSlice(threadNumber, isolated)
		thread.Thread = uint64(len(resCore.Threads))

		// NUMA node
		numaNode, err := sysfsNumaNode(entryPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to find NUMA node: %w", err)
		}

		thread.NUMANode = numaNode

		resCore.Threads = append(resCore.Threads, thread)

		cpu.Total++
	}

	// Asemble the data
	cpu.Sockets = []api.ResourcesCPUSocket{}
	for _, socket := range cpuSockets {
		// Initialize core list
		socket.Cores = []api.ResourcesCPUCore{}

		// Add the cores
		coreFrequency := uint64(0)
		coreFrequencyCount := uint64(0)
		for _, core := range cpuCores[int64(socket.Socket)] {
			if core.Frequency > 0 {
				coreFrequency += core.Frequency
				coreFrequencyCount++
			}

			socket.Cores = append(socket.Cores, *core)
		}

		// Record average frequency
		if coreFrequencyCount > 0 {
			socket.Frequency = coreFrequency / coreFrequencyCount
		}

		sort.SliceStable(socket.Cores, func(i int, j int) bool { return socket.Cores[i].Core < socket.Cores[j].Core })
		cpu.Sockets = append(cpu.Sockets, *socket)
	}

	sort.SliceStable(cpu.Sockets, func(i int, j int) bool { return cpu.Sockets[i].Socket < cpu.Sockets[j].Socket })

	// Set the architecture name
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil, fmt.Errorf("Failed to get uname: %w", err)
	}

	cpu.Architecture = strings.TrimRight(string(uname.Machine[:]), "\x00")

	return &cpu, nil
}
