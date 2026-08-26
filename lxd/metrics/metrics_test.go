package metrics

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/entity"
)

// BenchmarkMetricSetString benchmarks the MetricSet.String method, which is called on every metrics scrape.
// The fixture simulates 10 instances each with 2 CPUs, 2 disks, and 2 filesystems to produce a
// realistic label cardinality (4–5 labels/sample) and sample count (~600 samples total).
func BenchmarkMetricSetString(b *testing.B) {
	cpuModes := []string{"iowait", "irq", "idle", "nice", "softirq", "steal", "system", "user"}
	memTypes := []MetricType{
		MemoryActiveAnonBytes, MemoryActiveBytes, MemoryActiveFileBytes,
		MemoryCachedBytes, MemoryDirtyBytes, MemoryInactiveAnonBytes,
		MemoryInactiveFileBytes, MemoryMappedBytes, MemoryRSSBytes,
		MemoryShmemBytes, MemorySwapBytes, MemoryUnevictableBytes,
		MemoryWritebackBytes,
	}

	merged := NewMetricSet(nil)
	for i := range 10 {
		instLabels := map[string]string{
			"project": "default",
			"name":    fmt.Sprintf("instance-%02d", i),
		}

		m := NewMetricSet(instLabels)

		// CPU samples: 2 CPUs × 8 modes = 16 samples, labels: {project, name, cpu, mode}
		for cpu := range 2 {
			for _, mode := range cpuModes {
				m.AddSamples(CPUSecondsTotal, Sample{
					Value:  42.0,
					Labels: map[string]string{"cpu": strconv.Itoa(cpu), "mode": mode},
				})
			}
		}

		// Disk samples: 2 disks × 4 metric types = 8 samples, labels: {project, name, device}
		for _, dev := range []string{"sda", "vdb"} {
			devLabels := map[string]string{"device": dev}
			m.AddSamples(DiskReadBytesTotal, Sample{Value: 1024, Labels: devLabels})
			m.AddSamples(DiskReadsCompletedTotal, Sample{Value: 10, Labels: devLabels})
			m.AddSamples(DiskWrittenBytesTotal, Sample{Value: 2048, Labels: devLabels})
			m.AddSamples(DiskWritesCompletedTotal, Sample{Value: 20, Labels: devLabels})
		}

		// Filesystem samples: 2 mounts × 3 metric types = 6 samples, labels: {project, name, device, fstype, mountpoint}
		for _, fs := range []struct{ dev, fstype, mount string }{
			{"sda1", "ext4", "/"},
			{"sda2", "ext4", "/home"},
		} {
			fsLabels := map[string]string{"device": fs.dev, "fstype": fs.fstype, "mountpoint": fs.mount}
			m.AddSamples(FilesystemAvailBytes, Sample{Value: 1e9, Labels: fsLabels})
			m.AddSamples(FilesystemFreeBytes, Sample{Value: 2e9, Labels: fsLabels})
			m.AddSamples(FilesystemSizeBytes, Sample{Value: 10e9, Labels: fsLabels})
		}

		// Memory samples: 13 types, labels: {project, name}
		for _, mt := range memTypes {
			m.AddSamples(mt, Sample{Value: 1e6})
		}

		merged.Merge(m)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = merged.String()
	}
}

func TestMetricSet_FilterSamples(t *testing.T) {
	labels := map[string]string{"project": "default", "name": "jammy"}
	newMetricSet := func() *MetricSet {
		m := NewMetricSet(labels)
		require.Equal(t, labels, m.labels)
		m.AddSamples(CPUSecondsTotal, Sample{Value: 10})
		require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])
		return m
	}

	m := newMetricSet()
	filter := func(labels map[string]string) bool {
		return entity.InstanceURL(labels["project"], labels["name"]).String() == entity.InstanceURL("default", "jammy").String()
	}

	m.FilterSamples(filter)

	// Should still contain the sample
	require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])

	m = newMetricSet()
	filter = func(labels map[string]string) bool {
		return entity.InstanceURL(labels["project"], labels["name"]).String() == entity.InstanceURL("not-default", "jammy").String()
	}

	m.FilterSamples(filter)

	// Should no longer contain the sample.
	require.Equal(t, []Sample{}, m.set[CPUSecondsTotal])

	m = NewMetricSet(map[string]string{"project": "default"})
	m.AddSamples(CPUSecondsTotal, Sample{Value: 10})

	n := NewMetricSet(map[string]string{"name": "jammy"})
	n.AddSamples(CPUSecondsTotal, Sample{Value: 20})

	m.Merge(n)

	for _, sample := range m.set[CPUSecondsTotal] {
		hasKeys := make([]string, 0, len(sample.Labels))

		for k := range sample.Labels {
			hasKeys = append(hasKeys, k)
		}

		require.Contains(t, hasKeys, "project")
	}
}

func TestMetricSet_ReplicatorMetricsAreGauges(t *testing.T) {
	tests := []struct {
		name       string
		metricType MetricType
		sample     Sample
		wantLine   string
	}{
		{
			name:       "replicator count per project",
			metricType: Replicators,
			sample:     Sample{Labels: map[string]string{"project": "default"}, Value: 2},
			wantLine:   `lxd_replicators{project="default"} 2`,
		},
		{
			name:       "last run status is one-hot encoded",
			metricType: ReplicatorLastRunStatus,
			sample:     Sample{Labels: map[string]string{"project": "default", "name": "r1", "status": "Failed"}, Value: 1},
			wantLine:   `lxd_replicator_last_run_status{name="r1",project="default",status="Failed"} 1`,
		},
		{
			name:       "last success timestamp",
			metricType: ReplicatorLastSuccessTimestamp,
			sample:     Sample{Labels: map[string]string{"project": "default", "name": "r1"}, Value: 1750000000},
			wantLine:   `lxd_replicator_last_success_timestamp{name="r1",project="default"} 1.75e+09`,
		},
		{
			name:       "last success oldest snapshot timestamp",
			metricType: ReplicatorLastSuccessOldestSnapshotTimestamp,
			sample:     Sample{Labels: map[string]string{"project": "default", "name": "r1"}, Value: 1749999000},
			wantLine:   `lxd_replicator_last_success_oldest_snapshot_timestamp{name="r1",project="default"} 1.749999e+09`,
		},
		{
			name:       "never succeeded is reported as zero rather than omitted",
			metricType: ReplicatorLastSuccessTimestamp,
			sample:     Sample{Labels: map[string]string{"project": "default", "name": "r1"}, Value: 0},
			wantLine:   `lxd_replicator_last_success_timestamp{name="r1",project="default"} 0`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetricSet(nil)
			m.AddSamples(tt.metricType, tt.sample)

			out := m.String()

			// The values of all four metrics can decrease, so they must not be declared as counters.
			require.Contains(t, out, "# TYPE "+MetricNames[tt.metricType]+" gauge")
			require.Contains(t, out, tt.wantLine)
		})
	}
}
