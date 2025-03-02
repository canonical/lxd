package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// These mountpoints are excluded as they are irrelevant for metrics.
// /var/lib/docker/* subdirectories are excluded for this reason: https://github.com/prometheus/node_exporter/pull/1003
var defMountPointsExcluded = regexp.MustCompile(`^/(?:dev|proc|sys|var/lib/docker/.+)(?:$|/)`)
var defFSTypesExcluded = []string{
	"autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs", "fusectl", "fuse.lxcfs", "hugetlbfs", "iso9660", "mqueue", "nsfs", "overlay", "proc", "procfs", "pstore", "rpc_pipefs", "securityfs", "selinuxfs", "squashfs", "sysfs", "tracefs"}

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet},
}

func metricsGet(d *Daemon, r *http.Request) response.Response {
	out := metrics.Metrics{}

	diskStats, err := getDiskMetrics()
	if err != nil {
		logger.Warn("Failed to get disk metrics", logger.Ctx{"err": err})
	} else {
		out.Disk = diskStats
	}

	filesystemStats, err := getFilesystemMetrics()
	if err != nil {
		logger.Warn("Failed to get filesystem metrics", logger.Ctx{"err": err})
	} else {
		out.Filesystem = filesystemStats
	}

	memStats, err := getMemoryMetrics()
	if err != nil {
		logger.Warn("Failed to get memory metrics", logger.Ctx{"err": err})
	} else {
		out.Memory = memStats
	}

	netStats, err := getNetworkMetrics()
	if err != nil {
		logger.Warn("Failed to get network metrics", logger.Ctx{"err": err})
	} else {
		out.Network = netStats
	}

	out.ProcessesTotal, err = getTotalProcesses()
	if err != nil {
		logger.Warn("Failed to get total processes", logger.Ctx{"err": err})
	}

	cpuStats, err := getCPUMetrics()
	if err != nil {
		logger.Warn("Failed to get CPU metrics", logger.Ctx{"err": err})
	} else {
		out.CPU = cpuStats
	}

	return response.SyncResponse(true, &out)
}

func getCPUMetrics() (map[string]metrics.CPUMetrics, error) {
	stats, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/stat: %w", err)
	}

	out := map[string]metrics.CPUMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(stats))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		// Only consider CPU info, skip everything else. Skip aggregated CPU stats since there will
		// be stats for each individual CPU.
		if !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}

		// Validate the number of fields only for lines starting with "cpu".
		if len(fields) < 9 {
			return nil, fmt.Errorf("Invalid /proc/stat content: %q", line)
		}

		stats := metrics.CPUMetrics{}

		stats.SecondsUser, err = strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[1], err)
		}

		stats.SecondsUser /= 100

		stats.SecondsNice, err = strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[2], err)
		}

		stats.SecondsNice /= 100

		stats.SecondsSystem, err = strconv.ParseFloat(fields[3], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.SecondsSystem /= 100

		stats.SecondsIdle, err = strconv.ParseFloat(fields[4], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[4], err)
		}

		stats.SecondsIdle /= 100

		stats.SecondsIOWait, err = strconv.ParseFloat(fields[5], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[5], err)
		}

		stats.SecondsIOWait /= 100

		stats.SecondsIRQ, err = strconv.ParseFloat(fields[6], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[6], err)
		}

		stats.SecondsIRQ /= 100

		stats.SecondsSoftIRQ, err = strconv.ParseFloat(fields[7], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[7], err)
		}

		stats.SecondsSoftIRQ /= 100

		stats.SecondsSteal, err = strconv.ParseFloat(fields[8], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[8], err)
		}

		stats.SecondsSteal /= 100

		out[fields[0]] = stats
	}

	return out, nil
}

func getTotalProcesses() (uint64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("Failed to read dir %q: %w", "/proc", err)
	}

	pidCount := uint64(0)

	for _, entry := range entries {
		// Skip everything which isn't a directory
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip all non-PID directories
		_, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			continue
		}

		cmdlinePath := filepath.Join("/proc", name, "cmdline")

		cmdline, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		if string(cmdline) == "" {
			continue
		}

		pidCount++
	}

	return pidCount, nil
}

func getDiskMetrics() (map[string]metrics.DiskMetrics, error) {
	diskStats, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/diskstats: %w", err)
	}

	out := map[string]metrics.DiskMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(diskStats))

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, fmt.Errorf("Invalid /proc/diskstats content: %q", line)
		}

		stats := metrics.DiskMetrics{}

		stats.ReadsCompleted, err = strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		sectorsRead, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.ReadBytes = sectorsRead * 512

		stats.WritesCompleted, err = strconv.ParseUint(fields[7], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		sectorsWritten, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.WrittenBytes = sectorsWritten * 512

		out[fields[2]] = stats
	}

	return out, nil
}

func getFilesystemMetrics() (map[string]metrics.FilesystemMetrics, error) {
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/mounts: %w", err)
	}

	out := map[string]metrics.FilesystemMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(mounts))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 3 {
			return nil, fmt.Errorf("Invalid /proc/mounts content: %q", line)
		}

		// Skip uninteresting mounts
		if shared.ValueInSlice(fields[2], defFSTypesExcluded) || defMountPointsExcluded.MatchString(fields[1]) {
			continue
		}

		stats := metrics.FilesystemMetrics{}

		stats.Mountpoint = fields[1]

		statfs, err := filesystem.StatVFS(stats.Mountpoint)
		if err != nil {
			return nil, fmt.Errorf("Failed to stat %s: %w", stats.Mountpoint, err)
		}

		fsType, err := filesystem.FSTypeToName(int32(statfs.Type))
		if err == nil {
			stats.FSType = fsType
		}

		stats.AvailableBytes = statfs.Bavail * uint64(statfs.Bsize)
		stats.FreeBytes = statfs.Bfree * uint64(statfs.Bsize)
		stats.SizeBytes = statfs.Blocks * uint64(statfs.Bsize)

		out[fields[0]] = stats
	}

	return out, nil
}

func getMemoryMetrics() (metrics.MemoryMetrics, error) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return metrics.MemoryMetrics{}, fmt.Errorf("Failed to read /proc/meminfo: %w", err)
	}

	out := metrics.MemoryMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(content))

	// Variables for accurate RSS calculation using kernel memory accounting
	var memTotalBytes, memFreeBytes, buffersBytes, cachedBytes, shmemBytes uint64
	var foundMemTotal, foundMemFree, foundBuffers, foundCached, foundShmem bool

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 2 {
			return metrics.MemoryMetrics{}, fmt.Errorf("Invalid /proc/meminfo content: %q", line)
		}

		fields[0] = strings.TrimRight(fields[0], ":")

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return metrics.MemoryMetrics{}, fmt.Errorf("Failed to parse %q: %w", fields[1], err)
		}

		// Multiply suffix (kB)
		if len(fields) == 3 {
			value *= 1024
		}

		// Parse fields for both existing metrics and RSS calculation
		switch fields[0] {
		case "Active":
			out.ActiveBytes = value
		case "Active(anon)":
			out.ActiveAnonBytes = value
		case "Active(file)":
			out.ActiveFileBytes = value
		case "Buffers":
			buffersBytes = value
			foundBuffers = true
		case "Cached":
			cachedBytes = value
			foundCached = true
			out.CachedBytes = value
		case "Dirty":
			out.DirtyBytes = value
		case "HugePages_Free":
			out.HugepagesFreeBytes = value
		case "HugePages_Total":
			out.HugepagesTotalBytes = value
		case "Inactive":
			out.InactiveBytes = value
		case "Inactive(anon)":
			out.InactiveAnonBytes = value
		case "Inactive(file)":
			out.InactiveFileBytes = value
		case "Mapped":
			out.MappedBytes = value
		case "MemAvailable":
			out.MemAvailableBytes = value
		case "MemFree":
			memFreeBytes = value
			foundMemFree = true
			out.MemFreeBytes = value
		case "MemTotal":
			memTotalBytes = value
			foundMemTotal = true
			out.MemTotalBytes = value
		case "Shmem":
			shmemBytes = value
			foundShmem = true
			out.ShmemBytes = value
		case "SwapCached":
			out.SwapBytes = value
		case "Unevictable":
			out.UnevictableBytes = value
		case "Writeback":
			out.WritebackBytes = value
		}
	}

	// Calculate RSS using kernel memory accounting
	// This is the most accurate and efficient method as it uses the kernel's own memory accounting
	if foundMemTotal && foundMemFree && foundBuffers && foundCached && foundShmem {
		// Formula: RSS = MemTotal - (MemFree + Buffers + Cached - Shmem)
		// This matches how tools like 'free' calculate used memory
		rssBytes := memTotalBytes - (memFreeBytes + buffersBytes + cachedBytes - shmemBytes)
		out.RSSBytes = rssBytes
		logger.Debug("RSS metric using kernel memory accounting", 
					logger.Ctx{"formula": "MemTotal-(MemFree+Buffers+Cached-Shmem)", "value": rssBytes})
	} else {
		// Log warning if we can't calculate RSS using kernel accounting
		logger.Warn("Kernel RSS calculation failed - omitting RSS metric", 
				   logger.Ctx{"foundMemTotal": foundMemTotal, "foundMemFree": foundMemFree, 
							 "foundBuffers": foundBuffers, "foundCached": foundCached, 
							 "foundShmem": foundShmem})
	}

	return out, nil
}

func getNetworkMetrics() (map[string]metrics.NetworkMetrics, error) {
	out := map[string]metrics.NetworkMetrics{}

	for dev, state := range networkState() {
		stats := metrics.NetworkMetrics{}

		stats.ReceiveBytes = uint64(state.Counters.BytesReceived)
		stats.ReceiveDrop = uint64(state.Counters.PacketsDroppedInbound)
		stats.ReceiveErrors = uint64(state.Counters.ErrorsReceived)
		stats.ReceivePackets = uint64(state.Counters.PacketsReceived)
		stats.TransmitBytes = uint64(state.Counters.BytesSent)
		stats.TransmitDrop = uint64(state.Counters.PacketsDroppedOutbound)
		stats.TransmitErrors = uint64(state.Counters.ErrorsSent)
		stats.TransmitPackets = uint64(state.Counters.PacketsSent)

		out[dev] = stats
	}

	return out, nil
}
