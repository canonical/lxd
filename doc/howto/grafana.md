---
relatedlinks: "[LXD&#32;Grafana&#32;dashboard](https://grafana.com/grafana/dashboards/19131-lxd/)"
---

(grafana)=
# Set up a Grafana dashboard

To visualize the metrics and logs data, set up [Grafana](https://grafana.com/).
LXD provides a [Grafana dashboard](https://grafana.com/grafana/dashboards/19131-lxd/) that is configured to display the LXD metrics scraped by Prometheus and events sent to Loki.

```{note}
The dashboard requires Grafana 8.4 or later.
```

See the Grafana documentation for instructions on installing and signing in:

- [Install Grafana](https://grafana.com/docs/grafana/latest/setup-grafana/installation/)
- [Sign in to Grafana](https://grafana.com/docs/grafana/latest/setup-grafana/sign-in-to-grafana/)

Complete the following steps to import the [LXD dashboard](https://grafana.com/grafana/dashboards/19131-lxd/):

1. Configure Prometheus as a data source:

   1. From the Basic (quick setup) panel, choose {guilabel}`Data Sources`.

      ![Choose data source in Grafana](/images/grafana_welcome.png)

   1. Select {guilabel}`Prometheus`.

      ![Select Prometheus as a data source](/images/grafana_select_prometheus.png)

   1. In the {guilabel}`URL` field, enter the address of your Prometheus installation (`http://localhost:9090/` if running Prometheus locally).

      ![Enter Prometheus URL](/images/grafana_configure_prometheus.png)

   1. Keep the default configuration for the other fields and click {guilabel}`Save & test`.

1. Configure Loki as another data source:

   1. Select {guilabel}`Loki`.

      ![Select Loki as another data source](/images/grafana_select_loki.png)

   1. In the {guilabel}`URL` field, enter the address of your Loki installation (`http://localhost:3100/` if running Loki locally).

      ![Enter Loki URL](/images/grafana_configure_loki.png)

   1. Keep the default configuration for the other fields and click {guilabel}`Save & test`.

1. Import the LXD dashboard:

   1. Go back to the Basic (quick setup) panel and now choose {guilabel}`Dashboards` > {guilabel}`Import a dashboard`.
   1. In the {guilabel}`Find and import dashboards` field, enter the dashboard ID `19131`.

      ![Enter the LXD dashboard ID](/images/grafana_dashboard_import.png)

   1. Click {guilabel}`Load`.
   1. In the {guilabel}`LXD` drop-down menu, select the Prometheus and Loki data sources that you configured.

      ![Select the Prometheus data source](/images/grafana_dashboard_select_datasource.png)

   1. Click {guilabel}`Import`.

You should now see the LXD dashboard.
You can select the project and filter by instances.

![Resource overview in the LXD Grafana dashboard](/images/grafana_resources.png)

At the bottom of the page, you can see data for each instance.

![Instance data in the LXD Grafana dashboard](/images/grafana_instances.png)

```{note}
For proper operation of the Loki part of the dashboard, you need to ensure that the `instance` field matches the Prometheus job name.
You can change the `instance` field through the {config:option}`server-loki:loki.instance` configuration key.

The Prometheus `job_name` value can be found in `/var/snap/prometheus/current/prometheus.yml` (if you are using the snap) or `/etc/prometheus/prometheus.yaml` (otherwise).

To set the `loki.instance` configuration key, run the following command:
`lxc config set loki.instance=<job_name_value>`

You can check that setting via:
`lxc config get loki.instance`
```

## Scripted setup and LXD UI integration

As an alternative to the manual steps above, we provide a script to set up the Grafana dashboard. This only supports a single-node LXD installation.

1. Launch a new instance on your LXD server:

       lxc launch ubuntu:24.04 grafana --project default

1. Run the following commands to download and execute the script to set up Grafana on the `grafana` instance:

       curl -s https://raw.githubusercontent.com/canonical/lxd/refs/heads/main/scripts/setup-grafana.sh -o /tmp/setup-grafana.sh
       chmod +x /tmp/setup-grafana.sh
       /tmp/setup-grafana.sh grafana default

1. After the script finishes, sign in to Grafana with the default credentials `admin`/`admin` and change the password.

1. Import the LXD dashboard as described in step 3 of the manual steps in the preceding section.

The script installs Grafana, Prometheus, and Loki on a LXD instance. It also configures LXD to send metrics to Prometheus and logs to Loki. Additionally, it configures the LXD UI to be aware of the Grafana dashboard. This enables the UI to render a deep link {guilabel}`Metrics` to the Grafana dashboard from instance details pages (available since LXD 6.3):

![Metrics link in the LXD UI instance detail page](/images/grafana_lxd_ui_metrics_integration.png)

![Dashboard details for a running instance](/images/grafana_lxd_ui_instance_dashboard.png)
