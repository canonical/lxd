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

	// Method 1: Calculate RSS using kernel memory accounting
	// This is the most accurate and efficient method as it uses the kernel's own memory accounting
	if foundMemTotal && foundMemFree && foundBuffers && foundCached && foundShmem {
		// Formula: RSS = MemTotal - (MemFree + Buffers + Cached - Shmem)
		// This matches how tools like 'free' calculate used memory
		rssBytes := memTotalBytes - (memFreeBytes + buffersBytes + cachedBytes - shmemBytes)
		out.RSSBytes = rssBytes
		logger.Debug("RSS metric using kernel memory accounting", 
					logger.Ctx{"formula": "MemTotal-(MemFree+Buffers+Cached-Shmem)", "value": rssBytes})
		return out, nil
	}

	// Method 2: Process summation (if Method 1 fails)
	// Only use this method if system load is reasonable
	isLowLoad, err := isSystemLoadReasonable()
	if err == nil && isLowLoad {
		logger.Debug("Attempting process summation fallback for RSS calculation")
		rssTotal, err := sumProcessRSS()
		if err == nil {
			out.RSSBytes = rssTotal
			logger.Debug("RSS metric using process summation fallback", 
						logger.Ctx{"value": rssTotal})
			return out, nil
		} else {
			logger.Warn("Process summation fallback failed", logger.Ctx{"error": err})
		}
	} else {
		logger.Debug("Skipping process summation fallback due to high system load or error", 
				   logger.Ctx{"error": err})
	}

	// Method 3: Agent-only fallback
	// This is the last resort and only provides the RSS of the lxd-agent process
	statusContent, err := os.ReadFile("/proc/self/status")
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(statusContent))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}

			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}

			rssValue, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				// VmRSS is in kB
				out.RSSBytes = rssValue * 1024
				logger.Debug("RSS metric using agent-only fallback (limited accuracy)", 
							logger.Ctx{"value": out.RSSBytes})
				break
			}
		}
	}

	return out, nil
}

// isSystemLoadReasonable checks if the system load is low enough to
// safely run the process summation method
func isSystemLoadReasonable() (bool, error) {
	loadavg, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return false, fmt.Errorf("Failed to read /proc/loadavg: %w", err)
	}
	
	fields := strings.Fields(string(loadavg))
	if len(fields) == 0 {
		return false, fmt.Errorf("Invalid /proc/loadavg content")
	}
	
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return false, fmt.Errorf("Failed to parse load average: %w", err)
	}
	
	// Consider load reasonable if load average is under 5.0
	// This threshold could be adjusted based on system capacity
	return load < 5.0, nil
}

// sumProcessRSS calculates system-wide RSS by summing across all processes
// This is a fallback method used when kernel accounting fails
func sumProcessRSS() (uint64, error) {
	var totalRSS uint64
	var errorCount int
	
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("Failed to read /proc directory: %w", err)
	}

	for _, entry := range entries {
		// Skip non-PID directories
		if !entry.IsDir() {
			continue
		}

		// Check if directory name is a number (PID)
		pid := entry.Name()
		if _, err := strconv.ParseUint(pid, 10, 64); err != nil {
			continue
		}

		// Read process status file
		statusPath := filepath.Join("/proc", pid, "status")
		content, err := os.ReadFile(statusPath)
		if err != nil {
			// Process may have terminated - skip but count the error
			errorCount++
			continue
		}

		// Parse VmRSS value
		scanner := bufio.NewScanner(bytes.NewReader(content))
		foundRSS := false
		
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "VmRSS:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					// Extract numeric value
					rssValue, err := strconv.ParseUint(fields[1], 10, 64)
					if err == nil {
						// Add to total (convert from kB to bytes)
						totalRSS += rssValue * 1024
						foundRSS = true
					}
				}
				break
			}
		}
		
		if !foundRSS {
			// Some kernel threads might not have VmRSS - this is normal
			continue
		}
	}

	// If we had too many errors, the result might be inaccurate
	if errorCount > 10 {
		logger.Warn("High error count during process RSS summation", 
				   logger.Ctx{"errors": errorCount})
	}

	// Only fail if we couldn't read any processes
	if totalRSS == 0 && errorCount > 0 {
		return 0, fmt.Errorf("Failed to read RSS for any process")
	}

	return totalRSS, nil
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
