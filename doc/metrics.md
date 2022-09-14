---
discourse: 12281,11735
relatedlinks: https://grafana.com/grafana/dashboards/15726
---

(instance-metrics)=
# Instance metrics

```{youtube} https://www.youtube.com/watch?v=EthK-8hm_fY
```

<!-- Include start metrics intro -->
LXD collects metrics for all running instances.
These metrics cover the CPU, memory, network, disk and process usage.
They are meant to be consumed by Prometheus, and you can use Grafana to display the metrics as graphs.
<!-- Include end metrics intro -->

In cluster environments, LXD will only return the values for instances running on the server being accessed. It's expected that each cluster member will be scraped separately.

The instance metrics are updated when calling the `/1.0/metrics` endpoint.
They are cached for 8s to handle multiple scrapers. Fetching metrics is a relatively expensive operation for LXD to perform so consider scraping at a higher than default interval
if the impact is too high.

## Create metrics certificate

The `/1.0/metrics` endpoint is a special one as it also accepts a `metrics` type certificate.
This kind of certificate is meant for metrics only, and won't work for interaction with instances or any other LXD objects.

Here's how to create a new certificate (this is not specific to metrics):

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout metrics.key -nodes -out metrics.crt -days 3650 -subj "/CN=metrics.local"
```

*Note*: OpenSSL version 1.1.0+ is required for the above command to generate a proper certificate.

Now, this certificate needs to be added to the list of trusted clients:

```bash
lxc config trust add metrics.crt --type=metrics
```

## Add target to Prometheus

In order for Prometheus to scrape from LXD, it has to be added to the targets.

First, one needs to ensure that `core.https_address` is set so LXD can be reached over the network.
This can be done by running:

```bash
lxc config set core.https_address ":8443"
```

Alternatively, one can use `core.metrics_address` which is intended for metrics only.

Second, the newly created certificate and key, as well as the LXD server certificate need to be accessible to Prometheus.
For this, these three files can be copied to `/etc/prometheus/tls`:

```bash
# Create new tls directory
mkdir /etc/prometheus/tls

# Copy newly created certificate and key to tls directory
cp metrics.crt metrics.key /etc/prometheus/tls

# Copy LXD server certificate to tls directory
cp /var/snap/lxd/common/lxd/server.crt /etc/prometheus/tls

# Make sure Prometheus can read these files (usually, Prometheus is run as user "prometheus")
chown -R prometheus:prometheus /etc/prometheus/tls
```

Lastly, LXD has to be added as target.
For this, `/etc/prometheus/prometheus.yaml` needs to be edited.
Here's what the configuration needs to look like:

```yaml
scrape_configs:
  - job_name: lxd
    metrics_path: '/1.0/metrics'
    scheme: 'https'
    static_configs:
      - targets: ['foo.example.com:8443']
    tls_config:
      ca_file: 'tls/server.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      # XXX: server_name is required if the target name
      #      is not covered by the certificate (not in the SAN list)
      server_name: 'foo'
```

In the above example, `/etc/prometheus/tls/server.crt` looks like:

```
$ openssl x509 -noout -text -in /etc/prometheus/tls/server.crt
...
            X509v3 Subject Alternative Name:
                DNS:foo, IP Address:127.0.0.1, IP Address:0:0:0:0:0:0:0:1
...
```

Since the Subject Alternative Name (SAN) list doesn't include the host name provided in the `targets` list, it is required to override the name used for comparison using the `server_name` directive.

Here is an example of a `prometheus.yaml` configuration where multiple jobs are used to scrape the metrics of multiple LXD servers:

```yaml
scrape_configs:
  # abydos, langara and orilla are part of a single cluster
  # initially bootstrapped by abydos which is why all 3 nodes
  # share the its `ca_file` and `server_name`.
  #
  # Note: 2 params are provided:
  #   `project`: needed when not using the `default` project or
  #              when multiple are used.
  #   `target`: the individual cluster member to scrape because
  #             they only report about instances running locally.
  - job_name: "lxd-abydos"
    metrics_path: '/1.0/metrics'
    params:
      project: ['jdoe']
      target: ['abydos']
    scheme: 'https'
    static_configs:
      - targets: ['abydos.hosts.example.net:8444']
    tls_config:
      ca_file: 'tls/abydos.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      server_name: 'abydos'

  - job_name: "lxd-langara"
    metrics_path: '/1.0/metrics'
    params:
      project: ['jdoe']
      target: ['langara']
    scheme: 'https'
    static_configs:
      - targets: ['langara.hosts.example.net:8444']
    tls_config:
      ca_file: 'tls/abydos.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      server_name: 'abydos'

  - job_name: "lxd-orilla"
    metrics_path: '/1.0/metrics'
    params:
      project: ['jdoe']
      target: ['orilla']
    scheme: 'https'
    static_configs:
      - targets: ['orilla.hosts.example.net:8444']
    tls_config:
      ca_file: 'tls/abydos.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      server_name: 'abydos'

  # jupiter, mars and saturn are 3 standalone LXD servers.
  # Note: only the `default` project is used on them, so it is not specified.
  - job_name: "lxd-jupiter"
    metrics_path: '/1.0/metrics'
    scheme: 'https'
    static_configs:
      - targets: ['jupiter.example.com:9101']
    tls_config:
      ca_file: 'tls/jupiter.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      server_name: 'jupiter'

  - job_name: "lxd-mars"
    metrics_path: '/1.0/metrics'
    scheme: 'https'
    static_configs:
      - targets: ['mars.example.com:9101']
    tls_config:
      ca_file: 'tls/mars.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      server_name: 'mars'

  - job_name: "lxd-saturn"
    metrics_path: '/1.0/metrics'
    scheme: 'https'
    static_configs:
      - targets: ['saturn.example.com:9101']
    tls_config:
      ca_file: 'tls/saturn.crt'
      cert_file: 'tls/metrics.crt'
      key_file: 'tls/metrics.key'
      server_name: 'saturn'
```

## Provided metrics

The following metrics are provided:

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
