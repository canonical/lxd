test_init_preseed() {
  # - lxd init --preseed
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  cat <<EOF | LXD_DIR=${LXD_INIT_DIR} lxd init --preseed
config:
  core.https_address: 127.0.0.1:9999
EOF

  kill_lxd "${LXD_INIT_DIR}"

  return
}
