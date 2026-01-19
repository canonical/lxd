test_init_interactive() {
  # - lxd init
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_INIT_DIR}

    # XXX We need to remove the eth0 device from the default profile, which
    #     is typically attached by spawn_lxd.
    if lxc profile device get default eth0 name; then
      lxc profile device remove default eth0
    fi

    lxd init <<EOF
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
no
yes
EOF

    [ "$(lxc config get images.auto_update_interval)" = "0" ]
    lxc network list | grep -wF "lxdt$$"
    lxc storage list | grep -wF "my-storage-pool"
    lxc profile show default | grep -F "pool: my-storage-pool"
    lxc profile show default | grep -F "network: lxdt$$"
    echo -ne 'config: {}\ndevices: {}' | lxc profile edit default
    lxc network delete lxdt$$
  )
  kill_lxd "${LXD_INIT_DIR}"
}
