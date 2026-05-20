---
myst:
  html_meta:
    description: Monitor security events in LXD including authentication, authorisation, and administrative actions using the CLI, REST API, or Loki integration.
---

(howto-security-events)=
# Monitor security events

LXD emits security events that track authentication attempts, authorization
decisions, and administrative changes. You can access these events through
the CLI, the REST API, or by forwarding them to Loki for centralized log
retention and analysis.

For the full list of event types and field definitions, see {ref}`events-security`.

## View security events with the CLI

Use the `lxc monitor` command to stream security events in real time:

```bash
lxc monitor --type=security --format=yaml
```

You will see output like:

```yaml
type: security
timestamp: 2026-05-08T14:32:15Z
location: lxd1
metadata:
  name: authn_login_fail:tls
  level: warning
  description: "Authentication failure: untrusted client certificate"
  requestor:
    username: ""
    protocol: tls
    address: "192.168.1.100:45632"
    user_agent: "curl/7.68.0"
  request_path: /1.0/projects
  request_method: GET
```

## View security events with the REST API

Connect to the `/1.0/events` WebSocket endpoint with `type=security`.
Access requires appropriate permissions on the server.

For general event stream usage, see {ref}`events`.

## Monitor security events with Loki

In a production environment, forward security events to
[Loki](https://grafana.com/oss/loki/) for centralised audit log
aggregation and analysis.

For general Loki setup, see {ref}`logs_loki`. The steps below cover
security-event-specific configuration and queries.

### Configure security event forwarding

Ensure `security` is included in `loki.types`:

```bash
lxc config set loki.types=logging,lifecycle,security
```

LXD will forward security events to Loki in [OWASP (Open Worldwide Application Security Project)](https://owasp.org/) audit log format. See {ref}`events-security-loki-fields` for the full field mapping.

### Query security events

Use the LogCLI utility to query security events:

```bash
logcli query -t '{type="security"}'
```

Filter by a specific event type:

```bash
logcli query -t '{type="security"}' | grep 'authn_login_fail'
```

Filter by requestor identity (requires adding `user_id` to `loki.labels`):

```bash
logcli query -t '{type="security", user_id="tls/alice"}'
```

Alternatively, use a JSON parsing pipeline to filter without modifying labels:

```bash
logcli query -t '{type="security"} | json | user_id="tls/alice"'
```
