package resources

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

var sysDevicesCPU = "/sys/devices/system/cpu"

// GetCPUIsolated returns a slice of IDs corresponding to isolated threads.
func GetCPUIsolated() []int64 {
	isolatedPath := filepath.Join(sysDevicesCPU, "isolated")

	isolatedCpusInt := []int64{}
	if sysfsExists(isolatedPath) {
		buf, err := ioutil.ReadFile(isolatedPath)
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

// ParseCpuset parses a limits.cpu range into a list of CPU ids.
func ParseCpuset(cpu string) ([]int64, error) {
	cpus := []int64{}
	chunks := strings.Split(cpu, ",")
	for _, chunk := range chunks {
		if strings.Contains(chunk, "-") {
			// Range
			fields := strings.SplitN(chunk, "-", 2)
			if len(fields) != 2 {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}

			low, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}

			high, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}

			for i := low; i <= high; i++ {
				cpus = append(cpus, i)
			}
		} else {
			// Simple entry
			nr, err := strconv.ParseInt(chunk, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}
			cpus = append(cpus, nr)
		}
	}

	return cpus, nil
}

func getCPUCache(path string) ([]api.ResourcesCPUCache, error) {
	caches := []api.ResourcesCPUCache{}

	// List all the caches
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to list \"%s\"", path)
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
			return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "level"))
		}
		cache.Level = cacheLevel

		// Get the cache size
		content, err := ioutil.ReadFile(filepath.Join(entryPath, "size"))
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "size"))
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
				return nil, errors.Wrap(err, "Failed to parse cache size")
			}

			cache.Size = cacheSize * cacheSizeMultiplier
		}

		// Get the cache type
		cacheType, err := ioutil.ReadFile(filepath.Join(entryPath, "type"))
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "type"))
			}
		} else {
			cache.Type = strings.TrimSpace(string(cacheType))
		}

		// Add to the list
		caches = append(caches, cache)
	}

	return caches, nil
}

// GetCPU returns a filled api.ResourcesCPU struct ready for use by LXD
func GetCPU() (*api.ResourcesCPU, error) {
	cpu := api.ResourcesCPU{}

	// Get the isolated CPUs
	isolated := GetCPUIsolated()

	// Temporary storage
	cpuSockets := map[uint64]*api.ResourcesCPUSocket{}
	cpuCores := map[uint64]map[string]*api.ResourcesCPUCore{}

	// Open cpuinfo
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to open /proc/cpuinfo")
	}
	defer f.Close()
	cpuInfo := bufio.NewScanner(f)

	// List all the CPUs
	entries, err := ioutil.ReadDir(sysDevicesCPU)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysDevicesCPU)
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
		cpuSocket, err := readUint(filepath.Join(entryPath, "topology", "physical_package_id"))
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "topology", "physical_package_id"))
		}

		cpuCore, err := readUint(filepath.Join(entryPath, "topology", "core_id"))
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "topology", "core_id"))
		}

		cpuDie, err := readInt(filepath.Join(entryPath, "topology", "die_id"))
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "topology", "die_id"))
		}

		if cpuDie == -1 {
			// Architectures without support for die_id report -1, make that die 0 instead.
			cpuDie = 0
		}

		// Grab socket data if needed
		resSocket, ok := cpuSockets[cpuSocket]
		if !ok {
			resSocket = &api.ResourcesCPUSocket{}

			// Socket number
			resSocket.Socket = cpuSocket

			// CPU information
			for cpuInfo.Scan() {
				line := strings.TrimSpace(cpuInfo.Text())
				if !strings.HasPrefix(line, "processor") {
					continue
				}

				// Check if we're dealing with the right CPU
				fields := strings.SplitN(line, ":", 2)
				value := strings.TrimSpace(fields[1])

				if value != fmt.Sprintf("%v", cpuSocket) {
					continue
				}

				// Iterate until we hit the separator line
				for cpuInfo.Scan() {
					line := strings.TrimSpace(cpuInfo.Text())

					// End of processor section
					if line == "" {
						break
					}

					// Check if we already have the data and seek to next
					if resSocket.Vendor != "" && resSocket.Name != "" {
						continue
					}

					// Get key/value
					fields := strings.SplitN(line, ":", 2)
					key := strings.TrimSpace(fields[0])
					value := strings.TrimSpace(fields[1])

					if key == "vendor_id" {
						resSocket.Vendor = value
						continue
					}

					if key == "model name" {
						resSocket.Name = value
						continue
					}

					if key == "cpu" {
						resSocket.Name = value
						continue
					}
				}

				break
			}

			// Cache information
			if sysfsExists(filepath.Join(entryPath, "cache")) {
				socketCache, err := getCPUCache(filepath.Join(entryPath, "cache"))
				if err != nil {
					return nil, errors.Wrap(err, "Failed to get CPU cache information")
				}

				resSocket.Cache = socketCache
			}

			// Frequency
			if sysfsExists(filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq")) {
				freqMinimum, err := readUint(filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq"))
				}

				resSocket.FrequencyMinimum = freqMinimum / 1000
			} else if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_min_freq")) {
				freqMinimum, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_min_freq"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "cpufreq", "scaling_min_freq"))
				}

				resSocket.FrequencyMinimum = freqMinimum / 1000
			}

			if sysfsExists(filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq")) {
				freqTurbo, err := readUint(filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq"))
				}

				resSocket.FrequencyTurbo = freqTurbo / 1000
			} else if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_max_freq")) {
				freqTurbo, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_max_freq"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "cpufreq", "scaling_max_freq"))
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
			resCore.Core = cpuCore

			// Die number
			resCore.Die = uint64(cpuDie)

			// Frequency
			if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_cur_freq")) {
				freqCurrent, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_cur_freq"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "cpufreq", "scaling_cur_freq"))
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
			return nil, errors.Wrap(err, "Failed to parse thread number")
		}

		thread := api.ResourcesCPUThread{}
		thread.Online = true
		if sysfsExists(filepath.Join(entryPath, "online")) {
			online, err := readUint(filepath.Join(entryPath, "online"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "online"))
			}

			if online == 0 {
				thread.Online = false
			}
		}
		thread.ID = threadNumber
		thread.Isolated = int64InSlice(threadNumber, isolated)
		thread.Thread = uint64(len(resCore.Threads))

		// NUMA node
		numaNode, err := sysfsNumaNode(entryPath)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to find NUMA node")
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
		for _, core := range cpuCores[socket.Socket] {
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

		sort.Slice(socket.Cores, func(i int, j int) bool { return socket.Cores[i].Core < socket.Cores[j].Core })
		cpu.Sockets = append(cpu.Sockets, *socket)
	}
	sort.Slice(cpu.Sockets, func(i int, j int) bool { return cpu.Sockets[i].Socket < cpu.Sockets[j].Socket })

	// Set the architecture name
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get uname")
	}

	cpu.Architecture = strings.TrimRight(string(uname.Machine[:]), "\x00")

	return &cpu, nil
}
