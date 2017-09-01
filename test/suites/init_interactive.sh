test_init_interactive() {
  # - lxd init
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}"

  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_INIT_DIR}

    cat <<EOF | lxd init
dir
no
no
EOF

    lxc profile delete default
  )
  kill_lxd "${LXD_INIT_DIR}"

  return
}
