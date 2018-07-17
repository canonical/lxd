test_init_preseed() {
  # - lxd init --preseed
  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_INIT_DIR}

    storage_pool="lxdtest-$(basename "${LXD_DIR}")-data"
    # In case we're running against the ZFS backend, let's test
    # creating a zfs storage pool, otherwise just use dir.
    if [ "$lxd_backend" = "zfs" ]; then
        configure_loop_device loop_file_4 loop_device_4
        # shellcheck disable=SC2154
        zpool create -f -m none -O compression=on "lxdtest-$(basename "${LXD_DIR}")-preseed-pool" "${loop_device_4}"
        driver="zfs"
        source="lxdtest-$(basename "${LXD_DIR}")-preseed-pool"
    elif [ "$lxd_backend" = "ceph" ]; then
        driver="ceph"
        source=""
    else
        driver="dir"
        source=""
    fi

    cat <<EOF | lxd init --preseed
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: 15
storage_pools:
- name: ${storage_pool}
  driver: $driver
  config:
    source: $source
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

    lxc info | grep -q 'core.https_address: 127.0.0.1:9999'
    lxc info | grep -q 'images.auto_update_interval: "15"'
    lxc network list | grep -q "lxdt$$"
    lxc storage list | grep -q "${storage_pool}"
    lxc storage show "${storage_pool}" | grep -q "$source"
    lxc profile list | grep -q "test-profile"
    lxc profile show default | grep -q "pool: ${storage_pool}"
    lxc profile show test-profile | grep -q "limits.memory: 2GB"
    lxc profile show test-profile | grep -q "nictype: bridged"
    lxc profile show test-profile | grep -q "parent: lxdt$$"
    printf 'config: {}\ndevices: {}' | lxc profile edit default
    lxc profile delete test-profile
    lxc network delete lxdt$$
    lxc storage delete "${storage_pool}"

    if [ "$lxd_backend" = "zfs" ]; then
        # shellcheck disable=SC2154
        deconfigure_loop_device "${loop_file_4}" "${loop_device_4}"
    fi
  )
  kill_lxd "${LXD_INIT_DIR}"
}
