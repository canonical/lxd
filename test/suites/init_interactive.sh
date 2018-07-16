test_init_interactive() {
  # - lxd init
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_INIT_DIR}

    # XXX We need to remove the eth0 device from the default profile, which
    #     is typically attached by spawn_lxd.
    if lxc profile show default | grep -q eth0; then
      lxc profile device remove default eth0
    fi

    cat <<EOF | lxd init
no
yes
my-storage-pool
dir
no
yes
lxdt$$
auto
none
no
no
yes
EOF

    lxc info | grep -q 'images.auto_update_interval: "0"'
    lxc network list | grep -q "lxdt$$"
    lxc storage list | grep -q "my-storage-pool"
    lxc profile show default | grep -q "pool: my-storage-pool"
    lxc profile show default | grep -q "parent: lxdt$$"
    printf 'config: {}\ndevices: {}' | lxc profile edit default
    lxc network delete lxdt$$
  )
  kill_lxd "${LXD_INIT_DIR}"

  return
}
