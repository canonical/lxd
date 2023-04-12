(provided-metrics)=
# Provided metrics

## Provided instance metrics

The following instance metrics are provided:

* `lxd_cpu_effective_total`
* `lxd_cpu_seconds_total{cpu="<cpu>", mode="<mode>"}`
* `lxd_disk_read_bytes_total{device="<dev>"}`
* `lxd_disk_reads_completed_total{device="<dev>"}`
* `lxd_disk_written_bytes_total{device="<dev>"}`
* `lxd_disk_writes_completed_total{device="<dev>"}`
* `lxd_filesystem_avail_bytes{device="<dev>",fstype="<type>"}`
* `lxd_filesystem_free_bytes{device="<dev>",fstype="<type>"}`
* `lxd_filesystem_size_bytes{device="<dev>",fstype="<type>"}`
* `lxd_memory_Active_anon_bytes`
* `lxd_memory_Active_bytes`
* `lxd_memory_Active_file_bytes`
* `lxd_memory_Cached_bytes`
* `lxd_memory_Dirty_bytes`
* `lxd_memory_HugepagesFree_bytes`
* `lxd_memory_HugepagesTotal_bytes`
* `lxd_memory_Inactive_anon_bytes`
* `lxd_memory_Inactive_bytes`
* `lxd_memory_Inactive_file_bytes`
* `lxd_memory_Mapped_bytes`
* `lxd_memory_MemAvailable_bytes`
* `lxd_memory_MemFree_bytes`
* `lxd_memory_MemTotal_bytes`
* `lxd_memory_OOM_kills_total`
* `lxd_memory_RSS_bytes`
* `lxd_memory_Shmem_bytes`
* `lxd_memory_Swap_bytes`
* `lxd_memory_Unevictable_bytes`
* `lxd_memory_Writeback_bytes`
* `lxd_network_receive_bytes_total{device="<dev>"}`
* `lxd_network_receive_drop_total{device="<dev>"}`
* `lxd_network_receive_errs_total{device="<dev>"}`
* `lxd_network_receive_packets_total{device="<dev>"}`
* `lxd_network_transmit_bytes_total{device="<dev>"}`
* `lxd_network_transmit_drop_total{device="<dev>"}`
* `lxd_network_transmit_errs_total{device="<dev>"}`
* `lxd_network_transmit_packets_total{device="<dev>"}`
* `lxd_procs_total`

## Provided internal metrics

The following internal metrics are provided:

* `lxd_go_alloc_bytes_total`
* `lxd_go_alloc_bytes`
* `lxd_go_buck_hash_sys_bytes`
* `lxd_go_frees_total`
* `lxd_go_gc_sys_bytes`
* `lxd_go_goroutines`
* `lxd_go_heap_alloc_bytes`
* `lxd_go_heap_idle_bytes`
* `lxd_go_heap_inuse_bytes`
* `lxd_go_heap_objects`
* `lxd_go_heap_released_bytes`
* `lxd_go_heap_sys_bytes`
* `lxd_go_lookups_total`
* `lxd_go_mallocs_total`
* `lxd_go_mcache_inuse_bytes`
* `lxd_go_mcache_sys_bytes`
* `lxd_go_mspan_inuse_bytes`
* `lxd_go_mspan_sys_bytes`
* `lxd_go_next_gc_bytes`
* `lxd_go_other_sys_bytes`
* `lxd_go_stack_inuse_bytes`
* `lxd_go_stack_sys_bytes`
* `lxd_go_sys_bytes`
* `lxd_operations_total`
* `lxd_uptime_seconds`
* `lxd_warnings_total`
