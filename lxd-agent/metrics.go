package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/metrics"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/storage/filesystem"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// These mountpoints are excluded as they are irrelevant for metrics.
// /var/lib/docker/* subdirectories are excluded for this reason: https://github.com/prometheus/node_exporter/pull/1003
var defMountPointsExcluded = regexp.MustCompile("^/(dev|proc|sys|var/lib/docker/.+)($|/)")
var defFSTypesExcluded = []string{
	"autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs", "fusectl", "hugetlbfs", "iso9660", "mqueue", "nsfs", "overlay", "proc", "procfs", "pstore", "rpc_pipefs", "securityfs", "selinuxfs", "squashfs", "sysfs", "tracefs"}

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet},
}

func metricsGet(d *Daemon, r *http.Request) response.Response {
	out := metrics.Metrics{}

	diskStats, err := getDiskMetrics(d)
	if err != nil {
		logger.Warn("Failed to get disk metrics", log.Ctx{"err": err})
	} else {
		out.Disk = diskStats
	}

	filesystemStats, err := getFilesystemMetrics(d)
	if err != nil {
		logger.Warn("Failed to get filesystem metrics", log.Ctx{"err": err})
	} else {
		out.Filesystem = filesystemStats
	}

	memStats, err := getMemoryMetrics(d)
	if err != nil {
		logger.Warn("Failed to get memory metrics", log.Ctx{"err": err})
	} else {
		out.Memory = memStats
	}

	netStats, err := getNetworkMetrics(d)
	if err != nil {
		logger.Warn("Failed to get network metrics", log.Ctx{"err": err})
	} else {
		out.Network = netStats
	}

	out.ProcessesTotal, err = getTotalProcesses(d)
	if err != nil {
		logger.Warn("Failed to get total processes", log.Ctx{"err": err})
	}

	cpuStats, err := getCPUMetrics(d)
	if err != nil {
		logger.Warn("Failed to get CPU metrics", log.Ctx{"err": err})
	} else {
		out.CPU = cpuStats
	}

	return response.SyncResponse(true, &out)
}

func getCPUMetrics(d *Daemon) (map[string]metrics.CPUMetrics, error) {
	stats, err := ioutil.ReadFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/stat: %w", err)
	}

	out := map[string]metrics.CPUMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(stats))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		// Only consider CPU info, skip everything else. Skip aggregated CPU stats since there will
		// be stats for each individual CPU.
		if !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}

		stats := metrics.CPUMetrics{}

		stats.SecondsUser, err = strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[1], err)
		}

		stats.SecondsUser *= 10

		stats.SecondsNice, err = strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[2], err)
		}

		stats.SecondsNice *= 10

		stats.SecondsSystem, err = strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.SecondsSystem *= 10

		stats.SecondsIdle, err = strconv.ParseUint(fields[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[4], err)
		}

		stats.SecondsIdle *= 10

		stats.SecondsIOWait, err = strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[5], err)
		}

		stats.SecondsIdle *= 10

		stats.SecondsIRQ, err = strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[6], err)
		}

		stats.SecondsIRQ *= 10

		stats.SecondsSoftIRQ, err = strconv.ParseUint(fields[7], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[7], err)
		}

		stats.SecondsSoftIRQ *= 10

		stats.SecondsSteal, err = strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[8], err)
		}

		stats.SecondsSteal *= 10

		out[fields[0]] = stats
	}

	return out, nil
}

func getTotalProcesses(d *Daemon) (uint64, error) {
	entries, err := ioutil.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("Failed to read dir %q: %w", "/proc", err)
	}

	re := regexp.MustCompile(`^[[:digit:]]+$`)
	pidCount := uint64(0)

	for _, entry := range entries {
		// Skip everything which isn't a directory
		if !entry.IsDir() {
			continue
		}

		// Skip all non-PID directories
		if !re.MatchString(entry.Name()) {
			continue
		}

		cmdlinePath := filepath.Join("/proc", entry.Name(), "cmdline")

		cmdline, err := ioutil.ReadFile(cmdlinePath)
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

func getDiskMetrics(d *Daemon) (map[string]metrics.DiskMetrics, error) {
	diskStats, err := ioutil.ReadFile("/proc/diskstats")
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

func getFilesystemMetrics(d *Daemon) (map[string]metrics.FilesystemMetrics, error) {
	mounts, err := ioutil.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/mounts: %w", err)
	}

	out := map[string]metrics.FilesystemMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(mounts))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		// Skip uninteresting mounts
		if shared.StringInSlice(fields[2], defFSTypesExcluded) || defMountPointsExcluded.MatchString(fields[1]) {
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

		stats.SizeBytes = statfs.Blocks * uint64(statfs.Bsize)
		stats.SizeBytes = statfs.Blocks * uint64(statfs.Bsize)
		stats.SizeBytes = statfs.Blocks * uint64(statfs.Bsize)
		stats.SizeBytes = statfs.Blocks * uint64(statfs.Bsize)

		out[fields[0]] = stats
	}

	return out, nil
}

func getMemoryMetrics(d *Daemon) (metrics.MemoryMetrics, error) {
	content, err := ioutil.ReadFile("/proc/meminfo")
	if err != nil {
		return metrics.MemoryMetrics{}, fmt.Errorf("Failed to read /proc/meminfo: %w", err)
	}

	out := metrics.MemoryMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(content))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		fields[0] = strings.TrimRight(fields[0], ":")

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return metrics.MemoryMetrics{}, fmt.Errorf("Failed to parse %q: %w", fields[1], err)
		}

		// Multiply suffix (kB)
		if len(fields) == 3 {
			value *= 1024
		}

		// FIXME: Missing RSS
		switch fields[0] {
		case "Active":
			out.ActiveBytes = value
		case "Active(anon)":
			out.ActiveAnonBytes = value
		case "Active(file)":
			out.ActiveFileBytes = value
		case "Cached":
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
			out.MemFreeBytes = value
		case "MemTotal":
			out.MemTotalBytes = value
		case "Shmem":
			out.ShmemBytes = value
		case "SwapCached":
			out.SwapBytes = value
		case "Unevictable":
			out.UnevictableBytes = value
		case "Writeback":
			out.WritebackBytes = value
		}
	}

	return out, nil
}

func getNetworkMetrics(d *Daemon) (map[string]metrics.NetworkMetrics, error) {
	out := map[string]metrics.NetworkMetrics{}

	for dev, state := range networkState() {
		stats := metrics.NetworkMetrics{}

		stats.ReceiveBytes = uint64(state.Counters.BytesReceived)
		stats.ReceiveDrop = uint64(state.Counters.PacketsDroppedInbound)
		stats.ReceiveErrors = uint64(state.Counters.ErrorsReceived)
		stats.ReceivePackets = uint64(state.Counters.PacketsReceived)
		stats.TransmitBytes = uint64(state.Counters.BytesReceived)
		stats.TransmitDrop = uint64(state.Counters.PacketsDroppedOutbound)
		stats.TransmitErrors = uint64(state.Counters.ErrorsSent)
		stats.TransmitPackets = uint64(state.Counters.PacketsSent)

		out[dev] = stats
	}

	return out, nil
}
