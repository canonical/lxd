test_init_auto() {
  # - lxd init --auto --storage-backend zfs
  # and
  # - lxd init --auto
  # can't be easily tested on jenkins since it hard-codes "default" as pool
  # naming. This can cause naming conflicts when multiple test-suites are run on
  # a single runner.

  if [ "$(storage_backend "$LXD_DIR")" = "zfs" ]; then
    # lxd init --auto --storage-backend zfs --storage-pool <name>
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    spawn_lxd "${LXD_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    # shellcheck disable=SC2154
    zpool create -m none -O compression=on "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}"
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool"
    [ "$(LXD_DIR=${LXD_INIT_DIR} lxc profile device get default root pool)" = "default" ]

    kill_lxd "${LXD_INIT_DIR}"
    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"

    # lxd init --auto --storage-backend zfs --storage-pool <name>/<non-existing-dataset>
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    spawn_lxd "${LXD_INIT_DIR}" false

    # shellcheck disable=SC2154
    configure_loop_device loop_file_1 loop_device_1
    zpool create -m none -O compression=on "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}"
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/non-existing-dataset"
    kill_lxd "${LXD_INIT_DIR}"

    # lxd init --auto --storage-backend zfs --storage-pool <name>/<existing-dataset>
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    spawn_lxd "${LXD_INIT_DIR}" false

    zfs create -p -o mountpoint=none "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/existing-dataset"
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/existing-dataset"

    kill_lxd "${LXD_INIT_DIR}"
    zpool destroy "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool"
    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"

    # lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool <name> --auto
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    spawn_lxd "${LXD_INIT_DIR}" false

    ZFS_POOL="lxdtest-$(basename "${LXD_DIR}")-init"
    LXD_DIR=${LXD_INIT_DIR} lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    kill_lxd "${LXD_INIT_DIR}"
  fi

  # lxd init --network-address 127.0.0.1 --network-port LOCAL --auto
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  LXD_DIR=${LXD_INIT_DIR} lxd init --network-address 127.0.0.1 --network-port "$(local_tcp_port)" --auto

  kill_lxd "${LXD_INIT_DIR}"
}
