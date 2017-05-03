test_init_preseed() {
  # - lxd init --preseed
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  cat <<EOF | LXD_DIR=${LXD_INIT_DIR} lxd init --preseed
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: 15
networks:
- name: lxdt$$
  type: bridge
  config:
    ipv4.address: none
    ipv6.address: none
EOF

  LXD_DIR=${LXD_INIT_DIR} lxc info | grep -q 'core.https_address: 127.0.0.1:9999'
  LXD_DIR=${LXD_INIT_DIR} lxc info | grep -q 'images.auto_update_interval: "15"'
  LXD_DIR=${LXD_INIT_DIR} lxc network list | grep -q "lxdt$$"
  LXD_DIR=${LXD_INIT_DIR} lxc network delete lxdt$$

  kill_lxd "${LXD_INIT_DIR}"

  return
}
