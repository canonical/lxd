package resources

import (
	"bufio"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/units"
)

var sysDevicesNode = "/sys/devices/system/node"
var sysDevicesSystemMemory = "/sys/devices/system/memory"

type meminfo struct {
	Cached         uint64
	Buffers        uint64
	Total          uint64
	Free           uint64
	Used           uint64
	HugepagesTotal uint64
	HugepagesFree  uint64
	HugepagesSize  uint64
}

func parseMeminfo(path string) (*meminfo, error) {
	memory := meminfo{}

	// Open meminfo
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to open \"%s\"", path)
	}
	defer f.Close()
	memInfo := bufio.NewScanner(f)

	// Get common memory information
	for memInfo.Scan() {
		line := strings.TrimSpace(memInfo.Text())

		// Get key/value
		fields := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(fields[0])
		keyFields := strings.Split(key, " ")
		key = keyFields[len(keyFields)-1]
		value := strings.TrimSpace(fields[1])
		value = strings.Replace(value, " kB", "KiB", 1)

		if key == "MemTotal" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse MemTotal")
			}

			memory.Total = uint64(bytes)
			continue
		}

		if key == "MemFree" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse MemFree")
			}

			memory.Free = uint64(bytes)
			continue
		}

		if key == "MemUsed" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse MemUsed")
			}

			memory.Used = uint64(bytes)
			continue
		}

		if key == "Cached" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse Cached")
			}

			memory.Cached = uint64(bytes)
			continue
		}

		if key == "Buffers" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse Buffers")
			}

			memory.Buffers = uint64(bytes)
			continue
		}

		if key == "HugePages_Total" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse HugePages_Total")
			}

			memory.HugepagesTotal = uint64(bytes)
			continue
		}

		if key == "HugePages_Free" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse HugePages_Free")
			}

			memory.HugepagesFree = uint64(bytes)
			continue
		}

		if key == "Hugepagesize" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to parse Hugepagesize")
			}

			memory.HugepagesSize = uint64(bytes)
			continue
		}
	}

	return &memory, nil
}

func getMemoryBlockSizeBytes() uint64 {
	memoryBlockSizePath := filepath.Join(sysDevicesSystemMemory, "block_size_bytes")

	if !sysfsExists(memoryBlockSizePath) {
		return 0
	}

	// Get block size
	content, err := ioutil.ReadFile(memoryBlockSizePath)
	if err != nil {
		return 0
	}

	blockSize, err := strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
	if err != nil {
		return 0
	}

	return blockSize
}

func getTotalMemory(sysDevicesBase string) uint64 {
	blockSize := getMemoryBlockSizeBytes()
	if blockSize == 0 {
		return 0
	}

	entries, err := ioutil.ReadDir(sysDevicesBase)
	if err != nil {
		return 0
	}

	// Count the number of blocks
	var count uint64
	for _, entry := range entries {
		entryName := entry.Name()
		entryPath := filepath.Join(sysDevicesBase, entryName)

		// Ignore directories not starting with "memory"
		if !strings.HasPrefix(entryName, "memory") {
			continue
		}

		// Ignore invalid entries.
		if !sysfsExists(filepath.Join(entryPath, "online")) {
			continue
		}

		content, err := ioutil.ReadFile(filepath.Join(entryPath, "online"))
		if err != nil {
			return 0
		}

		// Only count the block if it's online
		if strings.TrimSpace(string(content)) == "1" {
			count++
		}
	}

	return blockSize * count
}

// GetMemory returns a filled api.ResourcesMemory struct ready for use by LXD
func GetMemory() (*api.ResourcesMemory, error) {
	memory := api.ResourcesMemory{}

	// Parse main meminfo
	info, err := parseMeminfo("/proc/meminfo")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse /proc/meminfo")
	}

	// Calculate the total memory from /sys/devices/system/memory, as the previously determined
	// value reports the amount of available system memory minus the amount reserved for the kernel.
	// If successful, replace the previous value, retrieved from /proc/meminfo.
	memTotal := getTotalMemory(sysDevicesSystemMemory)
	if memTotal > 0 {
		info.Total = memTotal
	}

	// Fill used values
	memory.HugepagesUsed = (info.HugepagesTotal - info.HugepagesFree) * info.HugepagesSize
	memory.HugepagesTotal = info.HugepagesTotal * info.HugepagesSize
	memory.HugepagesSize = info.HugepagesSize

	memory.Used = info.Total - info.Free - info.Cached - info.Buffers
	memory.Total = info.Total

	// Get NUMA information
	if sysfsExists(sysDevicesNode) {
		memory.Nodes = []api.ResourcesMemoryNode{}

		// List all the nodes
		entries, err := ioutil.ReadDir(sysDevicesNode)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysDevicesNode)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysDevicesNode, entryName)

			if !sysfsExists(filepath.Join(entryPath, "meminfo")) {
				continue
			}

			// Get NUMA node number
			nodeName := strings.TrimPrefix(entryName, "node")
			nodeNumber, err := strconv.ParseUint(nodeName, 10, 64)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to find NUMA node")
			}

			// Parse NUMA meminfo
			info, err := parseMeminfo(filepath.Join(entryPath, "meminfo"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse \"%s\"", filepath.Join(entryPath, "meminfo"))
			}

			// Setup the entry
			node := api.ResourcesMemoryNode{}
			node.NUMANode = nodeNumber

			node.HugepagesUsed = (info.HugepagesTotal - info.HugepagesFree) * memory.HugepagesSize
			node.HugepagesTotal = info.HugepagesTotal * memory.HugepagesSize

			node.Used = info.Used
			node.Total = info.Total

			// Calculate the total memory from /sys/devices/system/node/memory, as the previously determined
			// value reports the amount of available system memory minus the amount reserved for the kernel.
			// If successful, replace the previous value, retrieved from /sys/devices/system/node/meminfo.
			memTotal := getTotalMemory(entryPath)
			if memTotal > 0 {
				node.Total = memTotal
			}

			memory.Nodes = append(memory.Nodes, node)
		}
	}

	return &memory, nil
}
