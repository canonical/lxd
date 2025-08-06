test_init_dump() {
  # - lxd init --dump
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_INIT_DIR}

    storage_pool="lxdtest-$(basename "${LXD_DIR}")-data"
    driver="dir"

    cat <<EOF | lxd init --preseed
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: 15
storage_pools:
- name: ${storage_pool}
  driver: $driver
networks:
- name: lxdt$$
  type: bridge
  config:
    ipv4.address: none
    ipv6.address: none
profiles:
- name: default
  devices:
    root:
      path: /
      pool: ${storage_pool}
      type: disk
- name: test-profile
  description: "Test profile"
  config:
    limits.memory: 2GiB
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: lxdt$$
      type: nic
EOF
  lxd init --dump > config.yaml

cluster_uuid="$(lxc config get volatile.uuid)"

cat <<EOF > expected.yaml
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: "15"
  volatile.uuid: ${cluster_uuid}
networks:
- config:
    ipv4.address: none
    ipv6.address: none
  description: ""
  name: lxdt$$
  type: bridge
  project: default
storage_pools:
- config:
    source: ${LXD_DIR}/storage-pools/${storage_pool}
  description: ""
  name: ${storage_pool}
  driver: ${driver}
storage_volumes: []
profiles:
- config: {}
  description: Default LXD profile
  devices:
    eth0:
      name: eth0
      nictype: p2p
      type: nic
    root:
      path: /
      pool: ${storage_pool}
      type: disk
  name: default
- config:
    limits.memory: 2GiB
  description: Test profile
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: lxdt$$
      type: nic
  name: test-profile
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
  storage: ""
  network: ""

EOF

  diff -u config.yaml expected.yaml
)
  rm -f config.yaml expected.yaml
  kill_lxd "${LXD_INIT_DIR}"
}
