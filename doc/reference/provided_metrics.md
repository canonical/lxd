(provided-metrics)=
# Provided metrics

LXD provides a number of instance metrics and internal metrics.
See {ref}`metrics` for instructions on how to work with these metrics.

## Instance metrics

The following instance metrics are provided:

```{list-table}
   :header-rows: 1

* - Metric
  - Description
* - `lxd_cpu_effective_total`
  - Total number of effective CPUs
* - `lxd_cpu_seconds_total{cpu="<cpu>", mode="<mode>"}`
  - Total number of CPU time used (in seconds)
* - `lxd_disk_read_bytes_total{device="<dev>"}`
  - Total number of bytes read
* - `lxd_disk_reads_completed_total{device="<dev>"}`
  - Total number of completed reads
* - `lxd_disk_written_bytes_total{device="<dev>"}`
  - Total number of bytes written
* - `lxd_disk_writes_completed_total{device="<dev>"}`
  - Total number of completed writes
* - `lxd_filesystem_avail_bytes{device="<dev>",fstype="<type>"}`
  - Available space (in bytes)
* - `lxd_filesystem_free_bytes{device="<dev>",fstype="<type>"}`
  - Free space (in bytes)
* - `lxd_filesystem_size_bytes{device="<dev>",fstype="<type>"}`
  - Size of the file system (in bytes)
* - `lxd_memory_Active_anon_bytes`
  - Amount of anonymous memory on active LRU list
* - `lxd_memory_Active_bytes`
  - Amount of memory on active LRU list
* - `lxd_memory_Active_file_bytes`
  - Amount of file-backed memory on active LRU list
* - `lxd_memory_Cached_bytes`
  - Amount of cached memory
* - `lxd_memory_Dirty_bytes`
  - Amount of memory waiting to be written back to the disk
* - `lxd_memory_HugepagesFree_bytes`
  - Amount of free memory for `hugetlb`
* - `lxd_memory_HugepagesTotal_bytes`
  - Amount of used memory for `hugetlb`
* - `lxd_memory_Inactive_anon_bytes`
  - Amount of anonymous memory on inactive LRU list
* - `lxd_memory_Inactive_bytes`
  - Amount of memory on inactive LRU list
* - `lxd_memory_Inactive_file_bytes`
  - Amount of file-backed memory on inactive LRU list
* - `lxd_memory_Mapped_bytes`
  - Amount of mapped memory
* - `lxd_memory_MemAvailable_bytes`
  - Amount of available memory
* - `lxd_memory_MemFree_bytes`
  - Amount of free memory
* - `lxd_memory_MemTotal_bytes`
  - Amount of used memory
* - `lxd_memory_OOM_kills_total`
  - The number of out-of-memory kills
* - `lxd_memory_RSS_bytes`
  - Amount of anonymous and swap cache memory
* - `lxd_memory_Shmem_bytes`
  - Amount of cached file system data that is swap-backed
* - `lxd_memory_SReclaimable_bytes`
  - Amount of reclaimable slab memory
* - `lxd_memory_Swap_bytes`
  - Amount of used swap memory
* - `lxd_memory_Unevictable_bytes`
  - Amount of unevictable memory
* - `lxd_memory_Writeback_bytes`
  - Amount of memory queued for syncing to disk
* - `lxd_network_receive_bytes_total{device="<dev>"}`
  - Amount of received bytes on a given interface
* - `lxd_network_receive_drop_total{device="<dev>"}`
  - Amount of received dropped bytes on a given interface
* - `lxd_network_receive_errs_total{device="<dev>"}`
  - Amount of received errors on a given interface
* - `lxd_network_receive_packets_total{device="<dev>"}`
  - Amount of received packets on a given interface
* - `lxd_network_transmit_bytes_total{device="<dev>"}`
  - Amount of transmitted bytes on a given interface
* - `lxd_network_transmit_drop_total{device="<dev>"}`
  - Amount of transmitted dropped bytes on a given interface
* - `lxd_network_transmit_errs_total{device="<dev>"}`
  - Amount of transmitted errors on a given interface
* - `lxd_network_transmit_packets_total{device="<dev>"}`
  - Amount of transmitted packets on a given interface
* - `lxd_procs_total`
  - Number of running processes
```

## Internal metrics

The following internal metrics are provided:

```{list-table}
   :header-rows: 1

* - Metric
  - Description
* - `lxd_api_requests_completed_total`
  - Total number of completed requests. See [API rates metrics](api-rates-metrics).
* - `lxd_api_requests_ongoing`
  - Number of requests currently being handled. See [API rates metrics](api-rates-metrics).
* - `lxd_go_alloc_bytes_total`
  - Total number of bytes allocated (even if freed)
* - `lxd_go_alloc_bytes`
  - Number of bytes allocated and still in use
* - `lxd_go_buck_hash_sys_bytes`
  - Number of bytes used by the profiling bucket hash table
* - `lxd_go_frees_total`
  - Total number of frees
* - `lxd_go_gc_sys_bytes`
  - Number of bytes used for garbage collection system metadata
* - `lxd_go_goroutines`
  - Number of goroutines that currently exist
* - `lxd_go_heap_alloc_bytes`
  - Number of heap bytes allocated and still in use
* - `lxd_go_heap_idle_bytes`
  - Number of heap bytes waiting to be used
* - `lxd_go_heap_inuse_bytes`
  - Number of heap bytes that are in use
* - `lxd_go_heap_objects`
  - Number of allocated objects
* - `lxd_go_heap_released_bytes`
  - Number of heap bytes released to OS
* - `lxd_go_heap_sys_bytes`
  - Number of heap bytes obtained from system
* - `lxd_go_lookups_total`
  - Total number of pointer lookups
* - `lxd_go_mallocs_total`
  - Total number of `mallocs`
* - `lxd_go_mcache_inuse_bytes`
  - Number of bytes in use by `mcache` structures
* - `lxd_go_mcache_sys_bytes`
  - Number of bytes used for `mcache` structures obtained from system
* - `lxd_go_mspan_inuse_bytes`
  - Number of bytes in use by `mspan` structures
* - `lxd_go_mspan_sys_bytes`
  - Number of bytes used for `mspan` structures obtained from system
* - `lxd_go_next_gc_bytes`
  - Number of heap bytes when next garbage collection will take place
* - `lxd_go_other_sys_bytes`
  - Number of bytes used for other system allocations
* - `lxd_go_stack_inuse_bytes`
  - Number of bytes in use by the stack allocator
* - `lxd_go_stack_sys_bytes`
  - Number of bytes obtained from system for stack allocator
* - `lxd_go_sys_bytes`
  - Number of bytes obtained from system
* - `lxd_operations_total`
  - Number of running operations
* - `lxd_replicators`
  - Number of configured replicators in the project. See [Replicator metrics](replicator-metrics).
* - `lxd_replicator_last_run_status`
  - Whether the last replicator run ended in the given status. See [Replicator metrics](replicator-metrics).
* - `lxd_replicator_last_success_timestamp`
  - Time of the last successful replicator run (in seconds since the epoch). See [Replicator metrics](replicator-metrics).
* - `lxd_replicator_last_success_oldest_snapshot_timestamp`
  - Creation time of the oldest snapshot replicated by the last successful run (in seconds since the epoch). See [Replicator metrics](replicator-metrics).
* - `lxd_uptime_seconds`
  - Daemon uptime (in seconds)
* - `lxd_warnings_total`
  - Number of active warnings
```

(api-rates-metrics)=
## API rates metrics

The API rates metrics include `lxd_api_requests_completed_total` and `lxd_api_requests_ongoing`. These metrics can be consumed by an observability tool deployed externally (for example, the [Canonical Observability Stack](https://charmhub.io/topics/canonical-observability-stack) or another third-party tool) to help identify failures or overload on a LXD server. You can set thresholds on the observability tools for these metrics' values to trigger alarms and take programmatic actions.

These metrics consider all endpoints in the [LXD REST API](../api), with the exception of the `/` endpoint. Requests using an invalid URL are also counted. Requests against the metrics server are also counted. Both introduced metrics include a label `entity_type` based on the main entity type that the endpoint is operating on.

`lxd_api_requests_ongoing` contains the number of requests that are not yet completed by the time the metrics are queried. A request is considered completed when the response is returned to the client and any asynchronous operations spawned by that request are done. `lxd_api_requests_completed_total` contains the number of completed requests. This metric includes an additional label named `result` based on the outcome of the request. The label can have one of the following values:

- `error_server`, for errors on the server side, this includes responses with HTTP status codes from 500 to 599. Any failed asynchronous operations also fall into this category.
- `error_client`, for responses with HTTP status codes from 400 to 499, indicating an error on the client side.
- `succeeded`, for endpoints that executed successfully.

(replicator-metrics)=
## Replicator metrics

The replicator metrics report the health of {ref}`replicators <exp-replicators>`, so that replication failures and the current recovery point objective (RPO) are visible in an observability tool rather than only through the API.

Replicator state is global to the cluster, so these metrics are reported only by the cluster member that is currently the database leader. Scraping every member therefore yields one sample per replicator rather than one per member.

`lxd_replicators` carries a `project` label and reports the number of replicators configured in that project. A sample is emitted for every project, using `0` for projects that have no replicators, so that unprotected projects can be identified.

`lxd_replicator_last_run_status` carries `project`, `name` and `status` labels. A sample is emitted for each of the four possible statuses (`Pending`, `Running`, `Completed` and `Failed`), with a value of `1` for the status of the last run and `0` for the others. `Pending` means the replicator has never run. Because every status is always present, a status change never leaves a stale series behind.

`lxd_replicator_last_success_timestamp` and `lxd_replicator_last_success_oldest_snapshot_timestamp` carry `project` and `name` labels and report times in seconds since the epoch. The first is the completion time of the last successful run, and the second is the creation time of the oldest snapshot that run replicated. A value of `0` means no such time has been recorded yet.

Some example queries:

```
# Number of replicators whose last run failed
sum(lxd_replicator_last_run_status{status="Failed"})

# Breakdown by status
sum by (status) (lxd_replicator_last_run_status)

# Projects with no replication configured
lxd_replicators == 0

# Time since the last successful run, in seconds
time() - lxd_replicator_last_success_timestamp

# Current RPO, in seconds
time() - lxd_replicator_last_success_oldest_snapshot_timestamp

# Alert: last successful run is older than an hourly schedule allows
time() - lxd_replicator_last_success_timestamp > 3600

# Replicators that have never succeeded
lxd_replicator_last_success_timestamp == 0
```

## Related topics

How-to guides:

- {ref}`metrics`

{{performance_exp}}
