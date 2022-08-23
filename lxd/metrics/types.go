package metrics

// A Sample represents an OpenMetrics sample containing labels and the value.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// MetricSet represents a set of metrics.
type MetricSet struct {
	set    map[MetricType][]Sample
	labels map[string]string
}

// MetricType is a numeric code identifying the metric.
type MetricType int

const (
	// CPUSecondsTotal represents the total CPU seconds used.
	CPUSecondsTotal MetricType = iota
	// DiskReadBytesTotal represents the read bytes for a disk.
	DiskReadBytesTotal
	// DiskReadsCompletedTotal represents the completed for a disk.
	DiskReadsCompletedTotal
	// DiskWrittenBytesTotal represents the written bytes for a disk.
	DiskWrittenBytesTotal
	// DiskWritesCompletedTotal represents the completed writes for a disk.
	DiskWritesCompletedTotal
	// FilesystemAvailBytes represents the available bytes on a filesystem.
	FilesystemAvailBytes
	// FilesystemFreeBytes represents the free bytes on a filesystem.
	FilesystemFreeBytes
	// FilesystemSizeBytes represents the size in bytes of a filesystem.
	FilesystemSizeBytes
	// MemoryActiveAnonBytes represents the amount of anonymous memory on active LRU list.
	MemoryActiveAnonBytes
	// MemoryActiveFileBytes represents the amount of file-backed memory on active LRU list.
	MemoryActiveFileBytes
	// MemoryActiveBytes represents the amount of memory on active LRU list.
	MemoryActiveBytes
	// MemoryCachedBytes represents the amount of cached memory.
	MemoryCachedBytes
	// MemoryDirtyBytes represents the amount of memory waiting to get written back to the disk.
	MemoryDirtyBytes
	// MemoryHugePagesFreeBytes represents the amount of free memory for hugetlb.
	MemoryHugePagesFreeBytes
	// MemoryHugePagesTotalBytes represents the amount of used memory for hugetlb.
	MemoryHugePagesTotalBytes
	// MemoryInactiveAnonBytes represents the amount of anonymous memory on inactive LRU list.
	MemoryInactiveAnonBytes
	// MemoryInactiveFileBytes represents the amount of file-backed memory on inactive LRU list.
	MemoryInactiveFileBytes
	// MemoryInactiveBytes represents the amount of memory on inactive LRU list.
	MemoryInactiveBytes
	// MemoryMappedBytes represents the amount of mapped memory.
	MemoryMappedBytes
	//MemoryMemAvailableBytes represents the amount of available memory.
	MemoryMemAvailableBytes
	// MemoryMemFreeBytes represents the amount of free memory.
	MemoryMemFreeBytes
	// MemoryMemTotalBytes represents the amount of used memory.
	MemoryMemTotalBytes
	// MemoryRSSBytes represents the amount of anonymous and swap cache memory.
	MemoryRSSBytes
	// MemoryShmemBytes represents the amount of cached filesystem data that is swap-backed.
	MemoryShmemBytes
	// MemorySwapBytes represents the amount of swap memory.
	MemorySwapBytes
	// MemoryUnevictableBytes represents the amount of unevictable memory.
	MemoryUnevictableBytes
	// MemoryWritebackBytes represents the amount of memory queued for syncing to disk.
	MemoryWritebackBytes
	// NetworkReceiveBytesTotal represents the amount of received bytes on a given interface.
	NetworkReceiveBytesTotal
	// NetworkReceiveDropTotal represents the amount of received dropped bytes on a given interface.
	NetworkReceiveDropTotal
	// NetworkReceiveErrsTotal represents the amount of received errors on a given interface.
	NetworkReceiveErrsTotal
	// NetworkReceivePacketsTotal represents the amount of received packets on a given interface.
	NetworkReceivePacketsTotal
	// NetworkTransmitBytesTotal represents the amount of transmitted bytes on a given interface.
	NetworkTransmitBytesTotal
	// NetworkTransmitDropTotal represents the amount of transmitted dropped bytes on a given interface.
	NetworkTransmitDropTotal
	// NetworkTransmitErrsTotal represents the amount of transmitted errors on a given interface.
	NetworkTransmitErrsTotal
	// NetworkTransmitPacketsTotal represents the amount of transmitted packets on a given interface.
	NetworkTransmitPacketsTotal
	// ProcsTotal represents the number of running processes.
	ProcsTotal
)

// MetricNames associates a metric type to its name.
var MetricNames = map[MetricType]string{
	CPUSecondsTotal:             "lxd_cpu_seconds_total",
	DiskReadBytesTotal:          "lxd_disk_read_bytes_total",
	DiskReadsCompletedTotal:     "lxd_disk_reads_completed_total",
	DiskWrittenBytesTotal:       "lxd_disk_written_bytes_total",
	DiskWritesCompletedTotal:    "lxd_disk_writes_completed_total",
	FilesystemAvailBytes:        "lxd_filesystem_avail_bytes",
	FilesystemFreeBytes:         "lxd_filesystem_free_bytes",
	FilesystemSizeBytes:         "lxd_filesystem_size_bytes",
	MemoryActiveAnonBytes:       "lxd_memory_Active_anon_bytes",
	MemoryActiveFileBytes:       "lxd_memory_Active_file_bytes",
	MemoryActiveBytes:           "lxd_memory_Active_bytes",
	MemoryCachedBytes:           "lxd_memory_Cached_bytes",
	MemoryDirtyBytes:            "lxd_memory_Dirty_bytes",
	MemoryHugePagesFreeBytes:    "lxd_memory_HugepagesFree_bytes",
	MemoryHugePagesTotalBytes:   "lxd_memory_HugepagesTotal_bytes",
	MemoryInactiveAnonBytes:     "lxd_memory_Inactive_anon_bytes",
	MemoryInactiveFileBytes:     "lxd_memory_Inactive_file_bytes",
	MemoryInactiveBytes:         "lxd_memory_Inactive_bytes",
	MemoryMappedBytes:           "lxd_memory_Mapped_bytes",
	MemoryMemAvailableBytes:     "lxd_memory_MemAvailable_bytes",
	MemoryMemFreeBytes:          "lxd_memory_MemFree_bytes",
	MemoryMemTotalBytes:         "lxd_memory_MemTotal_bytes",
	MemoryRSSBytes:              "lxd_memory_RSS_bytes",
	MemoryShmemBytes:            "lxd_memory_Shmem_bytes",
	MemorySwapBytes:             "lxd_memory_Swap_bytes",
	MemoryUnevictableBytes:      "lxd_memory_Unevictable_bytes",
	MemoryWritebackBytes:        "lxd_memory_Writeback_bytes",
	NetworkReceiveBytesTotal:    "lxd_network_receive_bytes_total",
	NetworkReceiveDropTotal:     "lxd_network_receive_drop_total",
	NetworkReceiveErrsTotal:     "lxd_network_receive_errs_total",
	NetworkReceivePacketsTotal:  "lxd_network_receive_packets_total",
	NetworkTransmitBytesTotal:   "lxd_network_transmit_bytes_total",
	NetworkTransmitDropTotal:    "lxd_network_transmit_drop_total",
	NetworkTransmitErrsTotal:    "lxd_network_transmit_errs_total",
	NetworkTransmitPacketsTotal: "lxd_network_transmit_packets_total",
	ProcsTotal:                  "lxd_procs_total",
}

// MetricHeaders represents the metric headers which contain help messages as specified by OpenMetrics.
var MetricHeaders = map[MetricType]string{
	CPUSecondsTotal:             "# HELP lxd_cpu_seconds_total The total number of CPU time used in seconds.",
	DiskReadBytesTotal:          "# HELP lxd_disk_read_bytes_total The total number of bytes read.",
	DiskReadsCompletedTotal:     "# HELP lxd_disk_reads_completed_total The total number of completed reads.",
	DiskWrittenBytesTotal:       "# HELP lxd_disk_written_bytes_total The total number of bytes written.",
	DiskWritesCompletedTotal:    "# HELP lxd_disk_writes_completed_total The total number of completed writes.",
	FilesystemAvailBytes:        "# HELP lxd_filesystem_avail_bytes The number of available space in bytes.",
	FilesystemFreeBytes:         "# HELP lxd_filesystem_free_bytes The number of free space in bytes.",
	FilesystemSizeBytes:         "# HELP lxd_filesystem_size_bytes The size of the filesystem in bytes.",
	MemoryActiveAnonBytes:       "# HELP lxd_memory_Active_anon_bytes The amount of anonymous memory on active LRU list.",
	MemoryActiveFileBytes:       "# HELP lxd_memory_Active_file_bytes The amount of file-backed memory on active LRU list.",
	MemoryActiveBytes:           "# HELP lxd_memory_Active_bytes The amount of memory on active LRU list.",
	MemoryCachedBytes:           "# HELP lxd_memory_Cached_bytes The amount of cached memory.",
	MemoryDirtyBytes:            "# HELP lxd_memory_Dirty_bytes The amount of memory waiting to get written back to the disk.",
	MemoryHugePagesFreeBytes:    "# HELP lxd_memory_HugepagesFree_bytes The amount of free memory for hugetlb.",
	MemoryHugePagesTotalBytes:   "# HELP lxd_memory_HugepagesTotal_bytes The amount of used memory for hugetlb.",
	MemoryInactiveAnonBytes:     "# HELP lxd_memory_Inactive_anon_bytes The amount of file-backed memory on inactive LRU list.",
	MemoryInactiveFileBytes:     "# HELP lxd_memory_Inactive_file_bytes The amount of file-backed memory on inactive LRU list.",
	MemoryInactiveBytes:         "# HELP lxd_memory_Inactive_bytes The amount of memory on inactive LRU list.",
	MemoryMappedBytes:           "# HELP lxd_memory_Mapped_bytes The amount of mapped memory.",
	MemoryMemAvailableBytes:     "# HELP lxd_memory_MemAvailable_bytes The amount of available memory.",
	MemoryMemFreeBytes:          "# HELP lxd_memory_MemFree_bytes The amount of free memory.",
	MemoryMemTotalBytes:         "# HELP lxd_memory_MemTotal_bytes The amount of used memory.",
	MemoryRSSBytes:              "# HELP lxd_memory_RSS_bytes The amount of anonymous and swap cache memory.",
	MemoryShmemBytes:            "# HELP lxd_memory_Shmem_bytes The amount of cached filesystem data that is swap-backed.",
	MemorySwapBytes:             "# HELP lxd_memory_Swap_bytes The amount of used swap memory.",
	MemoryUnevictableBytes:      "# HELP lxd_memory_Unevictable_bytes The amount of unevictable memory.",
	MemoryWritebackBytes:        "# HELP lxd_memory_Writeback_bytes The amount of memory queued for syncing to disk.",
	NetworkReceiveBytesTotal:    "# HELP lxd_network_receive_bytes_total The amount of received bytes on a given interface.",
	NetworkReceiveDropTotal:     "# HELP lxd_network_receive_drop_total The amount of received dropped bytes on a given interface.",
	NetworkReceiveErrsTotal:     "# HELP lxd_network_receive_errs_total The amount of received errors on a given interface.",
	NetworkReceivePacketsTotal:  "# HELP lxd_network_receive_packets_total The amount of received packets on a given interface.",
	NetworkTransmitBytesTotal:   "# HELP lxd_network_transmit_bytes_total The amount of transmitted bytes on a given interface.",
	NetworkTransmitDropTotal:    "# HELP lxd_network_transmit_drop_total The amount of transmitted dropped bytes on a given interface.",
	NetworkTransmitErrsTotal:    "# HELP lxd_network_transmit_errs_total The amount of transmitted errors on a given interface.",
	NetworkTransmitPacketsTotal: "# HELP lxd_network_transmit_packets_total The amount of transmitted packets on a given interface.",
	ProcsTotal:                  "# HELP lxd_procs_total The number of running processes.",
}
