(preseed-yaml-file-fields)=
# Preseed YAML file fields

You can configure a new LXD installation and reconfigure an existing installation with a preseed YAML file.

The preseed YAML file fields are as follows:

```yaml
config:
  core.https_address: ""
  core.trust_password: ""
  images.auto_update_interval: 6

networks:
  - config:
      ipv4.address: auto
      ipv4.nat: "true"
      ipv6.address: auto
      ipv6.nat: "true"
    description: ""
    name: lxdbr0
    type: bridge
    project: default

storage_pools:
  - config: {}
    description: ""
    name: default
    driver: zfs

storage_volumes:
- name: my-vol
  pool: data

profiles:
  - config:
      limits.memory: 2GiB
    description: Default LXD profile
    devices:
      eth0:
        name: eth0
        network: lxdbr0
        type: nic
      root:
        path: /
        pool: default
        type: disk
    name: default

projects:
  - config:
      features.images: "true"
      features.networks: "true"
      features.networks.zones: "true"
      features.profiles: "true"
      features.storage.buckets: "true"
      features.storage.volumes: "true"
    description: Default LXD project
    name: default

cluster:
  enabled: true
  server_address: ""
  cluster_token: ""
  member_config:
  - entity: storage-pool
    name: default
    key: source
    value: ""
  - entity: storage-pool
    name: my-pool
    key: source
    value: ""
  - entity: storage-pool
    name: my-pool
    key: driver
    value: "zfs"
```

## Related topics

{{initialize}}
