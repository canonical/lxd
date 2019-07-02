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

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

var sysDevicesCPU = "/sys/devices/system/cpu"

func getCPUCache(path string) ([]api.ResourcesCPUCache, error) {
	caches := []api.ResourcesCPUCache{}

	// List all the caches
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
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

		// Get the cache level
		cacheLevel, err := readUint(filepath.Join(entryPath, "level"))
		if err != nil {
			return nil, err
		}

		cache.Level = cacheLevel

		// Get the cache size
		content, err := ioutil.ReadFile(filepath.Join(entryPath, "size"))
		if err != nil {
			return nil, err
		}
		cacheSizeStr := strings.TrimSpace(string(content))

		// Handle cache sizes in KiB
		cacheSizeMultiplier := uint64(1)
		if strings.HasSuffix(cacheSizeStr, "K") {
			cacheSizeMultiplier = 1024
			cacheSizeStr = strings.TrimSuffix(cacheSizeStr, "K")
		}

		cacheSize, err := strconv.ParseUint((cacheSizeStr), 10, 64)
		if err != nil {
			return nil, err
		}

		cache.Size = cacheSize * cacheSizeMultiplier

		// Get the cache type
		cacheType, err := ioutil.ReadFile(filepath.Join(entryPath, "type"))
		if err != nil {
			return nil, err
		}

		cache.Type = strings.TrimSpace(string(cacheType))

		// Add to the list
		caches = append(caches, cache)
	}

	return caches, nil
}

// GetCPU returns a filled api.ResourcesCPU struct ready for use by LXD
func GetCPU() (*api.ResourcesCPU, error) {
	cpu := api.ResourcesCPU{}

	// Temporary storage
	cpuSockets := map[uint64]*api.ResourcesCPUSocket{}
	cpuCores := map[uint64]map[uint64]*api.ResourcesCPUCore{}

	// Open cpuinfo
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cpuInfo := bufio.NewScanner(f)

	// List all the CPUs
	entries, err := ioutil.ReadDir(sysDevicesCPU)
	if err != nil {
		return nil, err
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
		if err != nil {
			return nil, err
		}

		cpuCore, err := readUint(filepath.Join(entryPath, "topology", "core_id"))
		if err != nil {
			return nil, err
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
					return nil, err
				}

				resSocket.Cache = socketCache
			}

			// Frequency
			if sysfsExists(filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq")) {
				freqMinimum, err := readUint(filepath.Join(entryPath, "cpufreq", "cpuinfo_min_freq"))
				if err != nil {
					return nil, err
				}

				resSocket.FrequencyMinimum = freqMinimum / 1000
			} else if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_min_freq")) {
				freqMinimum, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_min_freq"))
				if err != nil {
					return nil, err
				}

				resSocket.FrequencyMinimum = freqMinimum / 1000
			}

			if sysfsExists(filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq")) {
				freqTurbo, err := readUint(filepath.Join(entryPath, "cpufreq", "cpuinfo_max_freq"))
				if err != nil {
					return nil, err
				}

				resSocket.FrequencyTurbo = freqTurbo / 1000
			} else if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_max_freq")) {
				freqTurbo, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_max_freq"))
				if err != nil {
					return nil, err
				}

				resSocket.FrequencyTurbo = freqTurbo / 1000
			}

			// Record the data
			cpuSockets[cpuSocket] = resSocket
			cpuCores[cpuSocket] = map[uint64]*api.ResourcesCPUCore{}
		}

		// Grab core data if needed
		resCore, ok := cpuCores[cpuSocket][cpuCore]
		if !ok {
			resCore = &api.ResourcesCPUCore{}

			// Core number
			resCore.Core = cpuCore

			// NUMA node
			numaNode, err := sysfsNumaNode(entryPath)
			if err != nil {
				return nil, err
			}

			resCore.NUMANode = numaNode

			// Frequency
			if sysfsExists(filepath.Join(entryPath, "cpufreq", "scaling_cur_freq")) {
				freqCurrent, err := readUint(filepath.Join(entryPath, "cpufreq", "scaling_cur_freq"))
				if err != nil {
					return nil, err
				}

				resCore.Frequency = freqCurrent / 1000
			}

			// Initialize thread list
			resCore.Threads = []api.ResourcesCPUThread{}

			// Record the data
			cpuCores[cpuSocket][cpuCore] = resCore
		}

		// Grab thread data
		threadNumber, err := strconv.ParseInt(strings.TrimPrefix(entryName, "cpu"), 10, 64)
		if err != nil {
			return nil, err
		}

		thread := api.ResourcesCPUThread{}
		thread.Online = true
		if sysfsExists(filepath.Join(entryPath, "online")) {
			online, err := readUint(filepath.Join(entryPath, "online"))
			if err != nil {
				return nil, err
			}

			if online == 0 {
				thread.Online = false
			}
		}
		thread.ID = threadNumber
		thread.Thread = uint64(len(resCore.Threads))

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
		return nil, err
	}

	cpu.Architecture = strings.TrimRight(string(uname.Machine[:]), "\x00")

	return &cpu, nil
}
