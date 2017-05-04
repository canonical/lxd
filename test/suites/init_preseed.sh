test_init_preseed() {
  # - lxd init --preseed
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  cat <<EOF | LXD_DIR=${LXD_INIT_DIR} lxd init --preseed
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: 15
pools:
- name: data
  driver: dir
  config:
    source: ""
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
      parent: lxdt$$
      type: nic
EOF

  LXD_DIR=${LXD_INIT_DIR} lxc info | grep -q 'core.https_address: 127.0.0.1:9999'
  LXD_DIR=${LXD_INIT_DIR} lxc info | grep -q 'images.auto_update_interval: "15"'
  LXD_DIR=${LXD_INIT_DIR} lxc network list | grep -q "lxdt$$"
  LXD_DIR=${LXD_INIT_DIR} lxc storage list | grep -q "data"
  LXD_DIR=${LXD_INIT_DIR} lxc profile list | grep -q "test-profile"
  LXD_DIR=${LXD_INIT_DIR} lxc profile show default | grep -q "pool: data"
  LXD_DIR=${LXD_INIT_DIR} lxc profile show test-profile | grep -q "limits.memory: 2GB"
  LXD_DIR=${LXD_INIT_DIR} lxc profile show test-profile | grep -q "nictype: bridged"
  LXD_DIR=${LXD_INIT_DIR} lxc profile show test-profile | grep -q "parent: lxdt$$"
  LXD_DIR=${LXD_INIT_DIR} lxc profile delete default
  LXD_DIR=${LXD_INIT_DIR} lxc profile delete test-profile
  LXD_DIR=${LXD_INIT_DIR} lxc network delete lxdt$$
  LXD_DIR=${LXD_INIT_DIR} lxc storage delete data

  kill_lxd "${LXD_INIT_DIR}"

  return
}
