---
myst:
  html_meta:
    description: An index of how-to guides for LXD production deployment setup, including optimizing performance, monitoring metrics, and backup and recovery operations.
---

# Production setup

These how-to guides cover common operations to prepare an LXD server setup for production.

## Optimize performance

The `lxd-benchmark` tool measures the time to create instances in different configurations. In some scenarios, deployments can also be configured for increased bandwidth.

```{toctree}
:titlesonly:

Benchmark performance </howto/benchmark_performance>
Increase bandwidth </howto/network_increase_bandwidth>
```

## Monitor metrics and logs

LXD collects metrics and logs that can be viewed as raw data or used with observability tools like Loki and Grafana.

```{toctree}
:titlesonly:

Monitor metrics </metrics>
Send logs to Loki </howto/logs_loki>
Set up Grafana </howto/grafana>
```

## Back up and recover

Full and partial server backups protect against data loss. Instance recovery and disaster recovery options are available for different failure scenarios.

```{toctree}
:titlesonly:

Back up a server </backup>
Recover instances </howto/disaster_recovery>
Disaster recovery with storage replication </howto/disaster_recovery_replication>
Disaster recovery with replicators </howto/replicators_dr>
```

## Related topics

{{performance_exp}}

{{performance_ref}}
