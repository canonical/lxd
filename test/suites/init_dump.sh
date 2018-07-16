test_init_dump() {
  # - lxd init --dump
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
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
    limits.memory: 2GB
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: lxdt$$
      type: nic
EOF
  lxd init --dump > config.yaml
cat <<EOF > expected.yaml
config:
  core.https_address: 127.0.0.1:9999
  core.trust_password: true
  images.auto_update_interval: "15"
networks:
- config:
    ipv4.address: none
    ipv6.address: none
  description: ""
  managed: true
  name: lxdt$$
  type: bridge
storage_pools:
- config:
    source: ${LXD_DIR}/storage-pools/${storage_pool}
  description: ""
  name: ${storage_pool}
  driver: ${driver}
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
    limits.memory: 2GB
  description: Test profile
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: lxdt$$
      type: nic
  name: test-profile

EOF

  diff -u config.yaml expected.yaml
)
  rm -f config.yaml expected.yaml
  kill_lxd "${LXD_INIT_DIR}"
}
