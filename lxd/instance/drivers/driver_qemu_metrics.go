package drivers

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/instance/drivers/qmp"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/metrics"
	"github.com/lxc/lxd/shared"
)

func (d *qemu) getQemuMetrics() (*metrics.MetricSet, error) {
	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return nil, err
	}

	out := metrics.Metrics{}

	cpuStats, err := d.getQemuCPUMetrics(monitor)
	if err != nil {
		d.logger.Warn("Failed to get CPU metrics", log.Ctx{"err": err})
	} else {
		out.CPU = cpuStats
	}

	memoryStats, err := d.getQemuMemoryMetrics(monitor)
	if err != nil {
		d.logger.Warn("Failed to get memory metrics", log.Ctx{"err": err})
	} else {
		out.Memory = memoryStats
	}

	diskStats, err := d.getQemuDiskMetrics(monitor)
	if err != nil {
		d.logger.Warn("Failed to get memory metrics", log.Ctx{"err": err})
	} else {
		out.Disk = diskStats
	}

	networkState, err := d.getNetworkState()
	if err != nil {
		d.logger.Warn("Failed to get network metrics", log.Ctx{"err": err})
	} else {

		out.Network = make(map[string]metrics.NetworkMetrics)

		for name, state := range networkState {
			out.Network[name] = metrics.NetworkMetrics{
				ReceiveBytes:    uint64(state.Counters.BytesReceived),
				ReceiveDrop:     uint64(state.Counters.PacketsDroppedInbound),
				ReceiveErrors:   uint64(state.Counters.ErrorsReceived),
				ReceivePackets:  uint64(state.Counters.PacketsReceived),
				TransmitBytes:   uint64(state.Counters.BytesSent),
				TransmitDrop:    uint64(state.Counters.PacketsDroppedOutbound),
				TransmitErrors:  uint64(state.Counters.ErrorsSent),
				TransmitPackets: uint64(state.Counters.PacketsSent),
			}
		}
	}

	metricSet, err := metrics.MetricSetFromAPI(&out, map[string]string{"project": d.project, "name": d.name, "type": instancetype.VM.String()})
	if err != nil {
		return nil, err
	}

	return metricSet, nil
}

func (d *qemu) getQemuDiskMetrics(monitor *qmp.Monitor) (map[string]metrics.DiskMetrics, error) {
	stats, err := monitor.GetBlockStats()
	if err != nil {
		return nil, err
	}

	out := make(map[string]metrics.DiskMetrics)

	for dev, stat := range stats {
		out[dev] = metrics.DiskMetrics{
			ReadBytes:       uint64(stat.BytesRead),
			ReadsCompleted:  uint64(stat.ReadsCompleted),
			WrittenBytes:    uint64(stat.BytesWritten),
			WritesCompleted: uint64(stat.WritesCompleted),
		}
	}

	return out, nil
}

func (d *qemu) getQemuMemoryMetrics(monitor *qmp.Monitor) (metrics.MemoryMetrics, error) {
	stats, err := monitor.GetMemoryStats()
	if err != nil {
		return metrics.MemoryMetrics{}, err
	}

	out := metrics.MemoryMetrics{
		MemAvailableBytes: uint64(stats.AvailableMemory),
		MemFreeBytes:      uint64(stats.FreeMemory),
		MemTotalBytes:     uint64(stats.TotalMemory),
	}

	return out, nil
}

func (d *qemu) getQemuCPUMetrics(monitor *qmp.Monitor) (map[string]metrics.CPUMetrics, error) {
	// Get CPU metrics
	threadIDs, err := monitor.GetCPUs()
	if err != nil {
		return nil, err
	}

	cpuMetrics := map[string]metrics.CPUMetrics{}

	for i, threadID := range threadIDs {
		pid, err := ioutil.ReadFile(d.pidFilePath())
		if err != nil {
			return nil, err
		}

		statFile := filepath.Join("/proc", strings.TrimSpace(string(pid)), "task", strconv.Itoa(threadID), "stat")

		if !shared.PathExists(statFile) {
			continue
		}

		content, err := ioutil.ReadFile(statFile)
		if err != nil {
			return nil, err
		}

		fields := strings.Fields(string(content))

		stats := metrics.CPUMetrics{}

		stats.SecondsUser, err = strconv.ParseFloat(fields[13], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[13], err)
		}

		guestTime, err := strconv.ParseFloat(fields[42], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[42], err)
		}

		// According to proc(5), utime includes guest_time which therefore needs to be subtracted to get the correct time.
		stats.SecondsUser -= guestTime
		stats.SecondsUser /= 100

		stats.SecondsSystem, err = strconv.ParseFloat(fields[14], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[14], err)
		}

		stats.SecondsSystem /= 100

		cpuMetrics[fmt.Sprintf("cpu%d", i)] = stats
	}

	return cpuMetrics, nil
}
