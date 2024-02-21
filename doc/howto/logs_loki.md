(logs_loki)=
# How to send logs to Loki

<!-- Include start logs_loki intro -->
LXD publishes information about its activity in the form of events. The `lxc monitor` command allows you to view this information in your shell. There are two categories of LXD events: logs and life cycle. The `lxc monitor --type=logging --pretty` command will filter and display log type events like activity of the raft cluster, for instance, while `lxc monitor --type=lifecycle --pretty` will only display life cycle events like instances starting or stopping.

In a production environment, you might want to keep a log of these events in a dedicated system. [Loki](https://grafana.com/oss/loki/) is one such system, and LXD provides a configuration option to forward its event stream to Loki.
<!-- Include end logs_loki intro -->

## Configure LXD to send logs

See the Loki documentation for instructions on installing it:

- [Install Loki](https://grafana.com/docs/loki/latest/setup/install/)

Once you have a Loki server up and running, you can instruct LXD to send logs to your Loki server by setting the following option:

    lxc config set loki.api.url=http://<loki_server_IP>:3100

## Query Loki logs

Loki logs are typically viewed/queried using Grafana but Loki provides a command line utility called LogCLI allowing to query logs from your Loki server without the need for Grafana.

See the LogCLI documentation for instructions on installing it:

- [Install LogCLI](https://grafana.com/docs/loki/latest/query/logcli/)

With your LogCLI utility up and running, first configure it to query the server you have installed before by setting the appropriate environment variable:

    export LOKI_ADDR=http://<loki_server_IP>:3100

You can then query the Loki server to validate that your LXD events are getting through. LXD events all have the `app` key set to `lxd` so you can use the following `logcli` command to see LXD logs in Loki.

```{terminal}
:input: logcli query -t '{app="lxd"}'

2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Updating instance types
2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Expiring log files
2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Pruning resolved warnings
2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Updating images
2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Done pruning resolved warnings
2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Done expiring log files
2024-02-14T21:31:20Z {app="lxd", instance="node3", type="logging"} level="info" Done updating images
...
```

## Add labels

LXD pushes log entries with a set of predefined labels like `app`, `project`, `instance` and `name`. To see all existing labels, you can use `logcli labels`. Some log entries might contain information in their message that you would like to access as if they were keys. In the example below, you might want to have `requester-username` as a key to query.

```
2024-02-15T22:52:25Z {app="lxd", instance="node3", location="node3", name="c1", project="default", type="lifecycle"} requester-username="ubuntu" action="instance-started" source="/1.0/instances/c1" requester-address="@" requester-protocol="unix" instance-started
...
```

Use the following command to instruct LXD to move all occurrences of `requester-username="<user>"` into the label section:

    lxc config set loki.labels="requester-username"

This will transform the above log entry into:

```
2024-02-09T21:26:32Z {app="lxd", instance="node3", location="node3", name="c2", project="default", requester_username="ubuntu", type="lifecycle"} action="instance-started" source="/1.0/instances/c2" requester-address="@" requester-protocol="unix" instance-started
...
```

Note the replacement of `-` by `_`, as `-` cannot be used in keys. As `requested_username` is now a key, you can query Loki using it like this:

    logcli query -t '{requester_username="ubuntu"}'
