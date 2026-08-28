package resources

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/units"
)

var sysDevicesNode = "/sys/devices/system/node"

type meminfo struct {
	Total          uint64
	Free           uint64
	Available      uint64
	FilePages      uint64
	Shmem          uint64
	SReclaimable   uint64
	HugepagesTotal uint64
	HugepagesFree  uint64
	HugepagesSize  uint64
}

func parseMeminfo(path string) (*meminfo, error) {
	memory := meminfo{}

	// Open meminfo
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed opening %q: %w", path, err)
	}

	defer func() { _ = f.Close() }()
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
				return nil, fmt.Errorf("Failed parsing MemTotal: %w", err)
			}

			memory.Total = uint64(bytes)
			continue
		}

		if key == "MemFree" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing MemFree: %w", err)
			}

			memory.Free = uint64(bytes)
			continue
		}

		if key == "MemAvailable" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing MemAvailable: %w", err)
			}

			memory.Available = uint64(bytes)
			continue
		}

		if key == "FilePages" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing FilePages: %w", err)
			}

			memory.FilePages = uint64(bytes)
			continue
		}

		if key == "Shmem" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing Shmem: %w", err)
			}

			memory.Shmem = uint64(bytes)
			continue
		}

		if key == "SReclaimable" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing SReclaimable: %w", err)
			}

			memory.SReclaimable = uint64(bytes)
			continue
		}

		if key == "HugePages_Total" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing HugePages_Total: %w", err)
			}

			memory.HugepagesTotal = uint64(bytes)
			continue
		}

		if key == "HugePages_Free" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing HugePages_Free: %w", err)
			}

			memory.HugepagesFree = uint64(bytes)
			continue
		}

		if key == "Hugepagesize" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing Hugepagesize: %w", err)
			}

			memory.HugepagesSize = uint64(bytes)
			continue
		}
	}

	if memInfo.Err() != nil {
		return nil, fmt.Errorf("Failed scanning %q: %w", path, memInfo.Err())
	}

	return &memory, nil
}

// GetMemory returns a filled api.ResourcesMemory struct ready for use by LXD.
func GetMemory() (*api.ResourcesMemory, error) {
	memory := api.ResourcesMemory{}

	// Parse main meminfo
	info, err := parseMeminfo("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("Failed parsing /proc/meminfo: %w", err)
	}

	// Fill used values
	memory.HugepagesUsed = (info.HugepagesTotal - info.HugepagesFree) * info.HugepagesSize
	memory.HugepagesTotal = info.HugepagesTotal * info.HugepagesSize
	memory.HugepagesSize = info.HugepagesSize

	memory.Total = info.Total
	if info.Total > info.Available {
		memory.Used = info.Total - info.Available
	}

	// Get NUMA information
	if pathExists(sysDevicesNode) {
		memory.Nodes = []api.ResourcesMemoryNode{}

		// List all the nodes
		entries, err := os.ReadDir(sysDevicesNode)
		if err != nil {
			return nil, fmt.Errorf("Failed listing %q: %w", sysDevicesNode, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysDevicesNode, entryName)

			if !pathExists(filepath.Join(entryPath, "meminfo")) {
				continue
			}

			// Get NUMA node number
			nodeName := strings.TrimPrefix(entryName, "node")
			nodeNumber, err := strconv.ParseUint(nodeName, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed finding NUMA node: %w", err)
			}

			// Parse NUMA meminfo
			meminfoPath := filepath.Join(entryPath, "meminfo")
			info, err := parseMeminfo(meminfoPath)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", meminfoPath, err)
			}

			// Setup the entry
			node := api.ResourcesMemoryNode{}
			node.NUMANode = nodeNumber

			node.HugepagesUsed = (info.HugepagesTotal - info.HugepagesFree) * memory.HugepagesSize
			node.HugepagesTotal = info.HugepagesTotal * memory.HugepagesSize

			// The kernel does not report MemAvailable per NUMA node, so we approximate it with
			// the page cache excluding shmem plus the reclaimable slab
			reclaimable := info.SReclaimable
			if info.FilePages > info.Shmem {
				reclaimable += info.FilePages - info.Shmem
			}

			node.Total = info.Total
			if info.Total > info.Free+reclaimable {
				node.Used = info.Total - info.Free - reclaimable
			}

			memory.Nodes = append(memory.Nodes, node)
		}
	}

	return &memory, nil
}
