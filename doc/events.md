---
myst:
  html_meta:
    description: LXD events API reference covering logging, operation, lifecycle, ovn, and security events. Learn event types, structures, and how to access events via monitor or WebSocket.
---

(events)=
# Events

## Introduction

Events are messages about actions that have occurred over LXD. Using the API endpoint `/1.0/events` directly or via
[`lxc monitor`](lxc_monitor.md) will connect to a WebSocket through which events of the selected types will be streamed.

## Event types

LXD currently supports five event types.

- `logging`: Shows all logging messages regardless of the server logging level.
- `operation`: Shows all ongoing operations from creation to completion (including updates to their state and progress metadata).
- `lifecycle`: Shows an audit trail for specific actions occurring over LXD.
- `ovn`: Shows network-related events from OVN (Open Virtual Network).
- `security`: Shows security-related events including authentication attempts, authorisation decisions, and administrative changes. Requires appropriate permissions to view.

## Event structure

### Example

```yaml
location: cluster_name
metadata:
  action: network-updated
  requestor:
    protocol: unix
    username: root
  source: /1.0/networks/lxdbr0
timestamp: "2021-03-14T00:00:00Z"
type: lifecycle
```

- `location`: The cluster member name (if clustered).
- `timestamp`: Time that the event occurred in RFC3339 format.
- `type`: Type of event (one of `logging`, `operation`, `lifecycle`, `ovn`, or `security`).
- `metadata`: Information about the specific event type.

### Logging event structure

- `message`: The log message.
- `level`: The log-level of the log.
- `context`: Additional information included in the event.

(ref-events-operation)=
### Operation event structure

- `id`: The UUID of the operation.
- `class`: The type of operation (`task`, `token`, or `websocket`).
- `description`: A description of the operation.
- `created_at`: The operation's creation date.
- `updated_at`: The operation's date of last change.
- `status`: The current state of the operation.
- `status_code`: The operation status code.
- `resources`: Resources affected by this operation.
- `metadata`: Operation specific metadata.
- `may_cancel`: Whether the operation may be canceled.
- `err`: Error message of the operation.
- `location`: The cluster member name (if clustered).

### Life-cycle event structure

- `action`: The life-cycle action that occurred.
- `requestor`: Information about who is making the request (if applicable).
- `source`: Path to what is being acted upon.
- `context`: Additional information included in the event.

## Supported life-cycle events

| Name                                   | Description                                                           | Additional Information                                                                               |
| :------------------------------------- | :-------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------- |
| `certificate-created`                  | A new certificate has been added to the server trust store.           |                                                                                                      |
| `certificate-deleted`                  | The certificate has been deleted from the trust store.                |                                                                                                      |
| `certificate-updated`                  | The certificate's configuration has been updated.                     |                                                                                                      |
| `cluster-certificate-updated`          | The certificate for the whole cluster has changed.                    |                                                                                                      |
| `cluster-disabled`                     | Clustering has been disabled for this machine.                        |                                                                                                      |
| `cluster-enabled`                      | Clustering has been enabled for this machine.                         |                                                                                                      |
| `cluster-group-created`                | A new cluster group has been created.                                 |                                                                                                      |
| `cluster-group-deleted`                | A cluster group has been deleted.                                     |                                                                                                      |
| `cluster-group-renamed`                | A cluster group has been renamed.                                     |                                                                                                      |
| `cluster-group-updated`                | A cluster group has been updated.                                     |                                                                                                      |
| `cluster-member-added`                 | A new machine has joined the cluster.                                 |                                                                                                      |
| `cluster-member-removed`               | The cluster member has been removed from the cluster.                 |                                                                                                      |
| `cluster-member-renamed`               | The cluster member has been renamed.                                  | `old_name`: the previous name.                                                                       |
| `cluster-member-updated`               | The cluster member's configuration been edited.                       |                                                                                                      |
| `cluster-token-created`                | A join token for adding a cluster member has been created.            |                                                                                                      |
| `config-updated`                       | The server configuration has changed.                                 |                                                                                                      |
| `image-alias-created`                  | An alias has been created for an existing image.                      | `target`: the original instance.                                                                     |
| `image-alias-deleted`                  | An alias has been deleted for an existing image.                      | `target`: the original instance.                                                                     |
| `image-alias-renamed`                  | The alias for an existing image has been renamed.                     | `old_name`: the previous name.                                                                       |
| `image-alias-updated`                  | The configuration for an image alias has changed.                     | `target`: the original instance.                                                                     |
| `image-created`                        | A new image has been added to the image store.                        | `type`: `container` or `vm`.                                                                         |
| `image-deleted`                        | The image has been deleted from the image store.                      |                                                                                                      |
| `image-refreshed`                      | The local image copy has updated to the current source image version. |                                                                                                      |
| `image-retrieved`                      | The raw image file has been downloaded from the server.               | `target`: destination server.                                                                        |
| `image-secret-created`                 | A one-time key to fetch this image has been created.                  |                                                                                                      |
| `image-updated`                        | The image's configuration has changed.                                |                                                                                                      |
| `instance-backup-created`              | A backup of the instance has been created.                            |                                                                                                      |
| `instance-backup-deleted`              | The instance backup has been deleted.                                 |                                                                                                      |
| `instance-backup-renamed`              | The instance backup has been renamed.                                 | `old_name`: the previous name.                                                                       |
| `instance-backup-retrieved`            | The raw instance backup file has been downloaded.                     |                                                                                                      |
| `instance-console`                     | Connected to the console of the instance.                             | `type`: `console` or `vga`.                                                                          |
| `instance-console-reset`               | The console buffer has been reset.                                    |                                                                                                      |
| `instance-console-retrieved`           | The console log has been downloaded.                                  |                                                                                                      |
| `instance-created`                     | A new instance has been created.                                      |                                                                                                      |
| `instance-deleted`                     | The instance has been deleted.                                        |                                                                                                      |
| `instance-exec`                        | A command has been executed on the instance.                          | `command`: the command to be executed.                                                               |
| `instance-file-deleted`                | A file on the instance has been deleted.                              | `file`: path to the file.                                                                            |
| `instance-file-pushed`                 | The file has been pushed to the instance.                             | `file-source`: local file path. `file-destination`: destination file path. `info`: file information. |
| `instance-file-retrieved`              | The file has been downloaded from the instance.                       | `file-source`: instance file path. `file-destination`: destination file path.                        |
| `instance-log-deleted`                 | The instance's specified log file has been deleted.                   |                                                                                                      |
| `instance-log-retrieved`               | The instance's specified log file has been downloaded.                |                                                                                                      |
| `instance-metadata-retrieved`          | The instance's image metadata has been downloaded.                    |                                                                                                      |
| `instance-metadata-template-created`   | A new image template file for the instance has been created.          | `path`: relative file path.                                                                          |
| `instance-metadata-template-deleted`   | The image template file for the instance has been deleted.            | `path`: relative file path.                                                                          |
| `instance-metadata-template-retrieved` | The image template file for the instance has been downloaded.         | `path`: relative file path.                                                                          |
| `instance-metadata-updated`            | The instance's image metadata has changed.                            |                                                                                                      |
| `instance-paused`                      | The instance has been put in a paused state.                          |                                                                                                      |
| `instance-ready`                       | The instance is ready.                                                |                                                                                                      |
| `instance-renamed`                     | The instance has been renamed.                                        | `old_name`: the previous name.                                                                       |
| `instance-restarted`                   | The instance has restarted.                                           |                                                                                                      |
| `instance-restored`                    | The instance has been restored from a snapshot.                       | `snapshot`: name of the snapshot being restored.                                                     |
| `instance-resumed`                     | The instance has resumed after being paused.                          |                                                                                                      |
| `instance-shutdown`                    | The instance has shut down.                                           |                                                                                                      |
| `instance-snapshot-created`            | A snapshot of the instance has been created.                          |                                                                                                      |
| `instance-snapshot-deleted`            | The instance snapshot has been deleted.                               |                                                                                                      |
| `instance-snapshot-renamed`            | The instance snapshot has been renamed.                               | `old_name`: the previous name.                                                                       |
| `instance-snapshot-updated`            | The instance snapshot's configuration has changed.                    |                                                                                                      |
| `instance-started`                     | The instance has started.                                             |                                                                                                      |
| `instance-stopped`                     | The instance has stopped.                                             |                                                                                                      |
| `instance-updated`                     | The instance's configuration has changed.                             |                                                                                                      |
| `network-acl-created`                  | A new network ACL has been created.                                   |                                                                                                      |
| `network-acl-deleted`                  | The network ACL has been deleted.                                     |                                                                                                      |
| `network-acl-renamed`                  | The network ACL has been renamed.                                     | `old_name`: the previous name.                                                                       |
| `network-acl-updated`                  | The network ACL configuration has changed.                            |                                                                                                      |
| `network-created`                      | A network device has been created.                                    |                                                                                                      |
| `network-deleted`                      | The network device has been deleted.                                  |                                                                                                      |
| `network-forward-created`              | A new network forward has been created.                               |                                                                                                      |
| `network-forward-deleted`              | The network forward has been deleted.                                 |                                                                                                      |
| `network-forward-updated`              | The network forward has been updated.                                 |                                                                                                      |
| `network-peer-created`                 | A new network peer has been created.                                  |                                                                                                      |
| `network-peer-deleted`                 | The network peer has been deleted.                                    |                                                                                                      |
| `network-peer-updated`                 | The network peer has been updated.                                    |                                                                                                      |
| `network-renamed`                      | The network device has been renamed.                                  | `old_name`: the previous name.                                                                       |
| `network-updated`                      | The network device's configuration has changed.                       |                                                                                                      |
| `network-zone-created`                 | A new network zone has been created.                                  |                                                                                                      |
| `network-zone-deleted`                 | The network zone has been deleted.                                    |                                                                                                      |
| `network-zone-record-created`          | A new network zone record has been created.                           |                                                                                                      |
| `network-zone-record-deleted`          | The network zone record has been deleted.                             |                                                                                                      |
| `network-zone-record-updated`          | The network zone record has been updated.                             |                                                                                                      |
| `network-zone-updated`                 | The network zone has been updated.                                    |                                                                                                      |
| `operation-cancelled`                  | The operation has been canceled.                                      |                                                                                                      |
| `profile-created`                      | A new profile has been created.                                       |                                                                                                      |
| `profile-deleted`                      | The profile has been deleted.                                         |                                                                                                      |
| `profile-renamed`                      | The profile has been renamed .                                        | `old_name`: the previous name.                                                                       |
| `profile-updated`                      | The profile's configuration has changed.                              |                                                                                                      |
| `project-created`                      | A new project has been created.                                       |                                                                                                      |
| `project-deleted`                      | The project has been deleted.                                         |                                                                                                      |
| `project-renamed`                      | The project has been renamed.                                         | `old_name`: the previous name.                                                                       |
| `project-updated`                      | The project's configuration has changed.                              |                                                                                                      |
| `storage-pool-created`                 | A new storage pool has been created.                                  | `target`: cluster member name.                                                                       |
| `storage-pool-deleted`                 | The storage pool has been deleted.                                    |                                                                                                      |
| `storage-pool-updated`                 | The storage pool's configuration has changed.                         | `target`: cluster member name.                                                                       |
| `storage-volume-backup-created`        | A new backup for the storage volume has been created.                 | `type`: `container`, `virtual-machine`, `image`, or `custom`.                                        |
| `storage-volume-backup-deleted`        | The storage volume's backup has been deleted.                         |                                                                                                      |
| `storage-volume-backup-renamed`        | The storage volume's backup has been renamed.                         | `old_name`: the previous name.                                                                       |
| `storage-volume-backup-retrieved`      | The storage volume's backup has been downloaded.                      |                                                                                                      |
| `storage-volume-created`               | A new storage volume has been created.                                | `type`: `container`, `virtual-machine`, `image`, or `custom`.                                        |
| `storage-volume-deleted`               | The storage volume has been deleted.                                  |                                                                                                      |
| `storage-volume-renamed`               | The storage volume has been renamed.                                  | `old_name`: the previous name.                                                                       |
| `storage-volume-restored`              | The storage volume has been restored from a snapshot.                 | `snapshot`: name of the snapshot being restored.                                                     |
| `storage-volume-snapshot-created`      | A new storage volume snapshot has been created.                       | `type`: `container`, `virtual-machine`, `image`, or `custom`.                                        |
| `storage-volume-snapshot-deleted`      | The storage volume's snapshot has been deleted.                       |                                                                                                      |
| `storage-volume-snapshot-renamed`      | The storage volume's snapshot has been renamed.                       | `old_name`: the previous name.                                                                       |
| `storage-volume-snapshot-updated`      | The configuration for the storage volume's snapshot has changed.      |                                                                                                      |
| `storage-volume-updated`               | The storage volume's configuration has changed.                       |                                                                                                      |
| `warning-acknowledged`                 | The warning's status has been set to "acknowledged".                  |                                                                                                      |
| `warning-deleted`                      | The warning has been deleted.                                         |                                                                                                      |
| `warning-reset`                        | The warning's status has been set to "new".                           |                                                                                                      |

(events-security)=
## Security events

### Security event structure

- `name`: The security event identifier (e.g., `authn_login_fail:tls`, `authz_fail:can_edit:/1.0/projects/foo`).
- `level`: The severity level (`info`, `warning`).
- `description`: A human-readable description of the event.
- `requestor`: Who triggered the event (username, protocol, address, user agent). Omitted for daemon-level events.
- `project`: The project the request targeted. Omitted for daemon-level events.
- `request_path`: The REST API endpoint path. Omitted for daemon-level events.
- `request_method`: The HTTP method used. Omitted for daemon-level events.

### Security event types

LXD emits events across four categories.

**Authentication events**

| Event | Description |
|-------|-------------|
| `authn_login_fail:tls` | Failed authentication attempt when an untrusted TLS client certificate is presented to a protected endpoint. |
| `authn_token_created:<identity>` | A new bearer token was issued for an identity. The identity UUID is included in the event identifier. |
| `authn_token_revoked:<identity>` | A bearer token was revoked for an identity. The identity UUID is included in the event identifier. |
| `authn_token_reuse` | A bearer token was presented in an invalid, expired, or otherwise disallowed way, indicating possible token reuse, tampering, or other misuse. |
| `authn_certificate_change:<fingerprint>` | A TLS client certificate was replaced. The old certificate fingerprint is included in the event identifier. |

**Authorization events**

| Event | Description |
|-------|-------------|
| `authz_fail:<entitlement>:<entity>` | An action was denied due to insufficient permissions. Includes the required entitlement and the entity path that was accessed. |
| `authz_admin:group_create:<name>` | A new authentication group was created. |
| `authz_admin:group_edit:<name>` | An authentication group was modified. |
| `authz_admin:group_delete:<name>` | An authentication group was deleted. |
| `authz_admin:idp_group_create:<name>` | A new identity provider group was created. |
| `authz_admin:idp_group_edit:<name>` | An identity provider group was modified. |
| `authz_admin:idp_group_delete:<name>` | An identity provider group was deleted. |
| `authz_admin:identity_create:<method>/<identifier>` | A new identity was created (TLS certificates or bearer tokens only). OIDC identities are not created via API actions. |
| `authz_admin:identity_edit:<method>/<identifier>` | An identity was modified. |
| `authz_admin:identity_delete:<method>/<identifier>` | An identity was deleted. |

**Daemon lifecycle events**

| Event | Description |
|-------|-------------|
| `sys_startup` | The LXD daemon has started. Emitted once the event system is fully available. |
| `sys_shutdown` | The LXD daemon is shutting down. |
| `sys_monitor_disabled` | Security event monitoring (Loki) was disabled via a configuration change. This is a `warning`-level event. |

**User lifecycle events**

| Event | Description |
|-------|-------------|
| `user_created` | A new identity has been created (TLS, bearer, OIDC, or cluster-link methods). For OIDC, this fires on first login. |
| `user_updated` | An identity has been modified. For OIDC, this fires when user metadata changes on subsequent logins. |
| `user_deleted` | An identity has been deleted. |

(events-security-loki-fields)=
### Security event fields in Loki

When security events are forwarded to Loki, they are stored in
[OWASP (Open Worldwide Application Security Project)](https://owasp.org/)
audit log format with the following key fields:

| Field | Description |
|-------|-------------|
| `name` | The security event type identifier. |
| `event_source` | The cluster member name where the event occurred. |
| `cluster_identifier` | Unique identifier for the LXD cluster. |
| `cluster_member_name` | Name of the cluster member. |
| `project` | The project targeted by the request. Empty or omitted for daemon-level events. |
| `request_method` | The HTTP method used. |
| `request_uri` | The API endpoint path. |
| `user_id` | The requestor identity in format `<auth_method>/<identifier>`. |
| `source_ip` | The source IP address of the request. |
| `useragent` | The HTTP user agent string. |
| `level` | The event severity (`info`, `warning`). |
| `description` | Human-readable event description. |

For how to monitor and query security events, see {ref}`howto-security-events`.
