---
relatedlinks: https://grafana.com/grafana/dashboards/19131-lxd/
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

   1. In the {guilabel}`URL` field, enter the address of your Prometheus installation (`http://localhost:9090/`).

      ![Enter Prometheus URL](/images/grafana_configure_prometheus.png)

   1. Keep the default configuration for the other fields and click {guilabel}`Save & test`.

1. Configure Loki as another data source:

   1. Select {guilabel}`Loki`.

      ![Select Loli as another data source](/images/grafana_select_loki.png)

   1. In the {guilabel}`URL` field, enter the address of your Loki installation (`http://localhost:3100/`).

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
