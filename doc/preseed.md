# Non-interactive configuration via preseed YAML

The `lxd init` command supports a `--preseed` command line flag that
makes it possible to fully configure LXD daemon settings, storage
pools, network devices and profiles, in a non-interactive way.

For example, starting from a brand new LXD installation, the command
line:

```bash
    cat <<EOF | lxd init --preseed
config:
  core.https_address: 192.168.1.1:9999
  images.auto_update_interval: 15
networks:
- name: lxdbr0
  type: bridge
  config:
    ipv4.address: auto
    ipv6.address: none
EOF
```

will configure the LXD daemon to listen for HTTPS connections on port
9999 of the 192.168.1.1 address, to automatically update images every
15 hours, and to create a network bridge device named `lxdbr0`, which
will get assigned an IPv4 address automatically.

## Configure a brand new LXD

If you are configuring a brand new LXD instance, then the preseed
command will always succeed and apply the desired configuration (as
long as the given YAML contains valid keys and values), since there is
no existing state that might conflict with the desired one.

## Re-configuring an existing LXD

If you are re-configuring an existing LXD instance using the preseed
command, then the provided YAML configuration is meant to completely
overwrite existing entities (if the provided entities do not exist,
they will just be created, as in the brand new LXD case).

In case you are overwriting an existing entity you must provide the full
configuration of the new desired state for the entity (i.e. the semantics is
the same as a `PUT` request in the [RESTful API](rest-api.md)).

### Rollback

If some parts of the new desired configuration conflict with the
existing state (for example they try to change the driver of a storage
pool from `dir` to `zfs`), then the preseed command will fail and will
automatically try its best to rollback any change that was applied so
far.

For example it will delete entities that were created by the new
configuration and revert overwritten entities back to their original
state.

Failure modes when overwriting entities are the same as `PUT` requests
in the [RESTful API](rest-api.md).

Note however, that the rollback itself might potentially fail as well,
although rarely (typically due to backend bugs or limitations). Thus
care must be taken when trying to reconfigure an LXD daemon via
preseed.

## Default profile

Differently from the interactive init mode, the `lxd init --preseed`
command line will not modify the default profile in any particular
way, unless you explicitly express that in the provided YAML payload.

For instance, you will typically want to attach a root disk device and
a network interface to your default profile. See below for an example.

# Configuration format

The supported keys and values of the various entities are the same as
the ones documented in the [RESTful API](rest-api.md), but converted
to YAML for easier reading (however you can use JSON too, since YAML
is a superset of JSON).

Here follows an example of a preseed payload containing most of the
possible configuration knobs. You can use it as a template for your
own one, and add, change or remove what you need:

```yaml

# Daemon settings
config:
  core.https_address: 192.168.1.1:9999
  core.trust_password: sekret
  images.auto_update_interval: 6

# Storage pools
storage_pools:
- name: data
  driver: zfs
  config:     
    source: my-zfs-pool/my-zfs-dataset

# Network devices
networks:
- name: lxd-my-bridge
  type: bridge
  config:
    ipv4.address: auto
    ipv6.address: none

# Profiles
profiles:
- name: default
  devices:
    root:
      path: /
      pool: data
      type: disk
- name: test-profile
  description: "Test profile"
  config:
    limits.memory: 2GB
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: lxd-my-bridge
      type: nic
```
