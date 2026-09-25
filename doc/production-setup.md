---
myst:
  html_meta:
    description: An index of how-to guides for LXD production deployment setup, including optimizing performance and monitoring metrics.
---

# Production setup

These how-to guides cover common operations to prepare a LXD server setup for production.

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
Monitor security events </howto/security_events>
Send logs to Loki </howto/logs_loki>
Set up Grafana </howto/grafana>
```


## Related topics

{{performance_exp}}

{{performance_ref}}
