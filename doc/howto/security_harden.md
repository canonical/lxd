(howto-security-harden)=
# How to harden security for LXD

To increase the security posture of your LXD deployment, review the following hardening recommendations and apply those relevant to your setup.

## General

(howto-security-harden-supported)=
### Use a supported version

Use only supported LTS releases or the latest feature release of LXD, and ensure that you update it regularly to receive security updates and bugfixes. See: {ref}`ref-releases`.

(howto-security-harden-disable-unused)=
### Disable unused features

If you are not using certain LXD features, disable them to reduce the attack surface. For example:


- Disable the UI: 
   ```bash
   sudo snap set lxd ui.enable=false
   sudo systemctl reload snap.lxd.daemon
   ``` 
- Delete unused networks and storage pools.

## Access

(howto-security-harden-group)=
### Secure the `lxd` group

Users in the `lxd` group who access LXD through the local Unix socket are given full administrative control over LXD. Thus, ensure that only trusted users are members of the `lxd` group (or any custom group you configure via `snap.lxd.daemon.group`). Audit group membership regularly.

Also see: {ref}`howto-security-harden-restricted-group`.

(howto-security-harden-remote)=
### Harden remote API access

For {ref}`authentication`, LXD can use either {abbr}`TLS (Transport Layer Security)` client certificates or OpenID Connect:

- Client certificates:
   - Ensure that only clients with certificates issued by your trusted Certificate Authority (CA) can connect. The {config:option}`server-core:core.trust_ca_certificates` option is `false` by default. To prevent auto-trusting of CA-signed certificates, ensure it remains disabled.
   - Regularly audit and remove unused client certificates from the trust store.

- OpenID Connect:
   - Only set `oidc.client.secret` if required by the identity provider.
   - Configure your OIDC provider to issue short-lived access tokens.
   - Require multi-factor authentication (MFA) in your identity provider for accounts that can access LXD.

For {ref}`authorization`, use {ref}`restricted-tls-certs` or {ref}`fine-grained-authorization` where relevant to your setup.

Refer to the {ref}`authentication` and {ref}`authorization` pages for details.

(howto-security-harden-auth-expiry)=
### Decrease token expiry

By default, LXD cluster join tokens are set to expire after 3 hours, and LXD remote authentication tokens (used to access the LXD UI and API remotely) are set to never expire. Decrease the expiry times, such as to 15 minutes:

```bash
sudo lxc config set cluster.join_token_expiry 15M
sudo lxc config set core.remote_token_expiry 15M
```

(howto-security-harden-network)=
## Network security

Control traffic on LXD networks.

(howto-security-harden-acls)=
### Configure ACLs

{ref}`Network Access Control Lists <network-acls>` (ACLs) are used to control traffic between instances and external networks, as well as traffic between instances on the same network. Set ACL rules to limit traffic to only what is necessary.

(howto-security-harden-use-ip)=
### Limit network exposure

By default, LXD is only accessible locally through a Unix socket. If you need to {ref}`expose LXD to the network <server-expose>`, you must set the LXD server’s {config:option}`server-core:core.https_address`. To reduce the attack surface, do not set this address to a port alone.

Instead, use a trusted IP address on the LXD management interface along with a port, such as `192.0.2.10:8443`. If you only need local HTTPS access, use the loopback address and port, such as `127.0.0.1:8443`.

(howto-security-harden-instance)=
## Instance security

Along with the recommendations below, review all {ref}`instance security options <instance-options-security>` for further options that might be relevant to your setup.

(howto-security-harden-unprivileged)=
### Use unprivileged containers

By default, LXD containers are unprivileged. If you need to use privileged containers, make sure to put appropriate security measures in place. For more information, see: {ref}`container-security`.

(howto-security-harden-instance-resource-limits)=
### Set instance resource limits

There are multiple {ref}`instance-options-limits` that can be configured for instances. To decrease the potential damage from DoS attacks, set reasonable limits.

This is especially important for containers and their {config:option}`instance-resource-limits:limits.cpu`, {config:option}`instance-resource-limits:limits.memory`, and {config:option}`instance-resource-limits:limits.processes` options, which by default are set without limits. Review the {ref}`instance-options-limits` reference guide for other options you might want to restrict.

Rather than applying these limits on a per-instance basis, use either {ref}`projects`, {ref}`profiles <images-profiles>`, or both. Example syntax for using a profile:

```bash
sudo lxc profile create hardened1
sudo lxc profile set hardened1 limits.cpu=2 limits.memory=4GiB limits.processes=500
sudo lxc profile add <my-container> hardened1
```

(howto-security-harden-nesting-disable)=
### Disable container nesting

The instance configuration option {config:option}`instance-security:security.nesting` enables nested container capability. This increases complexity and can broaden the attack surface. The default for this setting is `false`. Do not set this to `true` unless absolutely needed.

Setting this option to `true` is especially dangerous in combination with {config:option}`instance-security:security.privileged` set to `true` because it provides root access to the host.

(howto-security-harden-isolate)=
### Isolate containers

If a set of containers do not need to share data with each other, enable the instance option {config:option}`instance-security:security.idmap.isolated` on each one. This configures them to use unique UID/GID maps, preventing potential {abbr}`DoS (Denial of Service)` attacks from one container to another. Only unprivileged containers can use this option.

(howto-security-harden-device)=
## Device security

(howto-security-harden-passthrough)=
### Limit device passthrough

PCI, USB, and disk device passthroughs give the container significant access to the host. Avoid adding devices to instances unless strictly necessary. Set {ref}`disk device <devices-disk>` mounts to {config:option}`device-disk-device-conf:readonly` where possible.

(howto-security-harden-spoof)=
### Prevent spoofing

With bridged NICs, the default configuration allows MAC or IP spoofing. For details on how to prevent this, see {ref}`exp-security-bridged`.

(howto-security-harden-storage)=
## Storage device security

The Linux kernel might ignore mount options if a block-based filesystem (like `ext4`) is already mounted with different options. Thus, sharing the same disk device across multiple storage pools can lead to unexpected mount behavior.

To avoid security issues, either dedicate a disk device per storage pool or ensure that all pools sharing a device use the same mount options. For more information, see the {ref}`storage-drivers-security` section of the {ref}`storage-drivers` reference guide.

(howto-security-harden-logging)=
## Logging

Increase logging and regularly audit the logs for suspicious activity.

(howto-security-harden-logging-system)=
### Use system logging

Enable system logging for the LXD daemon and set it to the `verbose` level:

```bash
sudo snap set lxd daemon.syslog=true
sudo snap set lxd daemon.verbose=true
```

Regularly check these logs using:

```bash
sudo snap logs lxd.daemon
```

By default, only the last 10 lines are output. To see more, use the `-n=[all|<#>]` flag.

For example, to see all logs, run:

```bash
sudo snap logs -n=all lxd.daemon
```

(howto-security-harden-logging-auditd)=
### Use `auditd` rules

Use `auditd` rules to track LXD command execution and configuration file changes.

Configure the audit daemon to track all commands to the LXD daemon:

```bash
-a always,exit -F path=/usr/sbin/lxc -p x -k lxd_execution
-a always,exit -F path=/usr/sbin/lxd -p x -k lxd_execution
-a always,exit -F path=/snap/bin/lxd.buginfo -p x -k lxd_execution
-a always,exit -F path=/snap/bin/lxd.check-kernel -p x -k lxd_execution
-a always,exit -F path=/snap/bin/lxd.lxc -p x -k lxd_execution
```

### `lxc` `monitor` and Loki

The`lxc monitor` command is used to view information about logging and life cycle LXD events. Consider using a dedicated system that allows you to keep a record of these events, such as Loki. See: {ref}`logs_loki`.

(howto-security-harden-multi-user)=
## Multi-user environment

These settings are relevant if your LXD server is used by multiple users, such as in a lab setting.

(howto-security-harden-restricted-group)=
### Use a restricted group for non-admin users

By default, both the `daemon.group` and `daemon.user.group` are set to `lxd`. This gives all users in the `lxd` group full local access to LXD through the Unix socket. This includes the ability to attach file system paths or devices to any instance, or tweak any instance’s security features.

Only users who are trusted with `sudo` access to your system should be in the `daemon.group`. Define and use a separate group for users who should not have admin access, such as `lxdusers`:

```bash
sudo groupadd lxdusers
sudo snap set lxd daemon.user.group=lxdusers
```

(howto-security-harden-projects)=
### Confine users to projects

You can confine users to specific projects, which can be configured with stricter restrictions to prevent misuse. For details, see: {ref}`projects-confine-users`, {ref}`exp-projects`, and {ref}`restricted-tls-certs`.

(howto-security-harden-host)=
## Harden the LXD host OS

To harden your deployment, also harden the host's operating system (OS). These are some ways you can harden the host OS:

- Keep your OS updated and install all available security patches.
- Use a firewall to drop unexpected inbound traffic and restrict outbound traffic as needed. Ensure only the necessary ports are open.
- For Ubuntu systems, subscribe to [Ubuntu Pro](https://ubuntu.com/pro).
- Use the latest [CIS hardening benchmarks](https://www.cisecurity.org/cis-benchmarks) for your OS.

(howto-security-harden-cis)=
### Ubuntu CIS hardening

For Ubuntu LTS releases subscribed to Ubuntu Pro, use the [Ubuntu Security Guide (USG)](https://documentation.ubuntu.com/security/docs/compliance/usg/) tool for CIS hardening.

If the Ubuntu system is running LXD containers, the USG audit will report failure on the following rule IDs:

```
content_rule_no_files_unowned_by_user
content_rule_file_permissions_ungroupowned
```

By design, LXD's unprivileged containers run inside a user namespace for greater isolation. This causes some files and directories under `/sys/fs/cgroup/lxc.payload.<container_name>` to appear as having no owner. Since this is expected, the USG tool's failure report for this can be ignored. If you wish, you can the [customize the tool's CIS profile](https://ubuntu.com/security/certifications/docs/usg/cis/customization) to always ignore it.

## Related topics

How-to guides:

- {ref}`network-bridge-firewall`
- {ref}`projects-confine`

Explanation:

- {ref}`exp-security`
- {ref}`authentication`
- {ref}`authorization`

Reference:

- {ref}`Instance-level security options <instance-options-security>`
