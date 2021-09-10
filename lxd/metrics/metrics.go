package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// NewMetricSet returns a new MetricSet.
func NewMetricSet(labels map[string]string) *MetricSet {
	out := MetricSet{set: make(map[MetricType][]Sample)}

	if labels != nil {
		out.labels = labels
	} else {
		out.labels = make(map[string]string)
	}

	return &out
}

// AddSamples adds samples of the type metricType to the MetricSet.
func (m *MetricSet) AddSamples(metricType MetricType, samples ...Sample) {
	for i := 0; i < len(samples); i++ {
		// Add global labels to samples
		for labelName, labelValue := range m.labels {
			// Ensure we always have a valid Labels map
			if samples[i].Labels == nil {
				samples[i].Labels = make(map[string]string)
			}

			samples[i].Labels[labelName] = labelValue
		}
	}

	m.set[metricType] = append(m.set[metricType], samples...)
}

// Merge merges two MetricSets.
func (m *MetricSet) Merge(metricSet *MetricSet) {
	if metricSet == nil {
		return
	}

	for k := range metricSet.set {
		m.set[k] = append(m.set[k], metricSet.set[k]...)
	}
}

func (m *MetricSet) String() string {
	var out strings.Builder
	metricTypes := []MetricType{}

	// Sort output by metric type name
	for metricType := range m.set {
		metricTypes = append(metricTypes, metricType)
	}

	sort.SliceStable(metricTypes, func(i, j int) bool {
		return int(metricTypes[i]) < int(metricTypes[j])
	})

	for _, metricType := range metricTypes {
		// Add HELP message as specified by OpenMetrics
		_, err := out.WriteString(MetricHeaders[metricType] + "\n")
		if err != nil {
			return ""
		}

		metricTypeName := ""

		if strings.HasSuffix(MetricNames[metricType], "_total") {
			metricTypeName = "counter"
		} else if strings.HasSuffix(MetricNames[metricType], "_bytes") {
			metricTypeName = "gauge"
		}

		// Add TYPE message as specified by OpenMetrics
		_, err = out.WriteString(fmt.Sprintf("# TYPE %s %s\n", MetricNames[metricType], metricTypeName))
		if err != nil {
			return ""
		}

		for _, sample := range m.set[metricType] {
			firstLabel := true
			labels := ""
			labelNames := []string{}

			// Add and sort labels if there are any
			for labelName := range sample.Labels {
				labelNames = append(labelNames, labelName)
			}

			sort.Strings(labelNames)

			for _, labelName := range labelNames {
				if !firstLabel {
					labels += ","
				}

				labels += fmt.Sprintf(`%s="%s"`, labelName, sample.Labels[labelName])
				firstLabel = false
			}

			if labels == "" {
				_, err = out.WriteString(fmt.Sprintf("%s %d\n", MetricNames[metricType], sample.Value))
			} else {
				_, err = out.WriteString(fmt.Sprintf("%s{%s} %d\n", MetricNames[metricType], labels, sample.Value))
			}
			if err != nil {
				return ""
			}
		}
	}

	return out.String()
}

// MetricSetFromAPI converts api.Metrics to a MetricSet, and returns it.
func MetricSetFromAPI(metrics *Metrics, labels map[string]string) (*MetricSet, error) {
	set := NewMetricSet(labels)

	// CPU stats
	for dev, stats := range metrics.CPU {
		getLabels := func(mode string) map[string]string {
			labels := map[string]string{"mode": mode}
			cpu := ""

			if dev != "cpu" {
				fmt.Sscanf(dev, "cpu%s", &cpu)
			}

			if cpu != "" {
				labels["cpu"] = cpu
			}

			return labels
		}

		set.AddSamples(CPUSecondsTotal,
			Sample{
				Value:  stats.SecondsIOWait,
				Labels: getLabels("iowait"),
			},
			Sample{
				Value:  stats.SecondsIRQ,
				Labels: getLabels("irq"),
			},
			Sample{
				Value:  stats.SecondsIdle,
				Labels: getLabels("idle"),
			},
			Sample{
				Value:  stats.SecondsNice,
				Labels: getLabels("nice"),
			},
			Sample{
				Value:  stats.SecondsSoftIRQ,
				Labels: getLabels("softirq"),
			},
			Sample{
				Value:  stats.SecondsSteal,
				Labels: getLabels("steal"),
			},
			Sample{
				Value:  stats.SecondsSystem,
				Labels: getLabels("system"),
			},
			Sample{
				Value:  stats.SecondsUser,
				Labels: getLabels("user"),
			},
		)
	}

	// Disk stats
	for dev, stats := range metrics.Disk {
		labels := map[string]string{"device": dev}

		set.AddSamples(DiskReadBytesTotal, Sample{Value: stats.ReadBytes, Labels: labels})
		set.AddSamples(DiskReadsCompletedTotal, Sample{Value: stats.ReadsCompleted, Labels: labels})
		set.AddSamples(DiskWritesCompletedTotal, Sample{Value: stats.WritesCompleted, Labels: labels})
		set.AddSamples(DiskWrittenBytesTotal, Sample{Value: stats.WrittenBytes, Labels: labels})
	}

	// Filesystem stats
	for dev, stats := range metrics.Filesystem {
		labels := map[string]string{"device": dev, "fstype": stats.FSType, "mountpoint": stats.Mountpoint}

		set.AddSamples(FilesystemAvailBytes, Sample{Value: stats.AvailableBytes, Labels: labels})
		set.AddSamples(FilesystemFreeBytes, Sample{Value: stats.FreeBytes, Labels: labels})
		set.AddSamples(FilesystemSizeBytes, Sample{Value: stats.SizeBytes, Labels: labels})
	}

	// Memory stats
	set.AddSamples(MemoryActiveAnonBytes, Sample{Value: metrics.Memory.ActiveAnonBytes})
	set.AddSamples(MemoryActiveBytes, Sample{Value: metrics.Memory.ActiveBytes})
	set.AddSamples(MemoryActiveFileBytes, Sample{Value: metrics.Memory.ActiveFileBytes})
	set.AddSamples(MemoryCachedBytes, Sample{Value: metrics.Memory.CachedBytes})
	set.AddSamples(MemoryDirtyBytes, Sample{Value: metrics.Memory.DirtyBytes})
	set.AddSamples(MemoryHugePagesFreeBytes, Sample{Value: metrics.Memory.HugepagesFreeBytes})
	set.AddSamples(MemoryHugePagesTotalBytes, Sample{Value: metrics.Memory.HugepagesTotalBytes})
	set.AddSamples(MemoryInactiveAnonBytes, Sample{Value: metrics.Memory.InactiveAnonBytes})
	set.AddSamples(MemoryInactiveBytes, Sample{Value: metrics.Memory.InactiveBytes})
	set.AddSamples(MemoryInactiveFileBytes, Sample{Value: metrics.Memory.InactiveFileBytes})
	set.AddSamples(MemoryMappedBytes, Sample{Value: metrics.Memory.MappedBytes})
	set.AddSamples(MemoryMemAvailableBytes, Sample{Value: metrics.Memory.MemAvailableBytes})
	set.AddSamples(MemoryMemFreeBytes, Sample{Value: metrics.Memory.MemFreeBytes})
	set.AddSamples(MemoryMemTotalBytes, Sample{Value: metrics.Memory.MemTotalBytes})
	set.AddSamples(MemoryRSSBytes, Sample{Value: metrics.Memory.RSSBytes})
	set.AddSamples(MemoryShmemBytes, Sample{Value: metrics.Memory.ShmemBytes})
	set.AddSamples(MemorySwapBytes, Sample{Value: metrics.Memory.SwapBytes})
	set.AddSamples(MemoryUnevictableBytes, Sample{Value: metrics.Memory.UnevictableBytes})
	set.AddSamples(MemoryWritebackBytes, Sample{Value: metrics.Memory.WritebackBytes})

	// Network stats
	for dev, stats := range metrics.Network {
		labels := map[string]string{"device": dev}

		set.AddSamples(NetworkReceiveBytesTotal, Sample{Value: stats.ReceiveBytes, Labels: labels})
		set.AddSamples(NetworkReceiveDropTotal, Sample{Value: stats.ReceiveDrop, Labels: labels})
		set.AddSamples(NetworkReceiveErrsTotal, Sample{Value: stats.ReceiveErrors, Labels: labels})
		set.AddSamples(NetworkReceivePacketsTotal, Sample{Value: stats.ReceivePackets, Labels: labels})
		set.AddSamples(NetworkTransmitBytesTotal, Sample{Value: stats.TransmitBytes, Labels: labels})
		set.AddSamples(NetworkTransmitDropTotal, Sample{Value: stats.TransmitDrop, Labels: labels})
		set.AddSamples(NetworkTransmitErrsTotal, Sample{Value: stats.TransmitErrors, Labels: labels})
		set.AddSamples(NetworkTransmitPacketsTotal, Sample{Value: stats.TransmitPackets, Labels: labels})
	}

	// Procs stats
	set.AddSamples(ProcsTotal, Sample{Value: metrics.ProcessesTotal})

	return set, nil
}
